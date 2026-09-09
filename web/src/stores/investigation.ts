import { computed, ref, watch } from 'vue'
import { defineStore } from 'pinia'

export const RANGE_HOURS: Record<string, number> = { '1h': 1, '24h': 24, '7d': 168, '30d': 720, '90d': 2160 }
export const CORE_RANGES = ['1h', '24h', '7d', '30d']

function saved() {
  try { return JSON.parse(sessionStorage.getItem('tindra:investigation') || 'null') } catch { return null }
}
function legacyProjects(): string[] {
  try { const ids = JSON.parse(sessionStorage.getItem('tindra:projectFilter') || '[]'); return Array.isArray(ids) ? ids.filter((id): id is string => typeof id === 'string') : [] } catch { return [] }
}

function legacyPreference(key: string): string | null {
  try { return localStorage.getItem(`tindra:perf:${key}`) } catch { return null }
}

export const useInvestigationStore = defineStore('investigation', () => {
  const initial = saved() ?? { environment: legacyPreference('env') ?? 'All', range: legacyPreference('window') ?? '24h' }
  const projectIds = ref<string[]>(Array.isArray(initial?.projectIds) ? initial.projectIds.filter((id: unknown) => typeof id === 'string') : legacyProjects())
  const environment = ref<string>(typeof initial?.environment === 'string' ? initial.environment : 'All')
  const range = ref<string>(Object.hasOwn(RANGE_HOURS, initial?.range ?? '') || initial?.range === 'All' ? initial.range : '24h')
  const absolute = ref<{ from: string; to: string } | null>(null)
  const routeError = ref('')
  const paused = ref(initial?.paused === true)
  const browsingHistory = ref(false)
  const anchor = ref(Date.now())
  const bounds = computed(() => {
    if (absolute.value) return absolute.value
    if (range.value === 'All') return null
    return { from: new Date(anchor.value - (RANGE_HOURS[range.value] ?? 24) * 3_600_000).toISOString(), to: new Date(anchor.value).toISOString() }
  })
  const scopeKey = computed(() => JSON.stringify([[...projectIds.value].sort(), environment.value, range.value, absolute.value]))
  watch(scopeKey, () => { anchor.value = Date.now(); browsingHistory.value = false }, { flush: 'sync' })
  watch([projectIds, environment, range, paused], () => {
    try { sessionStorage.setItem('tindra:investigation', JSON.stringify({ projectIds: projectIds.value, environment: environment.value, range: range.value, paused: paused.value })) } catch {}
  }, { deep: true })

  // Bounds are resolved once per refresh, and added only at request execution.
  // Cache keys retain the stable scope while background refresh preserves data.
  function request(path: string, comparison = false) {
    const [pathname, query = ''] = path.split('?')
    const p = new URLSearchParams(query)
    const window = bounds.value
    if (window) {
      let from = Date.parse(window.from), to = Date.parse(window.to)
      if (comparison) { const duration = to - from; to = from; from -= duration }
      p.set('from', new Date(from).toISOString()); p.set('to', new Date(to).toISOString())
      p.delete('offset')
      p.delete('all_time'); p.delete('as_of')
      // Chart resolution must follow custom bounds, not the previous preset.
      if (p.has('hours')) p.set('hours', String(Math.ceil((to - from) / 3_600_000)))
    }
    if (!window && range.value === 'All' && (pathname === '/api/issues' || pathname === '/api/issues/export')) {
      p.set('all_time', '1'); p.set('as_of', new Date(anchor.value).toISOString())
    }
    return `${pathname}?${p}`
  }
  function link(path: string) {
    const url = new URL(path, window.location.origin)
    const p = url.searchParams
    if (!p.has('project_id')) for (const id of projectIds.value.length ? [...projectIds.value].sort() : ['all']) p.append('project_id', id)
    if (!p.has('env') && !p.has('environment')) p.set('environment', environment.value === 'All' ? 'all' : environment.value)
    if (!p.has('range') && !p.has('window')) {
      p.set('range', absolute.value ? 'custom' : range.value === 'All' ? 'all' : range.value)
      if (absolute.value) { p.set('from', absolute.value.from); p.set('to', absolute.value.to) }
    }
    return url.pathname + '?' + p + url.hash
  }
  function refresh() { browsingHistory.value = false; anchor.value = Date.now() }
  function setRange(value: string) { absolute.value = null; range.value = value }
  return { routeError, projectIds, environment, range, absolute, paused, browsingHistory, anchor, bounds, scopeKey, request, link, refresh, setRange }
})
