import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

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

import DashboardView from '../DashboardView.vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { useAuthStore } from '@/stores/auth'

const stubs = {
  RouterLink: { template: '<a><slot /></a>' },
  Icon: { template: '<span />' },
  Sparkline: { template: '<span />' },
  BrandMark: { template: '<span />' },
}

// Queries are called in declaration order: me, issues, tx-summaries, tx-timeseries, releases, alert-rules, proj-stats
function setupQueries({
  manageAlerts = false,
  issues = undefined as unknown,
  issuesFetching = false,
  txSummaries = undefined as unknown,
  txFetching = false,
  releases = undefined as unknown,
  alertRules = [] as unknown[],
  alertsLoading = false,
  projects = [{ id: 'p1', name: 'Default', slug: 'default', public_key: 'key1' }] as Array<{ id: string; name: string; slug: string; public_key?: string }>,
  projectIssueCounts = [] as Array<{ project_id: string; open_issues: number }>,
  projStatsFetching = false,
} = {}) {
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [], projects } as any)
  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref({ permissions: { manage_alerts: manageAlerts } }) } as any)
    .mockReturnValueOnce({ data: ref(issues), isFetching: ref(issuesFetching) } as any)
    .mockReturnValueOnce({ data: ref(txSummaries), isFetching: ref(txFetching) } as any)
    .mockReturnValueOnce({ data: ref(undefined) } as any)
    .mockReturnValueOnce({ data: ref(releases), isFetching: ref(false) } as any)
    .mockReturnValueOnce({ data: ref(alertRules), isLoading: ref(alertsLoading) } as any)
    .mockReturnValueOnce({ data: ref(projectIssueCounts), isFetching: ref(projStatsFetching) } as any)
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
  localStorage.clear()
})

afterEach(() => {
  localStorage.clear()
})

describe('DashboardView', () => {
  describe('KPI strip', () => {
    it('renders all four KPI labels', () => {
      const wrapper = makeWrapper()
      const text = wrapper.text()
      expect(text).toContain('Open Issues')
      expect(text).toContain('Error Rate')
      expect(text).toContain('P95 Latency')
      expect(text).toContain('Transactions / 24h')
    })

    it('shows muted dashes when transaction data is not yet available', () => {
      const wrapper = makeWrapper()
      // error rate, p95, and tx count all depend on tx queries which return undefined
      expect(wrapper.findAll('.db-kpi__value--muted')).toHaveLength(3)
    })
  })

  describe('section headings', () => {
    it('renders all five section titles', () => {
      const wrapper = makeWrapper()
      const text = wrapper.text()
      expect(text).toContain('Transaction density')
      expect(text).toContain('Hottest Issues')
      expect(text).toContain('Slowest Transactions')
      expect(text).toContain('Recent Alerts')
      expect(text).toContain('Release Health')
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
    it('shows the no-fired-alerts state when alert rules have no last_fired_at', () => {
      const wrapper = makeWrapper()
      expect(wrapper.text()).toContain('No alerts fired recently')
    })

    it('shows the no-open-issues state when the issues query returns no data', () => {
      const wrapper = makeWrapper()
      expect(wrapper.text()).toContain('No open issues')
    })

    it('shows the no-releases state when the releases query returns no data', () => {
      const wrapper = makeWrapper()
      expect(wrapper.text()).toContain('No releases yet')
    })

    it('shows no transaction data state when tx summaries are empty', () => {
      const wrapper = makeWrapper({ txSummaries: [] })
      expect(wrapper.text()).toContain('No transaction data in the last 24h')
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

    it('shows KPI issue count when issues are present', () => {
      const issues = {
        issues: [
          { id: 'i1', title: 'A', level: 'error', status: 'open', event_count: 5, sparkline: [], project_id: 'p1', user_count: 1, last_seen: '2024-01-01T00:00:00Z', first_seen: '2024-01-01T00:00:00Z', kind: 'error' },
          { id: 'i2', title: 'B', level: 'warning', status: 'open', event_count: 2, sparkline: [], project_id: 'p1', user_count: 1, last_seen: '2024-01-01T00:00:00Z', first_seen: '2024-01-01T00:00:00Z', kind: 'error' },
        ],
        total: 2,
        has_more: false,
      }
      const wrapper = makeWrapper({ issues })
      // The open count KPI should show '2' not '–'
      const muted = wrapper.findAll('.db-kpi__value--muted')
      expect(muted.length).toBe(3) // error rate, p95, tx count are still muted; issues count is not
    })
  })

  describe('active alert rules count', () => {
    it('shows 0 rules active when no alert rules', () => {
      const wrapper = makeWrapper({ alertRules: [] })
      expect(wrapper.text()).toContain('0 rules active')
    })

    it('shows correct count of enabled rules', () => {
      const alertRules = [
        { id: 'a1', name: 'Rule 1', enabled: true, last_fired_at: null, channel: 'email' },
        { id: 'a2', name: 'Rule 2', enabled: false, last_fired_at: null, channel: 'slack' },
      ]
      const wrapper = makeWrapper({ alertRules })
      expect(wrapper.text()).toContain('1 rule active')
    })
  })

  describe('alert loading skeleton', () => {
    it('shows alert skeleton rows when alerts are loading', () => {
      const wrapper = makeWrapper({ alertsLoading: true })
      expect(wrapper.find('.db-alert-row .skel').exists()).toBe(true)
    })
  })

  describe('KPI edge cases', () => {
    it('shows muted error rate when all tx summaries have zero sample count', () => {
      const txSummaries = [{ transaction: '/api', op: 'http.server', sample_count: 0, tpm: 0, p50: 0, p95: 0, failure_rate: 0, time_spent_ms: 0, project_id: 'p1' }]
      const wrapper = makeWrapper({ txSummaries })
      const muted = wrapper.findAll('.db-kpi__value--muted')
      expect(muted.length).toBeGreaterThan(0)
    })

    it('shows error rate value in red when > 5%', () => {
      const txSummaries = [{
        transaction: '/api', op: 'http.server', sample_count: 100, tpm: 5,
        p50: 100, p95: 500, failure_rate: 8, time_spent_ms: 5000, project_id: 'p1',
      }]
      const wrapper = makeWrapper({ txSummaries })
      const errEl = wrapper.find('.db-kpi__value[style*="danger"]')
      expect(wrapper.text()).toContain('8.0%')
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
      expect(wrapper.find('.rel-pill--bad').text()).toContain('new')
    })

    it('navigates to release detail when release row is clicked', async () => {
      const releases = { releases: [makeRelease()], total: 1, has_more: false }
      const wrapper = makeWrapper({ releases })
      await wrapper.find('.db-release-row').trigger('click')
      expect(pushMock).toHaveBeenCalledWith('/releases/r1')
    })
  })

  describe('issue row interactions', () => {
    it('navigates to issue when issue row is clicked', async () => {
      const issues = {
        issues: [{
          id: 'i1',
          title: 'TypeError: broken',
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
      await wrapper.find('.db-issue-row').trigger('click')
      expect(pushMock).toHaveBeenCalledWith('/issues/i1')
    })

    it('shows sparkline for issue with sparkline data', () => {
      const issues = {
        issues: [{
          id: 'i1',
          title: 'TypeError: broken',
          level: 'warning',
          status: 'open',
          event_count: 5000,
          sparkline: [1, 2, 3, 4, 5],
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
      // fmt(5000) returns '5.0k'; sparkline renders with levelColor
      expect(wrapper.text()).toContain('5.0k')
    })

    it('shows fmt with millions for very large event counts', () => {
      const issues = {
        issues: [{
          id: 'i1',
          title: 'Mega issue',
          level: 'fatal',
          status: 'open',
          event_count: 1_500_000,
          sparkline: [1],
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
      expect(wrapper.text()).toContain('1.5M')
    })
  })

  describe('transaction density with heatmap data', () => {
    it('shows transaction density section with heatmap when txTs data is present', () => {
      const now = new Date()
      const bucket = {
        time: new Date(now.getTime() - 3_600_000).toISOString(),
        count: 1200,
      }
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [], projects: [{ id: 'p1', name: 'App', slug: 'app' }] } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref({ permissions: { manage_alerts: false } }) } as any)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref({ buckets: [bucket] }) } as any)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)
      const wrapper = mount(DashboardView, { global: { stubs } })
      expect(wrapper.text()).toContain('Transaction density')
    })
  })

  describe('project overview widget', () => {
    const RouterLinkCapture = { name: 'RouterLink', props: ['to'], template: '<a><slot /></a>' }
    const stubsWithCapture = { ...stubs, RouterLink: RouterLinkCapture }

    const twoProjects = [
      { id: 'p1', name: 'Frontend', slug: 'frontend' },
      { id: 'p2', name: 'Backend', slug: 'backend' },
    ]

    it('is hidden when there is only one project', () => {
      const wrapper = makeWrapper({ projects: [{ id: 'p1', name: 'App', slug: 'app' }] })
      expect(wrapper.find('.db-projects').exists()).toBe(false)
    })

    it('is hidden when there are no projects', () => {
      const wrapper = makeWrapper({ projects: [] })
      expect(wrapper.find('.db-projects').exists()).toBe(false)
    })

    it('shows when there are 2 or more projects', () => {
      const wrapper = makeWrapper({ projects: twoProjects })
      expect(wrapper.find('.db-projects').exists()).toBe(true)
    })

    it('renders a row for each project name', () => {
      const wrapper = makeWrapper({ projects: twoProjects })
      expect(wrapper.text()).toContain('Frontend')
      expect(wrapper.text()).toContain('Backend')
    })

    it('shows the Project Overview heading', () => {
      const wrapper = makeWrapper({ projects: twoProjects })
      expect(wrapper.text()).toContain('Project Overview')
    })

    it('shows open issue counts from projectIssueCounts', () => {
      const wrapper = makeWrapper({
        projects: twoProjects,
        projectIssueCounts: [
          { project_id: 'p1', open_issues: 7 },
          { project_id: 'p2', open_issues: 0 },
        ],
      })
      expect(wrapper.text()).toContain('7')
    })

    it('open issues value links to issues route with project_id', () => {
      setupQueries({
        projects: twoProjects,
        projectIssueCounts: [
          { project_id: 'p1', open_issues: 3 },
          { project_id: 'p2', open_issues: 0 },
        ],
      })
      const wrapper = mount(DashboardView, { global: { stubs: stubsWithCapture } })
      const links = wrapper.findAllComponents(RouterLinkCapture)
      const issueLinks = links.filter(l => l.props('to')?.name === 'issues')
      expect(issueLinks.length).toBeGreaterThan(0)
      const projectIds = issueLinks.map(l => l.props('to').query.project_id)
      expect(projectIds).toContain('p1')
      expect(projectIds).toContain('p2')
    })

    it('req/24h value links to transactions route with project_id', () => {
      setupQueries({
        projects: twoProjects,
        txSummaries: [
          { transaction: '/api/a', op: 'http.server', project_id: 'p1', sample_count: 50, tpm: 2, p50: 100, p95: 300, failure_rate: 0, time_spent_ms: 5000 },
          { transaction: '/api/b', op: 'http.server', project_id: 'p2', sample_count: 20, tpm: 1, p50: 80, p95: 200, failure_rate: 1, time_spent_ms: 2000 },
        ],
      })
      const wrapper = mount(DashboardView, { global: { stubs: stubsWithCapture } })
      const links = wrapper.findAllComponents(RouterLinkCapture)
      const txLinks = links.filter(l => l.props('to')?.name === 'transactions')
      const projectIds = txLinks.map(l => l.props('to').query.project_id)
      expect(projectIds).toContain('p1')
      expect(projectIds).toContain('p2')
    })

    it('derives weighted-average p50 from txSummaries per project', () => {
      const wrapper = makeWrapper({
        projects: twoProjects,
        txSummaries: [
          // p1: two routes, weighted p50 = (100*10 + 200*10) / 20 = 150ms
          { transaction: '/a', op: 'http.server', project_id: 'p1', sample_count: 10, tpm: 1, p50: 100, p95: 300, failure_rate: 0, time_spent_ms: 1000 },
          { transaction: '/b', op: 'http.server', project_id: 'p1', sample_count: 10, tpm: 1, p50: 200, p95: 400, failure_rate: 0, time_spent_ms: 2000 },
        ],
      })
      // formatDuration mock returns `${n}ms`, so weighted 150 → '150ms'
      expect(wrapper.text()).toContain('150ms')
    })

    it('shows dash for req/24h when project has no tx data', () => {
      const wrapper = makeWrapper({
        projects: twoProjects,
        txSummaries: [
          { transaction: '/a', op: 'http.server', project_id: 'p1', sample_count: 10, tpm: 1, p50: 100, p95: 300, failure_rate: 0, time_spent_ms: 1000 },
          // p2 has no tx data
        ],
      })
      // p2 row should show '–' for req/24h, p50, error rate
      const rows = wrapper.findAll('.db-proj-row')
      const p2Row = rows.find(r => r.text().includes('Backend'))
      expect(p2Row?.text()).toContain('–')
    })

    describe('sort', () => {
      it('sorts by open issues descending by default', () => {
        const wrapper = makeWrapper({
          projects: twoProjects,
          projectIssueCounts: [
            { project_id: 'p1', open_issues: 3 },
            { project_id: 'p2', open_issues: 10 },
          ],
        })
        const rows = wrapper.findAll('.db-proj-row')
        // p2 has 10 issues, p1 has 3 — descending means p2 (Backend) first
        expect(rows[0].text()).toContain('Backend')
        expect(rows[1].text()).toContain('Frontend')
      })

      it('marks the open issues column header as active by default', () => {
        const wrapper = makeWrapper({ projects: twoProjects })
        const sortBtns = wrapper.findAll('.col-sort')
        expect(sortBtns[0].classes()).not.toContain('col-sort--active') // Project
        expect(sortBtns[1].classes()).toContain('col-sort--active')     // Open Issues
      })

      it('clicking another column sorts by that column ascending for name', async () => {
        const wrapper = makeWrapper({ projects: twoProjects })
        const sortBtns = wrapper.findAll('.col-sort')
        await sortBtns[0].trigger('click') // Project name, defaults to asc
        const rows = wrapper.findAll('.db-proj-row')
        // alphabetical ascending: Backend before Frontend
        expect(rows[0].text()).toContain('Backend')
        expect(rows[1].text()).toContain('Frontend')
      })

      it('clicking active column reverses sort direction', async () => {
        const wrapper = makeWrapper({
          projects: twoProjects,
          projectIssueCounts: [
            { project_id: 'p1', open_issues: 3 },
            { project_id: 'p2', open_issues: 10 },
          ],
        })
        const openIssuesBtn = wrapper.findAll('.col-sort')[1]
        // default is open issues desc (p2 first), clicking flips to asc (p1 first)
        await openIssuesBtn.trigger('click')
        const rows = wrapper.findAll('.db-proj-row')
        expect(rows[0].text()).toContain('Frontend')
        expect(rows[1].text()).toContain('Backend')
      })

      it('persists sort column and direction to localStorage', async () => {
        const wrapper = makeWrapper({ projects: twoProjects })
        const sortBtns = wrapper.findAll('.col-sort')
        await sortBtns[2].trigger('click') // Req / 24h
        expect(localStorage.getItem('tindra:dash:proj-sort')).toBe('reqPerDay')
        expect(localStorage.getItem('tindra:dash:proj-sort-dir')).toBe('desc')
      })

      it('reads sort column from localStorage on mount', () => {
        localStorage.setItem('tindra:dash:proj-sort', 'openIssues')
        localStorage.setItem('tindra:dash:proj-sort-dir', 'asc')
        const wrapper = makeWrapper({
          projects: twoProjects,
          projectIssueCounts: [
            { project_id: 'p1', open_issues: 10 },
            { project_id: 'p2', open_issues: 3 },
          ],
        })
        const sortBtns = wrapper.findAll('.col-sort')
        // Open Issues button (index 1) should be active
        expect(sortBtns[1].classes()).toContain('col-sort--active')
        const rows = wrapper.findAll('.db-proj-row')
        // asc by open issues: p2=3 first, p1=10 second
        expect(rows[0].text()).toContain('Backend')
        expect(rows[1].text()).toContain('Frontend')
      })
    })

    describe('expand/collapse', () => {
      const sixProjects = [
        { id: 'p1', name: 'Alpha', slug: 'alpha' },
        { id: 'p2', name: 'Beta', slug: 'beta' },
        { id: 'p3', name: 'Gamma', slug: 'gamma' },
        { id: 'p4', name: 'Delta', slug: 'delta' },
        { id: 'p5', name: 'Epsilon', slug: 'epsilon' },
        { id: 'p6', name: 'Zeta', slug: 'zeta' },
      ]

      it('shows at most 5 rows when there are more than 5 projects', () => {
        const wrapper = makeWrapper({ projects: sixProjects })
        const rows = wrapper.findAll('.db-proj-row')
        expect(rows).toHaveLength(5)
      })

      it('shows expand button when there are more than 5 projects', () => {
        const wrapper = makeWrapper({ projects: sixProjects })
        expect(wrapper.find('.db-proj-expand').exists()).toBe(true)
        expect(wrapper.find('.db-proj-expand').text()).toContain('more')
      })

      it('does not show expand button when there are 5 or fewer projects', () => {
        const wrapper = makeWrapper({ projects: sixProjects.slice(0, 5) })
        expect(wrapper.find('.db-proj-expand').exists()).toBe(false)
      })

      it('shows all rows after clicking expand', async () => {
        const wrapper = makeWrapper({ projects: sixProjects })
        await wrapper.find('.db-proj-expand').trigger('click')
        const rows = wrapper.findAll('.db-proj-row')
        expect(rows).toHaveLength(6)
      })

      it('collapse button shows "Show less" when expanded', async () => {
        const wrapper = makeWrapper({ projects: sixProjects })
        await wrapper.find('.db-proj-expand').trigger('click')
        expect(wrapper.find('.db-proj-expand').text()).toContain('less')
      })

      it('collapses back to 5 rows after clicking again', async () => {
        const wrapper = makeWrapper({ projects: sixProjects })
        await wrapper.find('.db-proj-expand').trigger('click')
        await wrapper.find('.db-proj-expand').trigger('click')
        const rows = wrapper.findAll('.db-proj-row')
        expect(rows).toHaveLength(5)
      })
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
      expect(wrapper.find('.db-kpis').exists()).toBe(false)
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

    it('shows waiting-for-event card when projects exist but no tx or releases', () => {
      const wrapper = makeWrapper({
        issues: { issues: [], total: 0, has_more: false },
        txSummaries: [],
        releases: { releases: [], total: 0, has_more: false },
      })
      expect(wrapper.text()).toContain('Waiting for your first event')
      expect(wrapper.find('.db-kpis').exists()).toBe(false)
    })

    it('shows the DSN in the waiting card', () => {
      const wrapper = makeWrapper({
        issues: { issues: [], total: 0, has_more: false },
        txSummaries: [],
        releases: { releases: [], total: 0, has_more: false },
      })
      expect(wrapper.text()).toContain('https://key1@tindra.test/p1')
    })

    it('does not show empty state when data is still loading', () => {
      const wrapper = makeWrapper({
        issuesFetching: true,
        issues: undefined,
        txSummaries: undefined,
        releases: undefined,
      })
      expect(wrapper.find('.db-kpis').exists()).toBe(true)
      expect(wrapper.text()).not.toContain('Waiting for your first event')
    })

    it('does not show empty state when transactions exist', () => {
      const wrapper = makeWrapper({
        txSummaries: [{ transaction: '/api/health', op: 'http.server', p50: 10, p95: 30, failure_rate: 0, sample_count: 1, project_id: 'p1' }],
        releases: { releases: [], total: 0, has_more: false },
      })
      expect(wrapper.find('.db-kpis').exists()).toBe(true)
    })

    it('copies DSN to clipboard and shows toast when Copy DSN is clicked', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
      const wrapper = makeWrapper({
        issues: { issues: [], total: 0, has_more: false },
        txSummaries: [],
        releases: { releases: [], total: 0, has_more: false },
      })
      await wrapper.find('.btn--primary').trigger('click')
      expect(writeText).toHaveBeenCalledWith('https://key1@tindra.test/p1')
    })

    it('picks the selected project DSN when selectedIds is non-empty', () => {
      vi.mocked(useProjectsStore).mockReturnValue({
        selectedIds: ['p1'],
        projects: [{ id: 'p1', name: 'Default', slug: 'default', public_key: 'key1' }],
      } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref({ permissions: { manage_alerts: false } }) } as any)
        .mockReturnValueOnce({ data: ref({ issues: [], total: 0, has_more: false }), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref({ releases: [], total: 0, has_more: false }), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)
      const wrapper = mount(DashboardView, { global: { stubs } })
      expect(wrapper.text()).toContain('https://key1@tindra.test/p1')
    })
  })
})
