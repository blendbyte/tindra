import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { reactive, ref, toValue } from 'vue'

// Provide a localStorage shim for happy-dom (which requires --localstorage-file)
let lsStore: Record<string, string> = {}
const localStorageMock = {
  getItem: (key: string) => lsStore[key] ?? null,
  setItem: (key: string, val: string) => { lsStore[key] = val },
  removeItem: (key: string) => { delete lsStore[key] },
  clear: () => { lsStore = {} },
  get length() { return Object.keys(lsStore).length },
  key: (i: number) => Object.keys(lsStore)[i] ?? null,
}
Object.defineProperty(window, 'localStorage', { value: localStorageMock, writable: true })

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
  useAuthStore: vi.fn(),
}))

vi.mock('@/composables/useConfig', () => ({
  useConfig: vi.fn(() => ({ dsnFor: vi.fn((key: string, id: string) => `https://${key}@tindra.test/${id}`), baseUrl: ref('') })),
}))

vi.mock('@/composables/useToast', () => ({
  useToast: vi.fn(() => ({ show: vi.fn() })),
}))

import { apiFetch } from '@/api/client'
import DashboardView from '../DashboardView.vue'
import ProjectSetup from '@/components/ProjectSetup.vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { useAuthStore } from '@/stores/auth'

const stubs = {
  ProjectSetup: true,
  RouterLink: { template: '<a><slot /></a>' },
  Icon: { template: '<span />' },
  Sparkline: { template: '<span />' },
  BrandMark: { template: '<span />' },
}

// The view and its widgets each own some queries, so results are resolved by
// query key rather than call order. An unknown key fails loudly.
function queryName(options: any): string {
  const [head, sub] = toValue(options.queryKey) as unknown[]
  if (head === 'issues' && sub === 'attention') return 'issues/attention'
  return head === 'projects' ? `projects/${String(sub)}` : String(head)
}

// Returns the options of the first useQuery call with the given name.
function queryOptions(name: string): any {
  return vi.mocked(useQuery).mock.calls.map(c => c[0]).find(o => queryName(o) === name)
}

function setupQueries({
  manageAlerts = false,
  issues = undefined as unknown,
  issuesFetching = false,
  txSummaries = undefined as unknown,
  txFetching = false,
  txCounts = undefined as unknown,
  releases = undefined as unknown,
  alertRules = [] as unknown[],
  alertsLoading = false,
  uptimeMonitors = [] as unknown[],
  cronMonitors = [] as unknown[],
  projects = [{ id: 'p1', name: 'Default', slug: 'default', public_key: 'key1', event_count: 1 }] as Array<{ id: string; name: string; slug: string; public_key?: string; event_count?: number }>,
  projectSetup = projects.map(p => ({ id: p.id, setup_complete: true })) as { id: string; setup_complete: boolean }[] | null,
  projectIssueCounts = [] as Array<{ project_id: string; open_issues: number }>,
  projStatsFetching = false,
  attention = { regressed: [], regressed_total: 0, new: [], new_total: 0 } as unknown,
} = {}) {
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [], projects } as any)
  const results: Record<string, unknown> = {
    'me': { data: ref({ permissions: { manage_alerts: manageAlerts } }) },
    'projects/setup': { data: ref(projectSetup ?? undefined), refetch: vi.fn() },
    'issues': { data: ref(issues), isFetching: ref(issuesFetching) },
    'dash-tx': { data: ref(txSummaries), isFetching: ref(txFetching) },
    'dash-tx-counts': { data: ref(txCounts) },
    'releases': { data: ref(releases), isFetching: ref(false) },
    'alert-rules': { data: ref(alertRules), isLoading: ref(alertsLoading) },
    'issues/attention': { data: ref(attention), isFetching: ref(false), isError: ref(false), fetchStatus: ref('idle'), dataUpdatedAt: ref(0), refetch: vi.fn() },
    'uptime-monitors': { data: ref(uptimeMonitors), isFetching: ref(false), isError: ref(false), refetch: vi.fn() },
    'monitors': { data: ref(cronMonitors), isFetching: ref(false), isError: ref(false), refetch: vi.fn() },
    'projects/stats': { data: ref(projectIssueCounts), isFetching: ref(projStatsFetching) },
  }
  vi.mocked(useQuery).mockImplementation(((options: any) => {
    const name = queryName(options)
    if (!(name in results)) throw new Error(`unexpected query ${name}`)
    return results[name]
  }) as any)
}

function makeWrapper(options?: Parameters<typeof setupQueries>[0]) {
  setupQueries(options)
  return mount(DashboardView, { global: { stubs } })
}

beforeEach(() => {
  vi.mocked(useQuery).mockReset()
  vi.mocked(useProjectsStore).mockReset()
  vi.mocked(useAuthStore).mockReset()
  vi.mocked(useAuthStore).mockReturnValue({ user: { timezone: 'UTC' }, setUser: vi.fn() } as any)
  pushMock.mockReset()
  sessionStorage.clear()
  localStorage.clear()
})

afterEach(() => {
  localStorage.clear()
})

describe('DashboardView', () => {
  it('requests the lightweight issue overview', async () => {
    setupQueries()
    const wrapper = mount(DashboardView, { global: { stubs } })
    const query = queryOptions('issues')
    expect(query.queryKey.value).toEqual(['issues', 'overview', ''])
    await query.queryFn({ signal: new AbortController().signal })
    expect(apiFetch).toHaveBeenCalledWith('/api/issues/overview?', { signal: expect.any(AbortSignal) })
    wrapper.unmount()
  })
  it('requests only recent release health for the dashboard', async () => {
    setupQueries()
    const wrapper = mount(DashboardView, { global: { stubs } })
    const query = queryOptions('releases')
    expect(query.queryKey.value).toEqual(['releases', 'health', ''])
    await query.queryFn({ signal: new AbortController().signal })
    expect(apiFetch).toHaveBeenCalledWith('/api/releases/health?', { signal: expect.any(AbortSignal) })
    wrapper.unmount()
  })
  it('requests count-only buckets for the weekly heatmap', async () => {
    setupQueries()
    const wrapper = mount(DashboardView, { global: { stubs } })
    const query = queryOptions('dash-tx-counts')
    expect(query.queryKey.value[0]).toBe('dash-tx-counts')
    await query.queryFn({ signal: new AbortController().signal })
    expect(apiFetch).toHaveBeenCalledWith('/api/transactions/counts?hours=168', { signal: expect.any(AbortSignal) })
    wrapper.unmount()
  })
  describe('section headings', () => {
    it('renders every section title', () => {
      const wrapper = makeWrapper()
      const text = wrapper.text()
      expect(text).toContain('Vitals · 24h')
      expect(text).toContain('Transaction density')
      expect(text).toContain('Slowest transactions')
      expect(text).toContain('Recent alerts')
      expect(text).toContain('Release health')
    })
  })

  describe('Recent Alerts - configure link', () => {
    it('hides the configure link when the user lacks manage_alerts', () => {
      const wrapper = makeWrapper({ manageAlerts: false })
      expect(wrapper.text()).not.toContain('configure')
    })

    it('shows the configure link when the user has manage_alerts', () => {
      const wrapper = makeWrapper({ manageAlerts: true })
      expect(wrapper.text()).toContain('configure')
    })
  })

  describe('empty states', () => {
    it('shows the no-fired-alerts state when enabled rules have not fired', () => {
      const wrapper = makeWrapper({ alertRules: [{ id: 'a1', name: 'Rule 1', enabled: true, last_fired_at: null, channel: 'email' }] })
      expect(wrapper.text()).toContain('No alerts fired recently')
    })

    it('shows the no-releases state when the releases query returns no data', () => {
      const wrapper = makeWrapper()
      expect(wrapper.text()).toContain('No releases yet')
    })

    it('shows no transaction data state when tx summaries are empty', () => {
      const wrapper = makeWrapper({ txSummaries: [] })
      expect(wrapper.text()).toContain('No transaction data in the selected range')
    })
  })

  describe('loading skeleton', () => {
    it('shows skeleton rows while issues are fetching', () => {
      const wrapper = makeWrapper({ issuesFetching: true })
      expect(wrapper.find('.skel').exists()).toBe(true)
    })

    it('shows skeleton rows while transactions are fetching', () => {
      const wrapper = makeWrapper({ txFetching: true })
      expect(wrapper.find('.skel').exists()).toBe(true)
    })
  })

  describe('populated data', () => {
    it('shows issue titles when issues are loaded', () => {
      const issues = {
        issues: [{
          id: 'i1',
          title: 'TypeError: broken thing',
          level: 'error',
          status: 'open',
          event_count: 42,
          sparkline: [],
          project_id: 'p1',
          user_count: 1,
          last_seen: '2024-01-01T00:00:00Z',
          first_seen: '2024-01-01T00:00:00Z',
          kind: 'error',
        }],
        total: 1,
        has_more: false,
      }
      const wrapper = makeWrapper({ issues })
      expect(wrapper.text()).toContain('TypeError: broken thing')
    })

    it('shows transaction names when tx summaries are loaded', () => {
      const txSummaries = [{
        transaction: '/api/users',
        op: 'http.server',
        project_id: 'p1',
        sample_count: 50,
        tpm: 2.5,
        p50: 120,
        p95: 800,
        failure_rate: 1.5,
        time_spent_ms: 5000,
      }]
      const wrapper = makeWrapper({ txSummaries })
      expect(wrapper.text()).toContain('/api/users')
    })

    it('shows releases when release data is loaded', () => {
      const releases = {
        releases: [{
          id: 'r1',
          version: 'v2.0.0',
          project_id: 'p1',
          deployed_at: '2024-01-01T00:00:00Z',
          tx_count: 100,
          tx_p50: 120,
          tx_p95: 450,
          tx_error_rate: 0,
          new_issues: 0,
          regressed_issues: 0,
        }],
        total: 1,
        has_more: false,
      }
      const wrapper = makeWrapper({ releases })
      expect(wrapper.text()).toContain('v2.0.0')
    })

    it('shows fired alert name when alert rule has last_fired_at', () => {
      const alertRules = [{
        id: 'a1',
        name: 'High Error Rate',
        enabled: true,
        last_fired_at: '2024-01-01T00:00:00Z',
        channel: 'email',
        project_ids: [],
        trigger: 'error_rate_spike',
        threshold: 5,
      }]
      const wrapper = makeWrapper({ alertRules })
      expect(wrapper.text()).toContain('High Error Rate')
    })

  })

  describe('active alert rules count', () => {
    it('says nothing will notify anyone when there are no alert rules', () => {
      const wrapper = makeWrapper({ alertRules: [] })
      expect(wrapper.text()).toContain('No alert rules yet. Nothing will notify you.')
      expect(wrapper.text()).not.toContain('No alerts fired recently')
    })

    it('shows correct count of enabled rules', () => {
      const alertRules = [
        { id: 'a1', name: 'Rule 1', enabled: true, last_fired_at: null, channel: 'email' },
        { id: 'a2', name: 'Rule 2', enabled: false, last_fired_at: null, channel: 'slack' },
      ]
      const wrapper = makeWrapper({ alertRules })
      expect(wrapper.text()).toContain('1 of 2 rules on, 1 paused.')
    })
  })

  describe('alert loading skeleton', () => {
    it('shows alert skeleton rows when alerts are loading', () => {
      const wrapper = makeWrapper({ alertsLoading: true })
      expect(wrapper.find('.db-alert-row .skel').exists()).toBe(true)
    })
  })

  describe('release row details', () => {
    const makeRelease = (overrides = {}) => ({
      id: 'r1',
      version: 'v1.0.0',
      project_id: 'p1',
      deployed_at: '2024-01-01T00:00:00Z',
      tx_count: 100,
      tx_p50: 120,
      tx_p95: 450,
      tx_error_rate: 0,
      new_issues: 0,
      regressed_issues: 0,
      ...overrides,
    })

    it('applies bad dot class when release has regressed issues', () => {
      const releases = { releases: [makeRelease({ regressed_issues: 2 })], total: 1, has_more: false }
      const wrapper = makeWrapper({ releases })
      expect(wrapper.find('.db-release-row__dot--bad').exists()).toBe(true)
    })

    it('applies ok dot class when release has no issues', () => {
      const releases = { releases: [makeRelease()], total: 1, has_more: false }
      const wrapper = makeWrapper({ releases })
      expect(wrapper.find('.db-release-row__dot--ok').exists()).toBe(true)
    })

    it('shows new pill when release has new issues', () => {
      const releases = { releases: [makeRelease({ new_issues: 3 })], total: 1, has_more: false }
      const wrapper = makeWrapper({ releases })
      expect(wrapper.find('.rel-pill--new').text()).toContain('new')
    })

    it('marks a release with regressions in amber, not red', () => {
      const releases = { releases: [makeRelease({ regressed_issues: 2, new_issues: 1 })], total: 1, has_more: false }
      const wrapper = makeWrapper({ releases })
      expect(wrapper.find('.rel-pill--regressed').text()).toContain('2 regressed')
      expect(wrapper.find('.rel-pill--new').exists()).toBe(false)
    })

    it('navigates to release detail when release row is clicked', async () => {
      const releases = { releases: [makeRelease()], total: 1, has_more: false }
      const wrapper = makeWrapper({ releases })
      await wrapper.find('.db-release-row').trigger('click')
      expect(pushMock).toHaveBeenCalledWith('/releases/r1')
    })
  })

  describe('transaction density with heatmap data', () => {
    it('shows transaction density section with heatmap when txTs data is present', () => {
      const now = new Date()
      const bucket = {
        time: new Date(now.getTime() - 3_600_000).toISOString(),
        count: 1200,
      }
      setupQueries({
        projects: [{ id: 'p1', name: 'App', slug: 'app', event_count: 1 }],
        txCounts: { buckets: [bucket] },
      })
      const wrapper = mount(DashboardView, { global: { stubs } })
      expect(wrapper.text()).toContain('Transaction density')
      expect(wrapper.find('.db-heatmap__cell[data-tip*="1.2k events"]').exists()).toBe(true)
    })
  })

  describe('briefing', () => {
    it('says all clear when nothing is failing, new or regressed', () => {
      const wrapper = makeWrapper()
      expect(wrapper.find('.brief__verdict').text()).toBe('All clear.')
    })

    it('puts a failing monitor in the verdict and the broken group', () => {
      const wrapper = makeWrapper({
        uptimeMonitors: [{ id: 'u1', project_id: 'p1', name: 'API', status: 'active', state: 'down', went_down_at: new Date(Date.now() - 5 * 60_000).toISOString(), last_ok_at: null, last_status_code: 502, last_error: null, consecutive_failures: 3, recent_checks: [] }],
      })
      expect(wrapper.find('.brief__verdict').text().replace(/\u00a0/g, ' ')).toBe('1 thing is broken. No new or regressed issues.')
      expect(wrapper.find('#brief-broken').text()).toContain('Broken now')
      expect(wrapper.find('.brief-row').text()).toContain('API is down')
    })

    it('records this visit so the next one can show what changed', () => {
      makeWrapper()
      expect(JSON.parse(localStorage.getItem('tindra:dashboard:last-visit')!).seen).toBeGreaterThan(0)
    })
  })

  describe('widget layout', () => {
    it('leads with the briefing and keeps health and performance context beside and below it', () => {
      const wrapper = makeWrapper({
        projects: [{ id: 'p1', name: 'A', slug: 'a' }, { id: 'p2', name: 'B', slug: 'b' }],
      })
      const titles = (column: string) => wrapper.findAll(`${column} .db-sec__title`).map(t => t.text())
      expect(wrapper.find('.db-main > .brief').exists()).toBe(true)
      expect(titles('.db-tail')).toEqual(['Slowest transactions', 'Transaction density · last 7 days'])
      expect(titles('.db-aside')).toEqual(['Vitals · 24h', 'Release health', 'Projects', 'Recent alerts'])
    })

    it('fetches widget data only once the dashboard itself is shown', () => {
      makeWrapper({ projectSetup: [{ id: 'p1', setup_complete: false }] })
      expect(queryOptions('projects/setup')).toBeDefined()
      expect(queryOptions('issues')).toBeDefined()
      expect(queryOptions('alert-rules')).toBeUndefined()
      expect(queryOptions('dash-tx-counts')).toBeUndefined()
    })
  })

  describe('first-run / empty state', () => {
    it('shows no-projects card when projects list is empty and data has loaded', () => {
      const wrapper = makeWrapper({
        projects: [],
        issues: { issues: [], total: 0, has_more: false },
        txSummaries: [],
        releases: { releases: [], total: 0, has_more: false },
      })
      expect(wrapper.text()).toContain('No projects yet')
      expect(wrapper.find('.brief').exists()).toBe(false)
    })

    it('navigates to create project when button is clicked', async () => {
      const wrapper = makeWrapper({
        projects: [],
        issues: { issues: [], total: 0, has_more: false },
        txSummaries: [],
        releases: { releases: [], total: 0, has_more: false },
      })
      await wrapper.find('.btn--primary').trigger('click')
      expect(pushMock).toHaveBeenCalledWith('/settings/projects?new=1')
    })

    it('opens setup only when durable status says no data has arrived', () => {
      const wrapper = makeWrapper({ projectSetup: [{ id: 'p1', setup_complete: false }] })
      expect(wrapper.findComponent(ProjectSetup).exists()).toBe(true)
      expect(wrapper.find('.brief').exists()).toBe(false)
    })

    it('does not show onboarding while setup status is unavailable', () => {
      const wrapper = makeWrapper({ projectSetup: null })
      expect(wrapper.findComponent(ProjectSetup).exists()).toBe(false)
      expect(wrapper.find('.brief').exists()).toBe(true)
    })

    it('keeps the dashboard for a connected project with zero monthly events', () => {
      const wrapper = makeWrapper({
        projects: [{ id: 'p1', name: 'Quiet', slug: 'quiet', public_key: 'key1', event_count: 0 }],
        projectSetup: [{ id: 'p1', setup_complete: true }],
      })
      expect(wrapper.find('.brief').exists()).toBe(true)
      expect(wrapper.findComponent(ProjectSetup).exists()).toBe(false)
    })

    it('passes the explicit project to setup and lets users continue', async () => {
      const wrapper = makeWrapper({ projectSetup: [{ id: 'p1', setup_complete: false }] })
      const setup = wrapper.findComponent(ProjectSetup)
      expect(setup.props('project').id).toBe('p1')
      setup.vm.$emit('continue')
      await wrapper.vm.$nextTick()
      expect(wrapper.find('.brief').exists()).toBe(true)
    })

    it('uses a selected project even when another project is connected', () => {
      setupQueries({
        projects: [{ id: 'p1', name: 'Active', slug: 'active' }, { id: 'p2', name: 'New', slug: 'new' }],
        projectSetup: [{ id: 'p1', setup_complete: true }, { id: 'p2', setup_complete: false }],
      })
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: ['p2'], projects: [{ id: 'p1', name: 'Active', slug: 'active' }, { id: 'p2', name: 'New', slug: 'new' }] } as any)
      const wrapper = mount(DashboardView, { global: { stubs } })
      expect(wrapper.findComponent(ProjectSetup).props('project').id).toBe('p2')
    })

    it('respects setup dismissed elsewhere in this tab', () => {
      sessionStorage.setItem('tindra:setup-dismissed:p1', '1')
      const wrapper = makeWrapper({ projectSetup: [{ id: 'p1', setup_complete: false }] })
      expect(wrapper.findComponent(ProjectSetup).exists()).toBe(false)
    })

    it('requests setup milestones instead of monthly usage', async () => {
      const wrapper = makeWrapper()
      const query = queryOptions('projects/setup')
      await query.queryFn({ signal: new AbortController().signal })
      expect(apiFetch).toHaveBeenCalledWith('/api/setup-status', { signal: expect.any(AbortSignal) })
      wrapper.unmount()
    })
  })
})

it('reopens setup for another project after continuing when session storage is blocked', async () => {
 const projects = [{ id: 'p1', name: 'First', slug: 'first' }, { id: 'p2', name: 'Second', slug: 'second' }]
 setupQueries({ projects, projectSetup: projects.map(p => ({ id: p.id, setup_complete: false })) })
 const store = reactive({ projects, selectedIds: ['p1'] })
 vi.mocked(useProjectsStore).mockReturnValue(store as any)
 const read = vi.fn().mockImplementation(() => { throw new Error('blocked') })
 vi.stubGlobal('sessionStorage', { getItem: read })
 const wrapper = mount(DashboardView, { global: { stubs } })
 try {
  expect(read).toHaveBeenCalled()
  wrapper.findComponent(ProjectSetup).vm.$emit('continue')
  await wrapper.vm.$nextTick()
  expect(wrapper.findComponent(ProjectSetup).exists()).toBe(false)
  store.selectedIds = ['p2']
  await wrapper.vm.$nextTick()
  expect(wrapper.findComponent(ProjectSetup).props('project').id).toBe('p2')
 } finally { vi.unstubAllGlobals(); wrapper.unmount() }
})
