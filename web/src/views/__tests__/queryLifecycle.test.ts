import { afterEach, describe, expect, it, vi } from 'vitest'
import { shallowMount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createPinia, disposePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import type { Component } from 'vue'
import { useAuthStore } from '@/stores/auth'
import type { User } from '@/api/types'
import IssueDetailView from '../IssueDetailView.vue'
import SettingsView from '../SettingsView.vue'
import DashboardView from '../DashboardView.vue'
import MonitorsView from '../MonitorsView.vue'
import TransactionDetailView from '../TransactionDetailView.vue'
import TransactionProfileView from '../TransactionProfileView.vue'
import ReleaseDetailView from '../ReleaseDetailView.vue'
import IssueListView from '../IssueListView.vue'
import ReleasesView from '../ReleasesView.vue'
import LogsView from '../LogsView.vue'
import LoginView from '../LoginView.vue'
import DBQueriesView from '../DBQueriesView.vue'
import SpanSamplesPanel from '@/components/SpanSamplesPanel.vue'
import QuotaBanner from '@/components/QuotaBanner.vue'

// Keep Vue Query and apiFetch real: requests must reach fetch with a signal,
// and removing their last observer must cancel the actual network operation.
const admin = {
  id: 'user-1', name: 'Admin', email: 'admin@example.com', timezone: 'UTC',
  permissions: { manage_projects: true, manage_users: true, manage_alerts: true, manage_issues: true },
} as User
const projects = [
  { id: 'p1', name: 'API', slug: 'api', public_key: 'key1', event_count: 0 },
  { id: 'p2', name: 'Web', slug: 'web', public_key: 'key2', event_count: 0 },
]
let wrapper: VueWrapper | undefined
let client: QueryClient
let pinia: ReturnType<typeof createPinia>
type Request = { url: string; signal: AbortSignal; resolve: (data: unknown) => void }
let requests: Request[]

async function open(component: Component, path: string, seed: [unknown[], unknown][] = [], props: Record<string, unknown> = {}) {
  requests = []
  vi.stubGlobal('fetch', vi.fn((url: string, init: RequestInit) => new Promise<Response>((resolve, reject) => {
    const signal = init.signal as AbortSignal
    expect(init.credentials).toBe('include')
    expect(signal).toBeDefined()
    signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true })
    requests.push({ url, signal, resolve: data => resolve(new Response(JSON.stringify(data), {
      headers: { 'Content-Type': 'application/json' },
    })) })
  })))
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } })
  client.setQueryData(['me'], admin, { updatedAt: 1 })
  client.setQueryData(['projects', 'metadata'], projects, { updatedAt: 1 })
  for (const [key, data] of seed) client.setQueryData(key, data, { updatedAt: 1 })
  pinia = createPinia()
  useAuthStore(pinia).setUser(admin)
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/settings/:tab', component: { template: '<div />' } },
    { path: '/:section/:id?', component: { template: '<div />' } },
    { path: '/', component: { template: '<div />' } },
  ] })
  await router.push(path)
  await router.isReady()
  wrapper = shallowMount(component, { props, global: { directives: { tooltip: {} }, plugins: [pinia, router, [VueQueryPlugin, { queryClient: client }]] } })
  await flushPromises()
  return wrapper
}

function request(url: string) {
  const found = requests.find(r => r.url === url)
  expect(found, `missing ${url}; requested ${requests.map(r => r.url).join(', ')}`).toBeDefined()
  return found!
}

async function expectCancelled(urls: string[]) {
  const pending = urls.map(request)
  for (const r of pending) expect(r.signal.aborted).toBe(false)
  wrapper!.unmount()
  wrapper = undefined
  await flushPromises()
  for (const r of pending) expect(r.signal.aborted, r.url).toBe(true)
}

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  if (pinia) disposePinia(pinia)
  client?.clear()
  vi.unstubAllGlobals()
})

describe('view request lifecycle', () => {
  it('cancels issue reads, including dependent event and trace requests', async () => {
    await open(IssueDetailView, '/issues/issue-1', [
      [['issues', 'issue-1'], { id: 'issue-1', title: 'Failure', kind: 'error', event_count: 1, status: 'open', first_seen: '2026-01-01', last_seen: '2026-01-01' }],
      [['issues', 'issue-1', 'events', 0], { payload: { contexts: { trace: { trace_id: 'trace-1' } } } }],
      [['issues', 'issue-1', 'trace', 0], { id: 'tx-1' }],
    ])
    await expectCancelled(['/api/me', '/api/issues/issue-1', '/api/issues/issue-1/events/latest?offset=0',
      '/api/issues/issue-1/comments', '/api/issues/issue-1/history', '/api/users',
      '/api/issues/issue-1/trace?offset=0', '/api/transactions/tx-1/spans',
      '/api/issues/issue-1/tags', '/api/issues/issue-1/events/histogram'])
  })

  it('loads performance issue samples without requesting an error event', async () => {
    await open(IssueDetailView, '/issues/issue-1', [
      [['issues', 'issue-1'], { id: 'issue-1', title: 'N+1', kind: 'n1_query', event_count: 1, status: 'open', first_seen: '2026-01-01', last_seen: '2026-01-01' }],
    ])
    expect(requests.some(r => r.url.includes('/events/latest'))).toBe(false)
    await expectCancelled(['/api/issues/issue-1/perf-events'])
  })

  it.each(['overview', 'audit', 'alerts'])('cancels settings reads on the %s tab', async tab => {
    await open(SettingsView, `/settings/${tab}`)
    const urls = ['/api/config', '/api/tokens', '/api/projects', '/api/users', '/api/me', '/api/invites', '/api/settings']
    if (tab === 'overview') urls.push('/api/instance/health')
    if (tab === 'audit') urls.push('/api/audit?kind=&q=')
    if (tab === 'alerts') urls.push('/api/alert-rules')
    expect(requests.some(r => r.url.includes('/quota'))).toBe(false)
    expect(requests.some(r => r.url.includes('/firings'))).toBe(false)
    await expectCancelled(urls)
  })

  it('cancels dashboard reads and uses lightweight overview endpoints', async () => {
    await open(DashboardView, '/')
    await expectCancelled(['/api/me', '/api/config', '/api/projects', '/api/issues/overview?',
      '/api/transactions/summaries?hours=24', '/api/transactions/counts?hours=168',
      '/api/releases/health?', '/api/alert-rules', '/api/uptime-monitors?', '/api/monitors?', '/api/projects/stats'])
  })

  it('cancels release details and related issue and transaction reads', async () => {
    await open(ReleaseDetailView, '/releases/release-1')
    await expectCancelled(['/api/releases/release-1', '/api/releases/release-1/issues', '/api/releases/release-1/transactions'])
  })

  it('cancels transaction reads and preserves trace log scoping', async () => {
    await open(TransactionDetailView, '/transactions/tx-1', [
      [['transactions', 'tx-1'], { id: 'tx-1', transaction: 'GET /', trace_id: 'trace-1', project_id: 'p1', duration_ms: 100, op: 'http.server', status: 'ok', start_timestamp: '2026-01-01T00:00:00Z' }],
    ])
    await expectCancelled(['/api/transactions/tx-1', '/api/transactions/tx-1/spans', '/api/transactions/tx-1/errors',
      '/api/transactions/tx-1/flamegraph', '/api/logs?trace_id=trace-1&limit=50&project_id=p1'])
  })

  it('cancels profile summaries, percentiles and samples with the selected scope', async () => {
    await open(TransactionProfileView, '/performance/profile?name=checkout&op=http.server&project_id=p1')
    await expectCancelled(['/api/transactions/summaries?hours=24&name=checkout&op=http.server&project_id=p1',
      '/api/transactions/timeseries?hours=24&name=checkout&op=http.server&project_id=p1',
      '/api/transactions?name=checkout&op=http.server&project_id=p1'])
  })

  it('does not fetch monitor history until a monitor is expanded', async () => {
    await open(MonitorsView, '/monitors/cron')
    expect(requests.some(r => /\/(checkins|checks|stats)/.test(r.url))).toBe(false)
    await expectCancelled(['/api/monitors?', '/api/uptime-monitors?'])
  })

  it('cancels quota usage and settings requests when the banner is removed', async () => {
    await open(QuotaBanner, '/', [[['settings'], { event_limit: 1000 }]])
    await expectCancelled(['/api/projects', '/api/settings'])
  })
})

describe('filtered request lifecycle', () => {
  it.each([
    [LoginView, '/login', '/api/auth/providers'],
    [LogsView, '/logs', '/api/logs?'],
    [ReleasesView, '/releases', '/api/releases?'],
    [IssueListView, '/issues', '/api/issues?'],
  ])('cancels requests for $1 when leaving the view', async (view, path, prefix) => {
    await open(view, path)
    const target = requests.filter(r => r.url.startsWith(prefix))
    expect(target).toHaveLength(1)
    await expectCancelled(target.map(r => r.url))
  })

  it('cancels query summaries and timeseries together', async () => {
    await open(DBQueriesView, '/performance/db')
    await expectCancelled(['/api/spans/db?hours=24', '/api/spans/db/timeseries?hours=24'])
  })

  it('cancels span samples when the detail panel closes', async () => {
    await open(SpanSamplesPanel, '/', [], { row: { op: 'db.sql.query', description: 'SELECT 1', p50: 1, p95: 2, sample_count: 1, error_rate: 0 }, hours: 24, env: 'production' })
    await expectCancelled(['/api/spans/samples?op=db.sql.query&description=SELECT+1&hours=24&env=production'])
  })

  it.each(['cron', 'uptime'])('loads %s history only while its monitor is expanded', async kind => {
    const monitor = {
      id: 'monitor-1', project_id: 'p1', name: 'API health', status: 'active', state: 'ok',
      schedule: '0 * * * *', schedule_type: 'crontab', grace_period_secs: 300,
      recent_checkins: [], recent_checks: [], url: 'https://example.com', method: 'GET',
      interval_secs: 60, timeout_secs: 10, expected_codes: '200-299', consecutive_failures: 0,
    }
    const key = kind === 'cron' ? 'monitors' : 'uptime-monitors'
    const view = await open(MonitorsView, `/monitors/${kind}`, [[[key, ''], [monitor]]])
    expect(requests.some(r => /\/(checkins|checks|stats)/.test(r.url))).toBe(false)
    await view.get('.monrow:not(.monrow--header)').trigger('click')
    await flushPromises()
    const urls = kind === 'cron' ? ['/api/monitors/monitor-1/checkins?limit=50'] :
      ['/api/uptime-monitors/monitor-1/checks?limit=50', '/api/uptime-monitors/monitor-1/stats']
    const pending = urls.map(request)
    await view.get('.monrow:not(.monrow--header)').trigger('click')
    await flushPromises()
    for (const r of pending) expect(r.signal.aborted).toBe(true)
  })

  it('loads project quota only when expanded and cancels it on collapse', async () => {
    const view = await open(SettingsView, '/settings/projects', [[['projects', 'usage'], projects]])
    expect(requests.some(r => r.url.endsWith('/quota'))).toBe(false)
    await view.get('.proj-card__head--row').trigger('click')
    await flushPromises()
    const quota = request('/api/projects/p1/quota')
    await view.get('.proj-card__head--row').trigger('click')
    await flushPromises()
    expect(quota.signal.aborted).toBe(true)
  })

  it('unwraps alert rules and loads delivery history only for an expanded rule', async () => {
    const view = await open(SettingsView, '/settings/alerts')
    const rule = { id: 'rule-1', name: 'New errors', enabled: true, trigger: 'new_issue', channel: 'email', project_ids: ['p1'], email_to: 'admin@example.com' }
    request('/api/alert-rules').resolve({ rules: [rule] })
    await flushPromises()
    expect(view.text()).toContain('New errors')
    expect(requests.some(r => r.url.endsWith('/firings'))).toBe(false)
    await view.get('.rule__head').trigger('click')
    await flushPromises()
    request('/api/alert-rules/rule-1/firings').resolve({ firings: [] })
    await flushPromises()
    expect(client.getQueryData(['alert-firings', 'rule-1'])).toEqual([])
  })

  it('treats an empty alert response as no configured rules', async () => {
    const view = await open(SettingsView, '/settings/alerts')
    request('/api/alert-rules').resolve({})
    await flushPromises()
    expect(client.getQueryData(['alert-rules'])).toEqual([])
    expect(view.text()).toContain('No alert rules configured')
  })

  it('unwraps dashboard alert responses instead of caching their transport wrapper', async () => {
    await open(DashboardView, '/')
    request('/api/alert-rules').resolve({ rules: [] })
    await flushPromises()
    expect(client.getQueryData(['alert-rules'])).toEqual([])
  })
})

describe('pagination cancellation', () => {
  it.each(['issues', 'releases'])('aborts the next %s page when leaving the view', async kind => {
    const record = kind === 'issues'
      ? { id: 'issue-1', title: 'Failure', level: 'error', kind: 'error', status: 'open', project_id: 'p1', event_count: 1, user_count: 1, first_seen: '2026-01-01', last_seen: '2026-01-01', sparkline: [] }
      : { id: 'release-1', version: 'v1', project_id: 'p1', deployed_at: '2026-01-01', tx_count: 1, tx_p50: 1, tx_p95: 2, tx_error_rate: 0, new_issues: 0, regressed_issues: 0 }
    const view = await open(kind === 'issues' ? IssueListView : ReleasesView, `/${kind}`)
    const first = requests.find(r => r.url.startsWith(`/api/${kind}?`))!
    first.resolve({ [kind]: [record], total: 2, has_more: true, next_cursor_time: '2026-01-01T00:00:00Z', next_cursor_id: 'cursor-1' })
    await flushPromises()
    await view.get(kind === 'issues' ? '.list-footer__more' : '.list-footer .btn').trigger('click')
    const next = requests.find(r => r.url.includes('cursor_id=cursor-1'))!
    expect(next).toBeDefined()
    expect(new URL(next.url, 'http://localhost').searchParams.get('cursor_time')).toBe('2026-01-01T00:00:00Z')
    await expectCancelled([next.url])
  })
})

describe('monitor project metadata refresh', () => {
  it('shares the initial metadata request and refetches it through the monitor observer', async () => {
    await open(MonitorsView, '/monitors/cron')
    const url = '/api/projects/metadata'
    expect(requests.filter(r => r.url === url)).toHaveLength(1)
    request(url).resolve(projects)
    await flushPromises()

    const refresh = client.invalidateQueries({ queryKey: ['projects', 'metadata'], exact: true })
    await flushPromises()
    const metadataRequests = requests.filter(r => r.url === url)
    expect(metadataRequests).toHaveLength(2)
    expect(metadataRequests[1].signal.aborted).toBe(false)
    const updated = [...projects, { id: 'p3', name: 'Worker', slug: 'worker', public_key: 'key3', event_count: 0 }]
    metadataRequests[1].resolve(updated)
    await refresh
    expect(client.getQueryData(['projects', 'metadata'])).toEqual(updated)
  })
})
