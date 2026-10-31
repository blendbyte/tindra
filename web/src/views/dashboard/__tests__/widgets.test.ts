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
import VitalsWidget from '../VitalsWidget.vue'
import BriefingWidget from '../BriefingWidget.vue'
import ProjectsWidget from '../ProjectsWidget.vue'
import SlowTransactionsWidget from '../SlowTransactionsWidget.vue'
import ReleasesWidget from '../ReleasesWidget.vue'
import AlertsWidget from '../AlertsWidget.vue'
import TransactionDensityWidget from '../TransactionDensityWidget.vue'
import type { DashboardIssue, DashboardRelease } from '../shared'
import type { AttentionIssue, AttentionIssuesPage } from '../briefing'

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

  it('weights error rate, p95 and apdex by sample count and colours bad values', () => {
    const wrapper = mount(VitalsWidget, {
      props: {
        loading: false,
        txSummaries: [
          tx({ sample_count: 10, failure_rate: 0.02, p95: 100, apdex: 1 }),
          tx({ sample_count: 30, failure_rate: 0.06, p95: 500, apdex: 0.6 }),
        ],
      },
      global: { stubs },
    })
    // error (2*10 + 6*30) / 40 = 5.0, p95 (1000 + 15000) / 40 = 400, apdex (10 + 18) / 40 = 0.70
    const value = (key: string) => wrapper.find(`[data-vital="${key}"] .vitals__value`)
    expect(value('error-rate').text()).toBe('5.0%')
    expect(value('p95').text()).toBe('400ms')
    expect(value('transactions').text()).toBe('40')
    expect(value('apdex').text()).toBe('0.70')
    expect(value('error-rate').find('.vitals__value--warning').exists()).toBe(true)
    expect(value('apdex').find('.vitals__value--warning').exists()).toBe(true)
    expect(value('p95').find('span').classes()).toEqual([])
  })

  it('counts zero transactions for an empty summary list but leaves averages muted', () => {
    const wrapper = mount(VitalsWidget, { props: { loading: false, txSummaries: [] }, global: { stubs } })
    expect(wrapper.findAll('.vitals__value--muted')).toHaveLength(3)
    expect(wrapper.find('[data-vital="transactions"] .vitals__value').text()).toBe('0')
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
    busiest = [] as DashboardIssue[],
    openCount = 0,
  } = {}) {
    mockQueries({
      'issues': {
        data: ref(attentionMissing ? undefined : attention), isFetching: ref(false), isError: ref(attentionError),
        fetchStatus: ref('idle'), dataUpdatedAt: ref(0), refetch: vi.fn(),
      },
      'uptime-monitors': { data: ref(monitorsError ? undefined : uptime), isFetching: ref(false), isError: ref(monitorsError), refetch: vi.fn() },
      'monitors': { data: ref(cron), isFetching: ref(false), isError: ref(false), refetch: vi.fn() },
    })
    return mount(BriefingWidget, { props: { loading: false, openCount, busiest, lastVisit }, global: { stubs } })
  }

  it('says all clear when nothing is failing, new or regressed', () => {
    const wrapper = mountBriefing({ openCount: 12, uptime: [uptimeMonitor({ state: 'up', went_down_at: null })] })
    expect(wrapper.find('.brief__verdict').text()).toBe('All clear.')
    expect(wrapper.find('.brief__lede').text()).toContain('in the last 24h')
    expect(wrapper.find('.brief__calm').text()).toContain('12 open issues, 1 monitor healthy.')
    expect(wrapper.findAll('.brief-row')).toHaveLength(0)
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

  it('lists the busiest open issues that are not already shown', () => {
    const wrapper = mountBriefing({
      attention: page({ new: [attentionIssue('a')], new_total: 1 }),
      busiest: [issue('a', 9), issue('b', 3), issue('c', 7)],
    })
    const quiet = wrapper.findAll('.brief-row--quiet')
    expect(quiet.map(r => r.find('.brief-row__age').text())).toEqual(['7', '3'])
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

describe('ProjectsWidget', () => {
  it('renders nothing and keeps its query disabled with fewer than two projects', () => {
    vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [], projects: [{ id: 'p1', name: 'Solo' }] } as any)
    mockQueries({
      'projects/stats': { data: ref(undefined), isFetching: ref(false) },
      'uptime-monitors': { data: ref([]), isError: ref(false) },
      'monitors': { data: ref([]), isError: ref(false) },
    })
    const wrapper = mount(ProjectsWidget, { global: { stubs } })
    expect(wrapper.find('.proj').exists()).toBe(false)
    const options = vi.mocked(useQuery).mock.calls.map(c => c[0] as any).find(o => toValue(o.queryKey)[0] === 'projects')
    expect(toValue(options.enabled)).toBe(false)
  })

  it('lists selected projects with failing monitors first', () => {
    vi.mocked(useProjectsStore).mockReturnValue({
      selectedIds: [],
      projects: [{ id: 'p1', name: 'Frontend' }, { id: 'p2', name: 'Backend' }, { id: 'p3', name: 'Admin' }],
    } as any)
    mockQueries({
      'projects/stats': { data: ref([{ project_id: 'p1', open_issues: 9 }, { project_id: 'p3', open_issues: 2 }]), isFetching: ref(false) },
      'uptime-monitors': { data: ref([{ id: 'u', project_id: 'p2', name: 'API', status: 'active', state: 'down', went_down_at: null, last_ok_at: null, recent_checks: [] }]), isError: ref(false) },
      'monitors': { data: ref([]), isError: ref(false) },
    })
    const rows = mount(ProjectsWidget, { global: { stubs } }).findAll('.proj__row')
    expect(rows.map(r => r.find('.proj__name').text())).toEqual(['Backend', 'Frontend', 'Admin'])
    expect(rows[0].text()).toContain('1 monitor failing · 0 open')
    expect(rows[0].find('.proj__dot--bad').exists()).toBe(true)
    expect(rows[1].text()).toContain('9 open')
  })

  it('shows health as unknown rather than healthy when monitors failed to load', () => {
    vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [], projects: [{ id: 'p1', name: 'A' }, { id: 'p2', name: 'B' }] } as any)
    mockQueries({
      'projects/stats': { data: ref([]), isFetching: ref(false) },
      'uptime-monitors': { data: ref(undefined), isError: ref(true) },
      'monitors': { data: ref([]), isError: ref(false) },
    })
    const wrapper = mount(ProjectsWidget, { global: { stubs } })
    expect(wrapper.findAll('.proj__dot--unknown')).toHaveLength(2)
    expect(wrapper.find('.proj__dot--ok').exists()).toBe(false)
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
    expect(rows[0].find('.db-tx-row__pct--warn').text()).toBe('3.0% errors')
    expect(rows[0].find('.db-tx-row__pct--bad').exists()).toBe(false)
    expect(rows[1].find('.db-tx-row__val--warn').exists()).toBe(false)
    expect(rows[1].find('.db-tx-row__pct--bad').text()).toBe('12.0% errors')
    expect(rows[0].find('.db-tx-row__meta').text()).toBe('http · p50 600ms · 3.0% errors')
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
  })
})
