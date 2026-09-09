<script setup lang="ts">
import { ref, computed, watch, watchEffect, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { ApiError, apiFetch } from '@/api/client'
import type { Transaction, Span, TraceError, Log, LogListPage, FlameGraph } from '@/api/types'
import { formatDuration } from '@/utils/formatters'
import { useTimezone } from '@/composables/useTimezone'
import Icon from '@/components/Icon.vue'
import FlameGraphView from '@/components/FlameGraph.vue'

const route = useRoute()
const router = useRouter()
const tz = useTimezone()
const txId = computed(() => route.params.id as string)

const collapsedBranches = ref<Set<string>>(new Set())
const openDetails = ref<Set<string>>(new Set())
const expandedGroups = ref<Set<string>>(new Set())
const expandAllFlag = ref(false)
const focusedIdx = ref<number | null>(null)
const copiedTraceId = ref(false)
const copiedSpanId = ref<string | null>(null)
const searchInputRef = ref<HTMLInputElement | null>(null)
const hoveredRowKey = ref<string | null>(null)
const viewStart = ref(0)
const viewEnd = ref(1)
const panActive = ref(false)
const panDidMove = ref(false)
const panStartX = ref(0)
const panViewStart = ref(0)
const panViewRange = ref(0)
const suppressNextClick = ref(false)
const timelineRef = ref<HTMLElement | null>(null)
const waterfallRef = ref<HTMLElement | null>(null)
const leftPct = ref(30)
const isDividerDragging = ref(false)
const dividerStartX = ref(0)
const dividerStartPct = ref(0)

const AUTO_GROUP_THRESHOLD = 4

type SpanRow = { kind: 'span'; span: Span; depth: number }
type GroupRow = { kind: 'group'; key: string; op: string; count: number; depth: number; spans: Span[] }
type ChainRow = { kind: 'chain'; key: string; op: string; count: number; depth: number; head: Span; tail: Span; spans: Span[] }
type DisplayRow = SpanRow | GroupRow | ChainRow

const {
  data: tx,
  isLoading: isTxLoading,
  isError: isTxError,
  refetch: refetchTx,
} = useQuery({
  queryKey: computed(() => ['transactions', txId.value]),
  queryFn: ({ signal }) => apiFetch<Transaction>(`/api/transactions/${txId.value}`, { signal }),
})

const {
  data: spans,
  isLoading: isSpansLoading,
  isError: isSpansError,
  refetch: refetchSpans,
} = useQuery({
  queryKey: computed(() => ['transactions', txId.value, 'spans']),
  queryFn: ({ signal }) => apiFetch<Span[]>(`/api/transactions/${txId.value}/spans`, { signal }),
  enabled: computed(() => !!txId.value),
})

const spanList = computed(() => spans.value ?? [])
const spanQuery = ref('')

const filteredSpans = computed(() => {
  const q = spanQuery.value.trim().toLowerCase()
  if (!q) return spanList.value
  return spanList.value.filter(s =>
    s.description.toLowerCase().includes(q) ||
    s.op.toLowerCase().includes(q) ||
    s.status.toLowerCase().includes(q)
  )
})

watch(filteredSpans, () => { focusedIdx.value = null })

watchEffect(() => {
  if (tx.value?.transaction) document.title = `${tx.value.transaction} - Tindra`
})

const total = computed(() => {
  const txMs = tx.value?.duration_ms ?? 1
  if (!spanList.value.length) return txMs
  const maxSpanEnd = spanList.value.reduce((end, s) => Math.max(end, s.start_offset_ms + s.duration_ms), 0)
  return Math.max(txMs, maxSpanEnd)
})

watch(total, (t) => {
  viewStart.value = 0
  viewEnd.value = t
}, { immediate: true })

const criticalSpanCount = computed(() => spanList.value.filter(s => s.is_critical).length)

const critPathEndMs = computed(() => {
  const critical = spanList.value.filter(s => s.is_critical)
  if (critical.length === 0) return 0
  return critical.reduce((end, s) => Math.max(end, s.start_offset_ms + s.duration_ms), 0)
})

const hasCriticalPath = computed(() => criticalSpanCount.value > 0 && spanList.value.length > 1)

const ticks = computed(() => {
  const start = viewStart.value
  const end = viewEnd.value
  const range = end - start
  if (range <= 0) return []
  const rawStep = range / 6
  const mag = Math.pow(10, Math.floor(Math.log10(rawStep)))
  const n = rawStep / mag
  const step = n <= 1 ? mag : n <= 2 ? 2 * mag : n <= 5 ? 5 * mag : 10 * mag
  const out: number[] = []
  const firstTick = Math.ceil(start / step) * step
  for (let v = firstTick; v <= end; v += step) out.push(v)
  return out
})

const maxDurationSpanId = computed(() =>
  spanList.value.length === 0 ? null
    : spanList.value.reduce((m, s) => s.duration_ms > m.duration_ms ? s : m).id
)

const presentOps = computed(() => {
  const ops = new Set(spanList.value.map(s => s.op.split('.')[0]))
  return [...ops].sort()
})

// Number of direct children for each span_id, used for the child-count badge.
const childCounts = computed((): Map<string, number> => {
  const m = new Map<string, number>()
  for (const s of spanList.value) {
    if (s.parent_span_id) m.set(s.parent_span_id, (m.get(s.parent_span_id) ?? 0) + 1)
  }
  return m
})

const childrenByParent = computed(() => {
  const children = new Map<string, Span[]>()
  for (const span of spanList.value) {
    if (!span.parent_span_id) continue
    const siblings = children.get(span.parent_span_id)
    if (siblings) siblings.push(span)
    else children.set(span.parent_span_id, [span])
  }
  return children
})

// Self time = span's own duration minus the wall-clock coverage of its direct children.
// Children may overlap, so we merge intervals rather than naively summing durations.
function selfTimeMs(span: Span): number {
  const kids = childrenByParent.value.get(span.span_id) ?? []
  if (!kids.length) return span.duration_ms
  const intervals = kids
    .map(k => [k.start_offset_ms, k.start_offset_ms + k.duration_ms] as [number, number])
    .sort((a, b) => a[0] - b[0])
  let childMs = 0
  let [cur0, cur1] = intervals[0]
  for (let i = 1; i < intervals.length; i++) {
    const [s, e] = intervals[i]
    if (s <= cur1) { cur1 = Math.max(cur1, e) }
    else { childMs += cur1 - cur0; cur0 = s; cur1 = e }
  }
  childMs += cur1 - cur0
  return Math.max(0, span.duration_ms - childMs)
}

function formatAbsTimestamp(ms: number): string {
  return new Date(ms).toLocaleString('en-US', {
    month: 'short', day: 'numeric', year: 'numeric',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
    fractionalSecondDigits: 3,
    hour12: false,
    timeZone: tz.value,
  })
}

async function copySpanId(id: string) {
  await navigator.clipboard.writeText(id)
  copiedSpanId.value = id
  setTimeout(() => { copiedSpanId.value = null }, 1500)
}

function opLabel(op: string): string {
  return op.split('.')[0]
}

function opSuffix(op: string): string {
  const idx = op.indexOf('.')
  return idx >= 0 ? op.slice(idx + 1) : ''
}

function spanDataEntries(span: Span): [string, string][] {
  if (!span.data) return []
  try {
    const obj = typeof span.data === 'string' ? JSON.parse(span.data) : span.data
    return Object.entries(obj)
      .filter(([, v]) => v !== null && v !== undefined && v !== '')
      .map(([k, v]) => [k, typeof v === 'string' ? v : JSON.stringify(v)])
  } catch {
    return []
  }
}

function opColor(op: string) {
  const base = op.split('.')[0]
  switch (base) {
    case 'db':         return 'var(--success)'
    case 'http':       return 'var(--info)'
    case 'task':       return 'var(--warning)'
    case 'template':   return 'var(--accent)'
    case 'cache':      return 'oklch(0.65 0.10 220)'
    case 'pageload':
    case 'navigation':
    case 'browser':    return 'oklch(0.62 0.14 290)'
    case 'ui':         return 'oklch(0.62 0.14 340)'
    case 'resource':   return 'oklch(0.60 0.12 195)'
    case 'grpc':
    case 'rpc':        return 'oklch(0.58 0.13 255)'
    case 'graphql':    return 'oklch(0.58 0.18 345)'
    case 'queue':      return 'oklch(0.65 0.13 48)'
    case 'file':       return 'oklch(0.60 0.08 65)'
    default:           return 'oklch(0.55 0.04 250)'
  }
}

// Amber used consistently for all critical path markers.
const CRITICAL_COLOR = 'oklch(0.76 0.16 60)'

// Set of span_ids that have at least one child span in this transaction.
const spanHasChildren = computed((): Set<string> => {
  const allIds = new Set(spanList.value.map(s => s.span_id))
  const parents = new Set<string>()
  for (const span of spanList.value) {
    if (span.parent_span_id && allIds.has(span.parent_span_id)) {
      parents.add(span.parent_span_id)
    }
  }
  return parents
})

// Hierarchical span tree with branch collapse and auto-grouping of repeated op patterns.
const spanTree = computed((): DisplayRow[] => {
  const spans = spanList.value
  if (!spans.length) return []

  const bySpanId = new Map(spans.map(s => [s.span_id, s]))
  const children = new Map<string, Span[]>()
  const roots: Span[] = []

  for (const s of spans) {
    if (!s.parent_span_id || !bySpanId.has(s.parent_span_id)) {
      roots.push(s)
    } else {
      const kids = children.get(s.parent_span_id) ?? []
      kids.push(s)
      children.set(s.parent_span_id, kids)
    }
  }

  const rows: DisplayRow[] = []
  const visited = new Set<Span>()
  type Work = { siblings: Span[]; index: number; depth: number } | { span: Span; depth: number }
  const work: Work[] = [{ siblings: roots, index: 0, depth: 0 }]
  const descend = (span: Span, depth: number) => {
    if (!collapsedBranches.value.has(span.span_id)) {
      work.push({ siblings: children.get(span.span_id) ?? [], index: 0, depth })
    }
  }
  while (work.length) {
    const item = work.pop()!
    const depth = item.depth
    if ('span' in item) {
      if (visited.has(item.span)) continue
      visited.add(item.span)
      rows.push({ kind: 'span', span: item.span, depth })
      descend(item.span, depth + 1)
      continue
    }
    const { siblings, index: i } = item
    if (i >= siblings.length) continue
    const s = siblings[i]
    if (visited.has(s)) { work.push({ siblings, index: i + 1, depth }); continue }
    const base = s.op.split('.')[0]
    let j = i + 1
    while (j < siblings.length && siblings[j].op.split('.')[0] === base) j++
    if (j - i >= AUTO_GROUP_THRESHOLD) {
      const groupSpans = siblings.slice(i, j)
      const key = `grp:${s.span_id}:${groupSpans[groupSpans.length - 1].span_id}`
      rows.push({ kind: 'group', key, op: base, count: groupSpans.length, depth, spans: groupSpans })
      work.push({ siblings, index: j, depth })
      if (expandAllFlag.value || expandedGroups.value.has(key)) {
        for (let k = groupSpans.length - 1; k >= 0; k--) work.push({ span: groupSpans[k], depth })
      }
      continue
    }
    work.push({ siblings, index: i + 1, depth })
    const chain = [s]
    const chainSeen = new Set([s])
    let current = s
    for (;;) {
      const kids = children.get(current.span_id) ?? []
      if (kids.length !== 1 || kids[0].op.split('.')[0] !== base || chainSeen.has(kids[0]) || visited.has(kids[0])) break
      current = kids[0]
      chain.push(current)
      chainSeen.add(current)
    }
    if (chain.length >= 3) {
      const tail = chain[chain.length - 1]
      const key = `chain:${s.span_id}:${tail.span_id}`
      rows.push({ kind: 'chain', key, op: base, count: chain.length, depth, head: s, tail, spans: chain })
      for (const span of chain) visited.add(span)
      if (expandAllFlag.value || expandedGroups.value.has(key)) {
        for (const span of chain) rows.push({ kind: 'span', span, depth })
        descend(tail, depth + 1)
      }
    } else {
      visited.add(s)
      rows.push({ kind: 'span', span: s, depth })
      descend(s, depth + 1)
    }
  }
  return rows
})

// displayRows: hierarchical (with grouping) when no search active, flat filtered list during search.
const displayRows = computed((): DisplayRow[] => {
  if (spanQuery.value.trim()) {
    return filteredSpans.value.map(s => ({ kind: 'span' as const, span: s, depth: 0 }))
  }
  return spanTree.value
})

// Keep both waterfall columns bounded, including after Expand all or search.
const ROWS_PER_PAGE = 200
const rowPage = ref(0)
const rowOffset = computed(() => rowPage.value * ROWS_PER_PAGE)
const renderedRows = computed(() => displayRows.value.slice(rowOffset.value, rowOffset.value + ROWS_PER_PAGE))
watch(displayRows, () => { rowPage.value = 0; focusedIdx.value = null })
watch(focusedIdx, index => { if (index !== null) rowPage.value = Math.floor(index / ROWS_PER_PAGE) })
function changeRowPage(delta: number) {
  rowPage.value += delta
  focusedIdx.value = rowOffset.value
}

function toggleBranch(spanId: string) {
  const s = new Set(collapsedBranches.value)
  if (s.has(spanId)) s.delete(spanId)
  else s.add(spanId)
  collapsedBranches.value = s
}

function toggleDetail(id: string) {
  const s = new Set(openDetails.value)
  if (s.has(id)) s.delete(id)
  else s.add(id)
  openDetails.value = s
}

function toggleGroup(key: string) {
  if (expandAllFlag.value) {
    const allKeys = new Set(
      displayRows.value
        .filter((r): r is GroupRow | ChainRow => r.kind === 'group' || r.kind === 'chain')
        .map(r => r.key)
    )
    expandAllFlag.value = false
    allKeys.delete(key)
    expandedGroups.value = allKeys
    return
  }
  const s = new Set(expandedGroups.value)
  if (s.has(key)) s.delete(key)
  else s.add(key)
  expandedGroups.value = s
}

function rowKey(row: DisplayRow): string {
  return row.kind === 'span' ? row.span.id : row.key
}

function toPct(ms: number): string {
  const range = viewEnd.value - viewStart.value
  return `${((ms - viewStart.value) / range) * 100}%`
}
function toWidthPct(durationMs: number): string {
  const range = viewEnd.value - viewStart.value
  return `${(durationMs / range) * 100}%`
}
const isZoomed = computed(() => viewStart.value > 0.1 || viewEnd.value < total.value - 0.1)

function zoomAt(fraction: number, factor: number) {
  const t = total.value
  const range = viewEnd.value - viewStart.value
  const cursorMs = viewStart.value + fraction * range
  const newRange = Math.max(1, range * factor)
  let newStart = cursorMs - fraction * newRange
  let newEnd = newStart + newRange
  if (newStart < 0) { newEnd -= newStart; newStart = 0 }
  if (newEnd > t) { newStart -= (newEnd - t); newEnd = t }
  viewStart.value = Math.max(0, newStart)
  viewEnd.value = Math.min(t, newEnd)
}
function resetZoom() {
  viewStart.value = 0
  viewEnd.value = total.value
}

function handleDividerMouseDown(e: MouseEvent) {
  e.preventDefault()
  isDividerDragging.value = true
  dividerStartX.value = e.clientX
  dividerStartPct.value = leftPct.value
}

function expandAll() {
  collapsedBranches.value = new Set()
  expandAllFlag.value = true
}

function collapseAll() {
  collapsedBranches.value = new Set(spanHasChildren.value)
  expandedGroups.value = new Set()
  expandAllFlag.value = false
}

async function copyTraceId() {
  if (!tx.value) return
  await navigator.clipboard.writeText(tx.value.trace_id)
  copiedTraceId.value = true
  setTimeout(() => { copiedTraceId.value = false }, 1500)
}

function handleKeydown(e: KeyboardEvent) {
  const target = e.target as HTMLElement
  const inInput = target.tagName === 'INPUT' || target.tagName === 'TEXTAREA'

  if (!inInput && e.key === '/') {
    e.preventDefault()
    searchInputRef.value?.focus()
    return
  }

  if (inInput && e.key === 'Escape') {
    spanQuery.value = ''
    searchInputRef.value?.blur()
    return
  }

  if (inInput) return

  const n = displayRows.value.length
  if (n === 0) return

  if (e.key === 'j' || e.key === 'ArrowDown') {
    e.preventDefault()
    focusedIdx.value = focusedIdx.value === null ? 0 : Math.min(focusedIdx.value + 1, n - 1)
  } else if (e.key === 'k' || e.key === 'ArrowUp') {
    e.preventDefault()
    focusedIdx.value = focusedIdx.value === null ? n - 1 : Math.max(focusedIdx.value - 1, 0)
  } else if (e.key === 'Enter' && focusedIdx.value !== null) {
    e.preventDefault()
    const row = displayRows.value[focusedIdx.value]
    if (row.kind === 'span') toggleDetail(row.span.id)
    else toggleGroup(row.key)
  } else if (e.key === 'Escape') {
    focusedIdx.value = null
  } else if (e.key === '+' || e.key === '=') {
    e.preventDefault()
    zoomAt(0.5, 1 / 1.3)
  } else if (e.key === '-' || e.key === '_') {
    e.preventDefault()
    zoomAt(0.5, 1.3)
  }
}

function handleTimelineWheel(e: WheelEvent) {
  if (!e.metaKey && !e.ctrlKey) return
  e.preventDefault()
  const el = timelineRef.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  const fraction = (e.clientX - rect.left) / rect.width
  zoomAt(fraction, e.deltaY > 0 ? 1.2 : 1 / 1.2)
}

function handleTimelineMouseDown(e: MouseEvent) {
  if (e.button !== 0) return
  panActive.value = true
  panDidMove.value = false
  panStartX.value = e.clientX
  panViewStart.value = viewStart.value
  panViewRange.value = viewEnd.value - viewStart.value
}

function onDocumentMouseMove(e: MouseEvent) {
  if (panActive.value) {
    const el = timelineRef.value
    if (!el) return
    const dx = e.clientX - panStartX.value
    if (Math.abs(dx) < 4) return
    panDidMove.value = true
    const rect = el.getBoundingClientRect()
    const msDelta = (dx / rect.width) * panViewRange.value
    let newStart = panViewStart.value - msDelta
    const t = total.value
    if (newStart < 0) newStart = 0
    if (newStart + panViewRange.value > t) newStart = t - panViewRange.value
    viewStart.value = Math.max(0, newStart)
    viewEnd.value = viewStart.value + panViewRange.value
  }
  if (isDividerDragging.value) {
    const container = waterfallRef.value
    if (!container) return
    const containerWidth = container.getBoundingClientRect().width
    const dx = e.clientX - dividerStartX.value
    const deltaPct = (dx / containerWidth) * 100
    leftPct.value = Math.min(60, Math.max(15, dividerStartPct.value + deltaPct))
  }
}

function onDocumentMouseUp() {
  if (panActive.value && panDidMove.value) {
    suppressNextClick.value = true
    setTimeout(() => { suppressNextClick.value = false }, 50)
  }
  panActive.value = false
  panDidMove.value = false
  isDividerDragging.value = false
}

function handleRowClick(row: DisplayRow) {
  if (suppressNextClick.value) return
  if (row.kind === 'group' || row.kind === 'chain') toggleGroup(row.key)
  else toggleDetail(row.span.id)
}

onMounted(() => {
  document.addEventListener('keydown', handleKeydown)
  document.addEventListener('mousemove', onDocumentMouseMove)
  document.addEventListener('mouseup', onDocumentMouseUp)
})
onUnmounted(() => {
  document.removeEventListener('keydown', handleKeydown)
  document.removeEventListener('mousemove', onDocumentMouseMove)
  document.removeEventListener('mouseup', onDocumentMouseUp)
})

// Trace log correlation - only fetch when the transaction has a trace_id.
const traceId = computed(() => tx.value?.trace_id ?? '')
const projectId = computed(() => tx.value?.project_id ?? '')

const { data: traceLogs } = useQuery({
  queryKey: computed(() => ['trace-logs', projectId.value, traceId.value]),
  queryFn: ({ signal }) => {
    const params = new URLSearchParams({ trace_id: traceId.value, limit: '50' } as Record<string, string>)
    if (projectId.value) params.append('project_id', projectId.value)
    return apiFetch<LogListPage>(`/api/logs?${params}`, { signal })
  },
  enabled: computed(() => !!traceId.value),
})

const traceLogList = computed(() => traceLogs.value?.logs ?? [])

// Trace error correlation - errors sharing this transaction's trace_id.
const { data: traceErrorsData } = useQuery({
  queryKey: computed(() => ['transactions', txId.value, 'errors']),
  queryFn: ({ signal }) => apiFetch<TraceError[]>(`/api/transactions/${txId.value}/errors`, { signal }),
  enabled: computed(() => !!txId.value),
})
const traceErrorList = computed(() => traceErrorsData.value ?? [])

// Most transactions carry no profile: the SDK samples them out, or profiling
// is off. The endpoint answers 404 for that, so it is treated as "nothing to
// show" rather than retried as a failure.
const { data: flameGraph, isPending: profileLoading, isError: profileError, refetch: refetchProfile } = useQuery({
  queryKey: computed(() => ['transactions', txId.value, 'flamegraph']),
  queryFn: async ({ signal }) => {
    try {
      return await apiFetch<FlameGraph>(`/api/transactions/${txId.value}/flamegraph`, { signal })
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) return null
      throw error
    }
  },
  enabled: computed(() => !!txId.value),
  retry: false,
})


// Map span_id → errors for O(1) lookup when rendering timeline bars.
const errorsBySpanId = computed(() => {
  const m = new Map<string, TraceError[]>()
  for (const e of traceErrorList.value) {
    if (!e.span_id) continue
    const list = m.get(e.span_id) ?? []
    list.push(e)
    m.set(e.span_id, list)
  }
  return m
})

function traceLogOffset(log: Log): string {
  if (!tx.value) return '+0ms'
  const txStart = new Date(tx.value.start_timestamp).getTime()
  const logTs = new Date(log.timestamp).getTime()
  const ms = logTs - txStart
  if (ms < 0) return '<0ms'
  if (ms < 1000) return `+${ms}ms`
  return `+${(ms / 1000).toFixed(2)}s`
}

function traceErrorOffset(e: TraceError): string {
  if (!tx.value) return '+0ms'
  const txStart = new Date(tx.value.start_timestamp).getTime()
  const errTs = new Date(e.timestamp).getTime()
  const ms = errTs - txStart
  if (ms < 0) return '<0ms'
  if (ms < 1000) return `+${ms}ms`
  return `+${(ms / 1000).toFixed(2)}s`
}
</script>

<template>
  <!-- Error state -->
  <div v-if="isTxError" class="page">
    <div class="detail-breadcrumb">
      <a class="detail-breadcrumb__back" href="#" @click.prevent="router.back()">
        <Icon name="arrow-left" :size="12" />
        Transactions
      </a>
    </div>
    <div class="txerror" style="margin: 24px">
      <Icon name="alert-triangle" :size="14" class="txerror__icon" />
      Failed to load transaction.
      <button class="btn btn--ghost" @click="refetchTx()">Retry</button>
    </div>
  </div>

  <!-- Loading skeleton -->
  <div v-else-if="isTxLoading" class="page">
    <div class="detail-breadcrumb">
      <a class="detail-breadcrumb__back" href="#" @click.prevent="router.back()">
        <Icon name="arrow-left" :size="12" />
        Transactions
      </a>
      <div class="detail-breadcrumb__title">
        <span class="skel" style="width: 220px; height: 12px" />
      </div>
    </div>
    <div class="tx-detail-hero">
      <span class="skel" style="width: 320px; height: 22px; display: block; margin-bottom: 20px" />
      <div class="stat-row">
        <div v-for="i in 4" :key="i" class="stat">
          <span class="skel" style="width: 56px; height: 10px; display: block; margin-bottom: 6px" />
          <span class="skel" style="width: 88px; height: 22px; display: block" />
        </div>
      </div>
    </div>
    <div class="trace-search">
      <Icon name="search" :size="13" class="trace-search__icon" />
      <span class="skel" style="width: 240px; height: 10px" />
    </div>
    <div class="waterfall-grid">
      <div class="waterfall-left" style="overflow: hidden">
        <div class="span-row span-row--header"><span>Span</span><span>Duration</span></div>
        <div v-for="i in 8" :key="i" class="span-row" style="cursor: default">
          <span class="skel" :style="{ width: `${55 + (i % 3) * 15}%`, height: '10px' }" />
          <span class="skel" style="width: 44px; height: 10px" />
        </div>
      </div>
      <div class="timeline" style="overflow: hidden">
        <div class="timeline__axis" />
        <div v-for="i in 8" :key="i" class="timeline__row" style="cursor: default; display: flex; align-items: center; padding: 0 16px">
          <span class="skel" :style="{ marginLeft: `${(i % 4) * 10}%`, width: `${20 + (i % 5) * 12}%`, height: '12px', borderRadius: '2px' }" />
        </div>
      </div>
    </div>
  </div>

  <!-- Loaded -->
  <div v-else-if="tx" class="page" style="display: flex; flex-direction: column">
    <!-- Breadcrumb -->
    <div class="detail-breadcrumb">
      <a class="detail-breadcrumb__back" href="#" @click.prevent="router.back()">
        <Icon name="arrow-left" :size="12" />
        Transactions
      </a>
      <div class="detail-breadcrumb__title"><span>{{ tx.transaction }}</span></div>
      <div class="detail-breadcrumb__actions">
        <span class="tag">{{ tx.environment ?? '-' }}</span>
      </div>
    </div>

    <!-- Hero -->
    <div class="tx-detail-hero">
      <div class="tx-detail-hero__title">
        <span class="optag" :class="`optag--${tx.op.split('.')[0]}`">{{ tx.op.split('.')[0] }}</span>
        <h1 class="tx-detail-hero__name mono">{{ tx.transaction }}</h1>
      </div>
      <div class="stat-row">
        <div class="stat">
          <div class="stat__label">Duration</div>
          <div class="stat__value">{{ formatDuration(tx.duration_ms) }}</div>
          <div class="stat__sub">{{ spanList.length }} spans</div>
        </div>
        <div v-if="hasCriticalPath" class="stat">
          <div class="stat__label" style="display: flex; align-items: center; gap: 5px">
            <span
              style="display: inline-block; width: 7px; height: 7px; border-radius: 50%;"
              :style="{ background: CRITICAL_COLOR }"
            />
            Critical path
          </div>
          <div class="stat__value">{{ formatDuration(critPathEndMs) }}</div>
          <div class="stat__sub">{{ criticalSpanCount }} of {{ spanList.length }} spans</div>
        </div>
        <div class="stat">
          <div class="stat__label">Status</div>
          <div class="stat__value"><span class="tx-status" :class="`tx-status--${tx.status}`">{{ tx.status }}</span></div>
        </div>
        <div class="stat">
          <div class="stat__label">Started</div>
          <div class="stat__value stat__value--md">
            {{ new Date(tx.start_timestamp).toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false, timeZone: tz }) }}
          </div>
          <div class="stat__sub">{{ new Date(tx.start_timestamp).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric', timeZone: tz }) }}</div>
        </div>
        <div class="stat stat--copyable" v-tooltip="copiedTraceId ? 'Copied!' : 'Click to copy full trace ID'" @click="copyTraceId">
          <div class="stat__label">Trace ID</div>
          <div class="stat__value stat__value--mono stat__value--sm">
            {{ copiedTraceId ? 'Copied!' : tx.trace_id.slice(0, 16) + '…' }}
          </div>
        </div>
      </div>
    </div>

    <!-- Spans error -->
    <div v-if="isSpansError" class="txerror" style="margin: 12px 24px">
      <Icon name="alert-triangle" :size="14" class="txerror__icon" />
      Failed to load spans.
      <button class="btn btn--ghost" @click="refetchSpans()">Retry</button>
    </div>

    <!-- Search bar -->
    <div v-else class="trace-search">
      <Icon name="search" :size="13" class="trace-search__icon" />
      <input
        ref="searchInputRef"
        v-model="spanQuery"
        class="trace-search__input"
        placeholder="Search spans  /"
        spellcheck="false"
      />
      <button v-if="spanQuery" class="trace-search__clear" @click="spanQuery = ''">
        <Icon name="x" :size="12" />
      </button>
      <span v-if="spanQuery" class="trace-search__count">
        {{ filteredSpans.length }} / {{ spanList.length }}
      </span>
      <div v-if="!spanQuery && presentOps.length > 0" class="trace-search__legend">
        <span v-for="op in presentOps" :key="op" class="trace-search__leg">
          <span class="trace-search__leg-dot" :style="{ background: opColor(op) }" />
          {{ op }}
        </span>
      </div>
      <div v-if="spanList.length > 0" class="trace-search__zoom">
        <button
          class="span-tree-btn"
          v-tooltip="'Zoom out (-)'"
          @click="zoomAt(0.5, 1.3)"
        ><Icon name="minus" :size="10" /></button>
        <button
          v-if="isZoomed"
          class="span-tree-btn span-tree-btn--reset"
          v-tooltip="'Reset zoom'"
          @click="resetZoom"
        ><Icon name="maximize-2" :size="10" /></button>
        <button
          class="span-tree-btn"
          v-tooltip="'Zoom in (+)'"
          @click="zoomAt(0.5, 1 / 1.3)"
        ><Icon name="plus" :size="10" /></button>
      </div>
    </div>

    <!-- Waterfall -->
    <div v-if="displayRows.length > ROWS_PER_PAGE" class="trace-search" aria-label="Span pages">
      <button class="btn btn--ghost" :disabled="rowPage === 0" @click="changeRowPage(-1)">Previous</button>
      <span aria-live="polite">Rows {{ rowOffset + 1 }}–{{ Math.min(rowOffset + ROWS_PER_PAGE, displayRows.length) }} of {{ displayRows.length }}</span>
      <button class="btn btn--ghost" :disabled="rowOffset + ROWS_PER_PAGE >= displayRows.length" @click="changeRowPage(1)">Next</button>
    </div>
    <div v-if="!isSpansError" ref="waterfallRef" class="waterfall-grid" :style="{ gridTemplateColumns: leftPct + '% 4px 1fr' }">

      <!-- Left: span tree -->
      <div class="waterfall-left">
        <div class="span-row span-row--header">
          <span>Span</span>
          <div style="display: flex; align-items: center; gap: 8px">
            <span>Duration</span>
            <template v-if="spanList.length > 0">
              <button class="span-tree-btn" aria-label="Expand all" v-tooltip="'Expand all'" @click="expandAll">
                <Icon name="chevrons-down" :size="11" />
              </button>
              <button class="span-tree-btn" aria-label="Collapse all" v-tooltip="'Collapse all'" @click="collapseAll">
                <Icon name="chevrons-up" :size="11" />
              </button>
            </template>
          </div>
        </div>

        <!-- Loading -->
        <template v-if="isSpansLoading">
          <div v-for="i in 8" :key="i" class="span-row" style="cursor: default">
            <span class="skel" :style="{ width: `${55 + (i % 3) * 15}%`, height: '10px' }" />
            <span class="skel" style="width: 44px; height: 10px" />
          </div>
        </template>

        <!-- Empty -->
        <div v-else-if="spanList.length === 0" class="span-empty">
          No spans recorded for this transaction.
        </div>

        <!-- No filter match -->
        <div v-else-if="filteredSpans.length === 0" class="span-empty">
          No spans match "{{ spanQuery }}".
        </div>

        <template v-else>
          <template v-for="(row, i) in renderedRows" :key="row.kind === 'span' ? row.span.id : row.key">

            <!-- Group row: collapsed set of repeated op spans -->
            <div
              v-if="row.kind === 'group'"
              class="span-row span-row--group"
              :class="{ 'span-row--focused': focusedIdx === rowOffset + i, 'is-hovered': hoveredRowKey === rowKey(row) }"
              @click="handleRowClick(row)"
              @mouseenter="hoveredRowKey = rowKey(row)"
              @mouseleave="hoveredRowKey = null"
            >
              <div class="span-name" :style="{ paddingLeft: row.depth * 12 + 'px' }">
                <span class="span-name__caret">
                  <Icon :name="expandAllFlag || expandedGroups.has(row.key) ? 'chevron-down' : 'chevron-right'" :size="10" />
                </span>
                <span class="span-autogroup-badge">Autogrouped</span>
                <span class="span-name__arrow">→</span>
                <span class="span-name__op-label" :style="{ color: opColor(row.op) }">{{ row.op }}</span>
              </div>
              <span class="span-row__dur">{{ row.count }} spans</span>
            </div>

            <!-- Chain row: single-descendant chain of same-op spans -->
            <div
              v-else-if="row.kind === 'chain'"
              class="span-row span-row--group"
              :class="{ 'span-row--focused': focusedIdx === rowOffset + i, 'is-hovered': hoveredRowKey === rowKey(row) }"
              @click="handleRowClick(row)"
              @mouseenter="hoveredRowKey = rowKey(row)"
              @mouseleave="hoveredRowKey = null"
            >
              <div class="span-name" :style="{ paddingLeft: row.depth * 12 + 'px' }">
                <span class="span-name__caret">
                  <Icon :name="expandAllFlag || expandedGroups.has(row.key) ? 'chevron-down' : 'chevron-right'" :size="10" />
                </span>
                <span class="span-autogroup-badge">Chain</span>
                <span class="span-name__arrow">→</span>
                <span class="span-name__op-label" :style="{ color: opColor(row.op) }">{{ row.op }}</span>
              </div>
              <span class="span-row__dur">{{ row.count }} spans</span>
            </div>

            <!-- Span row -->
            <template v-else-if="row.kind === 'span'">
              <div
                class="span-row"
                :class="{
                  'span-row--open': openDetails.has(row.span.id),
                  'span-row--focused': focusedIdx === rowOffset + i,
                  'span-row--critical': row.span.is_critical,
                  'is-hovered': hoveredRowKey === rowKey(row),
                }"
                :style="{ opacity: hasCriticalPath && !row.span.is_critical ? 0.6 : 1 }"
                @click="handleRowClick(row)"
                @mouseenter="hoveredRowKey = rowKey(row)"
                @mouseleave="hoveredRowKey = null"
              >
                <div class="span-name" :style="{ paddingLeft: row.depth * 12 + 'px' }">
                  <!-- Chevron for parent spans -->
                  <span
                    v-if="spanHasChildren.has(row.span.span_id)"
                    class="span-name__caret"
                    @click.stop="toggleBranch(row.span.span_id)"
                  >
                    <Icon :name="collapsedBranches.has(row.span.span_id) ? 'chevron-right' : 'chevron-down'" :size="10" />
                  </span>
                  <span v-else class="span-name__caret" />
                  <span
                    class="span-name__dot"
                    :style="{
                      background: opColor(row.span.op),
                      boxShadow: row.span.is_critical ? `0 0 0 2px ${CRITICAL_COLOR}` : 'none',
                    }"
                  />
                  <span class="span-name__op-prefix" :style="{ color: opColor(row.span.op) }">{{ opLabel(row.span.op) }}</span>
                  <span v-if="opSuffix(row.span.op)" class="span-name__op-suffix">.{{ opSuffix(row.span.op) }}</span>
                  <span class="span-name__desc">{{ row.span.description }}</span>
                  <span
                    v-if="errorsBySpanId.get(row.span.span_id)?.length"
                    class="span-error-badge"
                    v-tooltip="`${errorsBySpanId.get(row.span.span_id)!.length} error(s) on this span`"
                  >{{ errorsBySpanId.get(row.span.span_id)!.length }}</span>
                  <span
                    v-if="(childCounts.get(row.span.span_id) ?? 0) > 0"
                    class="span-child-count"
                    v-tooltip="`${childCounts.get(row.span.span_id)} child span(s)`"
                  >{{ childCounts.get(row.span.span_id) }}</span>
                </div>
                <div class="span-row__right">
                  <span class="span-row__dur" :class="{ 'span-row__dur--crit': row.span.id === maxDurationSpanId }">
                    {{ formatDuration(row.span.duration_ms) }}
                  </span>
                </div>
              </div>
              <!-- Span detail panel -->
              <div v-if="openDetails.has(row.span.id)" class="span-detail">
                <div class="span-detail__section">
                  <div class="span-detail__section-title">General</div>
                  <div class="span-detail__grid">
                    <span class="span-detail__k">Span ID</span>
                    <span class="span-detail__v span-detail__v--copy" @click="copySpanId(row.span.span_id)">
                      <span class="span-detail__mono">{{ row.span.span_id }}</span>
                      <span class="span-detail__copy-hint">{{ copiedSpanId === row.span.span_id ? 'Copied!' : 'Copy' }}</span>
                    </span>
                    <span v-if="row.span.parent_span_id" class="span-detail__k">Parent Span ID</span>
                    <span v-if="row.span.parent_span_id" class="span-detail__v span-detail__mono span-detail__v--muted">{{ row.span.parent_span_id }}</span>
                    <span class="span-detail__k">Op</span>
                    <span class="span-detail__v">
                      <span class="span-detail__op-pill" :style="{ background: opColor(row.span.op) + '22', color: opColor(row.span.op) }">{{ row.span.op }}</span>
                    </span>
                    <span v-if="row.span.description" class="span-detail__k">Description</span>
                    <span v-if="row.span.description" class="span-detail__v span-detail__v--wrap">{{ row.span.description }}</span>
                    <span class="span-detail__k">Status</span>
                    <span class="span-detail__v">
                      <span class="span-detail__status" :class="`span-detail__status--${row.span.status}`">{{ row.span.status }}</span>
                    </span>
                  </div>
                </div>
                <div class="span-detail__section">
                  <div class="span-detail__section-title">Timing</div>
                  <div class="span-detail__grid">
                    <span class="span-detail__k">Duration</span>
                    <span class="span-detail__v">
                      {{ formatDuration(row.span.duration_ms) }}
                      <span class="span-detail__muted">({{ ((row.span.duration_ms / total) * 100).toFixed(1) }}% of total)</span>
                    </span>
                    <span class="span-detail__k">Self time</span>
                    <span class="span-detail__v">{{ formatDuration(selfTimeMs(row.span)) }}</span>
                    <span class="span-detail__k">Start</span>
                    <span class="span-detail__v span-detail__mono">{{ formatAbsTimestamp(row.span.start_timestamp_ms) }}</span>
                    <span class="span-detail__k">End</span>
                    <span class="span-detail__v span-detail__mono">{{ formatAbsTimestamp(row.span.start_timestamp_ms + row.span.duration_ms) }}</span>
                    <span class="span-detail__k">Offset</span>
                    <span class="span-detail__v span-detail__muted">+{{ row.span.start_offset_ms }}ms from tx start</span>
                    <span v-if="row.span.is_critical" class="span-detail__k">Critical path</span>
                    <span v-if="row.span.is_critical" class="span-detail__v" :style="{ color: CRITICAL_COLOR, fontWeight: 600 }">Yes</span>
                  </div>
                </div>
                <div v-if="spanDataEntries(row.span).length" class="span-detail__section">
                  <div class="span-detail__section-title">Span Data</div>
                  <div class="span-detail__grid span-detail__grid--attrs">
                    <template v-for="[k, v] in spanDataEntries(row.span)" :key="k">
                      <span class="span-detail__k span-detail__mono" :title="k">{{ k }}</span>
                      <span class="span-detail__v span-detail__v--wrap span-detail__mono">{{ v }}</span>
                    </template>
                  </div>
                </div>
              </div>
            </template>

          </template>
        </template>

      </div>

      <div class="waterfall-divider" @mousedown="handleDividerMouseDown" />

      <!-- Right: timeline -->
      <div
        ref="timelineRef"
        class="timeline"
        :style="{ overflowX: 'hidden', overflowY: 'auto', position: 'relative', cursor: panActive ? 'grabbing' : 'grab' }"
        @wheel="handleTimelineWheel"
        @mousedown="handleTimelineMouseDown"
      >
        <div
          v-for="v in ticks"
          :key="`tl-${v}`"
          class="timeline__tick"
          :style="{ left: toPct(v) }"
        />
        <div class="timeline__axis">
          <template v-for="v in ticks" :key="v">
            <span
              class="timeline__tick-label"
              :style="{ left: toPct(v) }"
            >
              {{ formatDuration(v) }}
            </span>
          </template>
        </div>

        <div v-if="isSpansLoading">
          <div v-for="i in 8" :key="i" class="timeline__row" style="cursor: default; display: flex; align-items: center; padding: 0 16px">
            <span class="skel" :style="{ marginLeft: `${(i % 4) * 10}%`, width: `${20 + (i % 5) * 12}%`, height: '12px', borderRadius: '2px' }" />
          </div>
        </div>

        <div
          v-for="(row, i) in renderedRows"
          :key="row.kind === 'span' ? row.span.id : row.key"
          class="timeline__row"
          :class="{ 'timeline__row--focused': focusedIdx === rowOffset + i, 'is-hovered': hoveredRowKey === rowKey(row) }"
          @click="handleRowClick(row)"
          @mouseenter="hoveredRowKey = rowKey(row)"
          @mouseleave="hoveredRowKey = null"
        >
          <!-- Group: single bar spanning the full op group range -->
          <template v-if="row.kind === 'group'">
            <div
              class="timeline__bar"
              :style="{
                left: toPct(row.spans.reduce((min, s) => Math.min(min, s.start_offset_ms), Infinity)),
                width: `max(4px, ${toWidthPct(row.spans.reduce((max, s) => Math.max(max, s.start_offset_ms + s.duration_ms), -Infinity) - row.spans.reduce((min, s) => Math.min(min, s.start_offset_ms), Infinity))})`,
                background: opColor(row.op),
                opacity: 0.45,
              }"
            >
              <span class="timeline__bar-label">{{ row.count }} × {{ row.op }}</span>
            </div>
          </template>
          <!-- Chain: single bar from head start to tail end -->
          <template v-else-if="row.kind === 'chain'">
            <div
              class="timeline__bar"
              :style="{
                left: toPct(row.head.start_offset_ms),
                width: `max(4px, ${toWidthPct(row.tail.start_offset_ms + row.tail.duration_ms - row.head.start_offset_ms)})`,
                background: opColor(row.op),
                opacity: 0.45,
              }"
            >
              <span class="timeline__bar-label">{{ row.count }} × {{ row.op }}</span>
            </div>
          </template>
          <!-- Span: standard bar -->
          <template v-else>
            <div
              class="timeline__bar"
              :class="{ 'timeline__bar--crit': row.span.id === maxDurationSpanId }"
              :style="{
                left: toPct(row.span.start_offset_ms),
                width: `max(4px, ${toWidthPct(row.span.duration_ms)})`,
                background: opColor(row.span.op),
                opacity: hasCriticalPath && !row.span.is_critical ? 0.3 : (openDetails.has(row.span.id) ? 1 : 0.85),
                outline: row.span.is_critical ? `1.5px solid ${CRITICAL_COLOR}` : 'none',
                outlineOffset: '1px',
              }"
              v-tooltip="`${row.span.description || row.span.op} · ${formatDuration(row.span.duration_ms)}`"
            >
              <span v-if="parseFloat(toWidthPct(row.span.duration_ms)) > 8" class="timeline__bar-label">
                {{ formatDuration(row.span.duration_ms) }}
              </span>
            </div>
            <span
              v-if="errorsBySpanId.get(row.span.span_id)?.length"
              class="timeline__error-dot"
              :style="{ left: `calc(${toPct(row.span.start_offset_ms)} + max(4px, ${toWidthPct(row.span.duration_ms)}) - 2px)` }"
              v-tooltip="`${errorsBySpanId.get(row.span.span_id)!.length} error(s) on this span`"
            />
          </template>
        </div>

      </div>
    </div>

    <!-- Flame graph. Named for what it is: TransactionProfileView is the
         aggregate view for a transaction name and means something else. -->
    <div v-if="flameGraph" class="trace-logs--flame">
      <FlameGraphView :graph="flameGraph" />
    </div>

    <p v-if="tx && !flameGraph && !profileLoading" class="trace-logs" style="padding: 16px">
      {{ profileError ? 'Could not load the profile.' : 'No profile available for this transaction.' }}
      <button v-if="profileError" class="btn" @click="refetchProfile()">Try again</button>
      <a class="btn" :href="`/setup?project_id=${tx.project_id}&check=profiles`">Check profiling setup</a>
    </p>

    <!-- Trace error correlation -->
    <div v-if="traceErrorList.length > 0" class="trace-logs">
      <div class="trace-logs__head">
        <Icon name="alert-circle" :size="11" style="color: var(--danger)" />
        Errors
        <span style="color: var(--text-3); font-weight: 400; text-transform: none; letter-spacing: 0">{{ traceErrorList.length }} in this trace</span>
      </div>
      <div class="trace-log-row" style="color: var(--text-3); font-size: 11px; font-weight: 500; text-transform: uppercase; letter-spacing: 0.04em">
        <span>Offset</span><span>Level</span><span>Issue</span>
      </div>
      <router-link
        v-for="e in traceErrorList"
        :key="e.event_id"
        :to="{ name: 'issue', params: { id: e.issue_id } }"
        class="trace-log-row trace-log-row--link"
      >
        <span class="trace-log-row__offset">{{ traceErrorOffset(e) }}</span>
        <span class="log-level" :class="`log-level--${e.level}`">{{ e.level }}</span>
        <span class="trace-log-row__body">{{ e.title }}</span>
      </router-link>
    </div>

    <!-- Trace log correlation -->
    <div v-if="traceLogList.length > 0" class="trace-logs">
      <div class="trace-logs__head">
        <Icon name="file-text" :size="11" />
        Logs
        <span style="color: var(--text-3); font-weight: 400; text-transform: none; letter-spacing: 0">{{ traceLogList.length }} entries</span>
      </div>
      <div class="trace-log-row" style="color: var(--text-3); font-size: 11px; font-weight: 500; text-transform: uppercase; letter-spacing: 0.04em">
        <span>Offset</span><span>Level</span><span>Message</span>
      </div>
      <div v-for="log in traceLogList" :key="log.id" class="trace-log-row">
        <span class="trace-log-row__offset">{{ traceLogOffset(log) }}</span>
        <span class="log-level" :class="`log-level--${log.level}`">{{ log.level }}</span>
        <span class="trace-log-row__body">{{ log.body }}</span>
      </div>
    </div>
  </div>
</template>
