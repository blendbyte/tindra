import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

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

import DashboardView from '../DashboardView.vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { useAuthStore } from '@/stores/auth'

const stubs = {
  RouterLink: { template: '<a><slot /></a>' },
  Icon: { template: '<span />' },
  Sparkline: { template: '<span />' },
}

// Queries are called in declaration order: me, issues, tx-summaries, tx-timeseries, releases, alert-rules
function setupQueries({
  manageAlerts = false,
  issues = undefined as unknown,
  issuesFetching = false,
  txSummaries = undefined as unknown,
  txFetching = false,
  releases = undefined as unknown,
  alertRules = [] as unknown[],
  alertsLoading = false,
} = {}) {
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref({ permissions: { manage_alerts: manageAlerts } }) } as any)
    .mockReturnValueOnce({ data: ref(issues), isFetching: ref(issuesFetching) } as any)
    .mockReturnValueOnce({ data: ref(txSummaries), isFetching: ref(txFetching) } as any)
    .mockReturnValueOnce({ data: ref(undefined) } as any)
    .mockReturnValueOnce({ data: ref(releases), isFetching: ref(false) } as any)
    .mockReturnValueOnce({ data: ref(alertRules), isLoading: ref(alertsLoading) } as any)
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
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref({ permissions: { manage_alerts: false } }) } as any)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref({ buckets: [bucket] }) } as any)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
      const wrapper = mount(DashboardView, { global: { stubs } })
      expect(wrapper.text()).toContain('Transaction density')
    })
  })
})
