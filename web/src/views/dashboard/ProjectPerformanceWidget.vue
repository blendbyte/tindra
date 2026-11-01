<script setup lang="ts">
import { computed, ref } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { apiFetch } from '@/api/client'
import { investigationQuery } from '@/router/investigation'
import { useInvestigationStore } from '@/stores/investigation'
import { useMonitorQueries } from '@/composables/useBriefingData'
import { formatDuration, formatFraction } from '@/utils/formatters'
import Icon from '@/components/Icon.vue'
import Sparkline from '@/components/Sparkline.vue'
import { brokenMonitors, healthyMonitorCounts } from './briefing'
import { apdexTone, enoughSamples, errorTone, failureRose, p95Rose, p95Tone, type PerformanceOverview, type ProjectPerformance, type Tone } from './overview'
import { fmt, useWindowLabel } from './shared'

interface ProjectIssueCount {
  project_id: string
  open_issues: number
}

const props = defineProps<{
  loading: boolean
  overview?: PerformanceOverview
}>()

const projects = useProjectsStore()
const investigation = useInvestigationStore()
const windowLabel = useWindowLabel()
const { uptime, cron } = useMonitorQueries()

const { data: projectIssueCounts } = useQuery({
  queryKey: ['projects', 'stats'],
  queryFn: ({ signal }) => apiFetch<ProjectIssueCount[]>('/api/projects/stats', { signal }),
  enabled: computed(() => projects.projects.length >= 2),
})

// Without monitor data a project's health is unknown, never shown as healthy.
const monitorsUnknown = computed(() =>
  (!!uptime.isError.value && !uptime.data.value) || (!!cron.isError.value && !cron.data.value))

interface Row {
  id: string
  name: string
  perf: ProjectPerformance | null
  open: number
  failing: number
  healthy: number
}

const visibleProjects = computed(() =>
  projects.projects.filter(p => !projects.selectedIds.length || projects.selectedIds.includes(p.id)))

const rows = computed((): Row[] => {
  const perf = new Map((props.overview?.projects ?? []).map(p => [p.project_id, p]))
  const open = new Map((projectIssueCounts.value ?? []).map(c => [c.project_id, c.open_issues]))
  const failing = new Map<string, number>()
  for (const b of brokenMonitors(uptime.data.value ?? [], cron.data.value ?? [])) {
    failing.set(b.monitor.project_id, (failing.get(b.monitor.project_id) ?? 0) + 1)
  }
  const healthy = healthyMonitorCounts(uptime.data.value ?? [], cron.data.value ?? [])
  return visibleProjects.value.map(p => {
    const row = perf.get(p.id)
    return {
      id: p.id,
      name: p.name,
      perf: row && row.requests > 0 ? row : null,
      open: open.get(p.id) ?? 0,
      failing: failing.get(p.id) ?? 0,
      healthy: healthy.get(p.id) ?? 0,
    }
  })
})

// ── Sort ──────────────────────────────────────────────────────────────────────

const COLUMNS = ['name', 'requests', 'p50', 'p95', 'errors', 'apdex', 'open', 'monitors'] as const
type Column = typeof COLUMNS[number]

function stored(key: string): string | null {
  try { return localStorage.getItem(key) } catch { return null }
}
function store(key: string, value: string) {
  try { localStorage.setItem(key, value) } catch { /* storage unavailable */ }
}

const storedSort = stored('tindra:dash:proj-sort')
// No column until someone picks one: the default order is by urgency.
const sortCol = ref<Column | null>(COLUMNS.includes(storedSort as Column) ? storedSort as Column : null)
const sortDir = ref<'asc' | 'desc'>(stored('tindra:dash:proj-sort-dir') === 'asc' ? 'asc' : 'desc')

function toggleSort(col: Column) {
  if (sortCol.value === col) {
    sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
  } else {
    sortCol.value = col
    sortDir.value = col === 'name' ? 'asc' : 'desc'
  }
  store('tindra:dash:proj-sort', col)
  store('tindra:dash:proj-sort-dir', sortDir.value)
}

function sortValue(row: Row, col: Column): number | string | null {
  switch (col) {
    case 'name': return row.name
    case 'requests': return row.perf?.requests ?? null
    case 'p50': return row.perf?.p50 ?? null
    case 'p95': return row.perf?.p95 ?? null
    case 'errors': return row.perf?.failure_rate ?? null
    case 'apdex': return row.perf?.apdex ?? null
    case 'open': return row.open
    case 'monitors': return row.failing * 1000 + row.healthy
  }
}

const sortedRows = computed((): Row[] => {
  const col = sortCol.value
  const list = [...rows.value]
  if (!col) {
    // Failing monitors first, then the highest error rate, then open issues.
    return list.sort((a, b) =>
      b.failing - a.failing
      || (b.perf?.failure_rate ?? 0) - (a.perf?.failure_rate ?? 0)
      || b.open - a.open
      || a.name.localeCompare(b.name))
  }
  const dir = sortDir.value === 'asc' ? 1 : -1
  return list.sort((a, b) => {
    const av = sortValue(a, col)
    const bv = sortValue(b, col)
    // A project without traffic has nothing to rank, whichever way this sorts.
    if (av === null || bv === null) return av === bv ? 0 : av === null ? 1 : -1
    if (typeof av === 'string' || typeof bv === 'string') return dir * String(av).localeCompare(String(bv))
    return dir * (av - bv)
  })
})

const LIMIT = 8
const expanded = ref(false)
const shown = computed(() => expanded.value ? sortedRows.value : sortedRows.value.slice(0, LIMIT))

// ── Cells ─────────────────────────────────────────────────────────────────────

function toneClass(row: ProjectPerformance, tone: Tone): string {
  return tone && enoughSamples(row) ? `ptable__val--${tone}` : ''
}

const projectQuery = (id: string) => ({ ...investigationQuery(investigation), project_id: id })

function ariaSort(col: Column): 'ascending' | 'descending' | undefined {
  if (sortCol.value !== col) return undefined
  return sortDir.value === 'asc' ? 'ascending' : 'descending'
}

const headers: { col: Column; label: string; cls: string }[] = [
  { col: 'p50', label: 'p50', cls: 'ptable__col--p50' },
  { col: 'p95', label: 'p95', cls: '' },
  { col: 'errors', label: 'Errors', cls: '' },
  { col: 'apdex', label: 'Apdex', cls: 'ptable__col--apdex' },
  { col: 'open', label: 'Open', cls: '' },
  { col: 'monitors', label: 'Monitors', cls: 'ptable__col--monitors' },
]
</script>

<template>
  <section v-if="visibleProjects.length >= 2" class="db-sec" aria-labelledby="projects-title">
    <div class="db-sec__head">
      <h2 id="projects-title" class="db-sec__title">Projects · {{ windowLabel }}</h2>
      <RouterLink :to="investigation.link('/performance/transactions')" class="db-sec__link">performance <Icon name="arrow-right" :size="11" /></RouterLink>
    </div>
    <div class="ptable-wrap">
      <table class="ptable">
        <thead>
          <tr>
            <th scope="col" class="ptable__name" :aria-sort="ariaSort('name')">
              <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'name' }" @click="toggleSort('name')">
                Project <span v-if="sortCol === 'name'" class="col-sort__icon"><Icon :name="sortDir === 'desc' ? 'arrow-down' : 'arrow-up'" :size="10" /></span>
              </button>
            </th>
            <th class="ptable__trace" aria-hidden="true" />
            <th scope="col" class="ptable__num ptable__col--requests" :aria-sort="ariaSort('requests')">
              <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'requests' }" @click="toggleSort('requests')">
                <span v-if="sortCol === 'requests'" class="col-sort__icon"><Icon :name="sortDir === 'desc' ? 'arrow-down' : 'arrow-up'" :size="10" /></span> Requests
              </button>
            </th>
            <th v-for="h in headers" :key="h.col" scope="col" class="ptable__num" :class="h.cls" :aria-sort="ariaSort(h.col)">
              <button class="col-sort" :class="{ 'col-sort--active': sortCol === h.col }" @click="toggleSort(h.col)">
                <span v-if="sortCol === h.col" class="col-sort__icon"><Icon :name="sortDir === 'desc' ? 'arrow-down' : 'arrow-up'" :size="10" /></span> {{ h.label }}
              </button>
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in shown" :key="row.id" class="ptable__row">
            <th scope="row" class="ptable__name">
              <RouterLink :to="{ name: 'transactions', query: projectQuery(row.id) }" class="ptable__project">
                <span class="ptable__dot" :class="row.failing ? 'ptable__dot--bad' : monitorsUnknown ? 'ptable__dot--unknown' : 'ptable__dot--ok'" />
                <span class="ptable__project-name" :title="row.name">{{ row.name }}</span>
              </RouterLink>
            </th>
            <td class="ptable__trace" aria-hidden="true">
              <Sparkline v-if="row.perf?.trend?.length" :data="row.perf.trend" :width="88" :height="18" color="var(--text-3)" />
            </td>
            <td class="ptable__num ptable__col--requests">
              <span v-if="loading" class="skel ptable__skel" />
              <template v-else-if="row.perf">{{ fmt(row.perf.requests) }}</template>
              <span v-else class="ptable__none">–</span>
            </td>
            <td class="ptable__num ptable__col--p50">
              <span v-if="loading" class="skel ptable__skel" />
              <template v-else-if="row.perf">{{ formatDuration(row.perf.p50) }}</template>
              <span v-else class="ptable__none">–</span>
            </td>
            <td class="ptable__num">
              <span v-if="loading" class="skel ptable__skel" />
              <span v-else-if="row.perf" :class="toneClass(row.perf, p95Tone(row.perf.p95))">
                <span
                  v-if="p95Rose(row.perf)"
                  class="ptable__rose"
                  :title="`Up from ${formatDuration(row.perf.prev_p95)} in the previous ${windowLabel}`"
                ><Icon name="arrow-up" :size="10" /><span class="sr-only">Up from {{ formatDuration(row.perf.prev_p95) }}:</span></span>
                {{ formatDuration(row.perf.p95) }}
              </span>
              <span v-else class="ptable__none">–</span>
            </td>
            <td class="ptable__num">
              <span v-if="loading" class="skel ptable__skel" />
              <span v-else-if="row.perf" :class="toneClass(row.perf, errorTone(row.perf.failure_rate))">
                <span
                  v-if="failureRose(row.perf)"
                  class="ptable__rose"
                  :title="`Up from ${formatFraction(row.perf.prev_failure_rate)} in the previous ${windowLabel}`"
                ><Icon name="arrow-up" :size="10" /><span class="sr-only">Up from {{ formatFraction(row.perf.prev_failure_rate) }}:</span></span>
                {{ formatFraction(row.perf.failure_rate) }}
              </span>
              <span v-else class="ptable__none">–</span>
            </td>
            <td class="ptable__num ptable__col--apdex">
              <span v-if="loading" class="skel ptable__skel" />
              <span v-else-if="row.perf" :class="toneClass(row.perf, apdexTone(row.perf.apdex))">{{ row.perf.apdex.toFixed(2) }}</span>
              <span v-else class="ptable__none">–</span>
            </td>
            <td class="ptable__num">
              <RouterLink :to="{ name: 'issues', query: projectQuery(row.id) }" class="ptable__link">{{ row.open }}</RouterLink>
            </td>
            <td class="ptable__num ptable__col--monitors">
              <RouterLink
                v-if="row.failing"
                :to="{ path: '/monitors', query: projectQuery(row.id) }"
                class="ptable__link ptable__val--danger"
              >{{ row.failing }} down</RouterLink>
              <RouterLink v-else-if="row.healthy" :to="{ path: '/monitors', query: projectQuery(row.id) }" class="ptable__link">{{ row.healthy }} up</RouterLink>
              <span v-else class="ptable__none">–</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <button v-if="sortedRows.length > LIMIT" class="ptable__expand" @click="expanded = !expanded">
      {{ expanded ? 'Show less' : `Show ${sortedRows.length - LIMIT} more` }}
    </button>
  </section>
</template>

<style scoped src="./section.css"></style>

<style scoped>
/* Columns drop by the width of this column rather than the window's, since the
   aside takes a different share of the page at each breakpoint. */
.ptable-wrap { container-type: inline-size; }

.ptable {
  width: 100%;
  border-collapse: collapse;
}

.ptable thead th {
  padding: 8px 8px;
  border-bottom: 1px solid var(--border-soft);
  font-size: var(--text-xs);
  font-weight: 500;
  color: var(--text-3);
  white-space: nowrap;
}

.ptable .col-sort { text-transform: uppercase; letter-spacing: 0.05em; }

.ptable tbody td,
.ptable tbody th {
  padding: 0 8px;
  height: 40px;
  border-bottom: 1px solid var(--border-soft);
}

.ptable tbody tr:last-child td,
.ptable tbody tr:last-child th { border-bottom: none; }
.ptable__row:hover td,
.ptable__row:hover th { background: var(--surface-2); }

.ptable__name { text-align: left; font-weight: 400; padding-left: 16px !important; width: 99%; max-width: 0; }
.ptable .ptable__col--monitors,
.ptable tbody td:last-child,
.ptable thead th:last-child { padding-right: 16px; }

.ptable__project {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  color: var(--text-1);
  font-size: var(--text-sm);
  font-weight: 500;
  text-decoration: none;
}

.ptable__project:hover .ptable__project-name { text-decoration: underline; text-underline-offset: 3px; }
.ptable__project-name { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

.ptable__dot { flex: none; width: 8px; height: 8px; border-radius: 50%; }
.ptable__dot--bad { background: var(--danger); }
.ptable__dot--ok { background: var(--success); }
.ptable__dot--unknown { background: var(--text-3); }

.ptable__trace { width: 88px; padding-right: 0 !important; line-height: 0; }

.ptable__num {
  text-align: right;
  white-space: nowrap;
  font-family: var(--mono);
  font-size: var(--text-sm);
  font-variant-numeric: tabular-nums;
  color: var(--text-2);
}

.ptable thead .ptable__num { font-family: inherit; }
.ptable thead .col-sort { justify-content: flex-end; }
.ptable thead .ptable__name .col-sort { justify-content: flex-start; }

.ptable__val--danger { color: var(--danger); }
.ptable__val--warning { color: var(--warning); }
.ptable__none { color: var(--text-3); }

.ptable__rose { display: inline-flex; vertical-align: -1px; margin-right: 2px; }

.ptable__skel { display: inline-block; width: 40px; height: 10px; }

.ptable__link { color: inherit; text-decoration: none; }
.ptable__link:hover { color: var(--accent); text-decoration: underline; text-underline-offset: 3px; }
.ptable__link.ptable__val--danger:hover { color: var(--danger); }

.ptable__expand {
  all: unset;
  box-sizing: border-box;
  display: block;
  width: 100%;
  padding: 8px 16px;
  border-top: 1px solid var(--border-soft);
  color: var(--accent);
  font-size: var(--text-xs);
  text-align: center;
  cursor: pointer;
}

.ptable__expand:hover { text-decoration: underline; text-underline-offset: 3px; }
.ptable__expand:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }

@container (max-width: 760px) {
  .ptable .ptable__trace { display: none; }
}

@container (max-width: 620px) {
  .ptable .ptable__col--p50,
  .ptable .ptable__col--apdex { display: none; }
}

@container (max-width: 460px) {
  .ptable .ptable__col--monitors { display: none; }
  .ptable tbody td:last-child,
  .ptable thead th:last-child { padding-right: 8px; }
}
</style>
