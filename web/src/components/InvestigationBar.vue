<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { useProjectsStore } from '@/stores/projects'
import { useInvestigationStore, CORE_RANGES } from '@/stores/investigation'
import { useViewFreshness } from '@/composables/useViewFreshness'
import { isTelemetry } from '@/router/investigation'
import FilterChip from './FilterChip.vue'

const route = useRoute()
const router = useRouter()
const state = useInvestigationStore()
const projects = useProjectsStore()
const telemetry = computed(() => isTelemetry(route.path))
const projectParams = computed(() => {
  const p = new URLSearchParams()
  for (const id of [...state.projectIds].sort()) p.append('project_id', id)
  return p.toString()
})
const { data: environments, isError: environmentError } = useQuery({
  queryKey: computed(() => ['environments', projectParams.value]),
  queryFn: ({ signal }) => apiFetch<string[]>(`/api/environments?${projectParams.value}`, { signal }),
  staleTime: 30_000,
  enabled: computed(() => telemetry.value && !state.routeError),
})
const envOptions = computed(() => ['All', ...[...new Set([...(environments.value ?? []), ...(state.environment === 'All' ? [] : [state.environment])])].sort()])
const ranges = computed(() => route.path === '/issues' ? [...CORE_RANGES, '90d', 'All'] : [...CORE_RANGES])
const { fetching, failed, updatedAt, age, online, interval, automatic, refresh } = useViewFreshness()
const scopeNote = computed(() => {
  if (route.path.startsWith('/monitors')) return 'Current monitor status, latest checks, and labeled uptime windows. Time and environment filters do not apply.'
  if (route.path.startsWith('/releases')) return 'All deployments. Release metrics cover the release; time and environment filters do not apply.'
  return 'This record has its own project and event time. Your investigation filters are kept for returning to the list.'
})
</script>

<template>
  <section class="investigation-bar" aria-label="Investigation filters and freshness">
    <button v-if="state.routeError" class="btn" @click="router.replace({ query: { project_id: 'all', environment: 'all', range: '24h' } })">Reset invalid link</button>
    <template v-if="telemetry">
      <FilterChip label="Time range" :value="state.absolute ? 'Custom' : state.range" :options="ranges" @change="state.setRange($event)" />
      <FilterChip label="Environment" :value="state.environment" :options="envOptions" @change="state.environment = $event" />
      <span v-if="environmentError" class="investigation-bar__note">Couldn't load environment options.</span>
      <span v-if="state.absolute" class="investigation-bar__note">{{ state.absolute.from }} to {{ state.absolute.to }}</span>
    </template>
    <span v-else class="investigation-bar__note">{{ scopeNote }}</span>
    <span v-if="projects.metadataReady && projects.invalidIds.length" class="investigation-bar__note" role="status">Some selected projects are unavailable. Choose projects in the navigation bar.</span>
    <div class="investigation-bar__freshness">
      <span aria-live="off">
        <template v-if="!online">Offline. </template>
        <template v-if="failed.length">Couldn't refresh {{ failed.length }} {{ failed.length === 1 ? 'panel' : 'panels' }}. </template>
        <template v-if="updatedAt">{{ failed.length ? 'Showing data from' : 'Updated' }} {{ age < 60 ? `${age}s` : `${Math.floor(age / 60)}m` }} ago</template>
        <template v-else>{{ fetching ? 'Loading…' : failed.length ? 'Data unavailable' : 'Waiting for data' }}</template>
      </span>
      <span>{{ state.browsingHistory ? 'Paused while browsing older results' : automatic ? `Auto-refresh every ${interval / 1000}s` : 'Auto-refresh paused' }}</span>
      <button class="btn btn--ghost" :disabled="fetching || !online" @click="refresh">{{ fetching ? 'Refreshing…' : state.browsingHistory ? 'Refresh latest' : 'Refresh now' }}</button>
      <button v-if="!state.absolute" class="btn btn--ghost" :aria-pressed="state.paused" @click="state.paused = !state.paused">{{ state.paused ? 'Resume' : 'Pause' }}</button>
    </div>
  </section>
</template>

<style scoped>
.investigation-bar { display: flex; align-items: center; flex-wrap: wrap; gap: 12px; padding: 12px 24px; border-bottom: 1px solid var(--border); font-size: var(--text-sm); }
.investigation-bar__note { color: var(--text-2); max-width: 75ch; }
.investigation-bar__freshness { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; margin-left: auto; color: var(--text-2); }
@media (max-width: 640px) { .investigation-bar { padding: 12px 16px; } .investigation-bar__freshness { margin-left: 0; width: 100%; } }
</style>
