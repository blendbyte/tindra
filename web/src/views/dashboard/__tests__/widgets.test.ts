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

vi.mock('@/utils/formatters', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/utils/formatters')>(),
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
import VitalsWidget from '../VitalsWidget.vue'
import BriefingWidget from '../BriefingWidget.vue'
import ProjectPerformanceWidget from '../ProjectPerformanceWidget.vue'
import PerformanceTrendWidget from '../PerformanceTrendWidget.vue'
import MonitorsOverviewWidget from '../MonitorsOverviewWidget.vue'
import BusiestIssuesWidget from '../BusiestIssuesWidget.vue'
import SlowTransactionsWidget from '../SlowTransactionsWidget.vue'
import ReleasesWidget from '../ReleasesWidget.vue'
import AlertsWidget from '../AlertsWidget.vue'
import TransactionDensityWidget from '../TransactionDensityWidget.vue'
import type { DashboardIssue, DashboardRelease } from '../shared'
import type { AttentionIssue, AttentionIssuesPage } from '../briefing'
import type { PerformanceOverview, ProjectPerformance } from '../overview'

const RouterLink = { name: 'RouterLink', props: ['to'], template: '<a :data-to="JSON.stringify(to)"><slot /></a>' }
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

const perf = (overrides: Partial<ProjectPerformance> = {}): ProjectPerformance => ({
  requests: 100, p50: 100, p95: 300, apdex: 0.98, failure_rate: 0,
  prev_requests: 100, prev_p95: 300, prev_failure_rate: 0,
  ...overrides,
})

const overviewOf = (total: Partial<ProjectPerformance> = {}, projects: ProjectPerformance[] = []): PerformanceOverview => ({
  total: perf(total), projects,
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

describe('VitalsWidget', () => {
  it('shows a skeleton in every cell while loading', () => {
    const wrapper = mount(VitalsWidget, { props: { loading: true }, global: { stubs } })
    expect(wrapper.findAll('.vitals__value .skel')).toHaveLength(4)
  })

  it('reads the real totals and colours values outside their range', () => {
    const wrapper = mount(VitalsWidget, {
      props: { loading: false, overview: overviewOf({ requests: 40, failure_rate: 0.05, prev_failure_rate: 0.05, p95: 400, prev_p95: 400, apdex: 0.7 }) },
      global: { stubs },
    })
    const value = (key: string) => wrapper.find(`[data-vital="${key}"] .vitals__value`)
    expect(value('error-rate').text()).toBe('5.0%')
    expect(value('p95').text()).toBe('400ms')
    expect(value('transactions').text()).toBe('40')
    expect(value('apdex').text()).toBe('0.70')
    expect(value('error-rate').find('.vitals__value--warning').exists()).toBe(true)
    expect(value('apdex').find('.vitals__value--warning').exists()).toBe(true)
    expect(value('p95').find('span').classes()).toEqual([])
  })

  it('counts zero transactions but leaves rates muted when nothing was measured', () => {
    const wrapper = mount(VitalsWidget, {
      props: { loading: false, overview: overviewOf({ requests: 0, p50: 0, p95: 0, apdex: 0 }) },
      global: { stubs },
    })
    expect(wrapper.findAll('.vitals__value--muted')).toHaveLength(3)
    expect(wrapper.find('[data-vital="transactions"] .vitals__value').text()).toBe('0')
  })

  it('does not colour a handful of requests', () => {
    const wrapper = mount(VitalsWidget, {
      props: { loading: false, overview: overviewOf({ requests: 3, failure_rate: 0.33, apdex: 0.2 }) },
      global: { stubs },
    })
    expect(wrapper.find('.vitals__value--danger').exists()).toBe(false)
  })

  it('marks p95 and error rate that rose against the previous window', () => {
    const wrapper = mount(VitalsWidget, {
      props: { loading: false, overview: overviewOf({ p95: 900, prev_p95: 300, failure_rate: 0.01, prev_failure_rate: 0.01 }) },
      global: { stubs },
    })
    expect(wrapper.find('[data-vital="p95"] .vitals__rose').text()).toContain('Up from 300ms')
    expect(wrapper.find('[data-vital="error-rate"] .vitals__rose').exists()).toBe(false)
  })
})

describe('BriefingWidget', () => {
  const minutesAgo = (m: number) => new Date(Date.now() - m * 60_000).toISOString()
  const attentionIssue = (id: string, overrides: Partial<AttentionIssue> = {}): AttentionIssue => ({
    id, project_id: 'p1', title: `TypeError: broken ${id}`, level: 'error', status: 'open',
    event_count: 3, user_count: 0, first_seen: minutesAgo(30), last_seen: minutesAgo(1),
    regressed_at: null, first_release: '1.5.0', assignee: null, sparkline: [0, 1, 2],
    ...overrides,
  })
  const page = (overrides: Partial<AttentionIssuesPage> = {}): AttentionIssuesPage => ({
    regressed: [], regressed_total: 0, new: [], new_total: 0, ...overrides,
  })
  const uptimeMonitor = (overrides = {}) => ({
    id: 'u1', project_id: 'p1', name: 'Gateway', status: 'active', state: 'down', went_down_at: minutesAgo(65),
    last_ok_at: null, last_status_code: 503, last_error: null, consecutive_failures: 4,
    recent_checks: [{ status: 'up', checked_at: minutesAgo(70) }, { status: 'down', checked_at: minutesAgo(60) }],
    ...overrides,
  })

  function mountBriefing({
    attention = page() as AttentionIssuesPage | undefined,
    attentionError = false,
    attentionMissing = false,
    monitorsError = false,
    uptime = [] as unknown[],
    cron = [] as unknown[],
    lastVisit = null as number | null,
    openCount = 0,
    overview = undefined as PerformanceOverview | undefined,
  } = {}) {
    mockQueries({
      'issues': {
        data: ref(attentionMissing ? undefined : attention), isFetching: ref(false), isError: ref(attentionError),
        fetchStatus: ref('idle'), dataUpdatedAt: ref(0), refetch: vi.fn(),
      },
      'uptime-monitors': { data: ref(monitorsError ? undefined : uptime), isFetching: ref(false), isError: ref(monitorsError), refetch: vi.fn() },
      'monitors': { data: ref(cron), isFetching: ref(false), isError: ref(false), refetch: vi.fn() },
    })
    return mount(BriefingWidget, { props: { loading: false, openCount, lastVisit, overview }, global: { stubs } })
  }

  it('says all clear when nothing is failing, new or regressed', () => {
    const wrapper = mountBriefing({ openCount: 12, uptime: [uptimeMonitor({ state: 'up', went_down_at: null })] })
    expect(wrapper.find('.brief__verdict').text()).toBe('All clear.')
    expect(wrapper.find('.brief__lede').text()).toContain('in the last 24h')
    expect(wrapper.find('.brief__calm').exists()).toBe(false)
    expect(wrapper.findAll('.brief-row')).toHaveLength(0)
  })

  it('does not say all clear beside vitals outside their healthy range', () => {
    const wrapper = mountBriefing({ overview: overviewOf({ failure_rate: 0.068, p95: 1480, apdex: 0.99 }) })
    expect(wrapper.find('.brief__verdict').text()).toBe('Nothing is broken.')
    const lede = wrapper.find('.brief__lede').text()
    expect(lede).toContain('No monitors failing')
    expect(lede).toContain('Outside the healthy range: error rate 6.8%, p95 1480ms.')
    expect(wrapper.find('.brief__degraded--danger').text()).toBe('6.8%')
    // Slow is degraded, not broken.
    expect(wrapper.find('.brief__degraded--warning').text()).toBe('1480ms')
  })

  it('says all clear when the vitals are in range or too few to judge', () => {
    expect(mountBriefing({ overview: overviewOf() }).find('.brief__verdict').text()).toBe('All clear.')
    expect(mountBriefing({ overview: overviewOf({ requests: 2, failure_rate: 0.5 }) }).find('.brief__verdict').text()).toBe('All clear.')
  })

  it('keeps the calm line under the briefing when something needs attention', () => {
    const wrapper = mountBriefing({
      openCount: 12,
      uptime: [uptimeMonitor(), uptimeMonitor({ id: 'u2', state: 'up', went_down_at: null })],
    })
    expect(wrapper.find('.brief__calm').text()).toContain('Everything else is steady: 12 open issues in total, 1 monitor healthy.')
  })

  it('does not call everything else steady while vitals are out of range', () => {
    const wrapper = mountBriefing({ openCount: 12, uptime: [uptimeMonitor()], overview: overviewOf({ failure_rate: 0.1 }) })
    expect(wrapper.find('.brief__calm').text()).toContain('12 open issues in total')
    expect(wrapper.find('.brief__calm').text()).not.toContain('steady')
  })

  it('leads with broken monitors and then lists regressed before new issues', () => {
    const wrapper = mountBriefing({
      uptime: [uptimeMonitor()],
      cron: [{ id: 'c1', project_id: 'p1', name: 'Digest', status: 'active', state: 'error', last_ok_at: minutesAgo(600), last_checkin_at: minutesAgo(5), next_expected_at: null, recent_checkins: [{ status: 'error', received_at: minutesAgo(5) }] }],
      attention: page({
        regressed: [attentionIssue('r1', { status: 'regressed', regressed_at: minutesAgo(10), assignee: 'Ada', user_count: 2 })],
        regressed_total: 1,
        new: [attentionIssue('n1')],
        new_total: 4,
      }),
    })
    expect(wrapper.find('.brief__verdict').text().replace(/\u00a0/g, ' ')).toBe('2 things are broken. 5 issues are new or regressed.')
    const rows = wrapper.findAll('.brief-row')
    expect(rows.map(r => r.find('.brief-chip').text())).toEqual(['Uptime', 'Cron', 'Regressed', 'New'])
    expect(rows[0].text()).toContain('Gateway is down')
    expect(rows[0].text()).toContain('HTTP 503 · 4 failed checks in a row')
    expect(rows[0].find('.brief-row__age').text()).toBe('1h 5m')
    expect(rows[1].text()).toContain('Digest failed its last run')
    expect(rows[2].find('.brief-row__type').text()).toBe('TypeError')
    expect(rows[2].text()).toContain('first seen in 1.5.0 · 2 users · Ada')
    expect(rows[3].text()).toContain('3 events')
    // Each link opens the issue list filtered to exactly the hidden group.
    const more = wrapper.findAll('.brief__more')
    expect(more.map(m => m.text())).toEqual(['3 more new'])
    expect(more[0].attributes('data-to')).toContain('status=New')
    expect(rows[2].attributes('data-to')).toContain('/issues/r1')
  })

  it('marks rows and summarises what changed since the last visit', () => {
    const wrapper = mountBriefing({
      lastVisit: Date.now() - 20 * 60_000,
      uptime: [uptimeMonitor({ went_down_at: minutesAgo(10) })],
      attention: page({
        new: [attentionIssue('old', { first_seen: minutesAgo(40) }), attentionIssue('fresh', { first_seen: minutesAgo(5) })],
        new_total: 9,
        unseen_new: 6,
        unseen_regressed: 0,
      }),
    })
    expect(wrapper.find('.brief__lede').text()).toBe('Since you last looked 2m ago: 1 monitor started failing and 6 new issues appeared.')
    const unseen = wrapper.findAll('.brief-row--unseen').map(r => r.find('.brief-row__title').text())
    expect(unseen).toHaveLength(2)
    expect(unseen[0]).toContain('Gateway is down')
    expect(unseen[1]).toContain('broken fresh')
  })

  it('never claims all clear when issues failed to load', () => {
    const wrapper = mountBriefing({ attentionMissing: true, attentionError: true })
    expect(wrapper.find('.brief__verdict').text()).toBe('No monitors are failing.')
    expect(wrapper.text()).toContain("We couldn't load new and regressed issues.")
  })

  it('never claims all clear when monitors failed to load', () => {
    const wrapper = mountBriefing({ monitorsError: true })
    expect(wrapper.find('.brief__verdict').text()).toBe('No new or regressed issues.')
    expect(wrapper.text()).toContain("We couldn't load monitors.")
  })

  it('never says nothing is new since the last visit when monitors failed to load', () => {
    const wrapper = mountBriefing({ monitorsError: true, lastVisit: Date.now() - 20 * 60_000 })
    expect(wrapper.find('.brief__lede').exists()).toBe(false)
    const withIssues = mountBriefing({
      monitorsError: true,
      lastVisit: Date.now() - 20 * 60_000,
      attention: page({ new: [attentionIssue('n')], new_total: 1, unseen_new: 1 }),
    })
    expect(withIssues.find('.brief__lede').text()).toContain('1 new issue appeared')
  })

  it('sends the last visit and window with the attention request', async () => {
    const visit = Date.UTC(2026, 9, 31, 8)
    mountBriefing({ lastVisit: visit })
    const options = vi.mocked(useQuery).mock.calls.map(c => c[0] as any).find(o => toValue(o.queryKey)[1] === 'attention')
    expect(toValue(options.queryKey)).toContain(visit)
    vi.mocked(apiFetch).mockResolvedValueOnce(page())
    await options.queryFn({ signal: new AbortController().signal })
    const url = String(vi.mocked(apiFetch).mock.calls[0][0])
    expect(url).toContain('/api/issues/attention?')
    expect(url).toContain(`last_visit=${encodeURIComponent(new Date(visit).toISOString())}`)
  })
})

describe('ProjectPerformanceWidget', () => {
  const twoProjects = [{ id: 'p1', name: 'Frontend' }, { id: 'p2', name: 'Backend' }]
  const threeProjects = [...twoProjects, { id: 'p3', name: 'Admin' }]

  function mountTable({
    projects = threeProjects,
    selectedIds = [] as string[],
    overview = overviewOf({}, []) as PerformanceOverview | undefined,
    counts = [] as { project_id: string; open_issues: number }[],
    uptime = [] as unknown[],
    cron = [] as unknown[],
    monitorsError = false,
    loading = false,
  } = {}) {
    vi.mocked(useProjectsStore).mockReturnValue({ selectedIds, projects } as any)
    mockQueries({
      'projects/stats': { data: ref(counts), isFetching: ref(false) },
      'uptime-monitors': { data: ref(monitorsError ? undefined : uptime), isError: ref(monitorsError) },
      'monitors': { data: ref(cron), isError: ref(false) },
    })
    return mount(ProjectPerformanceWidget, { props: { loading, overview }, global: { stubs } })
  }
  const names = (wrapper: ReturnType<typeof mountTable>) => wrapper.findAll('.ptable__project-name').map(n => n.text())
  const down = (id: string, project_id: string) => ({ id, project_id, name: id, status: 'active', state: 'down', went_down_at: null, last_ok_at: null, recent_checks: [] })
  const up = (id: string, project_id: string) => ({ id, project_id, name: id, status: 'active', state: 'up', recent_checks: [] })

  beforeEach(() => { localStorage.clear() })

  it('needs two projects in view, and keeps its query off with fewer than two', () => {
    const solo = mountTable({ projects: [{ id: 'p1', name: 'Solo' }] })
    expect(solo.find('.ptable').exists()).toBe(false)
    const options = vi.mocked(useQuery).mock.calls.map(c => c[0] as any).find(o => toValue(o.queryKey)[0] === 'projects')
    expect(toValue(options.enabled)).toBe(false)
    // One selected project of several would only repeat the vitals beside it.
    expect(mountTable({ selectedIds: ['p1'] }).find('.ptable').exists()).toBe(false)
    expect(names(mountTable({ selectedIds: ['p1', 'p2'] }))).toEqual(['Backend', 'Frontend'])
  })

  it('shows each project\'s performance as fractions read in percent', () => {
    const wrapper = mountTable({
      projects: twoProjects,
      overview: overviewOf({}, [perf({ project_id: 'p1', requests: 2500, p50: 120, p95: 480, prev_p95: 480, apdex: 0.91, failure_rate: 0.068, prev_failure_rate: 0.068, trend: [1, 2] })]),
      counts: [{ project_id: 'p1', open_issues: 7 }],
    })
    const row = wrapper.findAll('.ptable__row')[0]
    const cells = row.findAll('td').map(c => c.text())
    // trace, requests, p50, p95, errors, apdex, open, monitors
    expect(cells.slice(1)).toEqual(['2.5k', '120ms', '480ms', '6.8%', '0.91', '7', '–'])
    expect(row.find('.ptable__val--danger').text()).toBe('6.8%')
    expect(row.find('.ptable__val--warning').text()).toBe('0.91')
  })

  it('shows a dash rather than zeroes for a project without requests', () => {
    const wrapper = mountTable({ projects: twoProjects, overview: overviewOf({}, [perf({ project_id: 'p2', requests: 0, prev_requests: 40 })]) })
    const rows = wrapper.findAll('.ptable__row')
    for (const row of rows) {
      expect(row.findAll('td').slice(1, 6).map(c => c.text())).toEqual(['–', '–', '–', '–', '–'])
    }
  })

  it('never colours a project on a handful of requests', () => {
    const wrapper = mountTable({ projects: twoProjects, overview: overviewOf({}, [perf({ project_id: 'p1', requests: 3, failure_rate: 0.33, apdex: 0.1 })]) })
    expect(wrapper.find('.ptable__val--danger').exists()).toBe(false)
    expect(wrapper.find('.ptable__val--warning').exists()).toBe(false)
  })

  it('marks p95 and error rate that rose, naming the value they rose from', () => {
    const wrapper = mountTable({
      projects: twoProjects,
      overview: overviewOf({}, [perf({ project_id: 'p1', p95: 900, prev_p95: 300, failure_rate: 0.08, prev_failure_rate: 0.01 })]),
    })
    const marks = wrapper.findAll('.ptable__rose').map(m => m.text())
    expect(marks).toEqual(['Up from 300ms:', 'Up from 1.0%:'])
  })

  it('leads with failing monitors, then error rate, then open issues', () => {
    const wrapper = mountTable({
      overview: overviewOf({}, [perf({ project_id: 'p1', failure_rate: 0.04 }), perf({ project_id: 'p3', failure_rate: 0.09 })]),
      uptime: [down('u', 'p2')],
    })
    expect(names(wrapper)).toEqual(['Backend', 'Admin', 'Frontend'])
    expect(wrapper.findAll('.ptable__row')[0].text()).toContain('1 down')
    expect(wrapper.findAll('.ptable__row')[0].find('.ptable__dot--bad').exists()).toBe(true)
  })

  it('counts healthy monitors per project', () => {
    const wrapper = mountTable({ projects: twoProjects, uptime: [up('a', 'p1'), up('b', 'p1')] })
    expect(wrapper.findAll('.ptable__row').find(r => r.text().includes('Frontend'))!.text()).toContain('2 up')
  })

  it('sorts by a column, flips on a second click and remembers the choice', async () => {
    const overview = overviewOf({}, [perf({ project_id: 'p1', p95: 100 }), perf({ project_id: 'p2', p95: 900 })])
    const wrapper = mountTable({ overview })
    const button = wrapper.findAll('thead .col-sort').find(b => b.text().includes('p95'))!
    await button.trigger('click')
    // A project without traffic stays last whichever way the column sorts.
    expect(names(wrapper)).toEqual(['Backend', 'Frontend', 'Admin'])
    expect(wrapper.findAll('thead th').find(h => h.text().includes('p95'))!.attributes('aria-sort')).toBe('descending')
    await button.trigger('click')
    expect(names(wrapper)).toEqual(['Frontend', 'Backend', 'Admin'])
    expect(localStorage.getItem('tindra:dash:proj-sort')).toBe('p95')
    expect(names(mountTable({ overview }))).toEqual(['Frontend', 'Backend', 'Admin'])
  })

  it('ignores a stored sort column it does not know', () => {
    localStorage.setItem('tindra:dash:proj-sort', 'reqPerDay')
    const wrapper = mountTable({ projects: twoProjects, counts: [{ project_id: 'p2', open_issues: 3 }] })
    expect(names(wrapper)).toEqual(['Backend', 'Frontend'])
  })

  it('shows health as unknown rather than healthy when monitors failed to load', () => {
    const wrapper = mountTable({ projects: twoProjects, monitorsError: true })
    expect(wrapper.findAll('.ptable__dot--unknown')).toHaveLength(2)
    expect(wrapper.find('.ptable__dot--ok').exists()).toBe(false)
  })

  it('folds long project lists behind a toggle', async () => {
    const projects = Array.from({ length: 10 }, (_, i) => ({ id: `p${i}`, name: `Project ${i}` }))
    const wrapper = mountTable({ projects })
    expect(wrapper.findAll('.ptable__row')).toHaveLength(8)
    await wrapper.find('.ptable__expand').trigger('click')
    expect(wrapper.findAll('.ptable__row')).toHaveLength(10)
    expect(wrapper.find('.ptable__expand').text()).toBe('Show less')
  })

  it('shows skeletons for the figures while they load', () => {
    const wrapper = mountTable({ projects: twoProjects, overview: undefined, loading: true })
    expect(wrapper.findAll('.ptable__skel').length).toBeGreaterThan(0)
  })
})

describe('PerformanceTrendWidget', () => {
  const mountTrend = (data: unknown, isFetching = false) => {
    mockQueries({ 'dash-tx-series': { data: ref(data), isFetching: ref(isFetching) } })
    return mount(PerformanceTrendWidget, { global: { stubs: { ...stubs, TimeseriesChart: { props: ['series'], template: '<div class="chart" :data-series="series.map(s => s.id).join()" />' } } } })
  }

  it('charts requests and latency over the window', () => {
    const wrapper = mountTrend({ bucket_size: 'hour', buckets: [{ time: '2026-11-01T00:00:00Z', count: 4, p50: 10, p95: 20 }] })
    expect(wrapper.findAll('.chart').map(c => c.attributes('data-series'))).toEqual(['count', 'p50,p95'])
  })

  it('says so when there is nothing to chart, and shows skeletons while loading', () => {
    expect(mountTrend({ bucket_size: 'hour', buckets: [] }).text()).toContain('No transaction data in the selected range')
    expect(mountTrend(undefined, true).findAll('.trend__skel')).toHaveLength(2)
  })

  it('asks for the investigation window and environment', async () => {
    mountTrend({ bucket_size: 'hour', buckets: [] })
    const options = vi.mocked(useQuery).mock.calls.map(c => c[0] as any).find(o => toValue(o.queryKey)[0] === 'dash-tx-series')
    await options.queryFn({ signal: new AbortController().signal })
    const url = String(vi.mocked(apiFetch).mock.calls[0][0])
    expect(url).toContain('/api/transactions/timeseries?')
    expect(url).toContain('hours=24')
  })
})

describe('MonitorsOverviewWidget', () => {
  const minutesAgo = (m: number) => new Date(Date.now() - m * 60_000).toISOString()
  const up = (overrides = {}) => ({
    id: 'u1', project_id: 'p1', name: 'Gateway', status: 'active', state: 'up', last_checked_at: minutesAgo(2),
    recent_checks: [{ status: 'up', checked_at: minutesAgo(2) }],
    ...overrides,
  })
  const job = (overrides = {}) => ({
    id: 'c1', project_id: 'p1', name: 'Digest', status: 'active', state: 'ok', last_checkin_at: minutesAgo(5),
    recent_checkins: [{ status: 'ok', received_at: minutesAgo(5) }],
    ...overrides,
  })
  function mountMonitors(uptime: unknown = [], cron: unknown = [], projects = [{ id: 'p1', name: 'Shop' }, { id: 'p2', name: 'Pay' }]) {
    vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [], projects } as any)
    mockQueries({
      'uptime-monitors': { data: ref(uptime), isError: ref(false) },
      'monitors': { data: ref(cron), isError: ref(false) },
    })
    return mount(MonitorsOverviewWidget, { global: { stubs } })
  }

  it('renders nothing without monitors', () => {
    expect(mountMonitors().find('.db-sec').exists()).toBe(false)
  })

  it('lists every active monitor with its recent checks and sums up the rest', () => {
    const wrapper = mountMonitors([up(), up({ id: 'u2', name: 'Paused', status: 'paused' })], [job()])
    const rows = wrapper.findAll('.mon__row')
    expect(rows.map(r => r.find('.mon__name').text())).toEqual(['Gateway', 'Digest'])
    expect(rows[0].find('.mon__meta').text()).toBe('Shop · Uptime')
    expect(rows[1].find('.mon__meta').text()).toBe('Shop · Cron')
    expect(rows[0].find('.mon__age').text()).toBe('2m ago')
    expect(wrapper.find('.mon__summary').text()).toBe('2 up · 1 paused')
    expect(rows[0].find('.check-history__tick--ok').exists()).toBe(true)
  })

  it('leaves the project out when there is only one', () => {
    const wrapper = mountMonitors([up()], [], [{ id: 'p1', name: 'Shop' }])
    expect(wrapper.find('.mon__meta').text()).toBe('Uptime')
  })

  it('says a monitor has not reported instead of showing it as healthy, and sorts it last', () => {
    const wrapper = mountMonitors([up({ id: 'n', name: 'New', state: 'unknown', last_checked_at: null, recent_checks: [] }), up()])
    const rows = wrapper.findAll('.mon__row')
    expect(rows.map(r => r.find('.mon__name').text())).toEqual(['Gateway', 'New'])
    expect(rows[1].find('.mon__age--none').text()).toBe('Not checked yet')
    expect(wrapper.find('.mon__summary').text()).toBe('1 up · 1 not checked yet')
  })

  it('caps the list and links to the rest', () => {
    const many = Array.from({ length: 11 }, (_, i) => up({ id: `u${i}`, name: `M${i}` }))
    const wrapper = mountMonitors(many)
    expect(wrapper.findAll('.mon__row')).toHaveLength(8)
    expect(wrapper.find('.db-sec__link').text()).toContain('3 more')
  })

  it('warns when every monitor is paused', () => {
    const wrapper = mountMonitors([up({ status: 'paused' })])
    expect(wrapper.find('.mon__paused').text()).toContain('nothing is being checked')
    expect(wrapper.find('.mon').exists()).toBe(false)
  })

  it('treats a response that is not a list as empty', () => {
    expect(mountMonitors({ error: 'nope' }, null).find('.db-sec').exists()).toBe(false)
  })
})

describe('BusiestIssuesWidget', () => {
  it('renders nothing without issues', () => {
    const wrapper = mount(BusiestIssuesWidget, { props: { issues: [], openCount: 0 }, global: { stubs } })
    expect(wrapper.find('.db-sec').exists()).toBe(false)
  })

  it('lists issues with their event counts and links to all open issues', () => {
    const wrapper = mount(BusiestIssuesWidget, {
      props: { issues: [issue('a', 1200), { ...issue('b', 3), title: 'TypeError: nope' }], openCount: 27 },
      global: { stubs },
    })
    expect(wrapper.findAll('.busy__count').map(c => c.text())).toEqual(['1.2k', '3'])
    expect(wrapper.find('.busy__type').text()).toBe('TypeError')
    expect(wrapper.find('.db-sec__link').text()).toContain('all 27 open')
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

  it('flags slow rows in amber and keeps red for high failure rates', () => {
    const wrapper = mount(SlowTransactionsWidget, {
      props: { loading: false, txSummaries: [tx({ transaction: '/a', p50: 600, p95: 2500, failure_rate: 0.03 }), tx({ transaction: '/b', p95: 1500, failure_rate: 0.12 })] },
      global: { stubs },
    })
    const rows = wrapper.findAll('.db-tx-row')
    expect(rows[0].find('.db-tx-row__val--warn').text()).toBe('2500ms')
    expect(wrapper.find('.db-tx-row__val--bad').exists()).toBe(false)
    // Error rates follow the performance tables: amber from 1%, red from 5%.
    expect(rows[0].find('.tx-warn').text()).toBe('3.0% errors')
    expect(rows[0].find('.tx-failure').exists()).toBe(false)
    expect(rows[1].find('.db-tx-row__val--warn').exists()).toBe(false)
    expect(rows[1].find('.tx-failure').text()).toBe('12.0% errors')
    expect(rows[0].find('.db-tx-row__meta').text()).toBe('http · p50 600ms · 3.0% errors')
  })

  it('says "no errors" instead of printing a zero rate', () => {
    const wrapper = mount(SlowTransactionsWidget, {
      props: { loading: false, txSummaries: [tx({ transaction: '/a', p50: 600, p95: 2500, failure_rate: 0 })] },
      global: { stubs },
    })
    expect(wrapper.find('.db-tx-row__meta').text()).toBe('http · p50 600ms · no errors')
    expect(wrapper.find('.tx-warn').exists()).toBe(false)
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
    expect(wrapper.find('.db-sec__foot').text()).toBe('6 of 7 rules on, 1 paused.')
  })

  it('says nothing will notify anyone instead of a calm empty state when every rule is paused', () => {
    mockQueries({
      'me': { data: ref({ permissions: { manage_alerts: false } }) },
      'alert-rules': { data: ref([rule('a', null, false), rule('b', null, false)]), isLoading: ref(false) },
    })
    const wrapper = mount(AlertsWidget, { global: { stubs } })
    expect(wrapper.find('.db-empty').exists()).toBe(false)
    expect(wrapper.find('.db-silent').text()).toBe('All 2 rules are paused. Nothing will notify you.')
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
    expect(wrapper.findAll('.db-heatmap__tick').map(t => t.text())).toEqual(['00:00', '06:00', '12:00', '18:00'])
  })
})
