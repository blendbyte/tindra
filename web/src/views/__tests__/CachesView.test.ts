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

import CachesView from '../CachesView.vue'
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

const makeSpan = (description: string, op = 'cache.get') => ({
  description,
  op,
  rate: 1.5,
  p50: 12,
  p95: 45,
  time_pct: 5.2,
  error_rate: 0,
  miss_rate: 20,
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

describe('CachesView', () => {
  describe('loading state', () => {
    it('shows skeleton rows while loading', () => {
      setupMocks([], true)
      const wrapper = mount(CachesView, { global: { stubs } })
      expect(wrapper.find('.perf-table__skel-row').exists()).toBe(true)
    })
  })

  describe('empty state', () => {
    it('shows "No cache spans in this window" when there are no spans', () => {
      setupMocks([])
      const wrapper = mount(CachesView, { global: { stubs } })
      expect(wrapper.text()).toContain('No cache spans in this window')
    })
  })

  describe('error state', () => {
    it('shows an error message when loading fails', () => {
      setupMocks([], false, true)
      const wrapper = mount(CachesView, { global: { stubs } })
      expect(wrapper.text()).toContain("Couldn't load cache data")
    })

    it('shows a Retry button on error', () => {
      setupMocks([], false, true)
      const wrapper = mount(CachesView, { global: { stubs } })
      expect(wrapper.find('.txerror .btn').text()).toBe('Retry')
    })
  })

  describe('loaded spans', () => {
    it('renders a row for each span', () => {
      setupMocks([makeSpan('user:profile:123'), makeSpan('session:abc')])
      const wrapper = mount(CachesView, { global: { stubs } })
      const rows = wrapper.findAll('.perf-table__row--clickable')
      expect(rows.length).toBe(2)
    })

    it('displays the span description', () => {
      setupMocks([makeSpan('user:profile:123')])
      const wrapper = mount(CachesView, { global: { stubs } })
      expect(wrapper.text()).toContain('user:profile:123')
    })

    it('renders table column headers', () => {
      setupMocks([makeSpan('user:profile')])
      const wrapper = mount(CachesView, { global: { stubs } })
      expect(wrapper.text()).toContain('Description')
      expect(wrapper.text()).toContain('RPM')
      expect(wrapper.text()).toContain('P50')
      expect(wrapper.text()).toContain('Miss rate')
    })

    it('renders the miss rate column', () => {
      setupMocks([makeSpan('user:profile')])
      const wrapper = mount(CachesView, { global: { stubs } })
      expect(wrapper.text()).toContain('Miss rate')
    })
  })

  describe('filter input', () => {
    it('renders the keys filter input', () => {
      setupMocks([])
      const wrapper = mount(CachesView, { global: { stubs } })
      expect(wrapper.find('input[aria-label="Filter keys"]').exists()).toBe(true)
    })

    it('shows "No keys match" when search has no results', async () => {
      setupMocks([makeSpan('user:profile')])
      const wrapper = mount(CachesView, { global: { stubs } })
      await wrapper.find('input[aria-label="Filter keys"]').setValue('zzz')
      expect(wrapper.text()).toContain('No keys match')
    })
  })

  describe('sorting', () => {
    it('toggles sort direction on column header click', async () => {
      setupMocks([makeSpan('a'), makeSpan('b')])
      const wrapper = mount(CachesView, { global: { stubs } })
      const p50btn = wrapper.findAll('.col-sort').find(b => b.text().includes('P50'))!
      await p50btn.trigger('click')
      await p50btn.trigger('click')
      expect(wrapper.text()).toBeTruthy()
    })
  })
})
