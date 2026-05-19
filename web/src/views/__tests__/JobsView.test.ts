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
  FilterChip: { template: '<div />' },
  TimeseriesChart: { template: '<div />' },
  PerformanceSubnav: { template: '<div />' },
  SpanSamplesPanel: { template: '<div />' },
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

function setupMocks(summaries: unknown[] = [], isLoading = false, isError = false) {
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
  vi.mocked(usePerformanceStore).mockReturnValue({
    windowHrs: '24h',
    envFilter: 'All',
  } as any)

  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref(summaries), isLoading: ref(isLoading), isError: ref(isError), refetch: vi.fn() } as any)
    .mockReturnValueOnce({ data: ref(undefined) } as any)
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
})
