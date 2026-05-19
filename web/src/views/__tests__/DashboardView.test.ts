import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: vi.fn() })),
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

import DashboardView from '../DashboardView.vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'

const stubs = {
  RouterLink: { template: '<a><slot /></a>' },
  Icon: { template: '<span />' },
  Sparkline: { template: '<span />' },
}

// Queries are called in declaration order: me, issues, tx-summaries, tx-timeseries, releases, alert-rules
function setupQueries({ manageAlerts = false }: { manageAlerts?: boolean } = {}) {
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref({ permissions: { manage_alerts: manageAlerts } }) } as any)
    .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
    .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
    .mockReturnValueOnce({ data: ref(undefined) } as any)
    .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
    .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
}

function makeWrapper(options?: { manageAlerts?: boolean }) {
  setupQueries(options)
  return mount(DashboardView, { global: { stubs } })
}

beforeEach(() => {
  vi.mocked(useQuery).mockReset()
  vi.mocked(useProjectsStore).mockReset()
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
  })
})
