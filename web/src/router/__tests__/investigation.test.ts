import { describe, it, expect } from 'vitest'
import { createPinia } from 'pinia'
import { nextTick } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { installInvestigationRouter, investigationQuery } from '../investigation'
import { useInvestigationStore } from '@/stores/investigation'
import { useToast } from '@/composables/useToast'

function setup() {
  const pinia = createPinia()
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }] })
  installInvestigationRouter(router, pinia)
  return { router, state: useInvestigationStore(pinia) }
}
describe('investigation navigation', () => {
  it.each([
    '/performance/transactions', '/performance/queries', '/performance/caches',
    '/performance/jobs', '/performance/browser', '/dashboard', '/logs', '/transactions/profile',
  ])('resolves all-time issues to bounded requests on %s', async path => {
    const { router, state } = setup()
    await router.push('/issues?range=all&environment=production&user=alice&project_id=11111111-1111-4111-8111-111111111111')
    const notices = useToast().toasts.value.length
    await router.push({ path, query: { ...investigationQuery(state), op: 'http.server' }, hash: '#results' })
    expect(state.range).toBe('90d')
    expect(state.routeError).toBe('')
    expect(router.currentRoute.value.query).toMatchObject({ range: '90d', environment: 'production', user: 'alice', project_id: ['11111111-1111-4111-8111-111111111111'], op: 'http.server' })
    expect(router.currentRoute.value.hash).toBe('#results')
    const request = new URL(state.request('/api/transactions/summaries?hours=24'), 'http://test')
    expect(Date.parse(request.searchParams.get('to')!) - Date.parse(request.searchParams.get('from')!)).toBe(90 * 86400000)
    expect(request.searchParams.get('hours')).toBe('2160')
    expect(useToast().toasts.value.length).toBe(notices + 1)
    expect(useToast().toasts.value.at(-1)?.type).toBe('info')
    router.back(); await flushPromises()
    expect(router.currentRoute.value.path).toBe('/issues')
    expect(state.range).toBe('All')
    router.forward(); await flushPromises()
    expect(state.range).toBe('90d')
    expect(useToast().toasts.value.length).toBe(notices + 1)
  })
  it.each(['range=all', 'range=All', 'window=All', ''])('recovers an all-time session or direct link with %s', async query => {
    sessionStorage.setItem('tindra:investigation', JSON.stringify({ range: 'All' }))
    const { router, state } = setup()
    await router.push('/performance/queries?' + query)
    expect(state.range).toBe('90d')
    expect(router.currentRoute.value.query.range).toBe('90d')
    expect(router.currentRoute.value.query.window).toBeUndefined()
    expect(JSON.parse(sessionStorage.getItem('tindra:investigation')!).range).toBe('90d')
  })
  it('keeps all-time context on record and non-telemetry pages', async () => {
    const { router, state } = setup()
    await router.push('/issues?range=all')
    for (const path of ['/issues/abc', '/transactions/abc', '/releases', '/monitors/uptime']) {
      await router.push(path)
      expect(state.range).toBe('All')
    }
  })
  it('preserves a custom interval selected after All when navigating without query parameters', async () => {
    const { router, state } = setup()
    await router.push('/issues?range=all')
    await router.push('/issues?range=custom&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z')
    await router.push('/performance/queries')
    expect(state.range).toBe('All')
    expect(state.absolute).toEqual({ from: '2026-01-01T00:00:00Z', to: '2026-01-02T00:00:00Z' })
    expect(router.currentRoute.value.query.range).toBe('custom')
  })
  it('inherits legacy URL context and carries it across telemetry views', async () => {
    const { router, state } = setup()
    await router.push('/performance/transactions?project_id=11111111-1111-4111-8111-111111111111&env=eu-west&window=7d&op=http.server')
    expect(state.environment).toBe('eu-west')
    expect(state.range).toBe('7d')
    await router.push('/logs')
    expect(router.currentRoute.value.query).toMatchObject({ environment: 'eu-west', range: '7d', project_id: ['11111111-1111-4111-8111-111111111111'] })
  })
  it('explicit All overrides saved context and selector changes update the URL', async () => {
    const { router, state } = setup()
    state.projectIds = ['11111111-1111-4111-8111-111111111111']; state.environment = 'staging'
    await router.push('/issues?project_id=all&environment=all&range=24h')
    expect(state.projectIds).toEqual([]); expect(state.environment).toBe('All')
    state.environment = 'preview'
    await nextTick(); await flushPromises()
    expect(router.currentRoute.value.query.environment).toBe('preview')
  })
  it('restores browser history without overwriting the destination context', async () => {
    const { router, state } = setup()
    await router.push('/issues?environment=staging&range=7d')
    await router.push('/logs?environment=production&range=1h')
    router.back(); await flushPromises()
    expect(state.environment).toBe('staging'); expect(state.range).toBe('7d')
  })
  it('keeps absolute bounds when navigating and clears them on preset selection', async () => {
    const { router, state } = setup()
    await router.push('/logs?range=custom&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z')
    await router.push('/performance/browser')
    expect(state.absolute?.from).toBe('2026-01-01T00:00:00Z')
    state.setRange('1h'); await flushPromises()
    expect(router.currentRoute.value.query.from).toBeUndefined()
  })
  it('clears invalid-link errors when leaving investigation views', async () => {
    const { router, state } = setup()
    await router.push('/logs?range=invalid')
    expect(state.routeError).not.toBe('')
    await router.push('/settings')
    expect(state.routeError).toBe('')
  })
  it('rejects contradictory preset and absolute bounds instead of discarding bounds', async () => {
    const { router, state } = setup()
    await router.push('/logs?range=1h&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z')
    expect(state.routeError).toContain('combines a preset')
  })
  it.each(['All', '90d'])('uses custom request bounds independently of the previous %s preset', async (preset) => {
    const { router, state } = setup()
    state.setRange(preset)
    await router.push('/issues?range=custom&from=2026-01-01T00:00:00Z&to=2026-01-01T01:00:00Z')
    const request = new URL(state.request('/api/issues?hours=2160&all_time=1&as_of=old'), 'http://localhost')
    expect(request.searchParams.get('from')).toBe('2026-01-01T00:00:00.000Z')
    expect(request.searchParams.get('to')).toBe('2026-01-01T01:00:00.000Z')
    expect(request.searchParams.get('hours')).toBe('1')
    expect(request.searchParams.has('all_time')).toBe(false)
    expect(request.searchParams.has('as_of')).toBe(false)
  })

})

describe('investigation URL validation and refresh', () => {
  it.each([
    'project_id=not-a-uuid', 'project_id', 'project_id=all&project_id=11111111-1111-4111-8111-111111111111',
    'range=custom', 'range=custom&from=bad&to=bad',
    'from=2026-01-01T00:00:00&to=2026-01-02T00:00:00Z',
    'from=2026-01-02T00:00:00Z&to=2026-01-01T00:00:00Z',
    'from=2026-01-01T00:00:00Z&to=2026-06-01T00:00:00Z',
  ])('rejects invalid investigation query %s', async query => {
    const { router, state } = setup()
    await router.push('/issues?' + query)
    expect(state.routeError).not.toBe('')
  })
  it('hydrates implicit custom bounds, legacy issue ranges and canonical all-time links', async () => {
    const { router, state } = setup()
    await router.push('/issues?since=90d&env=All')
    expect(state.range).toBe('90d')
    expect(router.currentRoute.value.query.since).toBeUndefined()
    await router.push('/issues?range=all')
    expect(state.bounds).toBeNull()
    const request = new URL(state.request('/api/issues'), 'http://test')
    expect(request.searchParams.get('all_time')).toBe('1')
    expect(request.searchParams.get('as_of')).toBe(new Date(state.anchor).toISOString())
    await router.push('/logs?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z')
    expect(state.absolute?.from).toBe('2026-01-01T00:00:00Z')
    expect(router.currentRoute.value.query.range).toBe('custom')
  })
  it('advances stale relative bounds on navigation and preserves them when paused', async () => {
    const { router, state } = setup()
    await router.push('/issues')
    state.anchor = Date.now() - 60000
    const before = state.anchor
    await router.push('/logs')
    expect(state.anchor).toBeGreaterThan(before)
    state.paused = true
    state.anchor = before
    await router.push('/issues')
    expect(state.anchor).toBe(before)
  })
})

it('carries and clears the application user across views and browser history', async () => {
  const { router, state } = setup()
  await router.push('/performance/transactions?user=alice')
  const scope = state.scopeKey
  await router.push('/dashboard')
  expect(router.currentRoute.value.query.user).toBe('alice')
  expect(new URL(state.request('/api/transactions/summaries'), 'http://test').searchParams.get('user')).toBe('alice')
  await router.push('/logs?user=')
  expect(state.userIdentity).toBe('')
  expect(state.scopeKey).not.toBe(scope)
  router.back()
  await flushPromises()
  expect(state.userIdentity).toBe('alice')
})
