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
import { takeLastVisit } from './dashboard/briefing'
import BriefingWidget from './dashboard/BriefingWidget.vue'
import VitalsWidget from './dashboard/VitalsWidget.vue'
import ProjectsWidget from './dashboard/ProjectsWidget.vue'
import TransactionDensityWidget from './dashboard/TransactionDensityWidget.vue'
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

// When this browser last opened the dashboard; rows that changed after it are
// marked. Read once per mount so the marks stay while the page is open.
const lastVisit = takeLastVisit()

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
    <div class="db-body">
      <!-- Main column: the briefing, then quieter performance context ───────── -->
      <div class="db-main">
        <BriefingWidget :loading="loading" :open-count="openCount" :busiest="issues" :last-visit="lastVisit" />
        <div class="db-tail">
          <SlowTransactionsWidget :loading="loading" :tx-summaries="txSummaries" />
          <TransactionDensityWidget />
        </div>
      </div>

      <!-- Aside column ─────────────────────────────────────────────────────── -->
      <aside class="db-aside" aria-label="Health summary">
        <VitalsWidget :loading="loading" :tx-summaries="txSummaries" />
        <ReleasesWidget :loading="loading" :releases="releases" />
        <ProjectsWidget />
        <AlertsWidget />
      </aside>
    </div>
  </div>
</template>

<style scoped>
.empty-state--setup { display: block; }
.empty-state--setup > .empty-state__ghosts { display: none; }

/* ── Body layout ────────────────────────────────────────────────────────────── */

.db-body {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 360px;
  flex: 1;
}

.db-main {
  min-width: 0;
  border-right: 1px solid var(--border-soft);
}

.db-tail {
  padding: 8px 16px 40px;
}

.db-aside {
  min-width: 0;
  padding: 32px 8px 40px;
}

@media (max-width: 1080px) {
  .db-body { grid-template-columns: minmax(0, 1fr) 300px; }
}

@media (max-width: 768px) {
  .db-body { grid-template-columns: 1fr; }
  .db-main { border-right: none; }
  .db-tail { padding: 0 0 28px; }
  /* On phones the health summary follows the briefing, before performance. */
  .db-main { display: contents; }
  .db-aside { order: 1; padding: 8px 0 28px; border-top: 1px solid var(--border-soft); }
  .db-tail { order: 2; border-top: 1px solid var(--border-soft); padding-top: 28px; }
}
</style>
