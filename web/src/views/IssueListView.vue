<script setup lang="ts">
import { ref, reactive, computed, watch, onMounted, onUnmounted } from 'vue'
import { useWindowVirtualizer } from '@tanstack/vue-virtual'
import { useRouter, useRoute } from 'vue-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { useIssueNavStore } from '@/stores/issueNav'
import { useToast } from '@/composables/useToast'
import { apiFetch } from '@/api/client'
import { useInvestigationStore } from '@/stores/investigation'
import { useFormatters } from '@/composables/useFormatters'
import type { Issue, IssueListPage, User } from '@/api/types'
import FilterChip from '@/components/FilterChip.vue'
import Sparkline from '@/components/Sparkline.vue'
import Icon from '@/components/Icon.vue'
import QueryFeedback from '@/components/QueryFeedback.vue'
import BrandMark from '@/components/BrandMark.vue'
import IgnoreButton from '@/components/IgnoreButton.vue'
import type { IgnorePayload } from '@/components/IgnoreButton.vue'
import { useAppUserStore, routeUserIdentity } from '@/stores/appUser'

const router = useRouter()
const route = useRoute()
const projects = useProjectsStore()
const investigation = useInvestigationStore()
const navStore = useIssueNavStore()
const appUser = useAppUserStore()
const lensIdentity = computed(() => routeUserIdentity(route.query) || appUser.identity)

const effectiveProjectIds = computed(() => projects.selectedIds)
const { show: showToast } = useToast()
const qc = useQueryClient()
const { formatRel } = useFormatters()

const LS_NS = 'tindra:issues:'

function lsGet(key: string): string | null {
  try { return localStorage.getItem(LS_NS + key) } catch { return null }
}

function lsSet(key: string, val: string | null) {
  try {
    if (val === null) localStorage.removeItem(LS_NS + key)
    else localStorage.setItem(LS_NS + key, val)
  } catch { /* storage unavailable */ }
}

// URL param wins; fall back to localStorage, then hard default.
function qp(key: string, fallback: string, lsKey?: string) {
  const v = route.query[key]
  if (typeof v === 'string' && v) return v
  if (lsKey) return lsGet(lsKey) ?? fallback
  return fallback
}

const statusFilter = ref(qp('status', 'Open', 'status'))
const levelFilter = ref(qp('level', 'All', 'level'))
const envFilter = computed({ get: () => investigation.environment, set: (value: string) => { investigation.environment = value } })
const sinceFilter = computed({ get: () => investigation.range, set: (value: string) => investigation.setRange(value) })
const assigneeFilter = ref(qp('assignee', 'All', 'assignee'))
const tagKey = ref(qp('tag_key', ''))
const tagValue = ref(qp('tag_value', ''))
const search = ref(qp('q', ''))
const selectedIdx = ref(-1)
const searchInput = ref<HTMLInputElement | null>(null)
const assigneeOpen = ref(false)
const assigneeEl = ref<HTMLElement | null>(null)


const { data: me } = useQuery({
  queryKey: ['me'],
  queryFn: ({ signal }) => apiFetch<User>('/api/me', { signal }),
})

// Server-side filter values sent as query params.
const serverStatus = computed(() => statusFilter.value === 'All' ? '' : statusFilter.value.toLowerCase())
const serverLevel = computed(() => levelFilter.value === 'All' ? '' : levelFilter.value.toLowerCase())
const serverEnv = computed(() => envFilter.value === 'All' ? '' : envFilter.value)
const serverSince = computed(() => sinceFilter.value === 'All' ? '' : sinceFilter.value)
const serverAssigneeId = computed(() => {
  if (assigneeFilter.value === 'All' || assigneeFilter.value === '') return ''
  if (assigneeFilter.value === 'me') return me.value?.id ?? ''
  return assigneeFilter.value
})

const assigneeChipLabel = computed(() => {
  if (assigneeFilter.value === 'All' || assigneeFilter.value === '') return 'All'
  if (assigneeFilter.value === 'me') return 'Me'
  const u = (users.value as User[]).find(u => u.id === assigneeFilter.value)
  return u ? (u.name || u.email) : 'Unknown'
})

type Cursor = { cursor_time: string; cursor_id: string } | null

const nextCursor = ref<Cursor>(null)
const isFetchingMore = ref(false)
const extraIssues = ref<Issue[]>([])
const loadMoreError = ref(false)
let moreController: AbortController | undefined
onUnmounted(() => moreController?.abort())
let paginationGeneration = 0
function resetPagination() {
  moreController?.abort()
  paginationGeneration++
  extraIssues.value = []
  nextCursor.value = null
  isFetchingMore.value = false
  loadMoreError.value = false
}

function buildIssueParams(cursor: Cursor) {
  const params = new URLSearchParams()
  if (search.value) params.set('q', search.value)
  if (serverStatus.value) params.set('status', serverStatus.value)
  if (serverLevel.value) params.set('level', serverLevel.value)
  if (serverEnv.value) params.set('env', serverEnv.value)
  if (serverAssigneeId.value) params.set('assignee_id', serverAssigneeId.value)
  if (tagKey.value) params.set('tag_key', tagKey.value)
  if (tagValue.value) params.set('tag_value', tagValue.value)
  if (lensIdentity.value) params.set('user', lensIdentity.value)
  for (const id of [...effectiveProjectIds.value].sort()) params.append('project_id', id)
  if (cursor) {
    params.set('cursor_time', cursor.cursor_time)
    params.set('cursor_id', cursor.cursor_id)
  }
  return params.toString()
}

function exportIssues(format: 'csv' | 'json') {
  const params = new URLSearchParams()
  if (search.value) params.set('q', search.value)
  if (serverStatus.value) params.set('status', serverStatus.value)
  if (serverLevel.value) params.set('level', serverLevel.value)
  if (serverEnv.value) params.set('env', serverEnv.value)
  if (serverAssigneeId.value) params.set('assignee_id', serverAssigneeId.value)
  if (tagKey.value) params.set('tag_key', tagKey.value)
  if (tagValue.value) params.set('tag_value', tagValue.value)
  if (lensIdentity.value) params.set('user', lensIdentity.value)
  for (const id of [...effectiveProjectIds.value].sort()) params.append('project_id', id)
  params.set('format', format)
  window.location.href = investigation.request(`/api/issues/export?${params.toString()}`)
}

const issueQueryParams = computed(() => buildIssueParams(null))
const { data: firstPage, isFetching, isError, fetchStatus, dataUpdatedAt, refetch } = useQuery({
  queryKey: computed(() => ['issues', issueQueryParams.value, investigation.scopeKey]),
  queryFn: ({ signal }) => apiFetch<IssueListPage>(investigation.request(`/api/issues?${issueQueryParams.value}`), { signal }),
  refetchOnWindowFocus: false,
})

// Reset pagination when the query scope or successful first-page result changes.
// Query structural sharing preserves firstPage when polling returns identical
// contents, so a timestamp-only refresh keeps loaded pages and pending requests.
// A new refresh anchor also resets pagination so every page uses the same bounds.
watch([issueQueryParams, firstPage, () => investigation.scopeKey, () => investigation.anchor], ([, data]) => {
  resetPagination()
  if (!data || Array.isArray(data)) return
  nextCursor.value = data.has_more && data.next_cursor_time && data.next_cursor_id
    ? { cursor_time: data.next_cursor_time, cursor_id: data.next_cursor_id }
    : null
}, { immediate: true })

// allIssues handles both IssueListPage (new) and Issue[] (old server format).
const allIssues = computed<Issue[]>(() => {
  const page = firstPage.value
  if (!page) return extraIssues.value
  const base = Array.isArray(page) ? (page as Issue[]) : (page.issues ?? [])
  return [...base, ...extraIssues.value]
})
const total = computed(() => {
  const page = firstPage.value
  if (!page) return 0
  if (Array.isArray(page)) return page.length + extraIssues.value.length
  return page.total ?? 0
})
const serverHasMore = computed(() => nextCursor.value !== null)

async function loadMore() {
  if (!nextCursor.value || isFetchingMore.value || isFetching.value || isError.value || fetchStatus?.value === 'paused') return
  investigation.browsingHistory = true
  const controller = moreController = new AbortController()
  isFetchingMore.value = true
  loadMoreError.value = false
  const generation = paginationGeneration
  const scope = buildIssueParams(null)
  const params = buildIssueParams(nextCursor.value)
  try {
    const data = await apiFetch<IssueListPage>(investigation.request(`/api/issues?${params}`), { signal: controller.signal })
    if (controller.signal.aborted || generation !== paginationGeneration || scope !== buildIssueParams(null)) return
    if (!data) throw new Error('Missing issues response')
    extraIssues.value = [...extraIssues.value, ...(data.issues ?? [])]
    nextCursor.value = data.has_more && data.next_cursor_time && data.next_cursor_id
      ? { cursor_time: data.next_cursor_time, cursor_id: data.next_cursor_id }
      : null
  } catch {
    if (!controller.signal.aborted && generation === paginationGeneration && scope === buildIssueParams(null)) loadMoreError.value = true
  } finally {
    if (generation === paginationGeneration) isFetchingMore.value = false
  }
}

const { data: users = [] } = useQuery({
  queryKey: ['users'],
  queryFn: ({ signal }) => apiFetch<User[]>('/api/users', { signal }),
})

function projectName(projectId: string) {
  return (projects.projects as { id: string; name: string }[])?.find((p) => p.id === projectId)?.name ?? projectId
}

function assigneeInitial(iss: Issue) {
  if (!iss.assignee_id) return null
  const u = (users.value as User[]).find((u) => u.id === iss.assignee_id)
  if (!u) return iss.assignee_email?.[0]?.toUpperCase() ?? '?'
  return (u.name || u.email)[0].toUpperCase()
}

const filtered = computed(() => {
  const projectSet =
    effectiveProjectIds.value.length === 0 ? null : new Set(effectiveProjectIds.value)
  const qq = search.value.trim().toLowerCase()
  return allIssues.value.filter((iss) => {
    if (projectSet && !projectSet.has(iss.project_id)) return false
    if (qq && !iss.title.toLowerCase().includes(qq)) return false
    return true
  })
})

// ── Sorting ───────────────────────────────────────────────────────────────────
type SortCol = 'last_seen' | 'first_seen' | 'event_count' | 'title'
const VALID_SORT_COLS: SortCol[] = ['last_seen', 'first_seen', 'event_count', 'title']
const rawSort = qp('sort', 'last_seen', 'sort')
const sortCol = ref<SortCol>(VALID_SORT_COLS.includes(rawSort as SortCol) ? rawSort as SortCol : 'last_seen')
const sortDir = ref<'asc' | 'desc'>(qp('dir', 'desc', 'dir') === 'asc' ? 'asc' : 'desc')

function toggleSort(col: SortCol) {
  if (sortCol.value === col) {
    sortDir.value = sortDir.value === 'desc' ? 'asc' : 'desc'
  } else {
    sortCol.value = col
    sortDir.value = col === 'title' ? 'asc' : 'desc'
  }
}

const sorted = computed(() => {
  const list = [...filtered.value]
  list.sort((a, b) => {
    let cmp = 0
    switch (sortCol.value) {
      case 'last_seen':   cmp = new Date(a.last_seen).getTime()  - new Date(b.last_seen).getTime();  break
      case 'first_seen':  cmp = new Date(a.first_seen).getTime() - new Date(b.first_seen).getTime(); break
      case 'event_count': cmp = a.event_count - b.event_count; break
      case 'title':       cmp = a.title.localeCompare(b.title);  break
    }
    return sortDir.value === 'desc' ? -cmp : cmp
  })
  return list
})
// ─────────────────────────────────────────────────────────────────────────────

// ── Virtual scrolling ─────────────────────────────────────────────────────────
const listContainerRef = ref<HTMLElement | null>(null)
const rowVirtualizer = useWindowVirtualizer(computed(() => ({
  count: sorted.value.length,
  estimateSize: () => 52,
  overscan: 10,
  scrollMargin: listContainerRef.value
    ? listContainerRef.value.getBoundingClientRect().top + window.scrollY
    : 0,
})))
const virtualRows = computed(() => rowVirtualizer.value.getVirtualItems())
const totalVirtualSize = computed(() => rowVirtualizer.value.getTotalSize())
// ─────────────────────────────────────────────────────────────────────────────

// ── Multi-select ──────────────────────────────────────────────────────────────
const selectedIds = reactive(new Set<string>())

const allSelected = computed(
  () => sorted.value.length > 0 && sorted.value.every((iss) => selectedIds.has(iss.id)),
)

function toggleSelect(id: string) {
  selectedIds.has(id) ? selectedIds.delete(id) : selectedIds.add(id)
}

function toggleAll() {
  if (allSelected.value) {
    selectedIds.clear()
  } else {
    sorted.value.forEach((iss) => selectedIds.add(iss.id))
  }
}

watch([statusFilter, levelFilter, envFilter, sinceFilter, assigneeFilter, search], () => { selectedIds.clear(); mergeConfirm.value = false })

const { mutate: bulkUpdate } = useMutation({
  mutationFn: (body: { ids: string[]; status: string; ignore_until?: string; ignore_count_limit?: number }) =>
    apiFetch('/api/issues/bulk', { method: 'PATCH', body: JSON.stringify(body) }),
  onSuccess: () => {
    qc.invalidateQueries({ queryKey: ['issues'] })
    selectedIds.clear()
  },
})

function bulkResolve() {
  const ids = [...selectedIds]
  const n = ids.length
  bulkUpdate({ ids, status: 'resolved' })
  showToast(`Resolved ${n} issue${n === 1 ? '' : 's'}`, () => bulkUpdate({ ids, status: 'open' }))
}

function bulkIgnore(payload?: IgnorePayload) {
  const ids = [...selectedIds]
  const n = ids.length
  bulkUpdate({ ids, ...payload ?? { status: 'ignored' } })
  showToast(`Ignored ${n} issue${n === 1 ? '' : 's'}`, () => bulkUpdate({ ids, status: 'open' }))
}

function bulkUnignore() {
  const ids = [...selectedIds]
  const n = ids.length
  bulkUpdate({ ids, status: 'open' })
  showToast(`Unignored ${n} issue${n === 1 ? '' : 's'}`, () => bulkUpdate({ ids, status: 'ignored' }))
}

const mergeConfirm = ref(false)

const selectedIssues = computed(() =>
  allIssues.value.filter((iss) => selectedIds.has(iss.id)),
)

const canMerge = computed(() => {
  if (selectedIssues.value.length < 2) return false
  const pid = selectedIssues.value[0].project_id
  return selectedIssues.value.every((iss) => iss.project_id === pid)
})

const primaryIssue = computed(() =>
  [...selectedIssues.value].sort((a, b) => b.event_count - a.event_count)[0],
)

function projectSlug(projectId: string): string {
  return (projects.projects as { id: string; slug: string }[])?.find((p) => p.id === projectId)?.slug ?? projectId
}

const { mutate: mergeIssues, isPending: merging } = useMutation({
  mutationFn: ({ slug, ids }: { slug: string; ids: string[] }) =>
    apiFetch(`/api/projects/${slug}/issues/merge`, { method: 'POST', body: JSON.stringify({ issue_ids: ids }) }),
  onSuccess: () => {
    qc.invalidateQueries({ queryKey: ['issues'] })
    selectedIds.clear()
    mergeConfirm.value = false
    showToast('Issues merged')
  },
})

function confirmMerge() {
  const primary = primaryIssue.value
  if (!primary) return
  const slug = projectSlug(primary.project_id)
  const ids = [primary.id, ...[...selectedIds].filter((id) => id !== primary.id)]
  mergeIssues({ slug, ids })
}
// ─────────────────────────────────────────────────────────────────────────────

const { mutate: updateStatus } = useMutation({
  mutationFn: ({ id, ...body }: { id: string; [k: string]: unknown }) =>
    apiFetch(`/api/issues/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
  onSuccess: () => qc.invalidateQueries({ queryKey: ['issues'] }),
})

function levelColor(level: string) {
  switch (level) {
    case 'fatal': case 'error': return 'var(--danger)'
    case 'warning': return 'var(--warning)'
    case 'info': return 'var(--info)'
    default: return 'var(--text-3)'
  }
}

function openIssue(id: string) {
  navStore.set(sorted.value.map(i => i.id))
  router.push(`/issues/${id}`)
}

function navigateIssue(e: MouseEvent, idx: number, id: string) {
  // Let modified clicks (new tab, middle-click) use the href natively.
  if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return
  e.preventDefault()
  selectedIdx.value = idx
  openIssue(id)
}

function resolveSelected() {
  const cur = sorted.value[selectedIdx.value]
  if (!cur || cur.status === 'resolved') return
  updateStatus({ id: cur.id, status: 'resolved' })
  showToast(`Resolved: ${cur.title}`, () => updateStatus({ id: cur.id, status: 'open' }))
}

function ignoreSelected(payload?: IgnorePayload) {
  const cur = sorted.value[selectedIdx.value]
  if (!cur || cur.status === 'ignored') return
  updateStatus({ id: cur.id, ...(payload ?? { status: 'ignored' }) })
  showToast(`Ignored: ${cur.title}`, () => updateStatus({ id: cur.id, status: 'open' }))
}

function unignoreSelected() {
  const cur = sorted.value[selectedIdx.value]
  if (!cur || cur.status !== 'ignored') return
  updateStatus({ id: cur.id, status: 'open' })
  showToast(`Unignored: ${cur.title}`, () => updateStatus({ id: cur.id, status: 'ignored' }))
}

function onKey(e: KeyboardEvent) {
  if ((e.target as HTMLElement).tagName === 'INPUT') return
  if (e.metaKey || e.ctrlKey || e.altKey) return
  if (e.key === '/') { e.preventDefault(); searchInput.value?.focus(); return }
  if (e.key.toLowerCase() === 'j') { e.preventDefault(); selectedIdx.value = Math.min(sorted.value.length - 1, selectedIdx.value < 0 ? 0 : selectedIdx.value + 1) }
  else if (e.key.toLowerCase() === 'k') { e.preventDefault(); selectedIdx.value = Math.max(0, selectedIdx.value < 0 ? 0 : selectedIdx.value - 1) }
  else if (e.key === 'Enter') { const cur = sorted.value[selectedIdx.value]; if (cur) openIssue(cur.id) }
  else if (e.key.toLowerCase() === 'x') { const cur = sorted.value[selectedIdx.value]; if (cur) toggleSelect(cur.id) }
  else if (e.key.toLowerCase() === 'e') { selectedIds.size > 0 ? bulkResolve() : resolveSelected() }
  else if (e.key.toLowerCase() === 'i') { selectedIds.size > 0 ? bulkIgnore() : ignoreSelected() }
  else if (e.key.toLowerCase() === 'u') { selectedIds.size > 0 ? bulkUnignore() : unignoreSelected() }
}

function onMouseDownGlobal(e: MouseEvent) {
  if (assigneeEl.value && !assigneeEl.value.contains(e.target as Node)) assigneeOpen.value = false
}

onMounted(() => {
  document.addEventListener('keydown', onKey)
  document.addEventListener('mousedown', onMouseDownGlobal)
})
onUnmounted(() => {
  document.removeEventListener('keydown', onKey)
  document.removeEventListener('mousedown', onMouseDownGlobal)
})

function clearFilters() {
  statusFilter.value = 'Open'
  levelFilter.value = 'All'
  envFilter.value = 'All'
  sinceFilter.value = '24h'
  assigneeFilter.value = 'All'
  tagKey.value = ''
  tagValue.value = ''
  search.value = ''
  appUser.clear()
}

const isFiltered = computed(
  () =>
    search.value ||
    statusFilter.value !== 'Open' ||
    levelFilter.value !== 'All' ||
    envFilter.value !== 'All' ||
    sinceFilter.value !== '24h' ||
    assigneeFilter.value !== 'All' ||
    tagKey.value !== '' ||
    !!lensIdentity.value,
)

// True when only a client-side search/project filter is narrowing the results.
const isClientFiltered = computed(() => !!(search.value || projects.selectedIds.length > 0))

const activeFilterSummary = computed(() => {
  const parts: string[] = []
  if (statusFilter.value !== 'Open') parts.push(`Status: ${statusFilter.value}`)
  if (levelFilter.value !== 'All') parts.push(`Level: ${levelFilter.value}`)
  if (envFilter.value !== 'All') parts.push(`Env: ${envFilter.value}`)
  if (sinceFilter.value !== 'All') parts.push(`Time range: ${sinceFilter.value}`)
  if (assigneeFilter.value !== 'All') parts.push(assigneeFilter.value === 'me' ? 'Assigned to me' : `Assignee: ${(users.value as User[]).find(u => u.id === assigneeFilter.value)?.name || 'someone'}`)
  if (tagKey.value) parts.push(`${tagKey.value}${tagValue.value ? ': ' + tagValue.value : ''}`)
  if (lensIdentity.value) parts.push(`User: ${appUser.label || lensIdentity.value}`)
  if (search.value.trim()) parts.push(`"${search.value.trim()}"`)
  return parts.join(' · ')
})

const noProjects = computed(() => projects.isSuccess && projects.projects.length === 0)
const noOpenIssues = computed(() => statusFilter.value === 'Open' && !isFiltered.value)

// Sync filter/sort state → URL so F5 restores the same view.
watch(
  [statusFilter, levelFilter, assigneeFilter, tagKey, tagValue, search, sortCol, sortDir, lensIdentity],
  () => {
    const query = { ...route.query }
    for (const key of ['status', 'level', 'assignee', 'tag_key', 'tag_value', 'user', 'q', 'sort', 'dir']) delete query[key]
    if (statusFilter.value !== 'Open') query.status = statusFilter.value
    if (levelFilter.value !== 'All') query.level = levelFilter.value
    if (assigneeFilter.value !== 'All') query.assignee = assigneeFilter.value
    if (tagKey.value) query.tag_key = tagKey.value
    if (tagValue.value) query.tag_value = tagValue.value
    if (lensIdentity.value) query.user = lensIdentity.value
    if (search.value) query.q = search.value
    if (sortCol.value !== 'last_seen' || sortDir.value !== 'desc') {
      query.sort = sortCol.value
      query.dir = sortDir.value
    }
    router.replace({ query })
  },
)

// Persist filter/sort to localStorage so they survive navigation without URL params.
watch([statusFilter, levelFilter, assigneeFilter, sortCol, sortDir], () => {
  lsSet('status', statusFilter.value !== 'Open' ? statusFilter.value : null)
  lsSet('level', levelFilter.value !== 'All' ? levelFilter.value : null)
  lsSet('assignee', assigneeFilter.value !== 'All' ? assigneeFilter.value : null)
  lsSet('sort', sortCol.value !== 'last_seen' ? sortCol.value : null)
  lsSet('dir', sortDir.value !== 'desc' ? sortDir.value : null)
})
</script>

<template>
  <div class="page">
    <p v-if="investigation.range === 'All'" class="filterbar">All retained events, with counts and sparklines filtered by the selected environment.</p>
    <!-- Filter bar -->
    <div class="filterbar">
      <FilterChip
        label="Status"
        :value="statusFilter"
        :options="['Open', 'Resolved', 'Ignored', 'Regressed', 'All']"
        @change="statusFilter = $event"
      />
      <FilterChip
        label="Level"
        :value="levelFilter"
        :options="['All', 'Fatal', 'Error', 'Warning', 'Info']"
        @change="levelFilter = $event"
      />
      <!-- Assignee filter: custom dropdown since options carry UUIDs -->
      <div ref="assigneeEl" style="position: relative">
        <button
          class="filterchip"
          :class="{ 'filterchip--active': assigneeFilter !== 'All' }"
          @click="assigneeOpen = !assigneeOpen"
        >
          <span class="filterchip__label">Assignee:</span>
          <span class="filterchip__value">{{ assigneeChipLabel }}</span>
          <Icon name="chevron-down" :size="11" />
        </button>
        <div v-if="assigneeOpen" class="popover" style="left: 0; right: auto; min-width: 160px">
          <div class="popover__list">
            <div
              class="popover__item"
              :class="{ 'popover__item--active': assigneeFilter === 'All' }"
              @click="assigneeFilter = 'All'; assigneeOpen = false"
            >All</div>
            <div
              class="popover__item"
              :class="{ 'popover__item--active': assigneeFilter === 'me' }"
              @click="assigneeFilter = 'me'; assigneeOpen = false"
            >Me</div>
            <div
              v-for="u in (users as User[])"
              :key="u.id"
              class="popover__item"
              :class="{ 'popover__item--active': assigneeFilter === u.id }"
              @click="assigneeFilter = u.id; assigneeOpen = false"
            >{{ u.name || u.email }}</div>
          </div>
        </div>
      </div>
      <button v-if="tagKey" class="tag-chip" @click="tagKey = ''; tagValue = ''">
        <span class="tag-chip__k">{{ tagKey }}</span>
        <template v-if="tagValue">
          <span class="tag-chip__sep">:</span>
          <span class="tag-chip__v">{{ tagValue }}</span>
        </template>
        <Icon name="x" :size="10" />
      </button>
      <div class="filterbar__spacer" />
      <div class="export-menu">
        <button class="btn btn--ghost export-menu__trigger" title="Export issues">
          <Icon name="download" :size="11" />
          Export
        </button>
        <div class="export-menu__dropdown">
          <button class="export-menu__item" @click="exportIssues('csv')">
            <Icon name="file-text" :size="11" />
            Download CSV
          </button>
          <button class="export-menu__item" @click="exportIssues('json')">
            <Icon name="file-text" :size="11" />
            Download JSON
          </button>
        </div>
      </div>
      <button
        class="filterbar__refresh"
        :class="{ 'filterbar__refresh--fetching': isFetching }"
        :title="isFetching ? 'Refreshing…' : 'Refresh now (auto every 30s)'"
        @click="investigation.refresh(); refetch()"
      >
        <Icon name="refresh-cw" :size="11" :class="{ 'filterbar__refresh-spin': isFetching }" />
      </button>
      <div class="filterbar__search">
        <Icon name="search" :size="12" style="color: var(--text-3)" />
        <input
          ref="searchInput"
          v-model="search"
          placeholder="Search issues…"
          aria-label="Search issues"
        />
        <span class="nav__kbd">/</span>
      </div>
    </div>

    <QueryFeedback resource="issues" :failed="!!isError" :has-data="firstPage !== undefined"
      :refreshing="isFetching" :paused="fetchStatus === 'paused'" :updated-at="dataUpdatedAt" @retry="investigation.refresh(); refetch()" />
    <QueryFeedback resource="projects" :failed="!!projects.isError" :has-data="!!projects.hasLoaded"
      :refreshing="projects.isFetching" :paused="projects.fetchStatus === 'paused'" :updated-at="projects.dataUpdatedAt" @retry="projects.refetch()" />

    <div v-if="!firstPage && !isError && fetchStatus !== 'paused'" class="issuelist" role="status" aria-label="Loading issues">
      <div v-for="i in 8" :key="i" class="issuerow" aria-hidden="true">
        <span class="skel" style="height: 14px; width: 70%; grid-column: 1 / -1" />
      </div>
    </div>

    <!-- Keep successful results visible when a background refresh fails. -->
    <div v-else-if="firstPage && (filtered.length > 0 || isFiltered)" class="issuelist">
      <div class="issuerow issuerow--header">
        <input
          type="checkbox"
          class="row-check row-check--header"
          :checked="allSelected"
          :indeterminate="selectedIds.size > 0 && !allSelected"
          @click.stop
          @change="toggleAll"
        />
        <span />
        <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'title' }" @click="toggleSort('title')">
          Issue <span v-if="sortCol === 'title'" class="col-sort__icon">{{ sortDir === 'desc' ? '↓' : '↑' }}</span>
        </button>
        <span>Env</span>
        <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'event_count' }" style="justify-content: flex-end" @click="toggleSort('event_count')">
          <span v-if="sortCol === 'event_count'" class="col-sort__icon">{{ sortDir === 'desc' ? '↓' : '↑' }}</span> Events
        </button>
        <span style="text-align: right">Users</span>
        <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'last_seen' }" style="justify-content: flex-end" @click="toggleSort('last_seen')">
          <span v-if="sortCol === 'last_seen'" class="col-sort__icon">{{ sortDir === 'desc' ? '↓' : '↑' }}</span> Last seen
        </button>
        <span>Owner</span>
        <span>Status</span>
        <span />
      </div>

      <!-- Filter no-match -->
      <div v-if="isFiltered && filtered.length === 0" class="empty-filter">
        <div class="empty-filter__icon">
          <svg width="20" height="20" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M2 3h12M5 8h6M7.5 13h1" />
          </svg>
        </div>
        <p class="empty-filter__title">No matches</p>
        <p class="empty-filter__body">{{ activeFilterSummary }}</p>
        <button class="btn" @click="clearFilters">Clear filters</button>
      </div>

      <!-- Issue rows (virtualized) -->
      <div ref="listContainerRef" :style="{ height: `${totalVirtualSize}px`, position: 'relative' }">
        <div
          v-for="vRow in virtualRows"
          :key="vRow.key"
          :data-index="vRow.index"
          class="issuerow"
          :class="{
            'issuerow--selected': vRow.index === selectedIdx && selectedIdx >= 0,
            'issuerow--regressed': sorted[vRow.index]?.status === 'regressed',
            'issuerow--checked': selectedIds.has(sorted[vRow.index]?.id),
          }"
          :style="{ position: 'absolute', top: 0, left: 0, width: '100%', transform: `translateY(${vRow.start - rowVirtualizer.options.scrollMargin}px)` }"
          role="row"
          tabindex="0"
          @click="selectedIdx = vRow.index; openIssue(sorted[vRow.index].id)"
        >
          <input
            type="checkbox"
            class="row-check"
            :checked="selectedIds.has(sorted[vRow.index]?.id)"
            @click.stop
            @change="toggleSelect(sorted[vRow.index].id)"
          />
          <span class="leveldot" :class="sorted[vRow.index]?.kind === 'n1_query' ? 'leveldot--n1' : `leveldot--${sorted[vRow.index]?.level}`" />
          <a :href="investigation.link(`/issues/${sorted[vRow.index]?.id}`)" class="issue__title" @click.stop="navigateIssue($event, vRow.index, sorted[vRow.index].id)">
            <div style="min-width: 0; flex: 1">
              <div class="issue__title-text">
                <span class="issue__title-type">{{ sorted[vRow.index]?.title.split(':')[0] }}</span>
                <span style="color: var(--text-3)">: </span>
                <span class="issue__title-msg">{{ sorted[vRow.index]?.title.split(':').slice(1).join(':').trim() }}</span>
              </div>
              <div class="issue__sub">
                <span v-if="sorted[vRow.index]?.kind === 'n1_query'" class="kindbadge">N+1</span>
                <span class="projtag">{{ projectName(sorted[vRow.index]?.project_id) }}</span>
              </div>
            </div>
          </a>
          <span
            class="envbadge"
            :class="sorted[vRow.index]?.environment === 'production' ? 'envbadge--prod' : sorted[vRow.index]?.environment === 'staging' ? 'envbadge--staging' : ''"
          >{{ sorted[vRow.index]?.environment ?? '-' }}</span>
          <div class="events-cell">
            <span v-if="sorted[vRow.index]?.kind !== 'n1_query'" class="events-cell__spark" :style="{ color: levelColor(sorted[vRow.index]?.level) }">
              <Sparkline :data="sorted[vRow.index]?.sparkline ?? []" :width="36" :height="14" />
            </span>
            <span class="events-cell__num">{{ sorted[vRow.index]?.event_count.toLocaleString() }}</span>
          </div>
          <span class="users-cell"><Icon name="user" :size="11" class="users-cell__icon" />{{ (sorted[vRow.index]?.user_count ?? 0).toLocaleString() }}</span>
          <span class="time-cell">{{ formatRel(sorted[vRow.index]?.last_seen) }}</span>
          <span class="owner-cell">
            <span v-if="assigneeInitial(sorted[vRow.index])" class="owner-avatar" :title="sorted[vRow.index]?.assignee_email ?? ''">
              {{ assigneeInitial(sorted[vRow.index]) }}
            </span>
          </span>
          <span class="statuspill" :class="`statuspill--${sorted[vRow.index]?.status}`">{{ sorted[vRow.index]?.status }}</span>
          <span class="row-chevron"><Icon name="chevron-right" :size="12" /></span>
        </div>
      </div>

      <!-- List footer: counter + load more -->
      <div v-if="loadMoreError" class="txerror" role="alert">
        <Icon name="alert-triangle" :size="14" class="txerror__icon" />
        <span>Couldn't load more issues. The issues already loaded are still shown.</span>
        <button class="btn" :disabled="isFetchingMore || isFetching || isError || fetchStatus === 'paused'" @click="loadMore()">Try again</button>
      </div>
      <div v-if="sorted.length > 0 || serverHasMore" class="list-footer">
        <span class="list-footer__count">
          <template v-if="isClientFiltered">
            {{ filtered.length.toLocaleString() }} match{{ filtered.length === 1 ? '' : 'es' }}
            <span class="list-footer__sep">·</span>
          </template>
          {{ allIssues.length.toLocaleString() }} of {{ total.toLocaleString() }} loaded
        </span>
        <button
          v-if="serverHasMore"
          class="btn list-footer__more"
          :disabled="isFetchingMore || isFetching || isError || fetchStatus === 'paused'"
          @click="loadMore()"
        >
          <template v-if="isFetchingMore">Loading…</template>
          <template v-else>Load {{ Math.min(50, total - allIssues.length).toLocaleString() }} more</template>
        </button>
        <span v-else class="list-footer__done">All loaded</span>
      </div>
    </div>

    <!-- Empty state: no issues at all -->
    <div v-else-if="firstPage" class="empty-state">
      <!-- Ghost rows: decorative background texture -->
      <div class="empty-state__ghosts" aria-hidden="true">
        <div
          v-for="(w, i) in ['72%','58%','81%','64%','76%','53%']"
          :key="i"
          class="ghost-row"
        >
          <span />
          <span class="ghost ghost--dot" />
          <div style="display:flex;flex-direction:column;gap:6px">
            <span class="ghost ghost--bar" :style="{ width: w }" />
            <span class="ghost ghost--bar" style="width:88px;height:7px;opacity:0.6" />
          </div>
          <span class="ghost ghost--pill" />
          <span class="ghost ghost--bar" style="width:48px;margin-left:auto" />
          <span class="ghost ghost--bar" style="width:54px;margin-left:auto" />
          <span class="ghost ghost--pill" style="width:58px" />
          <span />
        </div>
      </div>

      <!-- Card overlay -->
      <div v-if="noProjects" class="empty-state__card">
        <div class="empty-state__icon">
          <BrandMark :size="32" />
        </div>
        <h2 class="empty-state__title">No projects yet</h2>
        <p class="empty-state__body">
          Create a project to get your DSN, then point your Sentry-compatible SDK at Tindra.
          Errors and traces appear here automatically, no code changes required.
        </p>
        <div class="empty-state__actions">
          <button class="btn btn--primary" @click="router.push('/settings/projects?new=1')">
            <Icon name="plus" :size="12" />
            Create project
          </button>
        </div>
      </div>

      <div v-else-if="noOpenIssues" class="empty-state__card">
        <div class="empty-state__icon">
          <Icon name="file-text" :size="28" />
        </div>
        <h2 class="empty-state__title">No open issues found</h2>
        <p class="empty-state__body">
          No open issues match your current selection. Use the status filter to review other issues.
        </p>
        <div class="empty-state__actions">
          <button class="btn" @click="statusFilter = 'All'">View all issues</button>
          <button class="btn" @click="router.push('/settings')">View project DSNs →</button>
        </div>
      </div>

      <div v-else class="empty-state__card">
        <div class="empty-state__icon">
          <Icon name="file-text" :size="28" />
        </div>
        <h2 class="empty-state__title">No issues found</h2>
        <p class="empty-state__body">
          No issues match your current selection. To verify your SDK setup, send a test event from your application.
        </p>
        <div class="empty-state__actions">
          <button class="btn" @click="router.push('/settings')">
            View project DSNs →
          </button>
        </div>
      </div>
    </div>

    <!-- Bulk action bar -->
    <Transition name="bulkbar">
      <div v-if="selectedIds.size > 0" class="bulkbar">
        <template v-if="mergeConfirm">
          <span class="bulkbar__count">Merge {{ selectedIds.size }} issues into:</span>
          <span class="bulkbar__merge-primary" :title="primaryIssue?.title">{{ primaryIssue?.title.split(':')[0] }}</span>
          <button class="btn btn--primary" :disabled="merging" @click="confirmMerge">Confirm</button>
          <button class="btn btn--ghost" @click="mergeConfirm = false">Cancel</button>
        </template>
        <template v-else>
          <span class="bulkbar__count">{{ selectedIds.size }} selected</span>
          <button class="btn btn--primary" @click="bulkResolve">Resolve</button>
          <IgnoreButton @ignore="bulkIgnore" />
          <button class="btn" @click="bulkUnignore">Unignore</button>
          <button v-if="canMerge" class="btn" @click="mergeConfirm = true">Merge</button>
          <button class="btn btn--ghost" @click="selectedIds.clear()">Clear</button>
        </template>
      </div>
    </Transition>
  </div>
</template>
