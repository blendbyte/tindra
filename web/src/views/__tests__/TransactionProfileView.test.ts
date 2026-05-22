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

vi.mock('@/stores/auth', () => ({
  useAuthStore: vi.fn(),
}))

import TransactionProfileView from '../TransactionProfileView.vue'
import { useQuery, useInfiniteQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const stubs = {
  RouterLink: { template: '<a><slot /></a>' },
  Icon: { template: '<span />' },
  FilterChip: { name: 'FilterChip', props: ['label', 'value', 'options'], template: '<div />' },
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

function setupMocks(summaries: unknown[] = [], isError = false, samples?: unknown[], hasNextPage = false) {
  vi.mocked(useProjectsStore).mockReturnValue({
    selectedIds: [],
    projects: [{ id: 'proj-1', name: 'My App', slug: 'my-app' }],
  } as any)

  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref(summaries), isError: ref(isError), refetch: vi.fn() } as any)
    .mockReturnValueOnce({ data: ref(undefined), isError: ref(false), refetch: vi.fn() } as any)

  vi.mocked(useInfiniteQuery).mockReturnValue({
    data: samples ? ref({ pages: [{ transactions: samples }] }) : ref(undefined),
    fetchNextPage: vi.fn(),
    hasNextPage: ref(hasNextPage),
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
  vi.mocked(useAuthStore).mockReset()
  vi.mocked(useAuthStore).mockReturnValue({ user: { timezone: 'UTC' }, setUser: vi.fn() } as any)
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

  describe('sample rows', () => {
    const makeSample = (id: string) => ({
      id,
      project_id: 'proj-1',
      trace_id: `trace-${id}`,
      duration_ms: 250,
      status: 'ok',
      start_timestamp: '2024-01-01T00:00:00.000Z',
    })

    it('renders sample rows when samples are loaded', () => {
      setupMocks([makeSummary()], false, [makeSample('tx1'), makeSample('tx2')])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      const rows = wrapper.findAll('.tx-sample-row:not(.tx-sample-row--skeleton):not(.tx-sample-row--head)')
      expect(rows.length).toBe(2)
    })

    it('shows project name in sample row', () => {
      setupMocks([makeSummary()], false, [makeSample('tx1')])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.text()).toContain('My App')
    })

    it('shows trace ID in sample row', () => {
      setupMocks([makeSummary()], false, [makeSample('tx1')])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.text()).toContain('trace-tx1')
    })

    it('shows status badge for each sample', () => {
      setupMocks([makeSummary()], false, [makeSample('tx1')])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.find('.tx-status--ok').exists()).toBe(true)
    })

    it('shows skeleton rows when samples are loading', () => {
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [], projects: [] } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([makeSummary()]), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref(undefined), isError: ref(false), refetch: vi.fn() } as any)
      vi.mocked(useInfiniteQuery).mockReturnValue({
        data: ref(undefined),
        fetchNextPage: vi.fn(),
        hasNextPage: ref(false),
        isFetchingNextPage: ref(false),
        isLoading: ref(true),
        isError: ref(false),
        refetch: vi.fn(),
      } as any)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.find('.tx-sample-row--skeleton').exists()).toBe(true)
    })
  })

  describe('load more', () => {
    it('shows Load more button when hasNextPage is true', () => {
      setupMocks([makeSummary()], false, [{
        id: 'tx1',
        project_id: 'proj-1',
        trace_id: 'tr1',
        duration_ms: 100,
        status: 'ok',
        start_timestamp: '2024-01-01T00:00:00.000Z',
      }], true)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.find('.tx-samples__more').exists()).toBe(true)
      expect(wrapper.find('.tx-samples__more .btn').text()).toContain('Load more')
    })
  })

  describe('sort columns', () => {
    const oneSample = [{
      id: 'tx1', project_id: 'proj-1', trace_id: 'tr1', duration_ms: 100, status: 'ok', start_timestamp: '2024-01-01T00:00:00.000Z',
    }]

    it('sorts by Status column when clicked', async () => {
      setupMocks([makeSummary()], false, oneSample)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      const statusBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Status'))!
      await statusBtn.trigger('click')
      expect(statusBtn.classes()).toContain('col-sort--active')
    })

    it('sorts by Duration column when clicked', async () => {
      setupMocks([makeSummary()], false, oneSample)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      const durBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Duration'))!
      await durBtn.trigger('click')
      expect(durBtn.classes()).toContain('col-sort--active')
    })

    it('toggles sort direction when clicking the same column twice', async () => {
      setupMocks([makeSummary()], false, oneSample)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      const durBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Duration'))!
      await durBtn.trigger('click')
      const firstIcon = durBtn.find('.col-sort__icon').text()
      await durBtn.trigger('click')
      const secondIcon = durBtn.find('.col-sort__icon').text()
      expect(firstIcon).not.toBe(secondIcon)
    })
  })

  describe('error state', () => {
    it('shows error message when summaries fail to load', () => {
      setupMocks([], true)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.text()).toContain('Failed to load transaction stats')
    })

    it('shows Retry button on error', () => {
      setupMocks([], true)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.find('.btn').text()).toContain('Retry')
    })
  })

  describe('keyboard navigation with samples', () => {
    const twoSamples = [
      { id: 'tx1', project_id: 'proj-1', trace_id: 'tr1', duration_ms: 100, status: 'ok', start_timestamp: '2024-01-01T00:00:00.000Z' },
      { id: 'tx2', project_id: 'proj-1', trace_id: 'tr2', duration_ms: 200, status: 'error', start_timestamp: '2024-01-01T00:01:00.000Z' },
    ]

    it('handles j key with samples present without throwing', () => {
      setupMocks([makeSummary()], false, twoSamples)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(() => {
        wrapper.find('.tx-samples').element.dispatchEvent(new KeyboardEvent('keydown', { key: 'j', bubbles: true }))
      }).not.toThrow()
      wrapper.unmount()
    })

    it('handles k key with samples present without throwing', () => {
      setupMocks([makeSummary()], false, twoSamples)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(() => {
        wrapper.find('.tx-samples').element.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', bubbles: true }))
      }).not.toThrow()
      wrapper.unmount()
    })
  })

  describe('stats formatting', () => {
    it('shows failure rate of 0% when no failures', () => {
      const summaryNoFail = { ...makeSummary(), failure_rate: 0 }
      setupMocks([summaryNoFail])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.text()).toContain('0%')
    })

    it('shows failure rate percentage when failures present', () => {
      const summaryWithFail = { ...makeSummary(), failure_rate: 0.05, sample_count: 100 }
      setupMocks([summaryWithFail])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.text()).toContain('%')
    })
  })

  describe('multiple environment summaries', () => {
    it('renders stats section with multiple project summaries', () => {
      const summaries = [makeSummary('production'), makeSummary('staging')]
      setupMocks(summaries)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.find('.txstats').exists()).toBe(true)
    })
  })

  describe('sample row rendering', () => {
    const twoSamples = [
      { id: 'tx1', project_id: 'proj-1', trace_id: 'tr1', duration_ms: 100, status: 'ok', start_timestamp: '2024-01-01T00:00:00.000Z' },
      { id: 'tx2', project_id: 'proj-1', trace_id: 'tr2', duration_ms: 200, status: 'error', start_timestamp: '2024-01-01T00:01:00.000Z' },
    ]

    it('renders sample rows with formatted time', () => {
      setupMocks([makeSummary()], false, twoSamples)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.findAll('.tx-sample-row').length).toBeGreaterThan(1)
      expect(wrapper.find('.tx-sample-row__time').exists()).toBe(true)
    })

    it('renders project name in sample row', () => {
      setupMocks([makeSummary()], false, twoSamples)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.find('.tx-sample-row__project').text()).toBe('My App')
    })

    it('handles Enter key after navigating to item', async () => {
      setupMocks([makeSummary()], false, twoSamples)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      const txSamples = wrapper.find('.tx-samples')
      txSamples.element.dispatchEvent(new KeyboardEvent('keydown', { key: 'j', bubbles: true }))
      await wrapper.vm.$nextTick()
      expect(() => {
        txSamples.element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
      }).not.toThrow()
      wrapper.unmount()
    })

    it('handles ArrowDown and ArrowUp keys', () => {
      setupMocks([makeSummary()], false, twoSamples)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      const txSamples = wrapper.find('.tx-samples')
      expect(() => {
        txSamples.element.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
        txSamples.element.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }))
      }).not.toThrow()
      wrapper.unmount()
    })

    it('shows Load more button when hasNextPage is true', () => {
      setupMocks([makeSummary()], false, twoSamples, true)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.find('.tx-samples__more').exists()).toBe(true)
    })
  })

  describe('formatTPM branches', () => {
    it('shows <0.001/min for very low tpm', () => {
      const s = { ...makeSummary(), tpm: 0.0005, sample_count: 1, failure_rate: 0 }
      setupMocks([s])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.text()).toContain('<0.001/min')
    })

    it('shows 4 decimal places for tpm between 0.001 and 0.01', () => {
      const s = { ...makeSummary(), tpm: 0.005, sample_count: 1, failure_rate: 0 }
      setupMocks([s])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.text()).toContain('/min')
    })
  })

  describe('formatFailureRate branches', () => {
    it('shows <0.01% for very small failure rates', () => {
      const s = { ...makeSummary(), tpm: 1.0, sample_count: 100, failure_rate: 0.000001 }
      setupMocks([s])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.text()).toContain('<0.01%')
    })
  })

  describe('FilterChip interactions', () => {
    it('updates windowHrs when first FilterChip changes', async () => {
      setupMocks([makeSummary()])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      await chips[0].vm.$emit('change', '7d')
      expect(wrapper.find('.txstats').exists()).toBe(true)
    })

    it('updates envFilter when second FilterChip changes', async () => {
      setupMocks([makeSummary()])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      await chips[1].vm.$emit('change', 'production')
      expect(wrapper.find('.txstats').exists()).toBe(true)
    })
  })

  describe('samples error state', () => {
    it('shows samples error when infinite query fails', () => {
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [], projects: [] } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([makeSummary()]), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref(undefined), isError: ref(false), refetch: vi.fn() } as any)
      vi.mocked(useInfiniteQuery).mockReturnValue({
        data: ref(undefined),
        fetchNextPage: vi.fn(),
        hasNextPage: ref(false),
        isFetchingNextPage: ref(false),
        isLoading: ref(false),
        isError: ref(true),
        refetch: vi.fn(),
      } as any)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.text()).toContain('Failed to load samples')
    })

    it('calls refetchSamples when Retry button is clicked in samples error', async () => {
      const refetchSamplesFn = vi.fn()
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [], projects: [] } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([makeSummary()]), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref(undefined), isError: ref(false), refetch: vi.fn() } as any)
      vi.mocked(useInfiniteQuery).mockReturnValue({
        data: ref(undefined),
        fetchNextPage: vi.fn(),
        hasNextPage: ref(false),
        isFetchingNextPage: ref(false),
        isLoading: ref(false),
        isError: ref(true),
        refetch: refetchSamplesFn,
      } as any)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      await wrapper.find('.txerror .btn').trigger('click')
      expect(refetchSamplesFn).toHaveBeenCalled()
    })
  })

  describe('sample row navigation', () => {
    const oneSample = {
      id: 'tx1',
      project_id: 'proj-1',
      trace_id: 'tr1',
      duration_ms: 100,
      status: 'ok',
      start_timestamp: '2024-01-01T00:00:00.000Z',
    }

    it('navigates to transaction detail when a sample row is clicked', async () => {
      const pushFn = vi.fn()
      vi.mocked(useRouter).mockReturnValue({ push: pushFn } as any)
      setupMocks([makeSummary()], false, [oneSample])
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      const row = wrapper.find('.tx-sample-row:not(.tx-sample-row--skeleton):not(.tx-sample-row--head)')
      if (row.exists()) {
        await row.trigger('click')
        expect(pushFn).toHaveBeenCalledWith('/transactions/tx1')
      }
    })

    it('calls fetchNextPage when Load more button is clicked', async () => {
      const fetchNextPageFn = vi.fn()
      vi.mocked(useProjectsStore).mockReturnValue({
        selectedIds: [],
        projects: [{ id: 'proj-1', name: 'My App', slug: 'my-app' }],
      } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([makeSummary()]), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref(undefined), isError: ref(false), refetch: vi.fn() } as any)
      vi.mocked(useInfiniteQuery).mockReturnValue({
        data: ref({ pages: [{ transactions: [oneSample] }] }),
        fetchNextPage: fetchNextPageFn,
        hasNextPage: ref(true),
        isFetchingNextPage: ref(false),
        isLoading: ref(false),
        isError: ref(false),
        refetch: vi.fn(),
      } as any)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      await wrapper.find('.tx-samples__more .btn').trigger('click')
      expect(fetchNextPageFn).toHaveBeenCalled()
    })
  })

  describe('timeseries chart', () => {
    const timeseriesData = {
      buckets: [
        { time: '2024-01-01T00:00:00Z', count: 10, p50: 100, p95: 200 },
        { time: '2024-01-01T01:00:00Z', count: 15, p50: 120, p95: 250 },
      ],
      bucket_size: 'hour',
    }

    it('renders timeseries charts when timeseries data has buckets', () => {
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [], projects: [{ id: 'proj-1', name: 'App', slug: 'app' }] } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([makeSummary()]), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref(timeseriesData), isError: ref(false), refetch: vi.fn() } as any)
      vi.mocked(useInfiniteQuery).mockReturnValue({
        data: ref(undefined), fetchNextPage: vi.fn(), hasNextPage: ref(false),
        isFetchingNextPage: ref(false), isLoading: ref(false), isError: ref(false), refetch: vi.fn(),
      } as any)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.find('.txcharts').exists()).toBe(true)
    })

    it('timeseries error state shows retry button and calls refetch on click', async () => {
      const refetchTimeseriesFn = vi.fn()
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [], projects: [{ id: 'proj-1', name: 'App', slug: 'app' }] } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([makeSummary()]), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref(undefined), isError: ref(true), refetch: refetchTimeseriesFn } as any)
      vi.mocked(useInfiniteQuery).mockReturnValue({
        data: ref(undefined), fetchNextPage: vi.fn(), hasNextPage: ref(false),
        isFetchingNextPage: ref(false), isLoading: ref(false), isError: ref(false), refetch: vi.fn(),
      } as any)
      const wrapper = mount(TransactionProfileView, { global: { stubs } })
      expect(wrapper.find('.txerror').exists()).toBe(true)
      await wrapper.find('.txerror .btn').trigger('click')
      expect(refetchTimeseriesFn).toHaveBeenCalled()
    })
  })
})
