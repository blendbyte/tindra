import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { useQueryClient, type Query } from '@tanstack/vue-query'
import { useRoute } from 'vue-router'
import { useInvestigationStore } from '@/stores/investigation'
import { isTelemetry } from '@/router/investigation'

export function isTelemetryQuery(query: Query) {
  const key = String(query.queryKey[0])
  if (key === 'alert-rules' || (key === 'projects' && query.queryKey[1] === 'stats')) return true
  return /^(issues|transactions|transaction-|user-traces|user-pageloads|logs|trace-logs|span-|web-vitals-|releases|release-issues|release-transactions|monitors|uptime-|checkins|dash-tx)/.test(key) && query.queryKey[1] !== 'metadata'
}

export function useViewFreshness() {
  const client = useQueryClient()
  const route = useRoute()
  const state = useInvestigationStore()
  const now = ref(Date.now())
  const revision = ref(0)
  const online = ref(navigator.onLine)
  const visible = ref(document.visibilityState !== 'hidden')
  const refreshing = ref(false)
  const unsubscribe = client.getQueryCache().subscribe(() => { revision.value++ })
  const queries = computed(() => {
    void revision.value
    return client.getQueryCache().findAll({ type: 'active', predicate: isTelemetryQuery })
  })
  const fetching = computed(() => refreshing.value || queries.value.some(q => q.state.fetchStatus === 'fetching'))
  const failed = computed(() => queries.value.filter(q => q.state.status === 'error'))
  const updatedAt = computed(() => queries.value.length && queries.value.every(q => q.state.dataUpdatedAt > 0)
    ? Math.min(...queries.value.map(q => q.state.dataUpdatedAt)) : 0)
  const age = computed(() => Math.max(0, Math.floor((now.value - updatedAt.value) / 1000)))
  const interval = computed(() => route.path === '/logs' ? 5_000 : isTelemetry(route.path) ? 30_000 : 60_000)
  const automatic = computed(() => !state.paused && !state.absolute && !state.browsingHistory && (isTelemetry(route.path) || /^\/(releases|monitors)(\/cron|\/uptime)?$/.test(route.path)))
  let lastAttempt = Date.now()
  async function refresh() {
    if (fetching.value || !online.value) return
    lastAttempt = Date.now()
    refreshing.value = true
    for (const query of queries.value) {
      const data = query.state.data as { pages?: unknown[]; pageParams?: unknown[] } | undefined
      if (data?.pages && data.pageParams && data.pages.length > 1) {
        client.setQueryData(query.queryKey, { ...data, pages: data.pages.slice(0, 1), pageParams: data.pageParams.slice(0, 1) }, { updatedAt: query.state.dataUpdatedAt })
      }
    }
    state.refresh()
    await nextTick()
    try { await client.refetchQueries({ type: 'active', predicate: isTelemetryQuery }) }
    finally { refreshing.value = false }
  }
  function tick() {
    now.value = Date.now()
    if (automatic.value && visible.value && online.value && now.value - lastAttempt >= interval.value) void refresh()
  }
  function activity() { online.value = navigator.onLine; visible.value = document.visibilityState !== 'hidden'; tick() }
  const timer = setInterval(tick, 1000)
  document.addEventListener('visibilitychange', activity)
  window.addEventListener('online', activity)
  window.addEventListener('offline', activity)
  watch(() => route.path, () => { lastAttempt = Date.now(); state.browsingHistory = false })
  onUnmounted(() => {
    clearInterval(timer); unsubscribe()
    document.removeEventListener('visibilitychange', activity)
    window.removeEventListener('online', activity); window.removeEventListener('offline', activity)
  })
  return { fetching, failed, updatedAt, age, online, interval, automatic, refresh }
}
