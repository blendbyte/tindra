<script setup lang="ts">
import { ref, computed, reactive, watch, watchEffect, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery, useMutation, useQueryClient, keepPreviousData } from '@tanstack/vue-query'
import { useToast } from '@/composables/useToast'
import { apiFetch } from '@/api/client'
import { useFormatters } from '@/composables/useFormatters'
import { useTimezone } from '@/composables/useTimezone'
import { useIssueNavStore } from '@/stores/issueNav'
import type { Issue, Event as TindraEvent, Comment, User, TagSummary, IssueHistoryEntry, EventSummary, EventListPage, HistogramBucket } from '@/api/types'
import Icon from '@/components/Icon.vue'
import TimeseriesChart from '@/components/TimeseriesChart.vue'
import IgnoreButton from '@/components/IgnoreButton.vue'
import type { IgnorePayload } from '@/components/IgnoreButton.vue'
import CodeContext from '@/components/CodeContext.vue'

const route = useRoute()
const router = useRouter()
const { show: showToast } = useToast()
const qc = useQueryClient()
const navStore = useIssueNavStore()
const { formatRel, formatTs } = useFormatters()
const tz = useTimezone()

const { data: me } = useQuery({
  queryKey: ['me'],
  queryFn: () => apiFetch<User>('/api/me'),
})

const issueId = computed(() => route.params.id as string)
const prevIssueId = computed(() => navStore.prevId(issueId.value))
const nextIssueId = computed(() => navStore.nextId(issueId.value))

function goPrev() { if (prevIssueId.value) router.push(`/issues/${prevIssueId.value}`) }
function goNext() { if (nextIssueId.value) router.push(`/issues/${nextIssueId.value}`) }

const showSystem = ref(false)
const showRawStack = ref(false)

const SECTION_DEFAULTS: Record<string, boolean> = {
  breadcrumbs: false,
  context: false,
  packages: true,
  events: false,
  tags: false,
  activity: false,
  request: false,
  trace: false,
}

function loadCollapsed(): Record<string, boolean> {
  try {
    const stored = localStorage.getItem('tindra:issue:collapsed')
    if (stored) return { ...SECTION_DEFAULTS, ...JSON.parse(stored) }
  } catch { /* ignore */ }
  return { ...SECTION_DEFAULTS }
}

const collapsed = reactive(loadCollapsed())

function toggleSection(key: string) {
  collapsed[key] = !collapsed[key]
  try { localStorage.setItem('tindra:issue:collapsed', JSON.stringify(collapsed)) } catch { /* ignore */ }
}
const copiedStack = ref(false)

function copyStack() {
  navigator.clipboard.writeText(rawStackText.value).then(() => {
    copiedStack.value = true
    setTimeout(() => { copiedStack.value = false }, 2000)
  })
}
const eventIndex = ref(0)
const commentBody = ref('')
const assignOpen = ref(false)
const assignEl = ref<HTMLElement | null>(null)
const showEventList = ref(false)
const loadedEvents = ref<EventSummary[]>([])
const eventListHasMore = ref(false)
const eventListLoading = ref(false)
const eventListCursor = ref<{ time: string; id: string } | null>(null)
const evtSortCol = ref<'received_at' | 'level' | 'environment' | 'release'>('received_at')
const evtSortDir = ref<'asc' | 'desc'>('desc')

const { data: issue, isError: issueError, refetch: refetchIssue } = useQuery({
  queryKey: computed(() => ['issues', issueId.value]),
  queryFn: () => apiFetch<Issue>(`/api/issues/${issueId.value}`),
})

watchEffect(() => {
  if (issue.value?.title) document.title = `${issue.value.title} - Tindra`
})

const { data: currentEvent } = useQuery({
  queryKey: computed(() => ['issues', issueId.value, 'events', eventIndex.value]),
  queryFn: () => apiFetch<TindraEvent>(`/api/issues/${issueId.value}/events/latest?offset=${eventIndex.value}`),
  enabled: computed(() => !!issueId.value && !!issue.value && issue.value.kind !== 'n1_query'),
  placeholderData: keepPreviousData,
})

const { data: perfEvents, isLoading: perfEventsLoading } = useQuery({
  queryKey: computed(() => ['issues', issueId.value, 'perf-events']),
  queryFn: () => apiFetch<import('@/api/types').PerfEvent[]>(`/api/issues/${issueId.value}/perf-events`),
  enabled: computed(() => !!issueId.value && issue.value?.kind === 'n1_query'),
})

const { data: comments = [] } = useQuery({
  queryKey: computed(() => ['issues', issueId.value, 'comments']),
  queryFn: () => apiFetch<Comment[]>(`/api/issues/${issueId.value}/comments`),
  enabled: computed(() => !!issueId.value),
})

const { data: history = [] } = useQuery({
  queryKey: computed(() => ['issues', issueId.value, 'history']),
  queryFn: () => apiFetch<IssueHistoryEntry[]>(`/api/issues/${issueId.value}/history`),
  enabled: computed(() => !!issueId.value),
})

const { data: users = [] } = useQuery({
  queryKey: ['users'],
  queryFn: () => apiFetch<User[]>('/api/users'),
})

const { mutate: updateStatus, isPending: updatingStatus } = useMutation({
  mutationFn: (body: Record<string, unknown>) =>
    apiFetch(`/api/issues/${issueId.value}`, { method: 'PATCH', body: JSON.stringify(body) }),
  onSuccess: () => {
    qc.invalidateQueries({ queryKey: ['issues'] })
    qc.invalidateQueries({ queryKey: ['issues', issueId.value] })
  },
})

const { mutate: assignIssue } = useMutation({
  mutationFn: (assigneeId: string | null) =>
    apiFetch(`/api/issues/${issueId.value}`, {
      method: 'PATCH',
      body: JSON.stringify({ assignee_id: assigneeId }),
    }),
  onSuccess: () => {
    qc.invalidateQueries({ queryKey: ['issues', issueId.value] })
    qc.invalidateQueries({ queryKey: ['issues'] })
    assignOpen.value = false
  },
})

const { mutate: postComment, isPending: postingComment } = useMutation({
  mutationFn: (body: string) =>
    apiFetch(`/api/issues/${issueId.value}/comments`, {
      method: 'POST',
      body: JSON.stringify({ body }),
    }),
  onSuccess: () => {
    commentBody.value = ''
    qc.invalidateQueries({ queryKey: ['issues', issueId.value, 'comments'] })
  },
})

// Comment editing state
const editingCommentId = ref<string | null>(null)
const editingBody = ref('')

function startEdit(comment: Comment) {
  editingCommentId.value = comment.id
  editingBody.value = comment.body
}

function cancelEdit() {
  editingCommentId.value = null
  editingBody.value = ''
}

const { mutate: saveEdit, isPending: savingEdit } = useMutation({
  mutationFn: ({ id, body }: { id: string; body: string }) =>
    apiFetch(`/api/comments/${id}`, { method: 'PUT', body: JSON.stringify({ body }) }),
  onSuccess: () => {
    cancelEdit()
    qc.invalidateQueries({ queryKey: ['issues', issueId.value, 'comments'] })
  },
})

const { mutate: deleteComment } = useMutation({
  mutationFn: (id: string) => apiFetch(`/api/comments/${id}`, { method: 'DELETE' }),
  onSuccess: () => qc.invalidateQueries({ queryKey: ['issues', issueId.value, 'comments'] }),
})

function confirmDeleteComment(id: string) {
  if (confirm('Delete this comment?')) deleteComment(id)
}

function canEditComment(comment: Comment) {
  return me.value?.id === comment.user_id
}

function canDeleteComment(comment: Comment) {
  return me.value?.id === comment.user_id || !!me.value?.permissions.manage_issues
}

function setStatus(status: string, undoStatus: string) {
  updateStatus({ status })
  showToast(status === 'resolved' ? 'Resolved' : 'Ignored', () => updateStatus({ status: undoStatus }))
}

function handleIgnore(payload: IgnorePayload) {
  updateStatus(payload)
  showToast('Ignored', () => updateStatus({ status: 'open' }))
}

function handleUnignore() {
  updateStatus({ status: 'open' })
  showToast('Unignored', () => updateStatus({ status: 'ignored' }))
}


const frames = computed(() => {
  const exc = currentEvent.value?.payload?.exception as { values?: { stacktrace?: { frames?: unknown[] } }[] } | undefined
  const fs = exc?.values?.[0]?.stacktrace?.frames ?? []
  return [...fs].reverse()
})

const eventPlatform = computed(() => (currentEvent.value?.payload as Record<string, unknown> | undefined)?.platform as string | undefined)

const rawStackText = computed(() => {
  const exc = currentEvent.value?.payload?.exception as { values?: { type?: string; value?: string; stacktrace?: { frames?: Record<string, unknown>[] } }[] } | undefined
  if (!exc?.values?.length) return ''
  return exc.values.map((ex) => {
    const header = [ex.type, ex.value].filter(Boolean).join(': ')
    const fs = ex.stacktrace?.frames ?? []
    const frameLines = [...fs].reverse().map((f) => {
      const loc = [f['filename'] ?? f['module'], f['lineno'] ? `line ${f['lineno']}` : null, f['function'] ? `in ${f['function']}` : null].filter(Boolean).join(', ')
      const code = f['context_line'] ? `\n    ${String(f['context_line']).trim()}` : ''
      return `  File "${loc}"${code}`
    })
    return [header, 'Traceback (most recent call last):', ...frameLines].join('\n')
  }).join('\n\n')
})

const isUnhandled = computed(() => {
  const exc = currentEvent.value?.payload?.exception as { values?: { mechanism?: { handled?: boolean } }[] } | undefined
  const handled = exc?.values?.[0]?.mechanism?.handled
  return handled === false || handled === null
})

const breadcrumbs = computed(() => {
  const bc = currentEvent.value?.payload?.breadcrumbs as { values?: unknown[] } | undefined
  return bc?.values ?? []
})

const expandedCrumbs = ref(new Set<number>())
watch(currentEvent, () => { expandedCrumbs.value = new Set() })

function toggleCrumb(i: number) {
  const s = new Set(expandedCrumbs.value)
  if (s.has(i)) s.delete(i); else s.add(i)
  expandedCrumbs.value = s
}

// JS SDK sends breadcrumb timestamps as Unix float seconds (e.g. 1716739678.943),
// not ISO strings. Handle both so formatTs always receives a valid ISO string.
function formatCrumbTime(ts: unknown): string {
  if (typeof ts === 'number') return formatTs(new Date(ts * 1000).toISOString())
  if (typeof ts === 'string') return formatTs(ts)
  return ''
}

const CONTEXT_LABELS: Record<string, string> = {
  runtime: 'Runtime',
  os: 'Operating System',
  browser: 'Browser',
  device: 'Device',
  app: 'App',
  gpu: 'GPU',
  culture: 'Culture',
}

const CONTEXT_ICONS: Record<string, string> = {
  browser: 'globe',
  device: 'squares',
  os: 'activity',
  runtime: 'zap',
  app: 'package',
  gpu: 'activity',
}

const CTX_SKIP_KEYS = new Set(['type', 'raw_description'])

const contexts = computed<{ key: string; label: string; icon: string; rows: [string, string][] }[]>(() => {
  const raw = currentEvent.value?.payload?.contexts as Record<string, unknown> | undefined
  if (!raw || typeof raw !== 'object') return []

  return Object.entries(raw)
    .filter(([key, val]) => key !== 'trace' && val !== null && typeof val === 'object' && !Array.isArray(val))
    .map(([key, val]) => {
      const obj = val as Record<string, unknown>
      const rows: [string, string][] = Object.entries(obj)
        .filter(([k, v]) => !CTX_SKIP_KEYS.has(k) && v !== null && v !== undefined && typeof v !== 'object')
        .map(([k, v]) => [k.replace(/_/g, ' '), String(v)])
      return {
        key,
        label: CONTEXT_LABELS[key] ?? key.charAt(0).toUpperCase() + key.slice(1),
        icon: CONTEXT_ICONS[key] ?? 'info',
        rows,
      }
    })
    .filter((c) => c.rows.length > 0)
})

const modules = computed<[string, string][]>(() => {
  const raw = currentEvent.value?.payload?.modules as Record<string, unknown> | undefined
  if (!raw || typeof raw !== 'object') return []
  return Object.entries(raw)
    .filter(([, v]) => v !== null && v !== undefined)
    .map(([k, v]) => [k, String(v)])
    .sort(([a], [b]) => a.localeCompare(b))
})

const showAllModules = ref(false)
const MODULES_PREVIEW = 10

function formatRequestBody(data: unknown): string {
  if (data === null || data === undefined) return ''
  if (typeof data === 'string') {
    try { return JSON.stringify(JSON.parse(data), null, 2) } catch { return data }
  }
  return JSON.stringify(data, null, 2)
}

// Sentry sends header values as ["value"] arrays; unwrap to plain string.
function unwrapHeaderVal(v: unknown): string {
  if (Array.isArray(v)) return v.map(String).join(', ')
  return String(v ?? '')
}

const BODY_PREVIEW_LINES = 15
const reqBodyExpanded = ref(false)
watch(currentEvent, () => { reqBodyExpanded.value = false })

const httpRequest = computed(() => {
  const req = currentEvent.value?.payload?.request as Record<string, unknown> | undefined
  if (!req || typeof req !== 'object') return null
  return {
    method: req.method as string | undefined,
    url: req.url as string | undefined,
    headers: req.headers as Record<string, unknown> | undefined,
    data: req.data,
    query_string: req.query_string,
    cookies: req.cookies as Record<string, unknown> | undefined,
    env: req.env as Record<string, string> | undefined,
  }
})

const reqBodyFull = computed(() => formatRequestBody(httpRequest.value?.data))
const reqBodyLines = computed(() => reqBodyFull.value.split('\n'))
const reqBodyTruncated = computed(() => reqBodyLines.value.length > BODY_PREVIEW_LINES)
const reqBodyDisplay = computed(() =>
  reqBodyExpanded.value || !reqBodyTruncated.value
    ? reqBodyFull.value
    : reqBodyLines.value.slice(0, BODY_PREVIEW_LINES).join('\n')
)

// Derive browser / OS / device from User-Agent when not in payload contexts.
function parseUAContexts(ua: string): Record<string, { label: string; icon: string; rows: [string, string][] }> {
  const out: Record<string, { label: string; icon: string; rows: [string, string][] }> = {}

  // Browser
  let bName = '', bVersion = ''
  const edgeM = ua.match(/Edg(?:e|A|iOS)?\/(\S+)/)
  const chromeM = ua.match(/(?:Chrome|CrMo|CriOS)\/(\d+(?:\.\d+)*)/)
  const ffM = ua.match(/Firefox\/(\d+(?:\.\d+)*)/)
  const safM = ua.match(/Version\/(\S+)\s+(?:Mobile\s+)?Safari/)
  if (edgeM) { bName = 'Edge'; bVersion = edgeM[1] }
  else if (chromeM) { bName = ua.includes('Mobile') ? 'Chrome Mobile' : 'Chrome'; bVersion = chromeM[1] }
  else if (ffM) { bName = ua.includes('Mobile') ? 'Firefox Mobile' : 'Firefox'; bVersion = ffM[1] }
  else if (safM) { bName = ua.includes('Mobile') ? 'Mobile Safari' : 'Safari'; bVersion = safM[1] }
  if (bName) {
    const rows: [string, string][] = [['name', bName]]
    if (bVersion) rows.push(['version', bVersion])
    out.browser = { label: 'Browser', icon: 'globe', rows }
  }

  // OS
  let osName = '', osVersion = ''
  const androidM = ua.match(/Android (\d+(?:[._]\d+)*)/)
  const iosM = ua.match(/(?:iPhone|iPad)[^;]*; CPU (?:iPhone )?OS (\d+_\d+)/)
  const winM = ua.match(/Windows NT (\d+\.\d+)/)
  const macM = ua.match(/Mac OS X (\d+[._]\d+)/)
  if (androidM) { osName = 'Android'; osVersion = androidM[1] }
  else if (iosM) { osName = ua.includes('iPad') ? 'iPadOS' : 'iOS'; osVersion = iosM[1].replace(/_/g, '.') }
  else if (winM) { osName = 'Windows'; osVersion = winM[1] }
  else if (macM) { osName = 'macOS'; osVersion = macM[1].replace(/[_]/g, '.') }
  else if (/Linux/.test(ua) && !androidM) osName = 'Linux'
  if (osName) {
    const rows: [string, string][] = [['name', osName]]
    if (osVersion) rows.push(['version', osVersion])
    out.os = { label: 'Operating System', icon: 'activity', rows }
  }

  // Device (Android model from UA)
  const devM = ua.match(/Android \d+(?:[._]\d+)*;\s*([^)]+)\)/)
  if (devM) {
    const model = devM[1].trim().replace(/\s+Build$/, '')
    if (model && model !== 'wv') {
      out.device = { label: 'Device', icon: 'squares', rows: [['model', model]] }
    }
  }

  return out
}

const displayContexts = computed(() => {
  const list = [...contexts.value]
  const existingKeys = new Set(list.map(c => c.key))

  const headers = httpRequest.value?.headers
  if (headers) {
    const uaEntry = Object.entries(headers).find(([k]) => k.toLowerCase() === 'user-agent')
    const ua = uaEntry ? unwrapHeaderVal(uaEntry[1]) : ''
    if (ua) {
      const derived = parseUAContexts(ua)
      for (const key of ['browser', 'device', 'os'] as const) {
        if (!existingKeys.has(key) && derived[key]) {
          list.unshift({ key, ...derived[key] })
        }
      }
    }
  }
  return list
})

const traceId = computed(() => {
  if (currentEvent.value?.trace_id) return currentEvent.value.trace_id
  const ctx = currentEvent.value?.payload?.contexts as Record<string, unknown> | undefined
  return (ctx?.trace as Record<string, unknown> | undefined)?.trace_id as string | undefined
})

const { data: linkedTransaction } = useQuery({
  queryKey: computed(() => ['issues', issueId.value, 'trace', eventIndex.value]),
  queryFn: () => apiFetch<import('@/api/types').Transaction | null>(`/api/issues/${issueId.value}/trace?offset=${eventIndex.value}`),
  enabled: computed(() => !!issueId.value && !!traceId.value),
})

const { data: traceSpans } = useQuery({
  queryKey: computed(() => ['transactions', linkedTransaction.value?.id, 'spans']),
  queryFn: () => apiFetch<import('@/api/types').Span[]>(`/api/transactions/${linkedTransaction.value!.id}/spans`),
  enabled: computed(() => !!linkedTransaction.value?.id),
})

const TRACE_PREVIEW_MAX = 10

const tracePreviewRows = computed(() => {
  const tx = linkedTransaction.value
  const spans = traceSpans.value
  if (!tx) return []
  const total = tx.duration_ms || 1
  const rows: { label: string; op: string; left: number; width: number; isTx: boolean }[] = []
  rows.push({ label: tx.transaction, op: tx.op, left: 0, width: 100, isTx: true })
  if (spans) {
    for (const s of spans.slice(0, TRACE_PREVIEW_MAX - 1)) {
      const left = Math.max(0, (s.start_offset_ms / total) * 100)
      const width = Math.max(0.5, (s.duration_ms / total) * 100)
      rows.push({ label: s.description || s.op, op: s.op, left, width, isTx: false })
    }
  }
  return rows
})

const { data: issueTags } = useQuery({
  queryKey: computed(() => ['issues', issueId.value, 'tags']),
  queryFn: () => apiFetch<TagSummary[]>(`/api/issues/${issueId.value}/tags`),
  enabled: computed(() => !!issueId.value),
})

const eventUser = computed(() => {
  const u = currentEvent.value?.payload?.user as Record<string, unknown> | undefined
  if (!u || typeof u !== 'object') return null
  return {
    id:         u.id         != null ? String(u.id)         : null,
    username:   u.username   != null ? String(u.username)   : null,
    email:      u.email      != null ? String(u.email)      : null,
    name:       u.name       != null ? String(u.name)       : null,
    ip_address: u.ip_address != null ? String(u.ip_address) : null,
  }
})

const eventUserInitial = computed(() => {
  const u = eventUser.value
  if (!u) return '?'
  const label = u.name || u.username || u.email || u.id || '?'
  return String(label)[0].toUpperCase()
})

const eventUserLabel = computed(() => {
  const u = eventUser.value
  if (!u) return null
  return u.name || u.username || u.email || u.id
})

const { data: histogram } = useQuery({
  queryKey: computed(() => ['issues', issueId.value, 'histogram']),
  queryFn: () => apiFetch<{ buckets: HistogramBucket[]; bucket_size: 'hour' | 'day' | 'week' }>(`/api/issues/${issueId.value}/events/histogram`),
  enabled: computed(() => !!issueId.value),
})

function formatHistogramTime(iso: string): string {
  const d = new Date(iso)
  const size = histogram.value?.bucket_size
  const zone = tz.value
  if (size === 'hour') {
    const dateStr = d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: zone })
    const fmt = new Intl.DateTimeFormat('en', { hour: 'numeric', hour12: false, timeZone: zone })
    const h = String(parseInt(fmt.format(d), 10) % 24).padStart(2, '0')
    const hNext = String((parseInt(fmt.format(d), 10) % 24 + 1) % 24).padStart(2, '0')
    return `${dateStr}  ${h}:00 – ${hNext}:00`
  }
  if (size === 'day') {
    return d.toLocaleDateString('en-US', { weekday: 'short', month: 'short', day: 'numeric', year: 'numeric', timeZone: zone })
  }
  const end = new Date(d.getTime() + 6 * 24 * 60 * 60 * 1000)
  const s = d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: zone })
  const e = end.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: zone })
  return `${s} – ${e}`
}

const assigneeUser = computed(() =>
  issue.value?.assignee_id
    ? (users.value ?? []).find((u) => u.id === issue.value?.assignee_id)
    : null,
)

type TimelineItem =
  | { kind: 'comment'; time: string; data: Comment }
  | { kind: 'history'; time: string; data: IssueHistoryEntry }

const timeline = computed<TimelineItem[]>(() => {
  const items: TimelineItem[] = [
    ...(comments.value ?? []).map((c) => ({ kind: 'comment' as const, time: c.created_at, data: c })),
    ...(history.value ?? []).map((h) => ({ kind: 'history' as const, time: h.created_at, data: h })),
  ]
  return items.sort((a, b) => new Date(a.time).getTime() - new Date(b.time).getTime())
})

function historyLabel(entry: IssueHistoryEntry): string {
  switch (entry.event_type) {
    case 'created': return 'Issue opened'
    case 'regressed': return 'Regressed'
    case 'status_changed': {
      const to = entry.details.to as string | undefined
      if (to === 'resolved') return 'Resolved'
      if (to === 'ignored') {
        const until = entry.details.ignore_until as string | undefined
        const count = entry.details.ignore_count_limit as number | undefined
        if (until) {
          const d = new Date(until)
          return `Ignored until ${d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', timeZone: tz.value })}`
        }
        if (count != null) return `Ignored for ${count.toLocaleString()} occurrence${count === 1 ? '' : 's'}`
        return 'Ignored forever'
      }
      if (to === 'open') return 'Reopened'
      return `Status changed to ${to ?? 'unknown'}`
    }
    case 'assigned': {
      const toId = entry.details.to_id as string | null | undefined
      if (!toId) return 'Unassigned'
      const u = (users.value ?? []).find((u) => u.id === toId)
      return `Assigned to ${u ? (u.name || u.email) : 'someone'}`
    }
    default: return entry.event_type
  }
}

function historyIcon(eventType: string): string {
  switch (eventType) {
    case 'created': return 'circle-dot'
    case 'regressed': return 'alert-triangle'
    case 'status_changed': return 'git-commit-vertical'
    case 'assigned': return 'user'
    default: return 'git-commit-vertical'
  }
}

function actorName(actorId: string | null): string {
  if (!actorId) return 'System'
  const u = (users.value ?? []).find((u) => u.id === actorId)
  return u ? (u.name || u.email) : 'Unknown'
}

const sortedEvents = computed(() => {
  const col = evtSortCol.value
  const dir = evtSortDir.value === 'asc' ? 1 : -1
  return [...loadedEvents.value].sort((a, b) => {
    const av: string = (a[col] ?? '') as string
    const bv: string = (b[col] ?? '') as string
    return av < bv ? -dir : av > bv ? dir : 0
  })
})

function toggleEvtSort(col: typeof evtSortCol.value) {
  if (evtSortCol.value === col) {
    evtSortDir.value = evtSortDir.value === 'asc' ? 'desc' : 'asc'
  } else {
    evtSortCol.value = col
    evtSortDir.value = col === 'received_at' ? 'desc' : 'asc'
  }
}

const TAG_PRIORITY = ['browser', 'browser.name', 'os', 'os.name', 'runtime', 'server_name', 'url', 'transaction']

function displayTags(tags: Record<string, string>, max = 3): [string, string][] {
  const result: [string, string][] = []
  for (const key of TAG_PRIORITY) {
    if (tags[key] && result.length < max) result.push([key, tags[key]])
  }
  for (const [k, v] of Object.entries(tags)) {
    if (!TAG_PRIORITY.includes(k) && result.length < max) result.push([k, v])
  }
  return result
}

async function loadEventPage(reset = false) {
  if (reset) {
    loadedEvents.value = []
    eventListCursor.value = null
    eventListHasMore.value = false
  }
  eventListLoading.value = true
  try {
    const params = new URLSearchParams()
    if (eventListCursor.value) {
      params.set('cursor_time', eventListCursor.value.time)
      params.set('cursor_id', eventListCursor.value.id)
    }
    const page = await apiFetch<EventListPage>(`/api/issues/${issueId.value}/events?${params}`)
    loadedEvents.value = [...loadedEvents.value, ...page.events]
    eventListHasMore.value = page.has_more
    eventListCursor.value = page.next_cursor_time && page.next_cursor_id
      ? { time: page.next_cursor_time, id: page.next_cursor_id }
      : null
  } finally {
    eventListLoading.value = false
  }
}

function toggleEventList() {
  showEventList.value = !showEventList.value
  if (showEventList.value && loadedEvents.value.length === 0) loadEventPage(true)
}

function selectEvent(ev: EventSummary) {
  const idx = sortedEvents.value.findIndex(e => e.id === ev.id)
  if (idx !== -1) eventIndex.value = idx
}

function submitComment(e: Event) {
  e.preventDefault()
  if (!commentBody.value.trim()) return
  postComment(commentBody.value.trim())
}

function onMouseDown(e: MouseEvent) {
  if (assignEl.value && !assignEl.value.contains(e.target as Node)) {
    assignOpen.value = false
  }
}

function onKey(e: KeyboardEvent) {
  if ((e.target as HTMLElement).tagName === 'INPUT' || (e.target as HTMLElement).tagName === 'TEXTAREA') return
  if (e.metaKey || e.ctrlKey) return
  if (e.key === 'Escape') router.push('/issues')
  else if (e.key === '[') { e.preventDefault(); goPrev() }
  else if (e.key === ']') { e.preventDefault(); goNext() }
  else if (e.key.toLowerCase() === 'e' && !updatingStatus.value && issue.value?.status !== 'resolved') setStatus('resolved', 'open')
  else if (e.key.toLowerCase() === 'i' && !updatingStatus.value && issue.value?.status !== 'ignored') setStatus('ignored', 'open')
  else if (e.key.toLowerCase() === 'a') { e.preventDefault(); assignOpen.value = !assignOpen.value }
  else if (e.key === 'ArrowLeft') eventIndex.value = Math.max(0, eventIndex.value - 1)
  else if (e.key === 'ArrowRight') eventIndex.value = Math.min((issue.value?.event_count ?? 1) - 1, eventIndex.value + 1)
}

onMounted(() => {
  document.addEventListener('keydown', onKey)
  document.addEventListener('mousedown', onMouseDown)
})
onUnmounted(() => {
  document.removeEventListener('keydown', onKey)
  document.removeEventListener('mousedown', onMouseDown)
})
</script>

<template>
  <div v-if="issue" class="page">
    <div class="detail-breadcrumb">
      <a class="detail-breadcrumb__back" href="#" @click.prevent="router.push('/issues')">
        <Icon name="arrow-left" :size="12" />
        Issues
      </a>
      <div v-if="navStore.ids.length > 0" class="detail-breadcrumb__nav">
        <button class="detail-nav-btn" :disabled="!prevIssueId" title="Previous issue [" @click="goPrev">
          <Icon name="chevron-left" :size="14" />
        </button>
        <button class="detail-nav-btn" :disabled="!nextIssueId" title="Next issue ]" @click="goNext">
          <Icon name="chevron-right" :size="14" />
        </button>
      </div>
      <div class="detail-breadcrumb__title"><span>{{ issue.id }}</span></div>
      <div class="detail-breadcrumb__actions">
        <!-- Assign button -->
        <div ref="assignEl" style="position: relative">
          <button
            class="btn"
            :class="{ 'btn--active': !!issue.assignee_id }"
            title="Assign (A)"
            @click="assignOpen = !assignOpen"
          >
            <Icon name="user" :size="12" />
            {{ assigneeUser ? (assigneeUser.name || assigneeUser.email.split('@')[0]) : 'Assign' }}
            <span class="btn__kbd">A</span>
          </button>

          <div v-if="assignOpen" class="popover assign-popover" style="min-width: 220px; right: 0; top: calc(100% + 4px)">
            <div class="popover__list">
              <div
                class="popover__item"
                :class="{ 'popover__item--active': !issue.assignee_id }"
                @click="assignIssue(null)"
              >
                <span class="popover__check" :class="{ 'popover__check--on': !issue.assignee_id }">
                  <Icon v-if="!issue.assignee_id" name="check" :size="10" />
                </span>
                <span style="color: var(--text-2)">Unassigned</span>
              </div>
              <div
                v-for="u in users"
                :key="u.id"
                class="popover__item"
                :class="{ 'popover__item--active': issue.assignee_id === u.id }"
                @click="assignIssue(u.id)"
              >
                <span class="popover__check" :class="{ 'popover__check--on': issue.assignee_id === u.id }">
                  <Icon v-if="issue.assignee_id === u.id" name="check" :size="10" />
                </span>
                <span class="assign-avatar">{{ (u.name || u.email)[0].toUpperCase() }}</span>
                <span>{{ u.name || u.email }}</span>
              </div>
            </div>
          </div>
        </div>

        <IgnoreButton
          v-if="issue?.status !== 'ignored'"
          direction="down"
          :disabled="updatingStatus"
          @ignore="handleIgnore"
        />
        <button
          v-else
          class="btn"
          :disabled="updatingStatus"
          @click="handleUnignore"
        >
          Unignore
        </button>
        <button class="btn btn--primary" :disabled="issue?.status === 'resolved' || updatingStatus" @click="setStatus('resolved', 'open')">
          Resolve <span class="btn__kbd">E</span>
        </button>
      </div>
    </div>

    <div class="detail-body">
      <div class="detail-hero">
        <div class="detail-hero__eyebrow">
          <template v-if="issue.kind === 'n1_query'">
            <span class="kindbadge kindbadge--filled">N+1</span>
            <span class="detail-hero__level">performance</span>
          </template>
          <template v-else>
            <span class="leveldot" :class="`leveldot--${issue.level}`" />
            <span class="detail-hero__level">{{ issue.level }}</span>
          </template>
          <span v-if="isUnhandled" class="detail-hero__unhandled">Unhandled</span>
          <span class="detail-hero__sep">·</span>
          <span class="detail-hero__project">{{ issue.project_id }}</span>
          <template v-if="issue.environment">
            <span class="detail-hero__sep">·</span>
            <span class="detail-hero__env" :class="issue.environment === 'production' ? 'detail-hero__env--prod' : ''">{{ issue.environment }}</span>
          </template>
          <template v-if="issue.release">
            <span class="detail-hero__sep">·</span>
            <RouterLink v-if="issue.release_id" :to="`/releases/${issue.release_id}`" class="detail-hero__release detail-hero__release--link">{{ issue.release }}</RouterLink>
            <span v-else class="detail-hero__release">{{ issue.release }}</span>
          </template>
        </div>

        <h1 class="detail-hero__title">
          <span class="mono">{{ issue.title.split(':')[0] }}</span><template v-if="issue.title.includes(':')">: {{ issue.title.split(':').slice(1).join(':').trim() }}</template>
        </h1>

        <div class="issue-meta">
          <span class="issue-meta__item" :title="new Date(issue.first_seen).toUTCString()">
            <span class="issue-meta__k">First</span> {{ formatRel(issue.first_seen) }}
          </span>
          <span class="issue-meta__dot" />
          <span class="issue-meta__item" :title="new Date(issue.last_seen).toUTCString()">
            <span class="issue-meta__k">Last</span> {{ formatRel(issue.last_seen) }}
          </span>
          <span class="issue-meta__dot" />
          <span class="issue-meta__item">
            {{ issue.event_count.toLocaleString() }} {{ issue.event_count === 1 ? 'event' : 'events' }}
          </span>
          <span class="issue-meta__dot" />
          <span class="issue-meta__item">
            {{ (issue.user_count ?? 0).toLocaleString() }} {{ (issue.user_count ?? 0) === 1 ? 'user' : 'users' }}
          </span>
          <span class="issue-meta__dot" />
          <span class="issue-meta__item">
            <span class="statuspill" :class="`statuspill--${issue.status}`">{{ issue.status }}</span>
            <template v-if="issue.status === 'ignored' && issue.ignore_until">
              <span class="issue-meta__k"> until {{ new Date(issue.ignore_until).toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: tz }) }}</span>
            </template>
            <template v-else-if="issue.status === 'ignored' && issue.ignore_count_limit != null">
              <span class="issue-meta__k"> · {{ Math.max(0, issue.ignore_count_limit - (issue.ignore_count ?? 0)) }} left</span>
            </template>
          </span>
          <span class="issue-meta__dot" />
          <span class="issue-meta__item">
            <span class="issue-meta__k">{{ assigneeUser ? '' : 'Unassigned' }}</span>
            <span v-if="assigneeUser">{{ assigneeUser.name || assigneeUser.email }}</span>
          </span>
        </div>

        <!-- Frequency histogram -->
        <div v-if="histogram && histogram.buckets.length > 1" class="hgram-wrap">
          <TimeseriesChart
            :times="histogram.buckets.map(b => b.time)"
            :series="[{ id: 'events', label: 'Events', type: 'bar', values: histogram.buckets.map(b => b.count) }]"
            :bucket-size="histogram.bucket_size"
            :height="104"
            :grid-lines="1"
            :format-time="formatHistogramTime"
          />
        </div>
      </div>

      <div class="detail-main">

      <!-- N+1 performance issue section -->
      <template v-if="issue.kind === 'n1_query'">
        <div class="section section--primary">
          <div class="section__head" style="cursor: default">
            <h2 class="section__title">Affected transactions</h2>
            <span class="section__count">{{ issue.event_count.toLocaleString() }} detections</span>
          </div>
          <div v-if="perfEventsLoading" class="section-empty">Loading...</div>
          <div v-else-if="!perfEvents?.length" class="section-empty">No transactions recorded yet.</div>
          <table v-else class="perf-table">
            <thead>
              <tr>
                <th>Transaction</th>
                <th>Repeated queries</th>
                <th>Time wasted</th>
                <th>Detected</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="pe in perfEvents" :key="pe.id">
                <td><RouterLink :to="`/transactions/${pe.transaction_id}`" class="link mono">{{ pe.transaction }}</RouterLink></td>
                <td>{{ pe.span_count }}×</td>
                <td>{{ pe.total_ms }}ms</td>
                <td :title="new Date(pe.created_at).toUTCString()">{{ formatRel(pe.created_at) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>

      <!-- Event instances (error issues only) -->
      <template v-if="issue.kind !== 'n1_query'">
      <div class="section">
        <div class="section__head" style="cursor: default">
          <h2 class="section__title">Event instances</h2>
          <span class="section__count">{{ issue.event_count.toLocaleString() }} occurrences</span>
          <div v-if="issue.event_count > 1" class="section__actions">
            <button class="btn btn--ghost" style="font-size: var(--text-xs)" @click="toggleEventList">
              {{ showEventList ? 'Hide list' : 'Show all' }}
            </button>
          </div>
        </div>
        <div class="eventnav">
          <button
            class="eventnav__btn"
            :class="{ 'eventnav__btn--dis': eventIndex >= issue.event_count - 1 }"
            :title="`Jump to first occurrence`"
            @click="eventIndex = issue.event_count - 1"
          >
            <Icon name="chevrons-left" :size="12" />
          </button>
          <button
            class="eventnav__btn"
            :class="{ 'eventnav__btn--dis': eventIndex === 0 }"
            @click="eventIndex = Math.max(0, eventIndex - 1)"
          >
            <Icon name="chevron-left" :size="12" />
            Newer
          </button>
          <div class="eventnav__cur">
            <span>Event</span>
            <span style="color: var(--text-1)">{{ eventIndex + 1 }} / {{ issue.event_count.toLocaleString() }}</span>
          </div>
          <button
            class="eventnav__btn"
            :class="{ 'eventnav__btn--dis': eventIndex >= issue.event_count - 1 }"
            @click="eventIndex = Math.min(issue.event_count - 1, eventIndex + 1)"
          >
            Older
            <Icon name="chevron-right" :size="12" />
          </button>
          <button
            class="eventnav__btn"
            :class="{ 'eventnav__btn--dis': eventIndex === 0 }"
            :title="`Jump to latest occurrence`"
            @click="eventIndex = 0"
          >
            <Icon name="chevrons-right" :size="12" />
          </button>
          <a
            v-if="currentEvent"
            class="eventnav__raw"
            :href="`/api/issues/${issueId}/events/latest?offset=${eventIndex}`"
            target="_blank"
            rel="noopener"
          >
            <Icon name="braces" :size="11" />
            Raw
          </a>
        </div>

        <!-- Full event list -->
        <div v-if="showEventList" class="eventlist">
          <table class="evttable">
            <thead>
              <tr>
                <th>
                  <button
                    class="col-sort"
                    :class="{ 'col-sort--active': evtSortCol === 'received_at' }"
                    @click="toggleEvtSort('received_at')"
                  >
                    Time
                    <em class="col-sort__icon">{{ evtSortCol === 'received_at' ? (evtSortDir === 'desc' ? '↓' : '↑') : '' }}</em>
                  </button>
                </th>
                <th>
                  <button
                    class="col-sort"
                    :class="{ 'col-sort--active': evtSortCol === 'level' }"
                    @click="toggleEvtSort('level')"
                  >
                    Level
                    <em class="col-sort__icon">{{ evtSortCol === 'level' ? (evtSortDir === 'desc' ? '↓' : '↑') : '' }}</em>
                  </button>
                </th>
                <th>
                  <button
                    class="col-sort"
                    :class="{ 'col-sort--active': evtSortCol === 'environment' }"
                    @click="toggleEvtSort('environment')"
                  >
                    Environment
                    <em class="col-sort__icon">{{ evtSortCol === 'environment' ? (evtSortDir === 'desc' ? '↓' : '↑') : '' }}</em>
                  </button>
                </th>
                <th>
                  <button
                    class="col-sort"
                    :class="{ 'col-sort--active': evtSortCol === 'release' }"
                    @click="toggleEvtSort('release')"
                  >
                    Release
                    <em class="col-sort__icon">{{ evtSortCol === 'release' ? (evtSortDir === 'desc' ? '↓' : '↑') : '' }}</em>
                  </button>
                </th>
                <th><span class="col-sort" style="cursor: default">Tags</span></th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="ev in sortedEvents"
                :key="ev.id"
                class="evttable__row"
                :class="{ 'evttable__row--active': sortedEvents[eventIndex]?.id === ev.id }"
                @click="selectEvent(ev)"
              >
                <td class="evttable__time mono" :title="new Date(ev.received_at).toUTCString()">
                  {{ formatRel(ev.received_at) }}
                </td>
                <td>
                  <span v-if="ev.level" class="leveldot" :class="`leveldot--${ev.level}`" style="margin-right:5px" />
                  <span style="font-size:var(--text-xs);color:var(--text-2)">{{ ev.level ?? '–' }}</span>
                </td>
                <td>
                  <span
                    v-if="ev.environment"
                    class="envbadge"
                    :class="ev.environment === 'production' ? 'envbadge--prod' : ''"
                  >{{ ev.environment }}</span>
                  <span v-else style="color:var(--text-3)">–</span>
                </td>
                <td class="mono evttable__release">{{ ev.release ?? '–' }}</td>
                <td class="evttable__tags">
                  <span v-for="[k, v] in displayTags(ev.tags)" :key="k" class="eventlist__tag">
                    <span class="eventlist__tag-k">{{ k }}</span>
                    <span class="eventlist__tag-v">{{ v }}</span>
                  </span>
                  <span v-if="Object.keys(ev.tags).length === 0" style="color:var(--text-3)">–</span>
                </td>
              </tr>
            </tbody>
          </table>
          <div v-if="eventListLoading && loadedEvents.length === 0" class="eventlist__empty">Loading…</div>
          <div v-else-if="loadedEvents.length === 0" class="eventlist__empty">No events found.</div>
          <div v-if="eventListHasMore || eventListLoading" class="eventlist__footer">
            <button
              class="btn btn--ghost"
              style="font-size: var(--text-xs)"
              :disabled="eventListLoading"
              @click="loadEventPage()"
            >
              {{ eventListLoading ? 'Loading…' : 'Load more' }}
            </button>
          </div>
        </div>
      </div>

      <!-- Stack trace - always visible, primary section -->
      <div v-if="frames.length > 0" class="section section--primary">
        <div class="section__head" style="cursor: default">
          <h2 class="section__title">Stack trace</h2>
          <div class="section__actions">
            <button class="btn btn--ghost" style="font-size: var(--text-xs)" @click="showRawStack = !showRawStack">
              {{ showRawStack ? 'Formatted' : 'Raw' }}
            </button>
            <button v-if="showRawStack" class="btn btn--ghost" style="font-size: var(--text-xs)" @click="copyStack">
              <Icon name="copy" :size="11" />
              {{ copiedStack ? 'Copied' : 'Copy' }}
            </button>
            <button v-if="!showRawStack" class="btn btn--ghost" style="font-size: var(--text-xs)" @click="showSystem = !showSystem">
              {{ showSystem ? 'Hide system frames' : 'Show system frames' }}
            </button>
          </div>
        </div>
        <pre v-if="showRawStack" class="stack__raw">{{ rawStackText }}</pre>
        <div v-else class="stack">
          <div
            v-for="(f, idx) in (frames as Record<string, unknown>[])"
            :key="idx"
            class="stack__frame"
            :class="[
              (f['in_app'] ?? true) ? 'stack__frame--app' : 'stack__frame--system',
              (!showSystem && !(f['in_app'] ?? true)) ? 'stack__frame--collapsed' : '',
            ]"
          >
            <span class="stack__file">
              {{ f['filename'] ?? f['module'] ?? 'unknown' }}
              <span class="stack__line">:{{ f['lineno'] }}</span>
            </span>
            <span class="stack__fn">{{ f['function'] }}</span>
            <CodeContext
              v-if="f['context_line'] != null && (showSystem || (f['in_app'] ?? true))"
              :pre-context="(f['pre_context'] as string[] | undefined) ?? []"
              :context-line="f['context_line'] as string"
              :post-context="(f['post_context'] as string[] | undefined) ?? []"
              :lineno="f['lineno'] as number"
              :platform="eventPlatform"
            />
          </div>
        </div>
      </div>

      <!-- HTTP Request -->
      <div v-if="httpRequest" class="section">
        <div class="section__head" @click="toggleSection('request')">
          <Icon name="chevron-right" :size="12" class="section__chevron" :class="{ 'section__chevron--open': !collapsed.request }" />
          <h2 class="section__title">HTTP Request</h2>
          <span v-if="httpRequest.method" class="section__badge section__badge--method">{{ httpRequest.method }}</span>
        </div>
        <div v-if="!collapsed.request" class="req-section">
          <div v-if="httpRequest.url" class="req-url">{{ httpRequest.url }}</div>

          <template v-if="httpRequest.headers && Object.keys(httpRequest.headers).length > 0">
            <div class="req-subtitle">Headers</div>
            <table class="ctx-table req-table">
              <tbody>
                <tr v-for="[k, v] in Object.entries(httpRequest.headers)" :key="k">
                  <td class="ctx-table__key">{{ k }}</td>
                  <td class="ctx-table__val mono">{{ unwrapHeaderVal(v) }}</td>
                </tr>
              </tbody>
            </table>
          </template>

          <template v-if="httpRequest.query_string">
            <div class="req-subtitle">Query String</div>
            <pre class="req-body">{{ typeof httpRequest.query_string === 'string' ? httpRequest.query_string : JSON.stringify(httpRequest.query_string, null, 2) }}</pre>
          </template>

          <template v-if="reqBodyFull">
            <div class="req-subtitle">
              Body
              <span class="req-subtitle__meta">{{ reqBodyLines.length }} lines</span>
            </div>
            <pre class="req-body">{{ reqBodyDisplay }}</pre>
            <button v-if="reqBodyTruncated" class="req-body__toggle" @click="reqBodyExpanded = !reqBodyExpanded">
              {{ reqBodyExpanded ? 'Show less' : `Show all ${reqBodyLines.length} lines` }}
            </button>
          </template>

          <template v-if="httpRequest.cookies && Object.keys(httpRequest.cookies).length > 0">
            <div class="req-subtitle">Cookies</div>
            <table class="ctx-table req-table">
              <tbody>
                <tr v-for="[k, v] in Object.entries(httpRequest.cookies)" :key="k">
                  <td class="ctx-table__key">{{ k }}</td>
                  <td class="ctx-table__val mono">{{ unwrapHeaderVal(v) }}</td>
                </tr>
              </tbody>
            </table>
          </template>
        </div>
      </div>

      <!-- Trace Preview -->
      <div v-if="linkedTransaction" class="section">
        <div class="section__head" @click="toggleSection('trace')">
          <Icon name="chevron-right" :size="12" class="section__chevron" :class="{ 'section__chevron--open': !collapsed.trace }" />
          <h2 class="section__title">Trace Preview</h2>
          <a
            class="section__link"
            :href="`/performance/transactions/${linkedTransaction.id}`"
            @click.stop="router.push(`/performance/transactions/${linkedTransaction.id}`)"
          >View Full Trace <Icon name="external" :size="10" /></a>
        </div>
        <div v-if="!collapsed.trace" class="trace-preview">
          <div
            v-for="(row, i) in tracePreviewRows"
            :key="i"
            class="trace-preview__row"
            :class="{ 'trace-preview__row--tx': row.isTx }"
          >
            <span class="trace-preview__label">
              <span class="trace-preview__op">{{ row.op }}</span>
              {{ row.label }}
            </span>
            <div class="trace-preview__track">
              <div
                class="trace-preview__bar"
                :class="{ 'trace-preview__bar--tx': row.isTx }"
                :style="{ left: row.left + '%', width: row.width + '%' }"
              />
            </div>
          </div>
          <div v-if="(traceSpans?.length ?? 0) > TRACE_PREVIEW_MAX - 1" class="trace-preview__more">
            +{{ traceSpans!.length - (TRACE_PREVIEW_MAX - 1) }} more spans
          </div>
        </div>
      </div>

      <!-- Breadcrumbs -->
      <div v-if="breadcrumbs.length > 0" class="section">
        <div class="section__head" @click="toggleSection('breadcrumbs')">
          <Icon name="chevron-right" :size="12" class="section__chevron" :class="{ 'section__chevron--open': !collapsed.breadcrumbs }" />
          <h2 class="section__title">Breadcrumbs</h2>
          <span class="section__count">{{ breadcrumbs.length }} events leading up to crash</span>
        </div>
        <div v-if="!collapsed.breadcrumbs" class="crumbs">
          <div
            v-for="(c, i) in (breadcrumbs as Record<string, unknown>[])"
            :key="i"
            class="crumb"
            :class="{ 'crumb--error': c['level'] === 'error' || c['type'] === 'error' }"
          >
            <div class="crumb__row">
              <span class="crumb__time">{{ formatCrumbTime(c['timestamp']) }}</span>
              <span class="crumb__type">{{ c['category'] ?? c['type'] }}</span>
              <span class="crumb__msg">{{ c['message'] ?? (typeof c['data'] !== 'object' ? c['data'] : '') }}</span>
              <button
                v-if="c['data'] !== null && c['data'] !== undefined && typeof c['data'] === 'object'"
                class="crumb__toggle"
                :class="{ 'crumb__toggle--open': expandedCrumbs.has(i) }"
                @click="toggleCrumb(i)"
              >{…}</button>
            </div>
            <div v-if="expandedCrumbs.has(i)" class="crumb__detail">
              <div
                v-for="[k, v] in Object.entries(c['data'] as Record<string, unknown>)"
                :key="k"
                class="crumb__detail-row"
              >
                <span class="crumb__detail-key">{{ k }}</span>
                <span class="crumb__detail-val">{{ typeof v === 'object' ? JSON.stringify(v) : v }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Contexts -->
      <div v-if="displayContexts.length > 0" class="section">
        <div class="section__head" @click="toggleSection('context')">
          <Icon name="chevron-right" :size="12" class="section__chevron" :class="{ 'section__chevron--open': !collapsed.context }" />
          <h2 class="section__title">Context</h2>
        </div>
        <div v-if="!collapsed.context" class="ctx-grid">
          <div v-for="ctx in displayContexts" :key="ctx.key" class="ctx-card">
            <div class="ctx-card__title">
              <Icon :name="ctx.icon" :size="12" class="ctx-card__icon" />
              {{ ctx.label }}
            </div>
            <table class="ctx-table">
              <tbody>
                <tr v-for="[k, v] in ctx.rows" :key="k">
                  <td class="ctx-table__key">{{ k }}</td>
                  <td class="ctx-table__val mono">{{ v }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>

      <!-- Packages -->
      <div v-if="modules.length > 0" class="section">
        <div class="section__head" @click="toggleSection('packages')">
          <Icon name="chevron-right" :size="12" class="section__chevron" :class="{ 'section__chevron--open': !collapsed.packages }" />
          <h2 class="section__title">Packages</h2>
          <span class="section__count">{{ modules.length }}</span>
        </div>
        <template v-if="!collapsed.packages">
          <table class="pkg-table">
            <tbody>
              <tr v-for="[name, version] in (showAllModules ? modules : modules.slice(0, MODULES_PREVIEW))" :key="name">
                <td class="pkg-table__name mono">{{ name }}</td>
                <td class="pkg-table__ver mono">{{ version }}</td>
              </tr>
            </tbody>
          </table>
          <button v-if="modules.length > MODULES_PREVIEW" class="btn btn--ghost pkg-table__more" @click="showAllModules = !showAllModules">
            {{ showAllModules ? 'Show less' : `Show ${modules.length - MODULES_PREVIEW} more` }}
          </button>
        </template>
      </div>

      </template><!-- /error-only sections -->

      </div><!-- /detail-main -->

      <div class="detail-sidebar">

      <!-- User -->
      <div v-if="eventUser" class="section user-card">
        <div class="section__head" style="cursor: default">
          <h2 class="section__title">User</h2>
        </div>
        <div class="user-card__body">
          <div class="user-card__avatar">{{ eventUserInitial }}</div>
          <div class="user-card__info">
            <span class="user-card__name">{{ eventUserLabel }}</span>
            <span v-if="eventUser.email && eventUser.email !== eventUserLabel" class="user-card__sub">{{ eventUser.email }}</span>
          </div>
        </div>
        <div class="user-card__rows">
          <template v-if="eventUser.id">
            <span class="user-card__key">ID</span>
            <span class="user-card__val mono">{{ eventUser.id }}</span>
          </template>
          <template v-if="eventUser.username && eventUser.username !== eventUserLabel">
            <span class="user-card__key">Username</span>
            <span class="user-card__val mono">{{ eventUser.username }}</span>
          </template>
          <template v-if="eventUser.ip_address">
            <span class="user-card__key">IP</span>
            <span class="user-card__val mono">{{ eventUser.ip_address }}</span>
          </template>
        </div>
      </div>

      <!-- Tags -->
      <div v-if="(issueTags?.length ?? 0) > 0" class="section">
        <div class="section__head" @click="toggleSection('tags')">
          <Icon name="chevron-right" :size="12" class="section__chevron" :class="{ 'section__chevron--open': !collapsed.tags }" />
          <h2 class="section__title">Tags</h2>
          <span class="section__count">{{ issueTags!.length }} keys</span>
        </div>
        <div v-if="!collapsed.tags" class="tags-dist">
          <div v-for="tag in issueTags" :key="tag.key" class="tag-dist">
            <div class="tag-dist__head">
              <span class="tag-dist__key">{{ tag.key }}</span>
              <span class="tag-dist__total">{{ tag.total.toLocaleString() }} events</span>
            </div>
            <div
              v-for="tv in tag.values"
              :key="tv.value"
              class="tag-val"
            >
              <a
                class="tag-val__label"
                :href="`/issues?tag_key=${encodeURIComponent(tag.key)}&tag_value=${encodeURIComponent(tv.value)}`"
                @click.prevent="router.push({ path: '/issues', query: { tag_key: tag.key, tag_value: tv.value } })"
              >{{ tv.value }}</a>
              <div class="tag-val__bar">
                <div class="tag-val__fill" :style="{ transform: `scaleX(${tv.pct / 100})` }" />
              </div>
              <span class="tag-val__pct">{{ tv.pct }}%</span>
            </div>
          </div>
        </div>
      </div>

      <!-- Activity / Comments -->
      <div class="section">
        <div class="section__head" @click="toggleSection('activity')">
          <Icon name="chevron-right" :size="12" class="section__chevron" :class="{ 'section__chevron--open': !collapsed.activity }" />
          <h2 class="section__title">Activity</h2>
          <span class="section__count">{{ timeline.length }} events</span>
        </div>

        <div v-if="!collapsed.activity" class="activity">
          <template v-for="item in timeline" :key="item.kind === 'comment' ? item.data.id : item.data.id">
            <!-- Comment -->
            <div v-if="item.kind === 'comment'" class="activity-item">
              <div class="activity-avatar">
                {{ ((item.data as Comment).user_name || (item.data as Comment).user_email)[0].toUpperCase() }}
              </div>
              <div class="activity-body">
                <div class="activity-meta">
                  <span class="activity-author">{{ (item.data as Comment).user_name || (item.data as Comment).user_email }}</span>
                  <span class="activity-time mono">{{ formatRel(item.time) }}</span>
                  <span v-if="(item.data as Comment).updated_at !== (item.data as Comment).created_at" class="activity-edited">(edited)</span>
                  <div class="activity-actions">
                    <button v-if="canEditComment(item.data as Comment)" class="activity-action" title="Edit" @click="startEdit(item.data as Comment)">
                      <Icon name="pencil" :size="11" />
                    </button>
                    <button v-if="canDeleteComment(item.data as Comment)" class="activity-action activity-action--danger" title="Delete" @click="confirmDeleteComment((item.data as Comment).id)">
                      <Icon name="trash" :size="11" />
                    </button>
                  </div>
                </div>
                <!-- Edit mode -->
                <div v-if="editingCommentId === (item.data as Comment).id" class="activity-edit">
                  <textarea
                    v-model="editingBody"
                    class="activity-compose__input"
                    rows="3"
                    @keydown.meta.enter="saveEdit({ id: (item.data as Comment).id, body: editingBody.trim() })"
                    @keydown.escape="cancelEdit"
                  />
                  <div class="activity-edit__footer">
                    <button class="btn btn--ghost" @click="cancelEdit">Cancel</button>
                    <button
                      class="btn btn--primary"
                      :disabled="!editingBody.trim() || savingEdit"
                      @click="saveEdit({ id: (item.data as Comment).id, body: editingBody.trim() })"
                    >Save</button>
                  </div>
                </div>
                <div v-else class="activity-text">{{ (item.data as Comment).body }}</div>
              </div>
            </div>

            <!-- History event -->
            <div v-else class="activity-event">
              <div class="activity-event__icon">
                <Icon :name="historyIcon((item.data as IssueHistoryEntry).event_type)" :size="12" />
              </div>
              <span class="activity-event__label">{{ historyLabel(item.data as IssueHistoryEntry) }}</span>
              <span class="activity-event__actor">{{ actorName((item.data as IssueHistoryEntry).actor_id) }}</span>
              <span class="activity-event__time mono">{{ formatRel(item.time) }}</span>
            </div>
          </template>

          <div v-if="timeline.length === 0" class="activity-empty">
            No activity yet.
          </div>

          <form class="activity-compose" @submit="submitComment">
            <textarea
              v-model="commentBody"
              class="activity-compose__input"
              placeholder="Leave a comment..."
              rows="3"
              @keydown.meta.enter="submitComment"
            />
            <div class="activity-compose__footer">
              <span class="mono" style="color: var(--text-3); font-size: var(--text-xs)">⌘↵ to submit</span>
              <button
                type="submit"
                class="btn btn--primary"
                :disabled="!commentBody.trim() || postingComment"
              >
                Comment
              </button>
            </div>
          </form>
        </div>
      </div>

      </div><!-- /detail-sidebar -->
    </div>
  </div>

  <div v-else-if="issueError" class="page">
    <div class="detail-breadcrumb">
      <a class="detail-breadcrumb__back" href="#" @click.prevent="router.push('/issues')">
        <Icon name="arrow-left" :size="12" />
        Issues
      </a>
    </div>
    <div class="txerror" style="margin: 24px">
      <Icon name="alert-triangle" :size="14" class="txerror__icon" />
      <span>Failed to load issue.</span>
      <button class="btn" @click="refetchIssue()">Try again</button>
    </div>
  </div>

  <div v-else class="page">
    <div class="detail-breadcrumb">
      <a class="detail-breadcrumb__back" href="#" @click.prevent="router.push('/issues')">
        <Icon name="arrow-left" :size="12" />
        Issues
      </a>
    </div>
    <div class="detail-body">
      <div class="detail-hero">
        <div class="skel-hero">
          <div class="skel skel--eyebrow" />
          <div class="skel skel--title" />
          <div class="skel skel--title skel--title-short" />
          <div class="skel skel--meta" />
        </div>
      </div>
      <div class="detail-main">
        <div class="skel-section">
          <div class="skel skel--section-head" />
          <div class="skel skel--stack" />
        </div>
      </div>
      <div class="detail-sidebar">
        <div class="skel-section">
          <div class="skel skel--section-head skel--section-head-short" />
          <div class="skel skel--sidebar-block" />
          <div class="skel skel--sidebar-block skel--sidebar-block-short" />
        </div>
      </div>
    </div>
  </div>
</template>
