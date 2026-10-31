<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
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
const ageLabel = computed(() => age.value < 60 ? `${age.value}s` : age.value < 3600 ? `${Math.floor(age.value / 60)}m` : `${Math.floor(age.value / 3600)}h`)
// The live switch says in words whether the page keeps itself current.
const liveLabel = computed(() => {
  if (!online.value) return 'Offline'
  if (state.paused) return 'Paused'
  return automatic.value ? 'Live' : 'Updated'
})
const canPause = computed(() => !state.absolute && (telemetry.value || automatic.value || state.paused))
// On phones every filter folds behind one button; its label keeps the scope visible.
const filtersOpen = ref(false)
const filterSummary = computed(() => [
  state.absolute ? 'Custom' : state.range,
  state.environment === 'All' ? 'All envs' : state.environment,
].join(' · '))

// The issue list's header sticks below this bar, so it needs the bar's real
// height, which grows when filters wrap.
const root = ref<HTMLElement | null>(null)
let resizeObserver: ResizeObserver | null = null
// An open panel on a phone would pin a third of the screen, so a scroll gesture
// outside it closes it. Gestures, not scroll events: the browser also scrolls
// when the keyboard opens or a shorter list clamps the page, and those must not.
function closeOnScroll(event: Event) {
  if (!filtersOpen.value || root.value?.contains(event.target as Node)) return
  filtersOpen.value = false
}
onMounted(() => {
  window.addEventListener('wheel', closeOnScroll, { passive: true })
  window.addEventListener('touchmove', closeOnScroll, { passive: true })
  if (typeof ResizeObserver === 'undefined' || !root.value) return
  resizeObserver = new ResizeObserver(([entry]) => {
    document.documentElement.style.setProperty('--investigation-bar-h', `${Math.round(entry.borderBoxSize?.[0]?.blockSize ?? entry.target.getBoundingClientRect().height)}px`)
  })
  resizeObserver.observe(root.value)
})
onUnmounted(() => {
  window.removeEventListener('wheel', closeOnScroll)
  window.removeEventListener('touchmove', closeOnScroll)
  resizeObserver?.disconnect()
  document.documentElement.style.removeProperty('--investigation-bar-h')
})
const scopeNote = computed(() => {
  if (route.path.startsWith('/monitors')) return 'Current monitor status, latest checks, and labeled uptime windows. Time and environment filters do not apply.'
  if (route.path.startsWith('/releases')) return 'All deployments. Release metrics cover the release; time and environment filters do not apply.'
  if (route.path === '/dashboard') return 'Transaction metrics use the selected filters. Open issues, alerts, releases, and monitors show current state across environments and users. Transaction density covers the last 7 days.'
  return 'This record has its own project and event time. Your investigation filters are kept for returning to the list.'
})
</script>

<template>
  <section ref="root" class="investigation-bar" :class="{ 'investigation-bar--filters-open': filtersOpen }" aria-label="Investigation filters and freshness">
    <button
      v-if="telemetry"
      class="investigation-bar__toggle"
      :aria-expanded="filtersOpen"
      aria-controls="investigation-scope"
      @click="filtersOpen = !filtersOpen"
    >
      <Icon name="filter" :size="13" />
      <span>Filters</span>
      <span v-if="state.pageFilterCount" class="investigation-bar__count">{{ state.pageFilterCount }}<span class="sr-only"> active</span></span>
      <span class="investigation-bar__summary">{{ filterSummary }}</span>
      <Icon name="chevron-down" :size="11" />
    </button>
    <div id="investigation-scope" class="investigation-bar__scope">
      <div v-if="telemetry" class="investigation-bar__filters">
        <FilterChip label="Time range" icon="clock" :value="state.absolute ? 'Custom' : state.range" :options="ranges" @change="state.setRange($event)" />
        <FilterChip label="Environment" icon="globe" :value="state.environment" :options="envOptions" @change="state.environment = $event" />
      </div>
      <UserFilter v-if="telemetry" compact class="investigation-bar__user" />
      <!-- Pages with their own filters (the issue list) teleport them here, so one bar holds every filter. -->
      <div id="page-filters" class="investigation-bar__page" />
    </div>
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
      <button v-tooltip="fetching ? 'Refreshing…' : state.browsingHistory ? 'Refresh latest' : 'Refresh now'" class="investigation-bar__icon" :disabled="fetching || !online" :aria-label="fetching ? 'Refreshing…' : state.browsingHistory ? 'Refresh latest' : 'Refresh now'" @click="refresh">
        <Icon name="refresh-cw" :size="12" :class="{ 'investigation-bar__spin': fetching }" />
      </button>
      <button
        v-if="canPause"
        v-tooltip="state.paused ? 'Resume auto-refresh' : 'Pause auto-refresh'"
        class="investigation-bar__live"
        :class="liveLabel === 'Live' ? 'investigation-bar__live--on' : 'investigation-bar__live--off'"
        :aria-label="`${liveLabel}, ${state.paused ? 'resume' : 'pause'} auto-refresh`"
        @click="state.paused = !state.paused"
      >
        <span class="investigation-bar__live-dot" aria-hidden="true" />
        <span>{{ liveLabel }}</span>
        <span v-if="updatedAt" class="investigation-bar__age" aria-hidden="true">· {{ ageLabel }}</span>
      </button>
      <span v-else-if="updatedAt" class="investigation-bar__live investigation-bar__live--static">
        {{ liveLabel }} <span class="investigation-bar__age">· {{ ageLabel }}</span>
      </span>
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
  .investigation-bar { padding: 4px 16px; gap: 4px; }
  .investigation-bar__filters { flex: 1; gap: 4px; }
  .investigation-bar__user:has(.user-chip) { order: 3; flex-basis: 100%; padding-bottom: 4px; }
  .investigation-bar__filters :deep(.filterchip) { min-height: 36px; gap: 4px; padding: 0 6px; }
  .investigation-bar__icon { width: 36px; height: 36px; }

}
.investigation-bar__scope { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; flex: 1; min-width: 0; }
.investigation-bar__page { display: contents; }
.investigation-bar__toggle { display: none; }

/* Live switch: a labelled state instead of a bare pause icon. */
.investigation-bar__live { display: inline-flex; align-items: center; gap: 6px; height: 28px; padding: 0 9px; border: 1px solid var(--border-soft); border-radius: 14px; background: transparent; color: var(--text-2); font-size: var(--text-xs); font-weight: 500; white-space: nowrap; cursor: pointer; }
.investigation-bar__live:hover { color: var(--text-1); border-color: var(--border); }
.investigation-bar__live:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.investigation-bar__live--static { cursor: default; border-color: transparent; }
.investigation-bar__live-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--text-3); }
.investigation-bar__live--on .investigation-bar__live-dot { background: var(--success); box-shadow: 0 0 0 3px oklch(from var(--success) l c h / 0.18); }
.investigation-bar__live--on { color: var(--text-1); }
.investigation-bar__age { display: inline-block; min-width: 4.5ch; font-family: var(--mono); font-variant-numeric: tabular-nums; color: var(--text-3); font-weight: 400; }
.investigation-bar__count { display: inline-flex; align-items: center; justify-content: center; min-width: 18px; height: 18px; padding: 0 5px; border-radius: 4px; background: var(--accent-soft); color: var(--accent); font-family: var(--mono); font-size: 11px; font-weight: 500; }
.investigation-bar__user { min-width: 0; }
.investigation-bar__user :deep(.filterchip) { padding: 0 7px; }
@media (max-width: 640px) {
  .investigation-bar__user :deep(.filterchip) { height: 36px; }
}
/* Phones: one Filters button carries the scope; the chips open below it.
   Declared last so it overrides the desktop defaults above. */
@media (max-width: 640px) {
  .investigation-bar__toggle { display: inline-flex; align-items: center; gap: 6px; min-width: 0; flex: 1; height: 36px; padding: 0 10px; border: 1px solid var(--border); border-radius: 4px; background: transparent; color: var(--text-1); font-size: var(--text-sm); }
  .investigation-bar__summary { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text-3); font-size: var(--text-xs); }
  .investigation-bar__toggle > svg { flex-shrink: 0; }
  .investigation-bar__toggle > svg:last-child { margin-left: auto; transition: transform 120ms; }
  .investigation-bar--filters-open .investigation-bar__toggle > svg:last-child { transform: rotate(180deg); }
  /* The scope's children join the bar's own row so an active user filter can
     stay visible while every other filter waits behind the toggle. */
  .investigation-bar__scope { display: contents; }
  .investigation-bar:not(.investigation-bar--filters-open) .investigation-bar__filters,
  .investigation-bar:not(.investigation-bar--filters-open) .investigation-bar__page > *,
  .investigation-bar:not(.investigation-bar--filters-open) .investigation-bar__user:not(:has(.user-chip)) { display: none; }
  /* Open panel: a zero-height break ends the toggle row, then scope chips and
     the user filter share one row and page filters follow. */
  .investigation-bar--filters-open::after { content: ''; order: 2; flex-basis: 100%; height: 0; }
  .investigation-bar--filters-open .investigation-bar__filters { order: 3; padding-top: 6px; }
  .investigation-bar--filters-open .investigation-bar__user { order: 3; padding-top: 6px; }
  /* One control size in the open panel, page filters included. */
  .investigation-bar--filters-open :deep(.filterchip) { height: 36px; min-height: 36px; }
  .investigation-bar--filters-open :deep(.filterbar__search) { height: 36px; }
  .investigation-bar--filters-open .investigation-bar__page > * { order: 4; flex-basis: 100%; padding-bottom: 8px; }
  .investigation-bar__live { height: 36px; }
}
</style>
