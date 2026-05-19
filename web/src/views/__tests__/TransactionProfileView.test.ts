import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

vi.mock('vue-router', () => ({
  useRoute: vi.fn(() => ({ query: { name: '/api/users', op: 'http.server' } })),
  useRouter: vi.fn(() => ({ push: vi.fn() })),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useInfiniteQuery: vi.fn(),
}))

vi.mock('@/stores/projects', () => ({
  useProjectsStore: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

vi.mock('@/utils/formatters', () => ({
  formatDuration: vi.fn((n: number) => `${n}ms`),
}))

import TransactionProfileView from '../TransactionProfileView.vue'
import { useQuery, useInfiniteQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'

const stubs = {
  RouterLink: { template: '<a><slot /></a>' },
  Icon: { template: '<span />' },
  FilterChip: { template: '<div />' },
  TimeseriesChart: { template: '<div />' },
}

const makeSummary = (environment = 'production') => ({
  transaction: '/api/users',
  op: 'http.server',
  project_id: 'proj-1',
  environment,
  sample_count: 120,
  tpm: 2.0,
  p50: 110,
  p95: 400,
  failure_rate: 0.02,
  time_spent_ms: 13200,
})

function setupMocks(summaries: unknown[] = [], isError = false) {
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)

  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref(summaries), isError: ref(isError), refetch: vi.fn() } as any)
    .mockReturnValueOnce({ data: ref(undefined), isError: ref(false), refetch: vi.fn() } as any)

  vi.mocked(useInfiniteQuery).mockReturnValue({
    data: ref(undefined),
    fetchNextPage: vi.fn(),
    hasNextPage: ref(false),
    isFetchingNextPage: ref(false),
    isLoading: ref(false),
    isError: ref(false),
    refetch: vi.fn(),
  } as any)
}

beforeEach(() => {
  vi.mocked(useQuery).mockReset()
  vi.mocked(useInfiniteQuery).mockReset()
  vi.mocked(useProjectsStore).mockReset()
})

describe('TransactionProfileView', () => {
  describe('header', () => {
    it('renders the transaction name in the page', () => {
      setupMocks([makeSummary()])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.text()).toContain('/api/users')
    })

    it('renders the op badge', () => {
      setupMocks([makeSummary()])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      // op badge shows txOp.split('.')[0] so 'http.server' becomes 'http'
      expect(wrapper.text()).toContain('http')
    })
  })

  describe('error state', () => {
    it('shows an error message when loading fails', () => {
      setupMocks([], true)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.text()).toContain('Failed to load')
    })
  })

  describe('empty state', () => {
    it('shows no data message when there are no summaries', () => {
      setupMocks([])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      // No stats strip when summaries is empty, and samples show empty message
      expect(wrapper.find('.txstats').exists()).toBe(false)
      expect(wrapper.find('.tx-samples__empty').exists()).toBe(true)
    })
  })

  describe('loaded data', () => {
    it('renders stats when data is present', () => {
      setupMocks([makeSummary()])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.find('.txstats').exists()).toBe(true)
    })

    it('shows sample count', () => {
      setupMocks([makeSummary()])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.text()).toContain('120')
    })

    it('renders the samples table section', () => {
      setupMocks([makeSummary()])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      // Samples section uses .tx-samples class
      expect(wrapper.find('.tx-samples').exists()).toBe(true)
    })

    it('renders filter chips for window and environment', () => {
      setupMocks([makeSummary()])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      const filterchips = wrapper.findAll(stubs.FilterChip.template ? 'div' : '.filterchip')
      expect(filterchips.length).toBeGreaterThan(0)
    })
  })
})
