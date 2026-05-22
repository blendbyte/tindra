<script setup lang="ts">
import { ref, computed, watch, watchEffect, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import type { Transaction, Span, TraceError, Log, LogListPage } from '@/api/types'
import { formatDuration } from '@/utils/formatters'
import { useTimezone } from '@/composables/useTimezone'
import Icon from '@/components/Icon.vue'

const route = useRoute()
const router = useRouter()
const tz = useTimezone()
const txId = computed(() => route.params.id as string)

const collapsedBranches = ref<Set<string>>(new Set())
const openDetails = ref<Set<string>>(new Set())
const expandedGroups = ref<Set<string>>(new Set())
const focusedIdx = ref<number | null>(null)
const copiedTraceId = ref(false)
const searchInputRef = ref<HTMLInputElement | null>(null)

const AUTO_GROUP_THRESHOLD = 4

type SpanRow = { kind: 'span'; span: Span; depth: number }
type GroupRow = { kind: 'group'; key: string; op: string; count: number; depth: number; spans: Span[] }
type DisplayRow = SpanRow | GroupRow

const {
  data: tx,
  isLoading: isTxLoading,
  isError: isTxError,
  refetch: refetchTx,
} = useQuery({
  queryKey: computed(() => ['transactions', txId.value]),
  queryFn: () => apiFetch<Transaction>(`/api/transactions/${txId.value}`),
})

const {
  data: spans,
  isLoading: isSpansLoading,
  isError: isSpansError,
  refetch: refetchSpans,
} = useQuery({
  queryKey: computed(() => ['transactions', txId.value, 'spans']),
  queryFn: () => apiFetch<Span[]>(`/api/transactions/${txId.value}/spans`),
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

const total = computed(() => tx.value?.duration_ms ?? 1)

const criticalSpanCount = computed(() => spanList.value.filter(s => s.is_critical).length)

const critPathEndMs = computed(() => {
  const critical = spanList.value.filter(s => s.is_critical)
  if (critical.length === 0) return 0
  return Math.max(...critical.map(s => s.start_offset_ms + s.duration_ms))
})

const hasCriticalPath = computed(() => criticalSpanCount.value > 0 && spanList.value.length > 1)

const ticks = computed(() => {
  const t = total.value
  const step = t <= 250 ? 50 : t <= 1000 ? 100 : 200
  const out: number[] = []
  for (let v = 0; v <= t; v += step) out.push(v)
  if (out[out.length - 1] !== t) out.push(t)
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

  function walkChildren(s: Span, depth: number): DisplayRow[] {
    const kids = children.get(s.span_id) ?? []
    return processLevel(kids, depth)
  }

  // Groups consecutive siblings sharing the same op base into a single collapsible row
  // when there are AUTO_GROUP_THRESHOLD or more of them.
  function processLevel(siblings: Span[], depth: number): DisplayRow[] {
    const rows: DisplayRow[] = []
    let i = 0
    while (i < siblings.length) {
      const s = siblings[i]
      const base = s.op.split('.')[0]
      let j = i + 1
      while (j < siblings.length && siblings[j].op.split('.')[0] === base) j++
      const runLen = j - i

      if (runLen >= AUTO_GROUP_THRESHOLD) {
        const groupSpans = siblings.slice(i, j)
        const key = `grp:${groupSpans[0].span_id}:${groupSpans[groupSpans.length - 1].span_id}`
        rows.push({ kind: 'group', key, op: base, count: runLen, depth, spans: groupSpans })
        if (expandedGroups.value.has(key)) {
          for (const gs of groupSpans) {
            rows.push({ kind: 'span', span: gs, depth })
            if (!collapsedBranches.value.has(gs.span_id)) {
              rows.push(...walkChildren(gs, depth + 1))
            }
          }
        }
        i = j
      } else {
        rows.push({ kind: 'span', span: s, depth })
        if (!collapsedBranches.value.has(s.span_id)) {
          rows.push(...walkChildren(s, depth + 1))
        }
        i++
      }
    }
    return rows
  }

  return processLevel(roots, 0)
})

// displayRows: hierarchical (with grouping) when no search active, flat filtered list during search.
const displayRows = computed((): DisplayRow[] => {
  if (spanQuery.value.trim()) {
    return filteredSpans.value.map(s => ({ kind: 'span' as const, span: s, depth: 0 }))
  }
  return spanTree.value
})

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
  const s = new Set(expandedGroups.value)
  if (s.has(key)) s.delete(key)
  else s.add(key)
  expandedGroups.value = s
}

function expandAll() {
  collapsedBranches.value = new Set()
}

function collapseAll() {
  collapsedBranches.value = new Set(spanHasChildren.value)
  expandedGroups.value = new Set()
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
  }
}

onMounted(() => document.addEventListener('keydown', handleKeydown))
onUnmounted(() => document.removeEventListener('keydown', handleKeydown))

// Trace log correlation - only fetch when the transaction has a trace_id.
const traceId = computed(() => tx.value?.trace_id ?? '')
const projectId = computed(() => tx.value?.project_id ?? '')

const { data: traceLogs } = useQuery({
  queryKey: computed(() => ['trace-logs', traceId.value]),
  queryFn: () => {
    const params = new URLSearchParams({ trace_id: traceId.value, limit: '50' } as Record<string, string>)
    if (projectId.value) params.append('project_id', projectId.value)
    return apiFetch<LogListPage>(`/api/logs?${params}`)
  },
  enabled: computed(() => !!traceId.value),
})

const traceLogList = computed(() => traceLogs.value?.logs ?? [])

// Trace error correlation - errors sharing this transaction's trace_id.
const { data: traceErrorsData } = useQuery({
  queryKey: computed(() => ['transactions', txId.value, 'errors']),
  queryFn: () => apiFetch<TraceError[]>(`/api/transactions/${txId.value}/errors`),
  enabled: computed(() => !!txId.value),
})
const traceErrorList = computed(() => traceErrorsData.value ?? [])

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
    <div style="flex: 1; min-height: 0; display: grid; grid-template-columns: 35% 1fr">
      <div style="border-right: 1px solid var(--border); overflow: hidden">
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
      <div class="detail-breadcrumb__title mono" style="overflow: hidden; text-overflow: ellipsis; white-space: nowrap">
        {{ tx.transaction }}
      </div>
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
        <div class="stat stat--copyable" :title="copiedTraceId ? 'Copied!' : 'Click to copy full trace ID'" @click="copyTraceId">
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
    </div>

    <!-- Waterfall -->
    <div v-if="!isSpansError" style="flex: 1; min-height: 0; display: grid; grid-template-columns: 35% 1fr">

      <!-- Left: span tree -->
      <div style="border-right: 1px solid var(--border); overflow: auto; display: flex; flex-direction: column">
        <div class="span-row span-row--header">
          <span>Span</span>
          <div style="display: flex; align-items: center; gap: 8px">
            <span>Duration</span>
            <template v-if="spanList.length > 0">
              <button class="span-tree-btn" title="Expand all" @click="expandAll">
                <Icon name="chevrons-down" :size="11" />
              </button>
              <button class="span-tree-btn" title="Collapse all" @click="collapseAll">
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
          <template v-for="(row, i) in displayRows" :key="row.kind === 'span' ? row.span.id : row.key">

            <!-- Group row: collapsed set of repeated op spans -->
            <div
              v-if="row.kind === 'group'"
              class="span-row span-row--group"
              :class="{ 'span-row--focused': focusedIdx === i }"
              @click="toggleGroup(row.key)"
            >
              <div class="span-name" :style="{ paddingLeft: row.depth * 12 + 'px' }">
                <span class="span-name__caret" :class="{ 'span-name__caret--open': expandedGroups.has(row.key) }">
                  <Icon :name="expandedGroups.has(row.key) ? 'chevron-down' : 'chevron-right'" :size="10" />
                </span>
                <span class="span-name__dot" :style="{ background: opColor(row.op) }" />
                <span class="span-name__text">{{ row.count }} × {{ row.op }}</span>
              </div>
              <span class="span-row__dur">{{ row.count }} spans</span>
            </div>

            <!-- Span row -->
            <template v-else-if="row.kind === 'span'">
              <div
                class="span-row"
                :class="{
                  'span-row--open': openDetails.has(row.span.id),
                  'span-row--focused': focusedIdx === i,
                }"
                :style="{ opacity: hasCriticalPath && !row.span.is_critical ? 0.45 : 1 }"
                @click="toggleDetail(row.span.id)"
              >
                <div class="span-name" :style="{ paddingLeft: row.depth * 12 + 'px' }">
                  <!-- Chevron: only rendered for parent spans; click collapses/expands children -->
                  <span
                    v-if="spanHasChildren.has(row.span.span_id)"
                    class="span-name__caret"
                    :class="{ 'span-name__caret--open': !collapsedBranches.has(row.span.span_id) }"
                    @click.stop="toggleBranch(row.span.span_id)"
                  >
                    <Icon :name="collapsedBranches.has(row.span.span_id) ? 'chevron-right' : 'chevron-down'" :size="10" />
                  </span>
                  <span v-else class="span-name__caret" />
                  <span
                    class="span-name__dot"
                    :style="{
                      background: opColor(row.span.op),
                      outline: row.span.is_critical ? `2px solid ${CRITICAL_COLOR}` : 'none',
                      outlineOffset: '1px',
                    }"
                  />
                  <span class="span-name__text">{{ row.span.description || row.span.op }}</span>
                  <span
                    v-if="errorsBySpanId.get(row.span.span_id)?.length"
                    class="span-error-badge"
                    :title="`${errorsBySpanId.get(row.span.span_id)!.length} error(s) on this span`"
                  >{{ errorsBySpanId.get(row.span.span_id)!.length }}</span>
                </div>
                <div class="span-row__right">
                  <button
                    class="span-detail-btn"
                    :class="{ 'span-detail-btn--active': openDetails.has(row.span.id) }"
                    :title="openDetails.has(row.span.id) ? 'Close details' : 'Open details'"
                    @click.stop="toggleDetail(row.span.id)"
                  >
                    <Icon name="info" :size="10" />
                  </button>
                  <span class="span-row__dur" :class="{ 'span-row__dur--crit': row.span.id === maxDurationSpanId }">
                    {{ formatDuration(row.span.duration_ms) }}
                  </span>
                </div>
              </div>
              <div v-if="openDetails.has(row.span.id)" class="span-detail">
                <span class="span-detail__k">description</span>
                <span class="span-detail__v">{{ row.span.description || '–' }}</span>
                <span class="span-detail__k">op</span>
                <span class="span-detail__v">{{ row.span.op }}</span>
                <span class="span-detail__k">duration</span>
                <span class="span-detail__v">
                  {{ formatDuration(row.span.duration_ms) }}
                  <span style="color: var(--text-3)"> ({{ ((row.span.duration_ms / total) * 100).toFixed(1) }}% of total)</span>
                </span>
                <span class="span-detail__k">start offset</span>
                <span class="span-detail__v">+{{ row.span.start_offset_ms }}ms</span>
                <span class="span-detail__k">status</span>
                <span class="span-detail__v">{{ row.span.status }}</span>
                <span v-if="row.span.is_critical" class="span-detail__k">critical path</span>
                <span v-if="row.span.is_critical" class="span-detail__v" :style="{ color: CRITICAL_COLOR }">yes</span>
              </div>
            </template>

          </template>
        </template>

        <!-- Summary label aligned with the timeline summary row -->
        <div v-if="hasCriticalPath" class="span-row" style="opacity: 1; cursor: default; border-top: 1px solid var(--border); margin-top: 2px">
          <div class="span-name">
            <span class="span-name__caret" style="opacity: 0" />
            <span class="span-name__dot" :style="{ background: CRITICAL_COLOR }" />
            <span class="span-name__text" :style="{ color: CRITICAL_COLOR, fontWeight: 500 }">
              Critical path
            </span>
          </div>
          <span class="span-row__dur" :style="{ color: CRITICAL_COLOR }">
            {{ criticalSpanCount }} spans
          </span>
        </div>
      </div>

      <!-- Right: timeline -->
      <div class="timeline" style="overflow-x: hidden; overflow-y: auto; position: relative">
        <div
          v-for="v in ticks"
          :key="`tl-${v}`"
          class="timeline__tick"
          :style="{ left: `${(v / total) * 100}%` }"
        />
        <div class="timeline__axis">
          <template v-for="v in ticks" :key="v">
            <span
              class="timeline__tick-label"
              :style="{
                left: `${(v / total) * 100}%`,
                transform: v === total ? 'translateX(calc(-100% - 2px))' : 'translateX(4px)',
              }"
            >
              {{ v === 0 ? '0' : formatDuration(v) }}
            </span>
          </template>
        </div>

        <div v-if="isSpansLoading">
          <div v-for="i in 8" :key="i" class="timeline__row" style="cursor: default; display: flex; align-items: center; padding: 0 16px">
            <span class="skel" :style="{ marginLeft: `${(i % 4) * 10}%`, width: `${20 + (i % 5) * 12}%`, height: '12px', borderRadius: '2px' }" />
          </div>
        </div>

        <div
          v-for="(row, i) in displayRows"
          :key="row.kind === 'span' ? row.span.id : row.key"
          class="timeline__row"
          :class="{ 'timeline__row--focused': focusedIdx === i }"
          @click="row.kind === 'group' ? toggleGroup(row.key) : toggleDetail(row.span.id)"
        >
          <!-- Group: single bar spanning the full op group range -->
          <template v-if="row.kind === 'group'">
            <div
              class="timeline__bar"
              :style="{
                left: `${(Math.min(...row.spans.map(s => s.start_offset_ms)) / total) * 100}%`,
                width: `max(4px, ${((Math.max(...row.spans.map(s => s.start_offset_ms + s.duration_ms)) - Math.min(...row.spans.map(s => s.start_offset_ms))) / total) * 100}%)`,
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
                left: `${(row.span.start_offset_ms / total) * 100}%`,
                width: `max(4px, ${(row.span.duration_ms / total) * 100}%)`,
                background: opColor(row.span.op),
                opacity: hasCriticalPath && !row.span.is_critical ? 0.3 : (openDetails.has(row.span.id) ? 1 : 0.85),
                outline: row.span.is_critical ? `1.5px solid ${CRITICAL_COLOR}` : 'none',
                outlineOffset: '1px',
              }"
              :title="`${row.span.description || row.span.op} · ${formatDuration(row.span.duration_ms)}`"
            >
              <span v-if="(row.span.duration_ms / total) * 100 > 8" class="timeline__bar-label">
                {{ formatDuration(row.span.duration_ms) }}
              </span>
            </div>
            <span
              v-if="errorsBySpanId.get(row.span.span_id)?.length"
              class="timeline__error-dot"
              :style="{ left: `calc(${(row.span.start_offset_ms / total) * 100}% + max(4px, ${(row.span.duration_ms / total) * 100}%) - 2px)` }"
              :title="`${errorsBySpanId.get(row.span.span_id)!.length} error(s)`"
            />
          </template>
        </div>

        <!-- Critical path summary row -->
        <div
          v-if="hasCriticalPath"
          class="timeline__row"
          style="cursor: default; border-top: 1px solid var(--border); margin-top: 2px"
        >
          <div
            class="timeline__bar"
            :style="{
              left: '0%',
              width: `${(critPathEndMs / total) * 100}%`,
              background: CRITICAL_COLOR,
              opacity: 0.18,
              outline: `1.5px solid ${CRITICAL_COLOR}`,
              outlineOffset: '0px',
            }"
          >
            <span
              class="timeline__bar-label"
              :style="{ color: CRITICAL_COLOR, fontWeight: 600, opacity: 1 }"
            >
              critical path · {{ criticalSpanCount }} spans · {{ formatDuration(critPathEndMs) }}
            </span>
          </div>
        </div>
      </div>
    </div>

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
