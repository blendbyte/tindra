import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref, toValue } from 'vue'

const pushMock = vi.fn()

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: pushMock })),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
}))

vi.mock('@/stores/projects', () => ({
  useProjectsStore: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

vi.mock('@/utils/formatters', () => ({
  formatDuration: vi.fn((n: number) => `${n}ms`),
  formatRel: vi.fn(() => '2m ago'),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: vi.fn(() => ({ user: { timezone: 'UTC' }, setUser: vi.fn() })),
}))

import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { apiFetch } from '@/api/client'
import { useInvestigationStore } from '@/stores/investigation'
import KpiStripWidget from '../KpiStripWidget.vue'
import HotIssuesWidget from '../HotIssuesWidget.vue'
import SlowTransactionsWidget from '../SlowTransactionsWidget.vue'
import ReleasesWidget from '../ReleasesWidget.vue'
import AlertsWidget from '../AlertsWidget.vue'
import MonitorsWidget from '../MonitorsWidget.vue'
import ProjectOverviewWidget from '../ProjectOverviewWidget.vue'
import TransactionDensityWidget from '../TransactionDensityWidget.vue'
import type { DashboardIssue, DashboardRelease } from '../shared'

const RouterLink = { name: 'RouterLink', props: ['to'], template: '<a><slot /></a>' }
const stubs = {
  RouterLink,
  Icon: { template: '<span />' },
  Sparkline: { template: '<span />' },
}

// Answers each useQuery call from a map keyed by the first query key segment.
function mockQueries(results: Record<string, unknown>) {
  vi.mocked(useQuery).mockImplementation(((options: any) => {
    const [head, sub] = toValue(options.queryKey) as unknown[]
    const name = head === 'projects' ? `projects/${String(sub)}` : String(head)
    if (!(name in results)) throw new Error(`unexpected query ${name}`)
    return results[name]
  }) as any)
}

const tx = (overrides = {}) => ({
  transaction: '/api/a', op: 'http.server', project_id: 'p1', sample_count: 10,
  tpm: 1, p50: 100, p95: 300, apdex: 1, failure_rate: 0, time_spent_ms: 1000,
  ...overrides,
})

const issue = (id: string, event_count: number): DashboardIssue => ({
  id, title: `Issue ${id}`, level: 'error', event_count, sparkline: [], project_id: 'p1',
})

const release = (id: string, overrides = {}): DashboardRelease => ({
  id, version: `v${id}`, project_id: 'p1', deployed_at: '2026-01-01T00:00:00Z',
  created_at: '2026-01-01T00:00:00Z', new_issues: 0, regressed_issues: 0,
  ...overrides,
})

beforeEach(() => {
  vi.mocked(useQuery).mockReset()
  vi.mocked(apiFetch).mockReset()
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [], projects: [] } as any)
  pushMock.mockReset()
})

describe('KpiStripWidget', () => {
  it('shows a skeleton in every card while loading', () => {
    const wrapper = mount(KpiStripWidget, { props: { loading: true, openCount: 0 } })
    expect(wrapper.findAll('.db-kpi .skel')).toHaveLength(5)
    expect(wrapper.find('.db-kpi__value').exists()).toBe(false)
  })

  it('weights error rate, p95 and apdex by sample count', () => {
    const wrapper = mount(KpiStripWidget, {
      props: {
        loading: false,
        openCount: 1500,
        txSummaries: [
          tx({ sample_count: 10, failure_rate: 2, p95: 100, apdex: 1 }),
          tx({ sample_count: 30, failure_rate: 6, p95: 500, apdex: 0.6 }),
        ],
      },
    })
    const values = wrapper.findAll('.db-kpi__value').map(v => v.text())
    // error (2*10 + 6*30) / 40 = 5.0, p95 (1000 + 15000) / 40 = 400, apdex (10 + 18) / 40 = 0.70
    expect(values).toEqual(['1.5k', '5.0%', '400ms', '0.70', '40'])
    expect(wrapper.find('.leveldot--fatal').exists()).toBe(true)
  })

  it('counts zero transactions for an empty summary list but leaves averages muted', () => {
    const wrapper = mount(KpiStripWidget, { props: { loading: false, openCount: 0, txSummaries: [] } })
    expect(wrapper.findAll('.db-kpi__value--muted')).toHaveLength(3)
    expect(wrapper.findAll('.db-kpi__value').at(-1)?.text()).toBe('0')
    expect(wrapper.find('.leveldot--ok').exists()).toBe(true)
  })
})

describe('HotIssuesWidget', () => {
  it('shows the five busiest issues, busiest first', () => {
    const issues = [issue('a', 1), issue('b', 6), issue('c', 3), issue('d', 5), issue('e', 2), issue('f', 4)]
    const wrapper = mount(HotIssuesWidget, { props: { loading: false, issues }, global: { stubs } })
    expect(wrapper.findAll('.db-issue-row__title').map(t => t.text())).toEqual(['Issue b', 'Issue d', 'Issue f', 'Issue c', 'Issue e'])
  })

  it('shows skeleton rows while loading', () => {
    const wrapper = mount(HotIssuesWidget, { props: { loading: true, issues: [issue('a', 1)] }, global: { stubs } })
    expect(wrapper.findAll('.db-issue-row .skel')).toHaveLength(15)
    expect(wrapper.text()).not.toContain('Issue a')
  })

  it('opens an issue from the keyboard', async () => {
    const wrapper = mount(HotIssuesWidget, { props: { loading: false, issues: [issue('a', 1)] }, global: { stubs } })
    const row = wrapper.find('.db-issue-row')
    expect(row.attributes('role')).toBe('link')
    await row.trigger('keydown', { key: 'Enter' })
    expect(pushMock).toHaveBeenCalledWith('/issues/a')
  })
})

describe('SlowTransactionsWidget', () => {
  it('lists the five slowest transactions by p95 and links to their profile', () => {
    const txSummaries = [100, 600, 300, 500, 200, 400].map(p95 => tx({ transaction: `/t${p95}`, p95 }))
    const wrapper = mount(SlowTransactionsWidget, { props: { loading: false, txSummaries }, global: { stubs } })
    expect(wrapper.findAll('.db-tx-row__name').map(n => n.text())).toEqual(['/t600', '/t500', '/t400', '/t300', '/t200'])
    const to = wrapper.findAllComponents(RouterLink).find(l => l.props('to')?.name === 'transaction-profile')?.props('to')
    expect(to.query).toMatchObject({ name: '/t600', op: 'http.server' })
  })

  it('flags slow and failing rows', () => {
    const wrapper = mount(SlowTransactionsWidget, {
      props: { loading: false, txSummaries: [tx({ p50: 600, p95: 2500, failure_rate: 3 })] },
      global: { stubs },
    })
    expect(wrapper.find('.db-tx-row__val--bad').text()).toBe('2500ms')
    expect(wrapper.find('.db-tx-row__pct--warn').text()).toBe('3.0%')
    expect(wrapper.find('.db-tx-row__bar-fill').attributes('style')).toContain('width: 30%')
  })
})

describe('ReleasesWidget', () => {
  it('shows at most five releases with their health pill', () => {
    const releases = [
      release('1', { regressed_issues: 2 }),
      release('2', { new_issues: 1 }),
      release('3'), release('4'), release('5'), release('6'),
    ]
    const wrapper = mount(ReleasesWidget, { props: { loading: false, releases }, global: { stubs } })
    expect(wrapper.findAll('.db-release-row')).toHaveLength(5)
    expect(wrapper.text()).not.toContain('v6')
    const pills = wrapper.findAll('.rel-pill').map(p => p.text())
    expect(pills.slice(0, 3)).toEqual(['2 regressed', '1 new', 'Clean'])
  })

  it('shows skeleton rows while loading', () => {
    const wrapper = mount(ReleasesWidget, { props: { loading: true, releases: [] }, global: { stubs } })
    expect(wrapper.findAll('.db-release-row')).toHaveLength(4)
    expect(wrapper.text()).not.toContain('No releases yet')
  })
})

describe('AlertsWidget', () => {
  const rule = (id: string, last_fired_at: string | null, enabled = true) => ({
    id, name: `Rule ${id}`, enabled, last_fired_at, channel: 'email',
  })

  it('lists the five most recently fired rules, newest first', () => {
    const rules = ['01', '05', '03', '06', '02', '04'].map(d => rule(d, `2026-01-${d}T00:00:00Z`))
    mockQueries({
      'me': { data: ref({ permissions: { manage_alerts: false } }) },
      'alert-rules': { data: ref([...rules, rule('x', null, false)]), isLoading: ref(false) },
    })
    const wrapper = mount(AlertsWidget, { global: { stubs } })
    expect(wrapper.findAll('.db-alert-row__name').map(n => n.text())).toEqual(['Rule 06', 'Rule 05', 'Rule 04', 'Rule 03', 'Rule 02'])
    expect(wrapper.find('.db-sec__foot').text()).toBe('6 rules active')
  })

  it('caches the unwrapped rule list', async () => {
    mockQueries({
      'me': { data: ref(undefined) },
      'alert-rules': { data: ref([]), isLoading: ref(false) },
    })
    mount(AlertsWidget, { global: { stubs } })
    const options = vi.mocked(useQuery).mock.calls.map(c => c[0] as any).find(o => toValue(o.queryKey)[0] === 'alert-rules')
    vi.mocked(apiFetch).mockResolvedValueOnce({ rules: [rule('a', null)] })
    await expect(options.queryFn({ signal: new AbortController().signal })).resolves.toEqual([rule('a', null)])
    vi.mocked(apiFetch).mockResolvedValueOnce(null)
    await expect(options.queryFn({ signal: new AbortController().signal })).resolves.toEqual([])
  })
})

describe('MonitorsWidget', () => {
  const minutesAgo = (m: number) => new Date(Date.now() - m * 60_000).toISOString()

  it('shows how long each problem monitor has been failing', () => {
    mockQueries({
      'uptime-monitors': { data: ref([
        { id: 'u1', name: 'API', status: 'active', state: 'down', went_down_at: minutesAgo(5), last_ok_at: null, last_status_code: null, last_error: 'timeout' },
      ]) },
      'monitors': { data: ref([
        { id: 'c1', name: 'ETL', status: 'active', state: 'missed', next_expected_at: minutesAgo(90), last_checkin_at: null },
        { id: 'c2', name: 'Backup', status: 'active', state: 'error', next_expected_at: null, last_checkin_at: minutesAgo(3 * 24 * 60) },
      ]) },
    })
    const wrapper = mount(MonitorsWidget, { global: { stubs } })
    const rows = wrapper.findAll('.db-mon-row')
    expect(rows.map(r => r.find('.db-mon-row__since').text())).toEqual(['5m', '1h 30m', '3d'])
    expect(rows[0].text()).toContain('timeout')
    expect(rows[1].text()).toContain('missed')
    expect(rows[2].text()).toContain('error')
  })

  it('scopes both monitor queries to the selected projects', () => {
    vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: ['p2', 'p1'], projects: [] } as any)
    mockQueries({ 'uptime-monitors': { data: ref([]) }, 'monitors': { data: ref([]) } })
    mount(MonitorsWidget, { global: { stubs } })
    const keys = vi.mocked(useQuery).mock.calls.map(c => toValue((c[0] as any).queryKey))
    expect(keys).toEqual([['uptime-monitors', 'project_id=p1&project_id=p2'], ['monitors', 'project_id=p1&project_id=p2']])
  })
})

describe('ProjectOverviewWidget', () => {
  it('renders nothing and keeps its query disabled with fewer than two projects', () => {
    vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [], projects: [{ id: 'p1', name: 'Solo' }] } as any)
    mockQueries({ 'projects/stats': { data: ref(undefined), isFetching: ref(false) } })
    const wrapper = mount(ProjectOverviewWidget, { global: { stubs } })
    expect(wrapper.find('.db-projects').exists()).toBe(false)
    const options = vi.mocked(useQuery).mock.calls[0][0] as any
    expect(toValue(options.enabled)).toBe(false)
  })

  it('only lists selected projects and shows skeletons until counts arrive', () => {
    vi.mocked(useProjectsStore).mockReturnValue({
      selectedIds: ['p2'],
      projects: [{ id: 'p1', name: 'Frontend' }, { id: 'p2', name: 'Backend' }],
    } as any)
    mockQueries({ 'projects/stats': { data: ref(undefined), isFetching: ref(true) } })
    const loading = mount(ProjectOverviewWidget, { global: { stubs } })
    expect(loading.findAll('.db-proj-row .skel')).toHaveLength(10)

    mockQueries({ 'projects/stats': { data: ref([{ project_id: 'p2', open_issues: 4 }]), isFetching: ref(false) } })
    const loaded = mount(ProjectOverviewWidget, { props: { txSummaries: [tx({ project_id: 'p2', failure_rate: 3 })] }, global: { stubs } })
    const rows = loaded.findAll('.db-proj-row')
    expect(rows).toHaveLength(1)
    expect(rows[0].text()).toContain('Backend')
    expect(rows[0].find('.db-proj-row__val--bad').text()).toBe('4')
    expect(rows[0].text()).toContain('3.0%')
  })
})

describe('TransactionDensityWidget', () => {
  it('passes the environment and app user to the weekly counts request', async () => {
    const investigation = useInvestigationStore()
    investigation.environment = 'production'
    mockQueries({ 'dash-tx-counts': { data: ref(undefined) } })
    mount(TransactionDensityWidget)
    const options = vi.mocked(useQuery).mock.calls[0][0] as any
    expect(toValue(options.queryKey)).toEqual(['dash-tx-counts', '', 'production', ''])
    await options.queryFn({ signal: new AbortController().signal })
    expect(apiFetch).toHaveBeenCalledWith('/api/transactions/counts?hours=168&env=production', { signal: expect.any(AbortSignal) })
  })

  it('renders an empty 7 by 24 grid without data', () => {
    mockQueries({ 'dash-tx-counts': { data: ref({ buckets: [] }) } })
    const wrapper = mount(TransactionDensityWidget)
    expect(wrapper.findAll('.db-heatmap__row')).toHaveLength(7)
    expect(wrapper.findAll('.db-heatmap__row .db-heatmap__cell')).toHaveLength(7 * 24)
    expect(wrapper.find('.db-heatmap__row .db-heatmap__cell').attributes('data-tip')).toMatch(/00:00–01:00 · no events$/)
  })
})
