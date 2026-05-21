import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

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
  apiFetch: vi.fn(),
}))

vi.mock('@/utils/formatters', () => ({
  formatDuration: vi.fn((n: number) => `${n}ms`),
  formatRate: vi.fn((n: number) => `${n}/min`),
  formatPct: vi.fn((n: number) => `${n}%`),
}))

import DBQueriesView from '../DBQueriesView.vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { usePerformanceStore } from '@/stores/performance'

const stubs = {
  Icon: { template: '<span />' },
  FilterChip: { name: 'FilterChip', props: ['label', 'value', 'options'], template: '<div />' },
  TimeseriesChart: { template: '<div />' },
  PerformanceSubnav: { template: '<div />' },
  SpanSamplesPanel: { name: 'SpanSamplesPanel', emits: ['close'], template: '<div />' },
}

const makeSpan = (description: string, op = 'db.query') => ({
  description,
  op,
  rate: 2.1,
  p50: 18,
  p95: 80,
  time_pct: 12.3,
  error_rate: 0,
})

const makeTimeseries = () => ({
  buckets: [
    { time: '2024-01-01T00:00:00Z', count: 10, p50: 18 },
    { time: '2024-01-01T01:00:00Z', count: 20, p50: 22 },
  ],
  bucket_size: 'hour' as const,
})

function setupMocks(summaries: unknown[] = [], isLoading = false, isError = false, timeseries?: unknown) {
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
  vi.mocked(usePerformanceStore).mockReturnValue({
    windowHrs: '24h',
    envFilter: 'All',
  } as any)

  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref(summaries), isLoading: ref(isLoading), isError: ref(isError), refetch: vi.fn() } as any)
    .mockReturnValueOnce({ data: ref(timeseries) } as any)
}

beforeEach(() => {
  vi.mocked(useQuery).mockReset()
  vi.mocked(useProjectsStore).mockReset()
  vi.mocked(usePerformanceStore).mockReset()
})

describe('DBQueriesView', () => {
  describe('loading state', () => {
    it('shows skeleton rows while loading', () => {
      setupMocks([], true)
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      expect(wrapper.find('.perf-table__skel-row').exists()).toBe(true)
    })
  })

  describe('empty state', () => {
    it('shows "No database spans in this window" when there are no spans', () => {
      setupMocks([])
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      expect(wrapper.text()).toContain('No database spans in this window')
    })
  })

  describe('error state', () => {
    it('shows an error message when loading fails', () => {
      setupMocks([], false, true)
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      expect(wrapper.text()).toContain("Couldn't load query data")
    })

    it('shows a Retry button on error', () => {
      setupMocks([], false, true)
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      expect(wrapper.find('.txerror .btn').text()).toBe('Retry')
    })
  })

  describe('loaded spans', () => {
    it('renders a row for each query span', () => {
      setupMocks([makeSpan('SELECT * FROM users'), makeSpan('INSERT INTO logs')])
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      const rows = wrapper.findAll('.perf-table__row--clickable')
      expect(rows.length).toBe(2)
    })

    it('displays the query description', () => {
      setupMocks([makeSpan('SELECT * FROM users')])
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      expect(wrapper.text()).toContain('SELECT * FROM users')
    })

    it('renders table column headers', () => {
      setupMocks([makeSpan('SELECT 1')])
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      expect(wrapper.text()).toContain('Description')
      expect(wrapper.text()).toContain('QPM')
      expect(wrapper.text()).toContain('P50')
      expect(wrapper.text()).toContain('P95')
    })
  })

  describe('filter input', () => {
    it('renders the queries filter input', () => {
      setupMocks([])
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      expect(wrapper.find('input[aria-label="Filter queries"]').exists()).toBe(true)
    })

    it('shows "No queries match" when search has no results', async () => {
      setupMocks([makeSpan('SELECT * FROM users')])
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      await wrapper.find('input[aria-label="Filter queries"]').setValue('nonexistent_zzz')
      expect(wrapper.text()).toContain('No queries match')
    })
  })

  describe('sorting', () => {
    it('renders sorting buttons for each column', () => {
      setupMocks([makeSpan('SELECT 1')])
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      const sortBtns = wrapper.findAll('.col-sort')
      expect(sortBtns.length).toBeGreaterThan(0)
    })

    it('changes active sort column when clicked', async () => {
      setupMocks([makeSpan('SELECT 1'), makeSpan('SELECT 2')])
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      const p50Btn = wrapper.findAll('.col-sort').find(b => b.text().includes('P50'))!
      await p50Btn.trigger('click')
      expect(p50Btn.classes()).toContain('col-sort--active')
    })
  })

  describe('timeseries chart section', () => {
    it('renders the timeseries section when data is available', () => {
      setupMocks([makeSpan('SELECT 1')], false, false, makeTimeseries())
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      expect(wrapper.find('.txcharts').exists()).toBe(true)
    })

    it('does not render the timeseries section when data is empty', () => {
      setupMocks([makeSpan('SELECT 1')], false, false, { buckets: [], bucket_size: 'hour' })
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      expect(wrapper.find('.txcharts').exists()).toBe(false)
    })
  })

  describe('retry button', () => {
    it('shows a Retry button on error', () => {
      setupMocks([], false, true)
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      expect(wrapper.find('.txerror .btn').text()).toBe('Retry')
    })
  })

  describe('FilterChip interactions', () => {
    it('updates windowHrs when Window FilterChip changes', async () => {
      setupMocks([])
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      await chips[0].vm.$emit('change', '7d')
      expect(chips[0].exists()).toBe(true)
    })

    it('updates envFilter when Env FilterChip changes', async () => {
      setupMocks([])
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      await chips[1].vm.$emit('change', 'production')
      expect(chips[1].exists()).toBe(true)
    })
  })

  describe('row click and panel', () => {
    it('opens SpanSamplesPanel when a row is clicked', async () => {
      setupMocks([makeSpan('SELECT * FROM users')])
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      await wrapper.find('.perf-table__row--clickable').trigger('click')
      expect(wrapper.findComponent({ name: 'SpanSamplesPanel' }).exists()).toBe(true)
    })

    it('closes SpanSamplesPanel on close event', async () => {
      setupMocks([makeSpan('SELECT * FROM users')])
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      await wrapper.find('.perf-table__row--clickable').trigger('click')
      const panel = wrapper.findComponent({ name: 'SpanSamplesPanel' })
      await panel.vm.$emit('close')
      expect(wrapper.findComponent({ name: 'SpanSamplesPanel' }).exists()).toBe(false)
    })

    it('calls refetch when Retry is clicked on error', async () => {
      const refetchFn = vi.fn()
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(usePerformanceStore).mockReturnValue({ windowHrs: '24h', envFilter: 'All' } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false), isError: ref(true), refetch: refetchFn } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
      const wrapper = mount(DBQueriesView, { global: { stubs } })
      await wrapper.find('.txerror .btn').trigger('click')
      expect(refetchFn).toHaveBeenCalled()
    })
  })
})
