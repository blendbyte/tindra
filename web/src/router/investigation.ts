import { useAppUserStore } from '@/stores/appUser'
import { watch } from 'vue'
import type { Pinia } from 'pinia'
import type { LocationQuery, LocationQueryRaw, Router } from 'vue-router'
import { RANGE_HOURS, useInvestigationStore } from '@/stores/investigation'

const first = (value: LocationQuery[string]) => Array.isArray(value) ? value[0] : value
export function hasInvestigation(path: string) {
  return /^\/(dashboard|issues|performance|transactions|logs|releases|monitors)(\/|$)/.test(path)
}
export function isTelemetry(path: string) {
  return path === '/dashboard' || path === '/issues' || path === '/logs' || path.startsWith('/performance/') || path === '/transactions/profile'
}
export function investigationQuery(state: ReturnType<typeof useInvestigationStore>): LocationQueryRaw {
  return {
    user: state.userIdentity,
    project_id: state.projectIds.length ? [...state.projectIds].sort() : 'all',
    environment: state.environment === 'All' ? 'all' : state.environment,
    range: state.absolute ? 'custom' : state.range === 'All' ? 'all' : state.range,
    ...(state.absolute ?? {}),
  }
}
export function installInvestigationRouter(router: Router, pinia: Pinia) {
  const state = useInvestigationStore(pinia)
  let navigating = false
  router.beforeEach((to) => {
    state.routeError = ''
    if (!hasInvestigation(to.path)) return
    navigating = true
    if (!state.paused && !state.absolute && Date.now() - state.anchor > 30_000 && to.path !== router.currentRoute.value.path) state.refresh()
    const q = to.query
    state.routeError = ''
    const ids = q.project_id === undefined ? [] : Array.isArray(q.project_id) ? q.project_id : [q.project_id]
    if (ids.some(id => id !== 'all' && (!id || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id))) || (ids.includes('all') && ids.length > 1)) {
      state.routeError = 'This link contains an invalid project selection. Choose projects in the navigation bar to continue.'
      navigating = false
      return
    }
    const rawRange = first(q.range) ?? (q.from !== undefined || q.to !== undefined ? 'custom' : undefined) ?? first(q.window) ?? (to.path === '/issues' ? first(q.since) : undefined)
    if (rawRange && !['all', 'All', 'custom'].includes(rawRange) && !Object.hasOwn(RANGE_HOURS, rawRange)) {
      state.routeError = 'This link contains an unsupported time range. Choose a time range above to continue.'
      navigating = false
      return
    }
    if ((q.from !== undefined || q.to !== undefined) && rawRange !== 'custom') {
      state.routeError = 'This link combines a preset with custom time bounds. Choose a time range above to continue.'
      navigating = false
      return
    }
    if (rawRange === 'custom' || q.from !== undefined || q.to !== undefined) {
      const from = first(q.from), end = first(q.to)
      if (!from || !end || !/T.*(?:Z|[+-]\d{2}:\d{2})$/i.test(from) || !/T.*(?:Z|[+-]\d{2}:\d{2})$/i.test(end) || !Number.isFinite(Date.parse(from)) || !Number.isFinite(Date.parse(end)) || Date.parse(from) >= Date.parse(end) || Date.parse(end) - Date.parse(from) > 90 * 86400000) {
        state.routeError = 'This link contains invalid time bounds. Choose a time range above to continue.'
        navigating = false
        return
      }
    }
    if (q.user !== undefined) {
      const identity = first(q.user) ?? ''
      const user = useAppUserStore(pinia)
      if (!identity) user.clear()
      else if (identity !== user.identity) user.select({ identity, user_id: identity, username: null, email: null, name: null, last_seen: '', project_id: '' })
    }
    if (q.project_id !== undefined) {
      const raw = Array.isArray(q.project_id) ? q.project_id : [q.project_id]
      state.projectIds = raw.includes('all') ? [] : [...new Set(raw.filter((id): id is string => !!id))].sort()
    }
    const env = first(q.environment) ?? first(q.env)
    if (env) state.environment = env === 'all' || env === 'All' ? 'All' : env
    const range = rawRange
    if (range && (Object.hasOwn(RANGE_HOURS, range) || range === 'all' || range === 'All')) state.setRange(range.toLowerCase() === 'all' ? 'All' : range)
    if (range === 'custom') {
      const from = first(q.from), end = first(q.to)
      if (from && end && Number.isFinite(Date.parse(from)) && Date.parse(from) < Date.parse(end)) state.absolute = { from, to: end }
    }
    const query = { ...q, ...investigationQuery(state) }
    delete query.env; delete query.window
    if (to.path === '/issues') delete query.since
    if (!state.absolute) { delete query.from; delete query.to }
    // A single repeated parameter becomes a string when browser history parses
    // the URL. Compare serialized URLs so Back/Forward never redirects just
    // because the equivalent in-memory value is an array.
    if (router.resolve({ path: to.path, query, hash: to.hash }).fullPath !== to.fullPath) return { path: to.path, query, hash: to.hash }
  })
  router.afterEach(() => { navigating = false })
  watch(() => state.scopeKey, () => {
    const route = router.currentRoute.value
    if (!navigating && hasInvestigation(route.path)) {
      const query = { ...route.query, ...investigationQuery(state) }
      delete query.env; delete query.window; delete query.since
      if (!state.absolute) { delete query.from; delete query.to }
      void router.replace({ query })
    }
  }, { flush: 'sync' })
}
