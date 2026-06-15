<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { apiFetch } from '@/api/client'
import { formatDuration } from '@/utils/formatters'
import { useFormatters } from '@/composables/useFormatters'
import { useTimezone } from '@/composables/useTimezone'
import type {
  IssueListPage,
  TransactionSummary,
  TxTimeseries,
  ReleaseListPage,
  AlertRule,
  User,
} from '@/api/types'

interface ProjectIssueCount {
  project_id: string
  open_issues: number
}

interface ProjectStatRow {
  projectId: string
  projectName: string
  openIssues: number
  reqPerDay: number | null
  p50: number | null
  errorRate: number | null
}
import Icon from '@/components/Icon.vue'
import Sparkline from '@/components/Sparkline.vue'
import BrandMark from '@/components/BrandMark.vue'
import { useConfig } from '@/composables/useConfig'
import { useToast } from '@/composables/useToast'

const router = useRouter()
const projects = useProjectsStore()
const { formatRel } = useFormatters()
const timezone = useTimezone()
const { dsnFor } = useConfig()
const { show: showToast } = useToast()

const { data: me } = useQuery({
  queryKey: ['me'],
  queryFn: () => apiFetch<User>('/api/me'),
})
const canManageAlerts = computed(() => me.value?.permissions.manage_alerts ?? false)

// ── Heatmap timezone helpers ──────────────────────────────────────────────────

function tzDateStr(date: Date, tz: string): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone: tz }).format(date)
}

function tzHour(date: Date, tz: string): number {
  return parseInt(new Intl.DateTimeFormat('en', { hour: 'numeric', hour12: false, timeZone: tz }).format(date), 10) % 24
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function buildQs(extra: Record<string, string> = {}) {
  const p = new URLSearchParams()
  for (const id of projects.selectedIds) p.append('project_id', id)
  for (const [k, v] of Object.entries(extra)) p.set(k, v)
  return p.toString()
}

const pKey = computed(() => [...projects.selectedIds].sort().join(','))

// ── Queries ───────────────────────────────────────────────────────────────────

const { data: issuesPage, isFetching: issuesFetching } = useQuery({
  queryKey: computed(() => ['dash-issues', pKey.value]),
  queryFn: () => apiFetch<IssueListPage>(`/api/issues?${buildQs({ status: 'open' })}`),
  refetchInterval: 30_000,
})

const { data: txSummaries, isFetching: txFetching } = useQuery({
  queryKey: computed(() => ['dash-tx', pKey.value]),
  queryFn: () =>
    apiFetch<TransactionSummary[]>(`/api/transactions/summaries?${buildQs({ hours: '24' })}`),
  refetchInterval: 60_000,
})

const { data: txTs } = useQuery({
  queryKey: computed(() => ['dash-ts', pKey.value]),
  queryFn: () =>
    apiFetch<TxTimeseries>(`/api/transactions/timeseries?${buildQs({ hours: '168' })}`),
  refetchInterval: 120_000,
})

const { data: releasesPage, isFetching: releasesFetching } = useQuery({
  queryKey: computed(() => ['dash-releases', pKey.value]),
  queryFn: () => apiFetch<ReleaseListPage>(`/api/releases?${buildQs()}`),
  refetchInterval: 60_000,
})

const { data: alertRules, isLoading: alertsLoading } = useQuery({
  queryKey: computed(() => ['dash-alerts', pKey.value]),
  queryFn: async () => {
    const res = await apiFetch<{ rules: AlertRule[] }>('/api/alert-rules')
    return res?.rules ?? []
  },
  refetchInterval: 120_000,
})

const { data: projectIssueCounts, isFetching: projStatsFetching } = useQuery({
  queryKey: ['dash-proj-stats'],
  queryFn: () => apiFetch<ProjectIssueCount[]>('/api/projects/stats'),
  enabled: computed(() => projects.projects.length >= 2),
  refetchInterval: 30_000,
})

// ── KPIs ──────────────────────────────────────────────────────────────────────

const openCount = computed(() => issuesPage.value?.total ?? 0)

const errorRate = computed((): number | null => {
  const s = txSummaries.value
  if (!s?.length) return null
  const total = s.reduce((acc, t) => acc + t.sample_count, 0)
  if (!total) return null
  return s.reduce((acc, t) => acc + t.failure_rate * t.sample_count, 0) / total
})

const p95Weighted = computed((): number | null => {
  const s = txSummaries.value
  if (!s?.length) return null
  const total = s.reduce((acc, t) => acc + t.sample_count, 0)
  if (!total) return null
  return s.reduce((acc, t) => acc + t.p95 * t.sample_count, 0) / total
})

const apdex = computed((): number | null => {
  const s = txSummaries.value
  if (!s?.length) return null
  const total = s.reduce((acc, t) => acc + t.sample_count, 0)
  if (!total) return null
  return s.reduce((acc, t) => acc + t.apdex * t.sample_count, 0) / total
})

const events24h = computed((): number | null => {
  const b = txTs.value?.buckets
  if (!b?.length) return null
  const cutoff = Date.now() - 24 * 3_600_000
  return b.filter(x => new Date(x.time).getTime() >= cutoff).reduce((s, x) => s + x.count, 0)
})

// ── Derived lists ─────────────────────────────────────────────────────────────

const hotIssues = computed(() =>
  [...(issuesPage.value?.issues ?? [])]
    .sort((a, b) => b.event_count - a.event_count)
    .slice(0, 5),
)

const slowTx = computed(() =>
  [...(txSummaries.value ?? [])].sort((a, b) => b.p95 - a.p95).slice(0, 5),
)

const releases = computed(() => (releasesPage.value?.releases ?? []).slice(0, 5))

const firedAlerts = computed(() =>
  [...(alertRules.value ?? [])]
    .filter((r): r is AlertRule & { last_fired_at: string } => r.last_fired_at !== null)
    .sort((a, b) => new Date(b.last_fired_at).getTime() - new Date(a.last_fired_at).getTime())
    .slice(0, 5),
)

const enabledAlerts = computed(() => (alertRules.value ?? []).filter(r => r.enabled).length)

const projectStats = computed((): ProjectStatRow[] => {
  if (projects.projects.length < 2) return []
  const txByProject = new Map<string, { total: number; p50sum: number; errSum: number }>()
  for (const s of txSummaries.value ?? []) {
    const cur = txByProject.get(s.project_id) ?? { total: 0, p50sum: 0, errSum: 0 }
    cur.total += s.sample_count
    cur.p50sum += s.p50 * s.sample_count
    cur.errSum += s.failure_rate * s.sample_count
    txByProject.set(s.project_id, cur)
  }
  const issueByProject = new Map<string, number>()
  for (const c of projectIssueCounts.value ?? []) {
    issueByProject.set(c.project_id, c.open_issues)
  }
  return projects.projects.map(p => {
    const tx = txByProject.get(p.id)
    return {
      projectId: p.id,
      projectName: p.name,
      openIssues: issueByProject.get(p.id) ?? 0,
      reqPerDay: tx ? tx.total : null,
      p50: tx && tx.total > 0 ? tx.p50sum / tx.total : null,
      errorRate: tx && tx.total > 0 ? tx.errSum / tx.total : null,
    }
  })
})

// ── Project overview sort + expand ────────────────────────────────────────────

type ProjSortCol = 'projectName' | 'openIssues' | 'reqPerDay' | 'p50' | 'errorRate'

function lsGet(key: string): string | null {
  try { return localStorage.getItem(key) } catch { return null }
}
function lsSet(key: string, val: string) {
  try { localStorage.setItem(key, val) } catch {}
}

const PROJ_DEFAULT_LIMIT = 5

const projSortCol = ref<ProjSortCol>((lsGet('tindra:dash:proj-sort') as ProjSortCol) ?? 'openIssues')
const projSortDir = ref<'asc' | 'desc'>((lsGet('tindra:dash:proj-sort-dir') as 'asc' | 'desc') ?? 'desc')
const projExpanded = ref(false)

function toggleProjSort(col: ProjSortCol) {
  if (projSortCol.value === col) {
    projSortDir.value = projSortDir.value === 'asc' ? 'desc' : 'asc'
    lsSet('tindra:dash:proj-sort-dir', projSortDir.value)
  } else {
    projSortCol.value = col
    projSortDir.value = col === 'projectName' ? 'asc' : 'desc'
    lsSet('tindra:dash:proj-sort', projSortCol.value)
    lsSet('tindra:dash:proj-sort-dir', projSortDir.value)
  }
}

function projSortIcon(col: ProjSortCol): string {
  if (projSortCol.value !== col) return ''
  return projSortDir.value === 'asc' ? '↑' : '↓'
}

const sortedProjectStats = computed((): ProjectStatRow[] => {
  const rows = [...projectStats.value]
  const col = projSortCol.value
  const dir = projSortDir.value === 'asc' ? 1 : -1
  return rows.sort((a, b) => {
    const av = a[col]
    const bv = b[col]
    if (av === null && bv === null) return 0
    if (av === null) return 1
    if (bv === null) return -1
    if (typeof av === 'string' && typeof bv === 'string') {
      return dir * av.localeCompare(bv)
    }
    return dir * ((av as number) - (bv as number))
  })
})

const visibleProjectStats = computed(() =>
  projExpanded.value ? sortedProjectStats.value : sortedProjectStats.value.slice(0, PROJ_DEFAULT_LIMIT),
)

// ── Heatmap ───────────────────────────────────────────────────────────────────

const dayLabels = computed(() => {
  const tz = timezone.value
  const todayStr = tzDateStr(new Date(), tz)
  const todayMs = new Date(todayStr).getTime()
  return Array.from({ length: 7 }, (_, i) => {
    const ms = todayMs - (6 - i) * 86_400_000
    return new Date(ms).toLocaleDateString('en-US', { weekday: 'short', timeZone: 'UTC' })
  })
})

const heatGrid = computed((): number[][] => {
  const grid: number[][] = Array.from({ length: 7 }, () => Array(24).fill(0))
  const buckets = txTs.value?.buckets
  if (!buckets?.length) return grid
  const tz = timezone.value
  const todayStr = tzDateStr(new Date(), tz)
  const todayMs = new Date(todayStr).getTime()
  for (const b of buckets) {
    const t = new Date(b.time)
    const bucketMs = new Date(tzDateStr(t, tz)).getTime()
    const daysAgo = Math.round((todayMs - bucketMs) / 86_400_000)
    if (daysAgo < 0 || daysAgo > 6) continue
    grid[6 - daysAgo][tzHour(t, tz)] += b.count
  }
  return grid
})

const heatMax = computed(() => Math.max(1, ...heatGrid.value.flat()))

function cellTip(di: number, hi: number, count: number): string {
  const day = dayLabels.value[di]
  const h = String(hi).padStart(2, '0')
  const hNext = hi === 23 ? '24' : String(hi + 1).padStart(2, '0')
  const countStr = count ? `${fmt(count)} event${count === 1 ? '' : 's'}` : 'no events'
  return `${day} ${h}:00–${hNext}:00 · ${countStr}`
}

function cellColor(v: number): string {
  if (!v) return ''
  const r = v / heatMax.value
  if (r < 0.12) return 'oklch(from var(--accent) l c h / 0.20)'
  if (r < 0.30) return 'oklch(from var(--accent) l c h / 0.40)'
  if (r < 0.55) return 'oklch(from var(--accent) l c h / 0.65)'
  if (r < 0.75) return 'var(--accent)'
  if (r < 0.90) return 'var(--warning)'
  return 'var(--danger)'
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function levelColor(level: string): string {
  switch (level) {
    case 'fatal':
    case 'error':
      return 'var(--danger)'
    case 'warning':
      return 'var(--warning)'
    case 'info':
      return 'var(--info)'
    default:
      return 'var(--text-3)'
  }
}

function fmt(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return String(n)
}


const loading = computed(
  () =>
    (issuesFetching.value && !issuesPage.value) ||
    (txFetching.value && !txSummaries.value) ||
    (releasesFetching.value && !releasesPage.value),
)

// ── First-run / empty state ───────────────────────────────────────────────────

const noProjects = computed(() => !(projects.projects?.length))

const firstProject = computed(() => {
  if (projects.selectedIds.length > 0) {
    return projects.projects.find(p => projects.selectedIds.includes(p.id)) ?? projects.projects[0] ?? null
  }
  return projects.projects[0] ?? null
})

const firstDsn = computed(() => {
  const p = firstProject.value
  if (!p) return null
  return dsnFor(p.public_key, p.id)
})

const isFirstRun = computed(() => {
  if (loading.value) return false
  if (noProjects.value) return true
  if (issuesPage.value === undefined || txSummaries.value === undefined) return false
  return (txSummaries.value?.length ?? 0) === 0 && (releasesPage.value?.releases?.length ?? 0) === 0
})

function copyDsn() {
  const dsn = firstDsn.value
  if (!dsn) return
  navigator.clipboard?.writeText(dsn).catch(() => {})
  showToast('DSN copied')
}
</script>

<template>
  <!-- First-run / empty state ───────────────────────────────────────────────── -->
  <div v-if="isFirstRun" class="empty-state">
    <div class="empty-state__ghosts" aria-hidden="true">
      <div
        v-for="(w, i) in ['72%','58%','81%','64%','76%','53%','69%','44%']"
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

    <div v-if="noProjects" class="empty-state__card">
      <div class="empty-state__icon">
        <BrandMark :size="32" />
      </div>
      <h2 class="empty-state__title">No projects yet</h2>
      <p class="empty-state__body">
        Create a project to get your DSN, then point any Sentry-compatible SDK at Tindra.
        Errors and traces will appear here automatically.
      </p>
      <div class="empty-state__actions">
        <button class="btn btn--primary" @click="router.push('/settings/projects?new=1')">
          <Icon name="plus" :size="12" />
          Create project
        </button>
      </div>
    </div>

    <div v-else class="empty-state__card">
      <div class="empty-state__icon">
        <Icon name="zap" :size="26" />
      </div>
      <h2 class="empty-state__title">Waiting for your first event</h2>
      <p class="empty-state__body">
        Point any Sentry-compatible SDK at Tindra using your project DSN.
        Errors, transactions, and releases will appear on this dashboard automatically.
      </p>
      <div v-if="firstDsn" class="empty-state__endpoint">
        <span class="empty-state__endpoint-label">Your DSN</span>
        <code class="mono">{{ firstDsn }}</code>
      </div>
      <div class="es-snippet">
        <span class="es-snippet__label">Quick start</span>
        <pre class="es-snippet__code">import * as Sentry from "@sentry/node"

Sentry.init({ dsn: "{{ firstDsn ?? 'YOUR_DSN' }}" })
Sentry.captureException(new Error("Hello, Tindra!"))</pre>
      </div>
      <div class="empty-state__actions">
        <button class="btn btn--primary" @click="copyDsn">
          <Icon name="copy" :size="12" />
          Copy DSN
        </button>
        <button class="btn" @click="router.push('/settings/projects')">
          Project settings
        </button>
      </div>
    </div>
  </div>

  <!-- Dashboard ─────────────────────────────────────────────────────────────── -->
  <div v-else class="page">

    <!-- KPI strip ──────────────────────────────────────────────────────────── -->
    <div class="db-kpis">
      <div class="db-kpi">
        <div class="db-kpi__label">Open Issues</div>
        <div v-if="loading" class="skel" style="width: 48px; height: 28px; margin-bottom: 4px" />
        <div v-else class="db-kpi__value">{{ fmt(openCount) }}</div>
        <div class="db-kpi__sub">
          <span :class="openCount > 0 ? 'leveldot leveldot--fatal' : 'leveldot leveldot--ok'" />
          open &amp; regressed
        </div>
      </div>

      <div class="db-kpi">
        <div class="db-kpi__label">Error Rate</div>
        <div v-if="loading" class="skel" style="width: 48px; height: 28px; margin-bottom: 4px" />
        <div
          v-else-if="errorRate !== null"
          class="db-kpi__value"
          :style="{
            color:
              errorRate > 5
                ? 'var(--danger)'
                : errorRate > 2
                  ? 'var(--warning)'
                  : undefined,
          }"
        >{{ errorRate.toFixed(1) }}%</div>
        <div v-else class="db-kpi__value db-kpi__value--muted">–</div>
        <div class="db-kpi__sub">weighted · 24h</div>
      </div>

      <div class="db-kpi">
        <div class="db-kpi__label">P95 Latency</div>
        <div v-if="loading" class="skel" style="width: 48px; height: 28px; margin-bottom: 4px" />
        <div
          v-else-if="p95Weighted !== null"
          class="db-kpi__value"
          :style="{
            color:
              p95Weighted > 2000
                ? 'var(--danger)'
                : p95Weighted > 1000
                  ? 'var(--warning)'
                  : undefined,
          }"
        >{{ formatDuration(p95Weighted) }}</div>
        <div v-else class="db-kpi__value db-kpi__value--muted">–</div>
        <div class="db-kpi__sub">weighted average · 24h</div>
      </div>

      <div class="db-kpi">
        <div class="db-kpi__label">Apdex</div>
        <div v-if="loading" class="skel" style="width: 48px; height: 28px; margin-bottom: 4px" />
        <div
          v-else-if="apdex !== null"
          class="db-kpi__value"
          :style="{
            color:
              apdex < 0.70
                ? 'var(--danger)'
                : apdex < 0.94
                  ? 'var(--warning)'
                  : undefined,
          }"
        >{{ apdex.toFixed(2) }}</div>
        <div v-else class="db-kpi__value db-kpi__value--muted">–</div>
        <div class="db-kpi__sub">weighted · T=500ms · 24h</div>
      </div>

      <div class="db-kpi">
        <div class="db-kpi__label">Transactions / 24h</div>
        <div v-if="loading" class="skel" style="width: 48px; height: 28px; margin-bottom: 4px" />
        <div v-else-if="events24h !== null" class="db-kpi__value">{{ fmt(events24h) }}</div>
        <div v-else class="db-kpi__value db-kpi__value--muted">–</div>
        <div class="db-kpi__sub">across all endpoints</div>
      </div>
    </div>

    <!-- Project overview (only when 2+ projects) ──────────────────────────── -->
    <div v-if="projects.projects.length >= 2" class="db-projects">
      <div class="db-sec__head">
        <span class="db-sec__title">Project Overview</span>
      </div>
      <div class="db-proj-head">
        <button class="col-sort" :class="{ 'col-sort--active': projSortCol === 'projectName' }" @click="toggleProjSort('projectName')">
          Project <em class="col-sort__icon">{{ projSortIcon('projectName') }}</em>
        </button>
        <button class="col-sort db-proj-head__num" :class="{ 'col-sort--active': projSortCol === 'openIssues' }" @click="toggleProjSort('openIssues')">
          Open Issues <em class="col-sort__icon">{{ projSortIcon('openIssues') }}</em>
        </button>
        <button class="col-sort db-proj-head__num" :class="{ 'col-sort--active': projSortCol === 'reqPerDay' }" @click="toggleProjSort('reqPerDay')">
          Req / 24h <em class="col-sort__icon">{{ projSortIcon('reqPerDay') }}</em>
        </button>
        <button class="col-sort db-proj-head__num" :class="{ 'col-sort--active': projSortCol === 'p50' }" @click="toggleProjSort('p50')">
          P50 <em class="col-sort__icon">{{ projSortIcon('p50') }}</em>
        </button>
        <button class="col-sort db-proj-head__num" :class="{ 'col-sort--active': projSortCol === 'errorRate' }" @click="toggleProjSort('errorRate')">
          Error Rate <em class="col-sort__icon">{{ projSortIcon('errorRate') }}</em>
        </button>
      </div>
      <template v-if="projStatsFetching && !projectIssueCounts">
        <div v-for="i in projects.projects.length" :key="i" class="db-proj-row">
          <div class="skel" style="width: 120px; height: 10px" />
          <div class="skel" style="width: 40px; height: 10px; justify-self: end" />
          <div class="skel" style="width: 50px; height: 10px; justify-self: end" />
          <div class="skel" style="width: 44px; height: 10px; justify-self: end" />
          <div class="skel" style="width: 44px; height: 10px; justify-self: end" />
        </div>
      </template>
      <template v-else>
        <div
          v-for="row in visibleProjectStats"
          :key="row.projectId"
          class="db-proj-row"
        >
          <span class="db-proj-row__name">{{ row.projectName }}</span>
          <RouterLink
            class="db-proj-row__val db-proj-row__link"
            :class="{ 'db-proj-row__val--bad': row.openIssues > 0 }"
            :to="{ name: 'issues', query: { project_id: row.projectId } }"
          >{{ row.openIssues }}</RouterLink>
          <RouterLink
            v-if="row.reqPerDay !== null"
            class="db-proj-row__val db-proj-row__link"
            :to="{ name: 'transactions', query: { project_id: row.projectId } }"
          >{{ fmt(row.reqPerDay) }}</RouterLink>
          <span v-else class="db-proj-row__val">–</span>
          <span
            class="db-proj-row__val"
            :class="{ 'db-proj-row__val--warn': row.p50 !== null && row.p50 > 500 }"
          >{{ row.p50 !== null ? formatDuration(row.p50) : '–' }}</span>
          <span
            class="db-proj-row__val"
            :class="{ 'db-proj-row__val--bad': row.errorRate !== null && row.errorRate > 2 }"
          >{{ row.errorRate !== null ? row.errorRate.toFixed(1) + '%' : '–' }}</span>
        </div>
        <button
          v-if="sortedProjectStats.length > PROJ_DEFAULT_LIMIT"
          class="db-proj-expand"
          @click="projExpanded = !projExpanded"
        >
          <template v-if="projExpanded">Show less</template>
          <template v-else>Show {{ sortedProjectStats.length - PROJ_DEFAULT_LIMIT }} more&hellip;</template>
        </button>
      </template>
    </div>

    <!-- Body ───────────────────────────────────────────────────────────────── -->
    <div class="db-body">

      <!-- Main column ──────────────────────────────────────────────────────── -->
      <div class="db-main">

        <!-- Transaction density heatmap -->
        <div class="db-sec">
          <div class="db-sec__head">
            <span class="db-sec__title">Transaction density - 7 days × 24 hours</span>
            <span class="db-sec__hint">each cell = 1 hour</span>
          </div>
          <div class="db-heatmap">
            <div
              v-for="(row, di) in heatGrid"
              :key="di"
              class="db-heatmap__row"
            >
              <span class="db-heatmap__dlabel">{{ dayLabels[di] }}</span>
              <div
                v-for="(cell, hi) in row"
                :key="hi"
                class="db-heatmap__cell"
                :data-tip="cellTip(di, hi, cell)"
                :style="cellColor(cell) ? { background: cellColor(cell) } : undefined"
              />
            </div>
            <div class="db-heatmap__foot">
              <span class="db-heatmap__leg-lbl">low</span>
              <div class="db-heatmap__cell db-heatmap__cell--legend" style="background: oklch(from var(--accent) l c h / 0.20)" />
              <div class="db-heatmap__cell db-heatmap__cell--legend" style="background: oklch(from var(--accent) l c h / 0.40)" />
              <div class="db-heatmap__cell db-heatmap__cell--legend" style="background: oklch(from var(--accent) l c h / 0.65)" />
              <div class="db-heatmap__cell db-heatmap__cell--legend" style="background: var(--accent)" />
              <div class="db-heatmap__cell db-heatmap__cell--legend" style="background: var(--warning)" />
              <div class="db-heatmap__cell db-heatmap__cell--legend" style="background: var(--danger)" />
              <span class="db-heatmap__leg-lbl">high</span>
            </div>
          </div>
        </div>

        <!-- Hottest issues -->
        <div class="db-sec">
          <div class="db-sec__head">
            <span class="db-sec__title">Hottest Issues</span>
            <RouterLink to="/issues" class="db-sec__link">all issues →</RouterLink>
          </div>
          <template v-if="loading">
            <div v-for="i in 5" :key="i" class="db-issue-row">
              <div class="skel" style="width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0" />
              <div class="skel" style="flex: 1; height: 10px" />
              <div class="skel" style="width: 28px; height: 10px; flex-shrink: 0" />
            </div>
          </template>
          <div v-else-if="hotIssues.length === 0" class="db-empty">
            <Icon name="check-circle" :size="18" style="color: var(--success)" />
            <div>No open issues</div>
          </div>
          <template v-else>
            <div
              v-for="issue in hotIssues"
              :key="issue.id"
              class="db-issue-row"
              @click="router.push(`/issues/${issue.id}`)"
            >
              <span :class="`leveldot leveldot--${issue.level}`" />
              <div class="db-issue-row__title">{{ issue.title }}</div>
              <div class="db-issue-row__right">
                <span class="db-issue-row__count">{{ fmt(issue.event_count) }}</span>
                <Sparkline
                  v-if="issue.sparkline?.length"
                  :data="issue.sparkline"
                  :width="40"
                  :height="14"
                  :color="levelColor(issue.level)"
                />
              </div>
            </div>
          </template>
        </div>

        <!-- Slowest transactions -->
        <div class="db-sec">
          <div class="db-sec__head">
            <span class="db-sec__title">Slowest Transactions</span>
            <RouterLink to="/performance/transactions" class="db-sec__link">open performance →</RouterLink>
          </div>
          <template v-if="loading">
            <div v-for="i in 5" :key="i" class="db-tx-row">
              <div class="skel" style="flex: 1; height: 10px" />
              <div class="skel" style="width: 44px; height: 10px" />
              <div class="skel" style="width: 44px; height: 10px" />
              <div class="skel" style="width: 80px; height: 5px; border-radius: 3px" />
            </div>
          </template>
          <div v-else-if="slowTx.length === 0" class="db-empty">
            <Icon name="activity" :size="18" style="color: var(--text-3)" />
            <div>No transaction data in the last 24h</div>
          </div>
          <template v-else>
            <div class="db-tx-head">
              <span>Op</span>
              <span>Transaction</span>
              <span>P50</span>
              <span>P95</span>
              <span>Error rate</span>
            </div>
            <RouterLink
              v-for="tx in slowTx"
              :key="`${tx.transaction}-${tx.op}`"
              :to="{ name: 'transaction-profile', query: { name: tx.transaction, op: tx.op } }"
              class="db-tx-row"
            >
              <span class="optag" :class="`optag--${tx.op.split('.')[0]}`">{{ tx.op.split('.')[0] }}</span>
              <span class="db-tx-row__name">{{ tx.transaction }}</span>
              <span
                class="db-tx-row__val"
                :class="{ 'db-tx-row__val--warn': tx.p50 > 500 }"
              >{{ formatDuration(tx.p50) }}</span>
              <span
                class="db-tx-row__val"
                :class="{ 'db-tx-row__val--warn': tx.p95 > 1000, 'db-tx-row__val--bad': tx.p95 > 2000 }"
              >{{ formatDuration(tx.p95) }}</span>
              <div class="db-tx-row__bar">
                <div class="db-tx-row__bar-bg">
                  <div
                    class="db-tx-row__bar-fill"
                    :class="{ 'db-tx-row__bar-fill--warn': tx.failure_rate > 2 }"
                    :style="{ width: `${Math.min(100, tx.failure_rate * 10)}%` }"
                  />
                </div>
                <span
                  class="db-tx-row__pct"
                  :class="{ 'db-tx-row__pct--warn': tx.failure_rate > 2 }"
                >{{ tx.failure_rate.toFixed(1) }}%</span>
              </div>
            </RouterLink>
          </template>
        </div>

      </div>

      <!-- Aside column ─────────────────────────────────────────────────────── -->
      <div class="db-aside">

        <!-- Recent alerts -->
        <div class="db-sec">
          <div class="db-sec__head">
            <span class="db-sec__title">Recent Alerts</span>
            <RouterLink v-if="canManageAlerts" to="/settings/alerts" class="db-sec__link">configure →</RouterLink>
          </div>
          <template v-if="alertsLoading">
            <div v-for="i in 3" :key="i" class="db-alert-row">
              <div class="skel" style="width: 20px; height: 20px; border-radius: 4px; flex-shrink: 0" />
              <div style="flex: 1">
                <div class="skel" style="width: 80%; height: 10px; margin-bottom: 5px" />
                <div class="skel" style="width: 50%; height: 9px" />
              </div>
            </div>
          </template>
          <div v-else-if="firedAlerts.length === 0" class="db-empty">
            <Icon name="check-circle" :size="18" style="color: var(--success)" />
            <div>No alerts fired recently</div>
          </div>
          <template v-else>
            <div
              v-for="a in firedAlerts"
              :key="a.id"
              class="db-alert-row"
            >
              <div class="db-alert-row__icon">
                <Icon name="zap" :size="11" style="color: var(--warning)" />
              </div>
              <div class="db-alert-row__body">
                <div class="db-alert-row__name">{{ a.name }}</div>
                <div class="db-alert-row__time">{{ formatRel(a.last_fired_at) }} · {{ a.channel }}</div>
              </div>
            </div>
          </template>
          <div class="db-sec__foot">
            {{ enabledAlerts }} rule{{ enabledAlerts === 1 ? '' : 's' }} active
          </div>
        </div>

        <!-- Release health -->
        <div class="db-sec">
          <div class="db-sec__head">
            <span class="db-sec__title">Release Health</span>
            <RouterLink to="/releases" class="db-sec__link">all releases →</RouterLink>
          </div>
          <template v-if="loading">
            <div v-for="i in 4" :key="i" class="db-release-row">
              <div class="skel" style="width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0" />
              <div style="flex: 1">
                <div class="skel" style="width: 90px; height: 10px; margin-bottom: 5px" />
                <div class="skel" style="width: 60px; height: 9px" />
              </div>
              <div class="skel" style="width: 50px; height: 18px; border-radius: 3px" />
            </div>
          </template>
          <div v-else-if="releases.length === 0" class="db-empty">
            <Icon name="package" :size="18" style="color: var(--text-3)" />
            <div>No releases yet</div>
          </div>
          <template v-else>
            <div
              v-for="rel in releases"
              :key="rel.id"
              class="db-release-row"
              @click="router.push(`/releases/${rel.id}`)"
            >
              <div
                class="db-release-row__dot"
                :class="{
                  'db-release-row__dot--bad': rel.regressed_issues > 0,
                  'db-release-row__dot--ok': rel.regressed_issues === 0 && rel.new_issues === 0,
                }"
              />
              <div class="db-release-row__info">
                <div class="db-release-row__version mono">{{ rel.version }}</div>
                <div class="db-release-row__time">{{ formatRel(rel.deployed_at) }}</div>
              </div>
              <span
                v-if="rel.regressed_issues > 0"
                class="rel-pill rel-pill--bad"
              >{{ rel.regressed_issues }} regressed</span>
              <span
                v-else-if="rel.new_issues > 0"
                class="rel-pill rel-pill--bad"
              >{{ rel.new_issues }} new</span>
              <span v-else class="rel-pill rel-pill--ok">Clean</span>
            </div>
          </template>
        </div>

      </div>
    </div>
  </div>
</template>

<style scoped>
/* ── KPI strip ──────────────────────────────────────────────────────────────── */

.db-kpis {
  display: grid;
  grid-template-columns: repeat(5, 1fr);
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}

.db-kpi {
  padding: 10px 16px;
  border-right: 1px solid var(--border);
}

.db-kpi:last-child {
  border-right: none;
}

.db-kpi__label {
  font-size: var(--text-xs);
  font-weight: 500;
  text-transform: uppercase;
  letter-spacing: 0.07em;
  color: var(--text-3);
  margin-bottom: 8px;
}

.db-kpi__value {
  font-size: var(--text-2xl);
  font-weight: 700;
  color: var(--text-1);
  letter-spacing: -0.04em;
  line-height: 1;
  margin-bottom: 6px;
  font-variant-numeric: tabular-nums;
}

.db-kpi__value--muted {
  color: var(--text-3);
  font-weight: 400;
}

.db-kpi__sub {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: var(--text-xs);
  color: var(--text-3);
}

/* ── Project overview ───────────────────────────────────────────────────────── */

.db-projects {
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}

.db-proj-head,
.db-proj-row {
  display: grid;
  grid-template-columns: 1fr 110px 100px 90px 110px;
  column-gap: 12px;
  padding: 7px 16px;
  align-items: center;
}

.db-proj-head {
  font-size: var(--text-xs);
  color: var(--text-3);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  border-bottom: 1px solid var(--border);
}

.db-proj-head__num {
  justify-self: end;
  justify-content: flex-end;
}

.db-proj-row {
  border-bottom: 1px solid var(--border);
  padding: 8px 16px;
}

.db-proj-row:last-of-type { border-bottom: none; }

.db-proj-expand {
  all: unset;
  box-sizing: border-box;
  cursor: pointer;
  display: block;
  width: 100%;
  padding: 8px 16px;
  font-size: var(--text-xs);
  color: var(--accent);
  text-align: center;
  border-top: 1px solid var(--border);
}

.db-proj-expand:hover { text-decoration: underline; text-underline-offset: 2px; }

.db-proj-row__name {
  font-size: var(--text-sm);
  color: var(--text-2);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.db-proj-row__val {
  font-size: var(--text-sm);
  color: var(--text-3);
  text-align: right;
  font-variant-numeric: tabular-nums;
}

.db-proj-row__val--warn { color: var(--warning); }
.db-proj-row__val--bad  { color: var(--danger); }

.db-proj-row__link {
  cursor: pointer;
  text-decoration: none;
}

.db-proj-row__link:hover {
  text-decoration: underline;
  text-underline-offset: 2px;
}

/* ── Body layout ────────────────────────────────────────────────────────────── */

.db-body {
  display: grid;
  grid-template-columns: 1fr 280px;
  align-items: start;
  flex: 1;
}

.db-main {
  border-right: 1px solid var(--border);
  min-width: 0;
}

.db-aside {
  min-width: 0;
}

/* ── Sections ───────────────────────────────────────────────────────────────── */

.db-sec + .db-sec {
  border-top: 1px solid var(--border);
}

.db-sec__head {
  padding: 9px 16px;
  background: var(--surface);
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.db-sec__title {
  font-size: var(--text-xs);
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-3);
}

.db-sec__hint {
  font-size: var(--text-xs);
  color: var(--text-3);
  opacity: 0.55;
}

.db-sec__link {
  font-size: var(--text-xs);
  color: var(--accent);
  opacity: 0.75;
  text-decoration: none;
}

.db-sec__link:hover { opacity: 1; }

.db-sec__foot {
  padding: 8px 16px;
  border-top: 1px solid var(--border);
  font-size: var(--text-xs);
  color: var(--text-3);
}

/* ── Empty state ────────────────────────────────────────────────────────────── */

.db-empty {
  padding: 28px 16px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  color: var(--text-3);
  font-size: var(--text-sm);
}

/* ── Heatmap ────────────────────────────────────────────────────────────────── */

.db-heatmap {
  padding: 8px 16px 10px;
}

.db-heatmap__row {
  display: flex;
  align-items: center;
  gap: 2px;
  margin-bottom: 2px;
}

.db-heatmap__dlabel {
  font-size: 10px;
  color: var(--text-3);
  opacity: 0.5;
  width: 28px;
  text-align: right;
  padding-right: 5px;
  flex-shrink: 0;
}

.db-heatmap__cell {
  position: relative;
  flex: 1;
  height: 11px;
  border-radius: 2px;
  background: var(--surface-2);
  min-width: 0;
}

.db-heatmap__cell[data-tip]:hover::after {
  content: attr(data-tip);
  position: absolute;
  bottom: calc(100% + 7px);
  left: 50%;
  transform: translateX(-50%);
  background: var(--surface);
  border: 1px solid var(--border);
  color: var(--text-2);
  font-size: 11px;
  line-height: 1.4;
  padding: 4px 8px;
  border-radius: 4px;
  white-space: nowrap;
  z-index: 200;
  pointer-events: none;
  box-shadow: 0 2px 6px oklch(0 0 0 / 0.12);
}

.db-heatmap__cell--legend {
  flex: none;
  width: 11px;
}

.db-heatmap__foot {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-top: 10px;
  justify-content: flex-end;
}

.db-heatmap__leg-lbl {
  font-size: 10px;
  color: var(--text-3);
  opacity: 0.5;
}

/* ── Alerts ─────────────────────────────────────────────────────────────────── */

.db-alert-row {
  padding: 9px 16px;
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.db-alert-row:last-of-type { border-bottom: none; }

.db-alert-row__icon {
  width: 20px;
  height: 20px;
  border-radius: 4px;
  background: oklch(from var(--warning) l c h / 0.12);
  border: 1px solid oklch(from var(--warning) l c h / 0.22);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.db-alert-row__name {
  font-size: var(--text-sm);
  color: var(--text-2);
  margin-bottom: 2px;
}

.db-alert-row__time {
  font-size: var(--text-xs);
  color: var(--text-3);
}

/* ── Issues ─────────────────────────────────────────────────────────────────── */

.db-issue-row {
  padding: 9px 16px;
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: center;
  gap: 10px;
  cursor: pointer;
}

.db-issue-row:last-of-type { border-bottom: none; }
.db-issue-row:hover { background: var(--surface-2); }

.db-issue-row__title {
  flex: 1;
  font-size: var(--text-sm);
  color: var(--text-2);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.db-issue-row__right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.db-issue-row__count {
  font-size: var(--text-xs);
  color: var(--text-3);
  font-variant-numeric: tabular-nums;
  min-width: 28px;
  text-align: right;
}

/* ── Releases ───────────────────────────────────────────────────────────────── */

.db-release-row {
  padding: 10px 16px;
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: center;
  gap: 10px;
  cursor: pointer;
}

.db-release-row:last-of-type { border-bottom: none; }
.db-release-row:hover { background: var(--surface-2); }

.db-release-row__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--accent);
  flex-shrink: 0;
}

.db-release-row__dot--bad { background: var(--danger); }
.db-release-row__dot--ok  { background: var(--success); }

.db-release-row__info { flex: 1; min-width: 0; }

.db-release-row__version {
  font-size: var(--text-sm);
  color: var(--accent);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.db-release-row__time {
  font-size: var(--text-xs);
  color: var(--text-3);
  margin-top: 2px;
}

.rel-pill {
  font-size: 10px;
  font-weight: 500;
  padding: 2px 7px;
  border-radius: 3px;
  white-space: nowrap;
  flex-shrink: 0;
}

.rel-pill--bad {
  background: oklch(from var(--danger) l c h / 0.12);
  color: var(--danger);
}

.rel-pill--ok {
  background: oklch(from var(--success) l c h / 0.12);
  color: var(--success);
}

/* ── Transactions table ─────────────────────────────────────────────────────── */

.db-tx-head {
  display: grid;
  grid-template-columns: 80px 1fr 72px 72px 120px;
  column-gap: 12px;
  padding: 7px 16px;
  font-size: var(--text-xs);
  color: var(--text-3);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  border-bottom: 1px solid var(--border);
}
.db-tx-head span:nth-child(n+3) { text-align: right; }

.db-tx-row {
  display: grid;
  grid-template-columns: 80px 1fr 72px 72px 120px;
  column-gap: 12px;
  padding: 8px 16px;
  border-bottom: 1px solid var(--border);
  align-items: center;
  text-decoration: none;
  color: inherit;
  cursor: pointer;
}

.db-tx-row:last-of-type { border-bottom: none; }
.db-tx-row:hover { background: var(--surface-2); }

.db-tx-row__name {
  font-size: var(--text-xs);
  color: var(--text-2);
  font-family: var(--mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  padding-right: 12px;
}

.db-tx-row__val {
  font-size: var(--text-sm);
  color: var(--text-3);
  text-align: right;
  font-variant-numeric: tabular-nums;
}

.db-tx-row__val--warn { color: var(--warning); }
.db-tx-row__val--bad  { color: var(--danger); }

.db-tx-row__bar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding-left: 10px;
}

.db-tx-row__bar-bg {
  flex: 1;
  height: 4px;
  background: var(--surface-2);
  border-radius: 2px;
  overflow: hidden;
}

.db-tx-row__bar-fill {
  height: 100%;
  background: var(--accent);
  border-radius: 2px;
}

.db-tx-row__bar-fill--warn { background: var(--danger); }

.db-tx-row__pct {
  font-size: 10px;
  color: var(--text-3);
  width: 30px;
  text-align: right;
  font-variant-numeric: tabular-nums;
}

@media (max-width: 768px) {
  /* KPI strip: 2-column grid (5 cards → 2+2+1) */
  .db-kpis {
    grid-template-columns: 1fr 1fr;
  }
  .db-kpi:nth-child(2) { border-right: none; }
  .db-kpi:nth-child(3) { border-top: 1px solid var(--border); }
  .db-kpi:nth-child(4) { border-top: 1px solid var(--border); border-right: none; }
  .db-kpi:nth-child(5) { border-top: 1px solid var(--border); grid-column: 1 / -1; border-right: none; }

  /* Body: single column, sidebar below main */
  .db-body {
    grid-template-columns: 1fr;
  }
  .db-main {
    border-right: none;
    border-bottom: 1px solid var(--border);
  }

  /* Project table: drop P50 + Error Rate, keep Project | Open Issues | Req/24h */
  .db-proj-head,
  .db-proj-row {
    grid-template-columns: 1fr 110px 100px;
  }
  .db-proj-head > :nth-child(4),
  .db-proj-head > :nth-child(5),
  .db-proj-row > :nth-child(4),
  .db-proj-row > :nth-child(5) { display: none; }

  /* Separate major sections with visible gaps so they read as distinct blocks */
  .db-projects { margin-top: 16px; border-top: 1px solid var(--border); }
  .db-body     { margin-top: 16px; }
  .db-sec + .db-sec { margin-top: 16px; }
  .db-sec__head { border-top: 1px solid var(--border); border-bottom: 1px solid var(--border); }

  /* Heatmap: allow horizontal scroll */
  .db-heatmap {
    overflow-x: auto;
    -webkit-overflow-scrolling: touch;
    padding-bottom: 4px;
  }
  .db-heatmap__row { flex-shrink: 0; }

  /* Transactions table: drop P50 + error-rate bar, keep op + name + P95 */
  .db-tx-head,
  .db-tx-row {
    grid-template-columns: 80px 1fr 72px;
  }
  .db-tx-head > :nth-child(3),
  .db-tx-row > :nth-child(3) { display: none; } /* P50 */
  .db-tx-head > :nth-child(5),
  .db-tx-row > :nth-child(5) { display: none; } /* error rate bar */
}

@media (max-width: 480px) {
  /* Project table: safety-net scroll only — no forced min-width so the grid
     adapts naturally and doesn't create spurious horizontal overflow */
  .db-projects {
    overflow-x: auto;
    -webkit-overflow-scrolling: touch;
  }

  /* KPI strip: single column on very small phones */
  .db-kpis {
    grid-template-columns: 1fr 1fr;
  }
  .db-kpi {
    padding: 12px 14px;
  }
  .db-kpi__value {
    font-size: var(--text-xl);
  }
}

.db-tx-row__pct--warn { color: var(--danger); }

/* ── First-run snippet ──────────────────────────────────────────────────────── */

.es-snippet {
  margin-top: 16px;
  padding: 10px 14px;
  background: var(--surface);
  border: 1px solid var(--border-soft);
  border-radius: 5px;
  text-align: left;
  width: 100%;
}

.es-snippet__label {
  display: block;
  font-size: var(--text-xs);
  color: var(--text-3);
  text-transform: uppercase;
  letter-spacing: 0.07em;
  margin-bottom: 8px;
}

.es-snippet__code {
  margin: 0;
  font-family: var(--mono);
  font-size: var(--text-xs);
  color: var(--text-2);
  line-height: 1.6;
  white-space: pre;
  overflow-x: auto;
}
</style>
