<script setup lang="ts">
import { computed, ref } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { apiFetch } from '@/api/client'
import { investigationQuery } from '@/router/investigation'
import { useInvestigationStore } from '@/stores/investigation'
import { formatDuration } from '@/utils/formatters'
import type { TransactionSummary } from '@/api/types'
import { fmt, useWindowLabel } from './shared'

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

const props = defineProps<{
  txSummaries?: TransactionSummary[]
}>()

const projects = useProjectsStore()
const investigation = useInvestigationStore()
const windowLabel = useWindowLabel()

const { data: projectIssueCounts, isFetching: projStatsFetching } = useQuery({
  queryKey: ['projects', 'stats'],
  queryFn: ({ signal }) => apiFetch<ProjectIssueCount[]>('/api/projects/stats', { signal }),
  enabled: computed(() => projects.projects.length >= 2),

})

const projectStats = computed((): ProjectStatRow[] => {
  if (projects.projects.length < 2) return []
  const txByProject = new Map<string, { total: number; p50sum: number; errSum: number }>()
  for (const s of props.txSummaries ?? []) {
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
  return projects.projects.filter(p => !projects.selectedIds.length || projects.selectedIds.includes(p.id)).map(p => {
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

// ── Sort + expand ─────────────────────────────────────────────────────────────

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
</script>

<template>
  <!-- Only shown when 2+ projects exist -->
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
        Req / {{ windowLabel }} <em class="col-sort__icon">{{ projSortIcon('reqPerDay') }}</em>
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
        <span class="db-proj-row__name" :title="row.projectName">{{ row.projectName }}</span>
        <RouterLink
          class="db-proj-row__val db-proj-row__link"
          :class="{ 'db-proj-row__val--bad': row.openIssues > 0 }"
          :to="{ name: 'issues', query: { ...investigationQuery(investigation), project_id: row.projectId } }"
        >{{ row.openIssues }}</RouterLink>
        <RouterLink
          v-if="row.reqPerDay !== null"
          class="db-proj-row__val db-proj-row__link"
          :to="{ name: 'transactions', query: { ...investigationQuery(investigation), project_id: row.projectId } }"
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
</template>

<style scoped src="./section.css"></style>

<style scoped>
/* ── Project overview ───────────────────────────────────────────────────────── */

.db-projects {
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
  margin-bottom: 32px;
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

@media (max-width: 768px) {
  /* Project table: drop P50 + Error Rate, keep Project | Open Issues | Req/24h */
  .db-proj-head,
  .db-proj-row {
    grid-template-columns: 1fr 110px 100px;
  }
  .db-proj-head > :nth-child(4),
  .db-proj-head > :nth-child(5),
  .db-proj-row > :nth-child(4),
  .db-proj-row > :nth-child(5) { display: none; }

  .db-projects { margin-top: 16px; border-top: 1px solid var(--border); }
}

@media (max-width: 480px) {
  /* Project table: safety-net scroll only. No forced min-width, so the grid
     adapts naturally and doesn't create spurious horizontal overflow */
  .db-projects {
    overflow-x: auto;
    -webkit-overflow-scrolling: touch;
  }
}
</style>
