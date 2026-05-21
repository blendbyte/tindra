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

import JobsView from '../JobsView.vue'
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

const makeJob = (description: string, op = 'task.run') => ({
  description,
  op,
  rate: 0.5,
  p50: 500,
  p95: 2000,
  time_pct: 8.1,
  error_rate: 0,
})

const makeTimeseries = () => ({
  buckets: [
    { time: '2024-01-01T00:00:00Z', count: 5, p50: 30 },
    { time: '2024-01-01T01:00:00Z', count: 8, p50: 35 },
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

describe('JobsView', () => {
  describe('loading state', () => {
    it('shows skeleton rows while loading', () => {
      setupMocks([], true)
      const wrapper = mount(JobsView, { global: { stubs } })
      expect(wrapper.find('.perf-table__skel-row').exists()).toBe(true)
    })
  })

  describe('empty state', () => {
    it('shows "No job or queue spans in this window" when there are no spans', () => {
      setupMocks([])
      const wrapper = mount(JobsView, { global: { stubs } })
      expect(wrapper.text()).toContain('No job or queue spans in this window')
    })
  })

  describe('error state', () => {
    it('shows an error message when loading fails', () => {
      setupMocks([], false, true)
      const wrapper = mount(JobsView, { global: { stubs } })
      expect(wrapper.text()).toContain("Couldn't load job data")
    })

    it('shows a Retry button on error', () => {
      setupMocks([], false, true)
      const wrapper = mount(JobsView, { global: { stubs } })
      expect(wrapper.find('.txerror .btn').text()).toBe('Retry')
    })
  })

  describe('loaded spans', () => {
    it('renders a row for each job span', () => {
      setupMocks([makeJob('send_email_job'), makeJob('cleanup_old_records')])
      const wrapper = mount(JobsView, { global: { stubs } })
      const rows = wrapper.findAll('.perf-table__row--clickable')
      expect(rows.length).toBe(2)
    })

    it('displays the job description', () => {
      setupMocks([makeJob('send_email_job')])
      const wrapper = mount(JobsView, { global: { stubs } })
      expect(wrapper.text()).toContain('send_email_job')
    })

    it('renders table column headers', () => {
      setupMocks([makeJob('send_email_job')])
      const wrapper = mount(JobsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Description')
      expect(wrapper.text()).toContain('TPM')
      expect(wrapper.text()).toContain('P50')
      expect(wrapper.text()).toContain('P95')
    })

    it('displays op tag for each row', () => {
      setupMocks([makeJob('my_job', 'task.run')])
      const wrapper = mount(JobsView, { global: { stubs } })
      expect(wrapper.find('.optag--task').exists()).toBe(true)
    })
  })

  describe('filter input', () => {
    it('renders the jobs filter input', () => {
      setupMocks([])
      const wrapper = mount(JobsView, { global: { stubs } })
      expect(wrapper.find('input[aria-label="Filter jobs"]').exists()).toBe(true)
    })

    it('shows "No jobs match" when search has no results', async () => {
      setupMocks([makeJob('send_email_job')])
      const wrapper = mount(JobsView, { global: { stubs } })
      await wrapper.find('input[aria-label="Filter jobs"]').setValue('nonexistent_zzz')
      expect(wrapper.text()).toContain('No jobs match')
    })
  })

  describe('timeseries chart section', () => {
    it('renders the timeseries section when data is available', () => {
      setupMocks([makeJob('worker')], false, false, makeTimeseries())
      const wrapper = mount(JobsView, { global: { stubs } })
      expect(wrapper.find('.txcharts').exists()).toBe(true)
    })

    it('does not render the timeseries section when data is empty', () => {
      setupMocks([makeJob('worker')], false, false, { buckets: [], bucket_size: 'hour' })
      const wrapper = mount(JobsView, { global: { stubs } })
      expect(wrapper.find('.txcharts').exists()).toBe(false)
    })
  })

  describe('FilterChip interactions', () => {
    it('updates windowHrs when Window FilterChip changes', async () => {
      setupMocks([])
      const wrapper = mount(JobsView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      await chips[0].vm.$emit('change', '7d')
      expect(chips[0].exists()).toBe(true)
    })

    it('updates envFilter when Env FilterChip changes', async () => {
      setupMocks([])
      const wrapper = mount(JobsView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      await chips[1].vm.$emit('change', 'production')
      expect(chips[1].exists()).toBe(true)
    })
  })

  describe('row click and panel', () => {
    it('opens SpanSamplesPanel when a row is clicked', async () => {
      setupMocks([makeJob('send_email_job')])
      const wrapper = mount(JobsView, { global: { stubs } })
      const row = wrapper.find('.perf-table__row--clickable')
      await row.trigger('click')
      expect(wrapper.findComponent({ name: 'SpanSamplesPanel' }).exists()).toBe(true)
    })

    it('closes SpanSamplesPanel on close event', async () => {
      setupMocks([makeJob('send_email_job')])
      const wrapper = mount(JobsView, { global: { stubs } })
      await wrapper.find('.perf-table__row--clickable').trigger('click')
      const panel = wrapper.findComponent({ name: 'SpanSamplesPanel' })
      await panel.vm.$emit('close')
      expect(wrapper.findComponent({ name: 'SpanSamplesPanel' }).exists()).toBe(false)
    })

    it('calls refetch when Retry button is clicked', async () => {
      const refetchFn = vi.fn()
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(usePerformanceStore).mockReturnValue({ windowHrs: '24h', envFilter: 'All' } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false), isError: ref(true), refetch: refetchFn } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
      const wrapper = mount(JobsView, { global: { stubs } })
      await wrapper.find('.txerror .btn').trigger('click')
      expect(refetchFn).toHaveBeenCalled()
    })
  })
})
