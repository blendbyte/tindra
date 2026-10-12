<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { apiFetch } from '@/api/client'
import { useInvestigationStore, RANGE_HOURS } from '@/stores/investigation'
import type {
  DashboardIssuePage,
  TransactionSummary,
  ReleaseHealthPage,
} from '@/api/types'
import Icon from '@/components/Icon.vue'
import BrandMark from '@/components/BrandMark.vue'
import ProjectSetup from '@/components/ProjectSetup.vue'
import { useProjectScope } from './dashboard/shared'
import KpiStripWidget from './dashboard/KpiStripWidget.vue'
import ProjectOverviewWidget from './dashboard/ProjectOverviewWidget.vue'
import TransactionDensityWidget from './dashboard/TransactionDensityWidget.vue'
import MonitorsWidget from './dashboard/MonitorsWidget.vue'
import HotIssuesWidget from './dashboard/HotIssuesWidget.vue'
import SlowTransactionsWidget from './dashboard/SlowTransactionsWidget.vue'
import AlertsWidget from './dashboard/AlertsWidget.vue'
import ReleasesWidget from './dashboard/ReleasesWidget.vue'

const router = useRouter()
const projects = useProjectsStore()
const investigation = useInvestigationStore()
const { buildQs, pKey } = useProjectScope()

// Durable setup milestones are independent of monthly usage and retention.
const { data: projectSetup, refetch: refetchSetup } = useQuery({
  queryKey: ['projects', 'setup'],
  queryFn: ({ signal }) => apiFetch<{ id: string; setup_complete: boolean }[]>('/api/setup-status', { signal }),
})

// ── Shared queries ────────────────────────────────────────────────────────────
// Each feeds more than one widget, and together they decide the page-wide
// loading state, which also gates the first-run screen. Queries used by a
// single widget live in that widget.

const { data: issuesPage, isFetching: issuesFetching } = useQuery({
  queryKey: computed(() => ['issues', 'overview', pKey.value]),
  queryFn: ({ signal }) => apiFetch<DashboardIssuePage>(`/api/issues/overview?${buildQs()}`, { signal }),

})

const { data: txSummaries, isFetching: txFetching } = useQuery({
  queryKey: computed(() => ['dash-tx', investigation.scopeKey]),
  queryFn: ({ signal }) =>
    apiFetch<TransactionSummary[]>(investigation.request(`/api/transactions/summaries?${buildQs({ hours: String(RANGE_HOURS[investigation.range] ?? 24), env: investigation.environment === 'All' ? '' : investigation.environment })}`), { signal }),

})

const { data: releasesPage, isFetching: releasesFetching } = useQuery({
  queryKey: computed(() => ['releases', 'health', pKey.value]),
  queryFn: ({ signal }) => apiFetch<ReleaseHealthPage>(`/api/releases/health?${buildQs()}`, { signal }),

})

const openCount = computed(() => issuesPage.value?.total ?? 0)
const issues = computed(() => issuesPage.value?.issues ?? [])
const releases = computed(() => releasesPage.value?.releases ?? [])

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

const visibleProjects = computed(() =>
  projects.selectedIds.length > 0
    ? projectSetup.value?.filter(p => projects.selectedIds.includes(p.id))
    : projectSetup.value,
)
const setupOpen = ref(false)
const setupDismissed = ref(false)
function setupWasDismissed(projectID: string) {
  try { return sessionStorage.getItem(`tindra:setup-dismissed:${projectID}`) === '1' } catch { return false }
}
const isFirstRun = computed(() => {
  if (loading.value || setupDismissed.value) return false
  if (noProjects.value) return true
  return !!visibleProjects.value?.length && visibleProjects.value.every(p => p.setup_complete === false && !setupWasDismissed(p.id))
})
watch(pKey, () => { setupDismissed.value = false; setupOpen.value = false })
watch(isFirstRun, (value) => { if (value && !noProjects.value) setupOpen.value = true }, { immediate: true })
function finishSetup() {
  setupDismissed.value = true
  setupOpen.value = false
  refetchSetup()
}
</script>

<template>
  <!-- First-run / empty state ───────────────────────────────────────────────── -->
  <div v-if="isFirstRun || setupOpen" class="empty-state" :class="{ 'empty-state--setup': !noProjects }">
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

    <ProjectSetup v-else-if="firstProject" :key="firstProject.id" :project="firstProject" @continue="finishSetup" />
  </div>

  <!-- Dashboard ─────────────────────────────────────────────────────────────── -->
  <div v-else class="page">

    <!-- KPI strip ──────────────────────────────────────────────────────────── -->
    <KpiStripWidget :loading="loading" :open-count="openCount" :tx-summaries="txSummaries" />

    <!-- Project overview (only when 2+ projects) ──────────────────────────── -->
    <ProjectOverviewWidget :tx-summaries="txSummaries" />

    <!-- Body ───────────────────────────────────────────────────────────────── -->
    <div class="db-body">

      <!-- Main column ──────────────────────────────────────────────────────── -->
      <div class="db-main">
        <TransactionDensityWidget />
        <MonitorsWidget />
        <HotIssuesWidget :loading="loading" :issues="issues" />
        <SlowTransactionsWidget :loading="loading" :tx-summaries="txSummaries" />
      </div>

      <!-- Aside column ─────────────────────────────────────────────────────── -->
      <div class="db-aside">
        <AlertsWidget />
        <ReleasesWidget :loading="loading" :releases="releases" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.empty-state--setup { display: block; }
.empty-state--setup > .empty-state__ghosts { display: none; }

/* ── Body layout ────────────────────────────────────────────────────────────── */

.db-body {
  display: grid;
  grid-template-columns: 1fr 280px;
  flex: 1;
}

.db-main {
  border-right: 1px solid var(--border);
  min-width: 0;
}

.db-aside {
  min-width: 0;
  background: var(--surface);
}

@media (max-width: 768px) {
  /* Body: single column, sidebar below main */
  .db-body {
    grid-template-columns: 1fr;
  }
  .db-main {
    border-right: none;
    border-bottom: 1px solid var(--border);
  }

  .db-body     { margin-top: 16px; }
}
</style>
