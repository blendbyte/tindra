<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { usePerformanceStore } from '@/stores/performance'
import { apiFetch } from '@/api/client'
import type { TransactionSummary, TxTimeseries, ReleaseListPage } from '@/api/types'
import { formatDuration } from '@/utils/formatters'
import FilterChip from '@/components/FilterChip.vue'
import Icon from '@/components/Icon.vue'
import BrandMark from '@/components/BrandMark.vue'
import TimeseriesChart from '@/components/TimeseriesChart.vue'
import PerformanceSubnav from '@/components/PerformanceSubnav.vue'

const router = useRouter()
const route = useRoute()
const projects = useProjectsStore()
const perf = usePerformanceStore()

const effectiveProjectIds = computed(() => {
  const v = route.query.project_id
  if (typeof v === 'string') return [v]
  if (Array.isArray(v)) return v as string[]
  return projects.selectedIds
})

const WINDOW_MAP: Record<string, number> = { '1h': 1, '24h': 24, '7d': 168, '30d': 720 }
const VALID_WINDOWS = Object.keys(WINDOW_MAP)

// URL params take priority (deep-linking), otherwise use the shared store value.
const rawWindow = route.query['window']
if (typeof rawWindow === 'string' && VALID_WINDOWS.includes(rawWindow)) {
  perf.windowHrs = rawWindow
}
const rawEnv = route.query['env']
if (typeof rawEnv === 'string' && rawEnv) {
  perf.envFilter = rawEnv
}

function lsGet(key: string): string | null {
  try { return localStorage.getItem('tindra:transactions:' + key) } catch { return null }
}
function lsSet(key: string, val: string | null) {
  try {
    if (val === null) localStorage.removeItem('tindra:transactions:' + key)
    else localStorage.setItem('tindra:transactions:' + key, val)
  } catch {}
}

const opFilter = ref((() => {
  const v = route.query['op']
  if (typeof v === 'string' && v) return v
  return lsGet('op') ?? 'All'
})())

const releaseFilter = ref((() => {
  const v = route.query['release']
  return typeof v === 'string' ? v : 'All'
})())

const hours = computed(() => WINDOW_MAP[perf.windowHrs] ?? 24)

type SortCol = 'transaction' | 'sample_count' | 'tpm' | 'p50' | 'p95' | 'apdex' | 'failure_rate' | 'time_spent_ms'
const VALID_SORT_COLS: SortCol[] = ['transaction', 'sample_count', 'tpm', 'p50', 'p95', 'apdex', 'failure_rate', 'time_spent_ms']
const rawSort = (() => { const v = route.query['sort']; return typeof v === 'string' ? v : (lsGet('sort') ?? 'time_spent_ms') })()
const sortCol = ref<SortCol>(VALID_SORT_COLS.includes(rawSort as SortCol) ? rawSort as SortCol : 'time_spent_ms')
const sortDir = ref<'asc' | 'desc'>((() => { const v = route.query['dir']; return (typeof v === 'string' ? v : (lsGet('dir') ?? 'desc')) === 'asc' ? 'asc' : 'desc' })())

function toggleSort(col: SortCol) {
  if (sortCol.value === col) {
    sortDir.value = sortDir.value === 'desc' ? 'asc' : 'desc'
  } else {
    sortCol.value = col
    sortDir.value = col === 'transaction' ? 'asc' : 'desc'
  }
}

// Sync filters → URL (restores view on F5)
watch([() => perf.windowHrs, () => perf.envFilter, opFilter, releaseFilter, sortCol, sortDir], () => {
  const query: Record<string, string> = {}
  if (perf.windowHrs !== '24h') query.window = perf.windowHrs
  if (perf.envFilter !== 'All') query.env = perf.envFilter
  if (opFilter.value !== 'All') query.op = opFilter.value
  if (releaseFilter.value !== 'All') query.release = releaseFilter.value
  if (sortCol.value !== 'time_spent_ms' || sortDir.value !== 'desc') {
    query.sort = sortCol.value
    query.dir = sortDir.value
  }
  const pid = route.query.project_id
  router.replace({ query: { ...query, ...(pid ? { project_id: pid } : {}) } })
})

// Persist op/sort/dir to localStorage (window+env handled by the store)
watch([opFilter, sortCol, sortDir], () => {
  lsSet('op', opFilter.value !== 'All' ? opFilter.value : null)
  lsSet('sort', sortCol.value !== 'time_spent_ms' ? sortCol.value : null)
  lsSet('dir', sortDir.value !== 'desc' ? sortDir.value : null)
})

// Fetch available releases for the release filter chip
const { data: releasesPage } = useQuery({
  queryKey: ['releases'],
  queryFn: () => apiFetch<ReleaseListPage>('/api/releases'),
})

const releaseOptions = computed(() => {
  const projectSet = effectiveProjectIds.value.length > 0 ? new Set(effectiveProjectIds.value) : null
  const versions = (releasesPage.value?.releases ?? [])
    .filter((r) => !projectSet || projectSet.has(r.project_id))
    .map((r) => r.version)
  return ['All', ...Array.from(new Set(versions))]
})

// Base params: window + env + project + release - no op filter
const txParams = computed(() => {
  const p = new URLSearchParams()
  p.set('hours', String(hours.value))
  if (perf.envFilter !== 'All') p.set('env', perf.envFilter)
  if (releaseFilter.value !== 'All') p.set('release', releaseFilter.value)
  for (const id of effectiveProjectIds.value) p.append('project_id', id)
  return p.toString()
})

// Timeseries params include op so the chart tracks the selected op
const txParamsWithOp = computed(() => {
  if (opFilter.value === 'All') return txParams.value
  const p = new URLSearchParams(txParams.value)
  p.set('op', opFilter.value)
  return p.toString()
})

// Always fetch all summaries - op filtering is client-side for instant feedback
const { data: rawSummaries, isLoading, isError, refetch } = useQuery({
  queryKey: ['transaction-summaries', txParams],
  queryFn: () => apiFetch<TransactionSummary[]>(`/api/transactions/summaries?${txParams.value}`),
})

// Previous period - same window shifted back by one window length, for comparison indicators
const compParams = computed(() => {
  const p = new URLSearchParams(txParams.value)
  p.set('offset', String(hours.value))
  return p.toString()
})

const { data: compSummaries } = useQuery({
  queryKey: ['transaction-summaries-comp', compParams],
  queryFn: () => apiFetch<TransactionSummary[]>(`/api/transactions/summaries?${compParams.value}`),
  enabled: computed(() => !!rawSummaries.value?.length),
})

const compMap = computed(() => {
  const map = new Map<string, { p50: number; p95: number }>()
  for (const s of compSummaries.value ?? []) {
    map.set(`${s.transaction}|${s.op}|${s.project_id}`, { p50: s.p50, p95: s.p95 })
  }
  return map
})

function getDelta(s: TransactionSummary, metric: 'p50' | 'p95') {
  const prev = compMap.value.get(`${s.transaction}|${s.op}|${s.project_id}`)
  if (!prev || prev[metric] === 0) return null
  const pct = ((s[metric] - prev[metric]) / prev[metric]) * 100
  if (Math.abs(pct) < 5) return null
  return {
    label: `${pct > 0 ? '↑' : '↓'}${Math.abs(pct).toFixed(0)}%`,
    cls: pct > 0 ? 'tx-delta--worse' : 'tx-delta--better',
  }
}

const timeseries = ref<TxTimeseries | null>(null)
watch(
  txParamsWithOp,
  async (params) => {
    timeseries.value = await apiFetch<TxTimeseries>(`/api/transactions/timeseries?${params}`)
  },
  { immediate: true },
)

// Distinct ops and per-op counts, derived live from the full unfiltered dataset
const availableOps = computed(() => {
  const ops = new Set<string>()
  for (const s of rawSummaries.value ?? []) ops.add(s.op)
  return Array.from(ops).sort()
})

const opCounts = computed(() => {
  const counts: Record<string, number> = {}
  for (const s of rawSummaries.value ?? []) counts[s.op] = (counts[s.op] ?? 0) + s.sample_count
  return counts
})

// Client-side filtered list - instant, no extra network request
const filteredSummaries = computed(() => {
  const list = rawSummaries.value ?? []
  return opFilter.value === 'All' ? list : list.filter(s => s.op === opFilter.value)
})

const summaries = computed(() => {
  const list = [...filteredSummaries.value]
  list.sort((a, b) => {
    if (sortCol.value === 'transaction') {
      const cmp = a.transaction.localeCompare(b.transaction)
      return sortDir.value === 'asc' ? cmp : -cmp
    }
    const diff = a[sortCol.value] - b[sortCol.value]
    return sortDir.value === 'desc' ? -diff : diff
  })
  return list
})

function opClass(op: string) {
  return `optag--${op.split('.')[0]}`
}

function formatTPM(tpm: number) {
  if (tpm < 0.01) return '<0.01/min'
  if (tpm >= 100) return `${Math.round(tpm)}/min`
  return `${tpm.toFixed(2)}/min`
}

function formatTimeSpent(ms: number) {
  if (ms >= 3_600_000) return `${(ms / 3_600_000).toFixed(2)}hr`
  if (ms >= 60_000) return `${(ms / 60_000).toFixed(2)}min`
  if (ms >= 1_000) return `${(ms / 1_000).toFixed(2)}s`
  return `${ms}ms`
}

function formatFailureRate(rate: number) {
  if (rate === 0) return '0%'
  if (rate < 0.0001) return '<0.01%'
  return `${(rate * 100).toFixed(2)}%`
}

function projectName(projectId: string) {
  return (projects.projects as { id: string; name: string }[])?.find((p) => p.id === projectId)?.name ?? projectId
}

function sortIcon(col: SortCol) {
  if (sortCol.value !== col) return ''
  return sortDir.value === 'desc' ? '↓' : '↑'
}

const noProjects = computed(() => !projects.projects?.length)
const noData = computed(() => !isLoading.value && !isError.value && filteredSummaries.value.length === 0)

const stats = computed(() => {
  const list = filteredSummaries.value
  if (list.length === 0) return null
  const totalCount = list.reduce((s, x) => s + x.sample_count, 0)
  const totalTpm = list.reduce((s, x) => s + x.tpm, 0)
  const p50 = totalCount > 0 ? list.reduce((s, x) => s + x.p50 * x.sample_count, 0) / totalCount : 0
  const p95 = totalCount > 0 ? list.reduce((s, x) => s + x.p95 * x.sample_count, 0) / totalCount : 0
  const apdex = totalCount > 0 ? list.reduce((s, x) => s + x.apdex * x.sample_count, 0) / totalCount : 0
  const failureRate = totalCount > 0 ? list.reduce((s, x) => s + x.failure_rate * x.sample_count, 0) / totalCount : 0
  return { totalCount, totalTpm, p50, p95, apdex, failureRate }
})

function apdexClass(score: number): string {
  if (score >= 0.94) return 'tx-apdex--good'
  if (score >= 0.70) return 'tx-apdex--fair'
  return 'tx-apdex--poor'
}

</script>

<template>
  <div class="page">
    <PerformanceSubnav />

    <!-- Empty state: no projects -->
    <div v-if="noProjects" class="empty-state">
      <div class="empty-state__ghosts" aria-hidden="true">
        <div v-for="(w, i) in ['68%','54%','78%','61%','72%']" :key="i" class="ghost-row">
          <span class="ghost ghost--dot" />
          <div style="display:flex;flex-direction:column;gap:6px">
            <span class="ghost ghost--bar" :style="{ width: w }" />
            <span class="ghost ghost--bar" style="width:80px;height:7px;opacity:0.6" />
          </div>
          <span class="ghost ghost--pill" />
          <span class="ghost ghost--bar" style="width:52px;margin-left:auto" />
          <span class="ghost ghost--bar" style="width:48px;margin-left:auto" />
          <span class="ghost ghost--pill" style="width:52px" />
          <span />
        </div>
      </div>
      <div class="empty-state__card">
        <div class="empty-state__icon">
          <BrandMark :size="32" />
        </div>
        <h2 class="empty-state__title">No projects yet</h2>
        <p class="empty-state__body">
          Create a project to get your DSN, then point your Sentry-compatible SDK at Tindra.
          Transactions appear here automatically once your SDK is connected.
        </p>
        <div class="empty-state__actions">
          <button class="btn btn--primary" @click="router.push('/settings/projects?new=1')">
            <Icon name="plus" :size="12" />
            Create project
          </button>
        </div>
      </div>
    </div>

    <!-- Normal view -->
    <template v-else>
      <!-- Filter bar -->
      <div class="filterbar">
        <FilterChip
          label="Window"
          :value="perf.windowHrs"
          :options="['1h', '24h', '7d', '30d']"
          @change="perf.windowHrs = $event"
        />
        <FilterChip
          label="Env"
          :value="perf.envFilter"
          :options="['All', 'production', 'staging', 'development']"
          @change="perf.envFilter = $event"
        />
        <FilterChip
          v-if="releaseOptions.length > 1"
          label="Release"
          :value="releaseFilter"
          :options="releaseOptions"
          @change="releaseFilter = $event"
        />
      </div>

      <!-- Op tabs - only shown when there are 2+ distinct ops -->
      <div v-if="availableOps.length > 1" class="optabs">
        <button
          class="optab"
          :class="{ 'optab--active': opFilter === 'All' }"
          @click="opFilter = 'All'"
        >
          All
          <span class="optab__count">{{ Object.values(opCounts).reduce((a, b) => a + b, 0).toLocaleString() }}</span>
        </button>
        <button
          v-for="op in availableOps"
          :key="op"
          class="optab"
          :class="{ 'optab--active': opFilter === op }"
          @click="opFilter = op"
        >
          <span class="optab__dot" :class="`optab__dot--${op.split('.')[0]}`" />
          {{ op }}
          <span class="optab__count">{{ (opCounts[op] ?? 0).toLocaleString() }}</span>
        </button>
      </div>

      <div v-if="stats" class="txstats">
        <div class="txstat">
          <span class="txstat__label">Requests</span>
          <span class="txstat__value">{{ stats.totalCount.toLocaleString() }}</span>
        </div>
        <div class="txstat">
          <span class="txstat__label">TPM</span>
          <span class="txstat__value">{{ formatTPM(stats.totalTpm) }}</span>
        </div>
        <div class="txstat">
          <span class="txstat__label">P50</span>
          <span class="txstat__value">{{ formatDuration(stats.p50) }}</span>
        </div>
        <div class="txstat">
          <span class="txstat__label">P95</span>
          <span class="txstat__value">{{ formatDuration(stats.p95) }}</span>
        </div>
        <div class="txstat">
          <span class="txstat__label">Apdex</span>
          <span class="txstat__value" :class="apdexClass(stats.apdex)">{{ stats.apdex.toFixed(2) }}</span>
        </div>
        <div class="txstat">
          <span class="txstat__label">Error rate</span>
          <span class="txstat__value" :class="stats.failureRate > 0 ? 'tx-failure' : ''">{{ formatFailureRate(stats.failureRate) }}</span>
        </div>
      </div>

      <!-- Error state -->
      <div v-if="isError" class="txerror">
        <Icon name="alert-circle" :size="16" class="txerror__icon" />
        <span>Couldn't load transactions. Check your connection and try again.</span>
        <button class="btn" @click="refetch()">Retry</button>
      </div>

      <!-- Empty state: no transactions -->
      <div v-else-if="noData" class="empty-state" style="margin-top: 48px">
        <div class="empty-state__card">
          <div class="empty-state__icon empty-state__icon--ok">
            <svg width="28" height="28" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M3 8.5l3 3 7-7" />
            </svg>
          </div>
          <h2 class="empty-state__title">No transactions in this window</h2>
          <p class="empty-state__body">Try a wider time window or check your SDK configuration.</p>
        </div>
      </div>

      <!-- Data view (charts + table) -->
      <template v-else>
        <!-- Charts -->
        <div v-if="timeseries && timeseries.buckets.length > 0" class="txcharts">
          <div class="txchart-panel">
            <div class="txchart-panel__label">Requests</div>
            <TimeseriesChart
              :times="timeseries.buckets.map(b => b.time)"
              :series="[{ id: 'count', label: 'Requests', type: 'bar', values: timeseries.buckets.map(b => b.count) }]"
              :bucket-size="timeseries.bucket_size"
            />
          </div>
          <div class="txchart-panel">
            <div class="txchart-panel__label">
              Latency
              <span class="txchart-panel__legend">
                <span class="txchart-panel__leg"><span class="txchart-panel__leg-dot txchart-panel__leg-dot--p50" />P50</span>
                <span class="txchart-panel__leg"><span class="txchart-panel__leg-dot txchart-panel__leg-dot--p95" />P95</span>
              </span>
            </div>
            <TimeseriesChart
              :times="timeseries.buckets.map(b => b.time)"
              :series="[
                { id: 'p50', label: 'P50', type: 'line', values: timeseries.buckets.map(b => b.p50) },
                { id: 'p95', label: 'P95', type: 'line', values: timeseries.buckets.map(b => b.p95), dashed: true, dimmed: true },
              ]"
              :bucket-size="timeseries.bucket_size"
              :format-value="v => formatDuration(v)"
            />
          </div>
        </div>

        <!-- Table -->
        <div class="txrow txrow--header">
          <span>Op</span>
          <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'transaction' }" @click="toggleSort('transaction')">
            Transaction <em class="col-sort__icon">{{ sortIcon('transaction') }}</em>
          </button>
          <span>Project</span>
          <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'sample_count' }" @click="toggleSort('sample_count')">
            Count <em class="col-sort__icon">{{ sortIcon('sample_count') }}</em>
          </button>
          <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'tpm' }" @click="toggleSort('tpm')">
            TPM <em class="col-sort__icon">{{ sortIcon('tpm') }}</em>
          </button>
          <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'p50' }" @click="toggleSort('p50')">
            P50 <em class="col-sort__icon">{{ sortIcon('p50') }}</em>
          </button>
          <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'p95' }" @click="toggleSort('p95')">
            P95 <em class="col-sort__icon">{{ sortIcon('p95') }}</em>
          </button>
          <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'apdex' }" @click="toggleSort('apdex')">
            Apdex <em class="col-sort__icon">{{ sortIcon('apdex') }}</em>
          </button>
          <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'failure_rate' }" @click="toggleSort('failure_rate')">
            Failure % <em class="col-sort__icon">{{ sortIcon('failure_rate') }}</em>
          </button>
          <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'time_spent_ms' }" style="justify-content: flex-end" @click="toggleSort('time_spent_ms')">
            Time spent <em class="col-sort__icon">{{ sortIcon('time_spent_ms') }}</em>
          </button>
        </div>

        <template v-if="isLoading">
          <div v-for="i in 6" :key="i" class="txrow" aria-hidden="true">
            <span class="ghost ghost--pill" style="width:40px;height:20px" />
            <span class="ghost ghost--bar" :style="{ width: ['72%','55%','83%','61%','78%','68%'][i-1] }" />
            <span class="ghost ghost--bar" style="width:70px" />
            <span class="ghost ghost--bar" style="width:32px" />
            <span class="ghost ghost--bar" style="width:48px" />
            <span class="ghost ghost--bar" style="width:32px" />
            <span class="ghost ghost--bar" style="width:32px" />
            <span class="ghost ghost--bar" style="width:28px" />
            <span class="ghost ghost--bar" style="width:24px" />
            <span class="ghost ghost--bar" style="width:40px" />
          </div>
        </template>
        <template v-else>
          <RouterLink
            v-for="(s, i) in summaries"
            :key="`${s.transaction}-${s.op}-${s.project_id}-${i}`"
            class="txrow"
            :to="{ name: 'transaction-profile', query: { name: s.transaction, op: s.op, project_id: s.project_id } }"
          >
            <span class="optag" :class="opClass(s.op)">{{ s.op.split('.')[0] }}</span>
            <span class="mono" style="color: var(--text-1); white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">{{ s.transaction }}</span>
            <span class="projtag">{{ projectName(s.project_id) }}</span>
            <span class="tx-num-cell">{{ s.sample_count.toLocaleString() }}</span>
            <span class="tx-num-cell">{{ formatTPM(s.tpm) }}</span>
            <span class="tx-num-cell tx-num-cell--stat">
              <span>{{ formatDuration(s.p50) }}</span>
              <span v-if="getDelta(s, 'p50')" class="tx-delta" :class="getDelta(s, 'p50')!.cls" :title="`vs. prev ${perf.windowHrs}`">{{ getDelta(s, 'p50')!.label }}</span>
            </span>
            <span class="tx-num-cell tx-num-cell--stat">
              <span>{{ formatDuration(s.p95) }}</span>
              <span v-if="getDelta(s, 'p95')" class="tx-delta" :class="getDelta(s, 'p95')!.cls" :title="`vs. prev ${perf.windowHrs}`">{{ getDelta(s, 'p95')!.label }}</span>
            </span>
            <span class="tx-num-cell" :class="apdexClass(s.apdex)">{{ s.apdex.toFixed(2) }}</span>
            <span class="tx-num-cell" :class="s.failure_rate > 0 ? 'tx-failure' : ''">{{ formatFailureRate(s.failure_rate) }}</span>
            <span class="tx-num-cell tx-num-cell--right">{{ formatTimeSpent(s.time_spent_ms) }}</span>
          </RouterLink>
        </template>
      </template>
    </template>
  </div>
</template>
