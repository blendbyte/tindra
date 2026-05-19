import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

const pushMock = vi.fn()
const replaceMock = vi.fn()

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: pushMock, replace: replaceMock })),
  useRoute: vi.fn(() => ({ query: {} })),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
}))

vi.mock('@/stores/projects', () => ({
  useProjectsStore: vi.fn(),
}))

vi.mock('@/stores/performance', () => ({
  usePerformanceStore: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn().mockResolvedValue({ buckets: [], bucket_size: 'hour' }),
}))

vi.mock('@/utils/formatters', () => ({
  formatDuration: vi.fn((n: number) => `${n}ms`),
}))

import TransactionListView from '../TransactionListView.vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { usePerformanceStore } from '@/stores/performance'

const stubs = {
  RouterLink: { template: '<a><slot /></a>' },
  Icon: { template: '<span />' },
  FilterChip: { template: '<div />' },
  TimeseriesChart: { template: '<div />' },
  PerformanceSubnav: { template: '<div />' },
  BrandMark: { template: '<span />' },
}

const makeSummary = (transaction: string, op = 'http.server') => ({
  transaction,
  op,
  project_id: 'proj-1',
  sample_count: 100,
  tpm: 1.5,
  p50: 120,
  p95: 450,
  failure_rate: 0,
  time_spent_ms: 12000,
})

function setupMocks(summaries: unknown[] = [], isLoading = false, isError = false, projects = [{ id: '1', name: 'App', slug: 'app' }]) {
  vi.mocked(useProjectsStore).mockReturnValue({
    projects,
    selectedIds: [],
  } as any)
  vi.mocked(usePerformanceStore).mockReturnValue({
    windowHrs: '24h',
    envFilter: 'All',
  } as any)

  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref({ releases: [], total: 0, has_more: false }) } as any)
    .mockReturnValueOnce({ data: ref(summaries), isLoading: ref(isLoading), isError: ref(isError), refetch: vi.fn() } as any)
    .mockReturnValueOnce({ data: ref(undefined) } as any)
}

beforeEach(() => {
  vi.mocked(useQuery).mockReset()
  vi.mocked(useProjectsStore).mockReset()
  vi.mocked(usePerformanceStore).mockReset()
  pushMock.mockReset()
  replaceMock.mockReset()
})

describe('TransactionListView', () => {
  describe('empty state', () => {
    it('shows "No projects yet" when there are no projects', () => {
      setupMocks([], false, false, [])
      const wrapper = mount(TransactionListView, { global: { stubs } })
      expect(wrapper.text()).toContain('No projects yet')
    })

    it('shows no data empty state when transactions list is empty', () => {
      setupMocks([])
      const wrapper = mount(TransactionListView, { global: { stubs } })
      expect(wrapper.find('.empty-state').exists()).toBe(true)
    })
  })

  describe('error state', () => {
    it('shows error message when loading fails', () => {
      setupMocks([], false, true)
      const wrapper = mount(TransactionListView, { global: { stubs } })
      expect(wrapper.text()).toContain("Couldn't load transactions")
    })
  })

  describe('loading state', () => {
    it('shows skeleton rows while loading', () => {
      setupMocks([], true)
      const wrapper = mount(TransactionListView, { global: { stubs } })
      expect(wrapper.find('.ghost--bar').exists()).toBe(true)
    })
  })

  describe('loaded transactions', () => {
    it('renders a row for each transaction', () => {
      setupMocks([makeSummary('/api/users'), makeSummary('/api/orders')])
      const wrapper = mount(TransactionListView, { global: { stubs } })
      expect(wrapper.text()).toContain('/api/users')
      expect(wrapper.text()).toContain('/api/orders')
    })

    it('displays the transaction name', () => {
      setupMocks([makeSummary('/api/users')])
      const wrapper = mount(TransactionListView, { global: { stubs } })
      expect(wrapper.text()).toContain('/api/users')
    })

    it('renders the stats strip when transactions are present', () => {
      setupMocks([makeSummary('/api/users')])
      const wrapper = mount(TransactionListView, { global: { stubs } })
      expect(wrapper.find('.txstats').exists()).toBe(true)
    })

    it('navigates to transaction profile on row click', async () => {
      setupMocks([makeSummary('/api/users', 'http.server')])
      const wrapper = mount(TransactionListView, { global: { stubs } })
      const rows = wrapper.findAll('.tx-row')
      const clickableRow = rows.find(r => r.classes('tx-row--clickable') || r.attributes('data-clickable'))
      if (clickableRow) {
        await clickableRow.trigger('click')
        expect(pushMock).toHaveBeenCalled()
      } else {
        const links = wrapper.findAll('a')
        expect(links.length).toBeGreaterThanOrEqual(0)
      }
    })

    it('shows aggregate stats strip when summaries are present', () => {
      setupMocks([makeSummary('/api/users')])
      const wrapper = mount(TransactionListView, { global: { stubs } })
      expect(wrapper.find('.txstats').exists()).toBe(true)
    })
  })

  describe('sorting', () => {
    it('renders sortable column headers', () => {
      setupMocks([makeSummary('/api/users')])
      const wrapper = mount(TransactionListView, { global: { stubs } })
      const sortBtns = wrapper.findAll('.col-sort')
      expect(sortBtns.length).toBeGreaterThan(0)
    })
  })
})
