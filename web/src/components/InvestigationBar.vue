<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { useProjectsStore } from '@/stores/projects'
import { useInvestigationStore, CORE_RANGES } from '@/stores/investigation'
import { useViewFreshness } from '@/composables/useViewFreshness'
import { isTelemetry } from '@/router/investigation'
import FilterChip from './FilterChip.vue'
import Icon from './Icon.vue'
import UserFilter from './UserFilter.vue'
import { vTooltip } from '@/directives/tooltip'

const details = ref<HTMLDetailsElement | null>(null)
function dismissDetails(event: Event) {
  if (!details.value?.open) return
  if (event instanceof KeyboardEvent) {
    if (event.key !== 'Escape') return
    details.value.open = false
    details.value.querySelector('summary')?.focus()
  } else if (!details.value.contains(event.target as Node)) details.value.open = false
}
onMounted(() => {
  document.addEventListener('pointerdown', dismissDetails)
  document.addEventListener('keydown', dismissDetails)
})
onUnmounted(() => {
  document.removeEventListener('pointerdown', dismissDetails)
  document.removeEventListener('keydown', dismissDetails)
})
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
const ranges = computed(() => route.path === '/issues' ? [...CORE_RANGES, 'All'] : [...CORE_RANGES])
const { fetching, failed, updatedAt, age, online, interval, automatic, refresh } = useViewFreshness()
const scopeNote = computed(() => {
  if (route.path.startsWith('/monitors')) return 'Current monitor status, latest checks, and labeled uptime windows. Time and environment filters do not apply.'
  if (route.path.startsWith('/releases')) return 'All deployments. Release metrics cover the release; time and environment filters do not apply.'
  if (route.path === '/dashboard') return 'Transaction metrics use the selected filters. Open issues, alerts, releases, and monitors show current state across environments and users. Transaction density covers the last 7 days.'
  return 'This record has its own project and event time. Your investigation filters are kept for returning to the list.'
})
</script>

<template>
  <section class="investigation-bar" aria-label="Investigation filters and freshness">
    <button v-if="state.routeError" class="btn" @click="router.replace({ query: { project_id: 'all', environment: 'all', range: '24h' } })">Reset invalid link</button>
    <div v-if="telemetry" class="investigation-bar__filters">
      <FilterChip label="Time range" icon="clock" :value="state.absolute ? 'Custom' : state.range" :options="ranges" @change="state.setRange($event)" />
      <FilterChip label="Environment" icon="globe" :value="state.environment" :options="envOptions" @change="state.environment = $event" />
    </div>
    <UserFilter v-if="telemetry" compact class="investigation-bar__user" />
    <div class="investigation-bar__actions">
      <details ref="details" class="investigation-bar__details">
        <summary v-tooltip="'Data freshness and scope'" class="investigation-bar__icon" :class="{ 'investigation-bar__warning': !online || failed.length || environmentError || (projects.metadataReady && projects.invalidIds.length) }" aria-label="Data freshness and scope">
          <Icon :name="!online || failed.length || environmentError || (projects.metadataReady && projects.invalidIds.length) ? 'alert-triangle' : 'info'" :size="14" />
        </summary>
        <div class="investigation-bar__popover">
          <p>
            <template v-if="!online">Offline. </template>
            <template v-if="failed.length">Couldn't refresh {{ failed.length }} {{ failed.length === 1 ? 'panel' : 'panels' }}. </template>
            <template v-if="updatedAt">{{ failed.length ? 'Showing data from' : 'Updated' }} {{ age < 60 ? `${age}s` : `${Math.floor(age / 60)}m` }} ago</template>
            <template v-else>{{ fetching ? 'Loading…' : failed.length ? 'Data unavailable' : 'Waiting for data' }}</template>
          </p>
          <p>{{ state.browsingHistory ? 'Paused while browsing older results' : automatic ? `Auto-refresh every ${interval / 1000}s` : 'Auto-refresh paused' }}</p>
          <p v-if="!telemetry || route.path === '/dashboard'">{{ scopeNote }}</p>
          <p v-if="state.absolute">{{ state.absolute.from }} to {{ state.absolute.to }}</p>
          <p v-if="environmentError">Couldn't load environment options.</p>
          <p v-if="projects.metadataReady && projects.invalidIds.length">Some selected projects are unavailable. Choose projects in the navigation bar.</p>
        </div>
      </details>
      <button v-tooltip="fetching ? 'Refreshing…' : state.browsingHistory ? 'Refresh latest' : 'Refresh now'" class="investigation-bar__icon investigation-bar__refresh" :disabled="fetching || !online" :aria-label="fetching ? 'Refreshing…' : state.browsingHistory ? 'Refresh latest' : 'Refresh now'" @click="refresh">
        <Icon name="refresh-cw" :size="12" :class="{ 'investigation-bar__spin': fetching }" />
        <span v-if="updatedAt" class="investigation-bar__age" aria-hidden="true">{{ age < 60 ? `${age}s` : age < 3600 ? `${Math.floor(age / 60)}m` : `${Math.floor(age / 3600)}h` }}</span>
      </button>
      <button v-if="!state.absolute && (telemetry || automatic || state.paused)" v-tooltip="state.paused ? 'Resume auto-refresh' : 'Pause auto-refresh'" class="investigation-bar__icon" :aria-label="state.paused ? 'Resume' : 'Pause'" :aria-pressed="state.paused" @click="state.paused = !state.paused">
        <Icon :name="state.paused ? 'play' : 'pause'" :size="14" />
      </button>
    </div>
  </section>
</template>

<style scoped>
.investigation-bar { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; min-height: 44px; padding: 0 16px; border-bottom: 1px solid var(--border); font-size: var(--text-sm); }
.investigation-bar__filters { display: flex; align-items: center; gap: 8px; min-width: 0; }
.investigation-bar__filters :deep(.filterchip-wrap) { min-width: 0; }
.investigation-bar__filters :deep(.filterchip-wrap:first-child) { flex-shrink: 0; }
.investigation-bar__filters :deep(.filterchip) { max-width: 100%; }
.investigation-bar__filters :deep(.filterchip__value) { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.investigation-bar__filters :deep(svg) { flex-shrink: 0; }
.investigation-bar__actions { position: relative; display: flex; align-items: center; gap: 2px; margin-left: auto; flex-shrink: 0; }
.investigation-bar__icon { display: flex; align-items: center; justify-content: center; width: 28px; height: 28px; padding: 0; border: 0; border-radius: 4px; background: transparent; color: var(--text-2); cursor: pointer; list-style: none; }
.investigation-bar__icon::-webkit-details-marker { display: none; }
.investigation-bar__icon:hover { color: var(--text-1); background: var(--surface-2); }
.investigation-bar__icon:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.investigation-bar__icon:disabled { opacity: .45; cursor: default; }
.investigation-bar__icon[aria-pressed="true"], .investigation-bar__warning { color: var(--accent); }
.investigation-bar__details { position: static; }
.investigation-bar__popover { position: absolute; right: 0; top: calc(100% + 8px); z-index: 100; width: 280px; max-width: calc(100vw - 24px); padding: 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--bg); box-shadow: 0 4px 16px #0002; color: var(--text-2); line-height: 1.5; overflow-wrap: anywhere; }
.investigation-bar__popover p { margin: 0; }
.investigation-bar__popover p + p { margin-top: 8px; }
.investigation-bar__spin { animation: investigation-spin 1s linear infinite; }
@keyframes investigation-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .investigation-bar__spin { animation: none; } }
@media (max-width: 640px) {
  .investigation-bar { padding: 0 16px; gap: 4px; }
  .investigation-bar__filters { flex: 1; gap: 4px; }
  .investigation-bar__user:has(.user-chip) { order: 3; flex-basis: 100%; padding-bottom: 4px; }
  .investigation-bar__filters :deep(.filterchip) { min-height: 36px; gap: 4px; padding: 0 6px; }
  .investigation-bar__icon { width: 36px; height: 36px; }

}
.investigation-bar__refresh { width: auto; min-width: 48px; padding: 0 6px; gap: 4px; line-height: 1; }
.investigation-bar__refresh :deep(svg) { display: block; flex-shrink: 0; }
.investigation-bar__age { font-size: var(--text-sm); font-variant-numeric: tabular-nums; display: block; line-height: 1; text-box: trim-both cap alphabetic; }
.investigation-bar__user { min-width: 0; }
.investigation-bar__user :deep(.filterchip) { padding: 0 7px; }
@media (max-width: 640px) {
  .investigation-bar__user :deep(.filterchip) { height: 36px; }
}
</style>
