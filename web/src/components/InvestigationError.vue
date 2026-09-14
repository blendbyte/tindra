<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useInvestigationStore } from '@/stores/investigation'
import Icon from './Icon.vue'
import PerformanceSubnav from './PerformanceSubnav.vue'

const route = useRoute()
const router = useRouter()
const state = useInvestigationStore()
const invalidLink = computed(() => !!state.routeError)
const title = computed(() => invalidLink.value ? 'Check the filters in this link' : 'Choose a shorter time range')
const description = computed(() => state.routeError || (state.absolute
  ? 'This view supports up to 90 days at a time. Choose a shorter range to load its data.'
  : 'All time is only available for Issues. Choose a range of up to 90 days to load this view.'))

function recover() {
  if (!invalidLink.value) {
    state.setRange('90d')
    return
  }
  // Keep record identifiers and view-specific filters while removing invalid
  // investigation parameters, including legacy aliases and custom bounds.
  const query = { ...route.query }
  for (const key of ['project_id', 'environment', 'env', 'range', 'window', 'since', 'from', 'to', 'user']) delete query[key]
  void router.replace({ query: { ...query, project_id: 'all', environment: 'all', range: '24h', user: '' }, hash: route.hash })
}
</script>

<template>
  <main class="page investigation-error">
    <PerformanceSubnav v-if="route.path.startsWith('/performance/')" />
    <h1 v-else class="investigation-error__page-title">{{ route.meta?.title || 'Investigation' }}</h1>
    <section class="investigation-error__body" role="alert" aria-labelledby="investigation-error-title">
      <Icon name="alert-circle" :size="24" class="investigation-error__icon" />
      <h2 id="investigation-error-title">{{ title }}</h2>
      <p>{{ description }}</p>
      <button type="button" class="btn" @click="recover">{{ invalidLink ? 'Reset filters' : 'Show last 90 days' }}</button>
    </section>
  </main>
</template>

<style scoped>
.investigation-error__page-title { padding: 20px 24px; margin: 0; font-size: var(--text-lg); font-weight: 600; border-bottom: 1px solid var(--border); }
.investigation-error__body { display: flex; flex-direction: column; align-items: flex-start; gap: 16px; width: min(100%, 560px); padding: 48px 24px; }
.investigation-error__icon { color: var(--text-3); }
.investigation-error__body h2 { margin: 0; font-size: var(--text-lg); font-weight: 600; color: var(--text-1); }
.investigation-error__body p { margin: 0; color: var(--text-2); line-height: 1.6; overflow-wrap: anywhere; }
</style>
