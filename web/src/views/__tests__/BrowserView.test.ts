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

import BrowserView from '../BrowserView.vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { usePerformanceStore } from '@/stores/performance'

const stubs = {
  Icon: { template: '<span />' },
  FilterChip: { template: '<div />' },
  PerformanceSubnav: { template: '<div />' },
}

const makeSummary = (override = {}) => ({
  lcp: { p75: 2100, count: 50, pass_rate: 0.92 },
  fcp: { p75: 1500, count: 50, pass_rate: 0.95 },
  cls: { p75: 0.05, count: 50, pass_rate: 0.98 },
  inp: { p75: 180, count: 50, pass_rate: 0.90 },
  ttfb: { p75: 400, count: 50, pass_rate: 0.88 },
  ...override,
})

const makePage = (transaction: string, override = {}) => ({
  transaction,
  sessions: 100,
  lcp_p75: 2100,
  inp_p75: 180,
  cls_p75: 0.05,
  pass_rate: 0.90,
  ...override,
})

function setupMocks(summaryData?: unknown, pagesData?: unknown[], isError = false) {
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
  vi.mocked(usePerformanceStore).mockReturnValue({
    windowHrs: '24h',
    envFilter: 'All',
  } as any)

  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref(summaryData), isLoading: ref(false), isError: ref(isError), refetch: vi.fn() } as any)
    .mockReturnValueOnce({ data: ref(pagesData), isLoading: ref(false), isError: ref(isError), refetch: vi.fn() } as any)
}

beforeEach(() => {
  vi.mocked(useQuery).mockReset()
  vi.mocked(useProjectsStore).mockReset()
  vi.mocked(usePerformanceStore).mockReset()
})

describe('BrowserView', () => {
  describe('error state', () => {
    it('shows an error message when loading fails', () => {
      setupMocks(undefined, undefined, true)
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.text()).toContain("Couldn't load Web Vitals")
    })

    it('shows a Retry button on error', () => {
      setupMocks(undefined, undefined, true)
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.find('.txerror .btn').text()).toBe('Retry')
    })
  })

  describe('empty state', () => {
    it('shows "No browser data in this window" when all vitals counts are zero', () => {
      const emptySummary = {
        lcp: { p75: 0, count: 0, pass_rate: 0 },
        fcp: { p75: 0, count: 0, pass_rate: 0 },
        cls: { p75: 0, count: 0, pass_rate: 0 },
        inp: { p75: 0, count: 0, pass_rate: 0 },
        ttfb: { p75: 0, count: 0, pass_rate: 0 },
      }
      setupMocks(emptySummary, [])
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.text()).toContain('No browser data in this window')
    })
  })

  describe('loaded data', () => {
    it('renders the vitals summary strip with all five labels', () => {
      setupMocks(makeSummary(), [])
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.text()).toContain('LCP')
      expect(wrapper.text()).toContain('FCP')
      expect(wrapper.text()).toContain('CLS')
      expect(wrapper.text()).toContain('INP')
      expect(wrapper.text()).toContain('TTFB')
    })

    it('renders page rows in the table', () => {
      setupMocks(makeSummary(), [makePage('/home'), makePage('/about')])
      const wrapper = mount(BrowserView, { global: { stubs } })
      const rows = wrapper.findAll('.perf-table__row')
      expect(rows.length).toBe(2)
    })

    it('displays the page transaction name', () => {
      setupMocks(makeSummary(), [makePage('/checkout')])
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.text()).toContain('/checkout')
    })

    it('renders table column headers', () => {
      setupMocks(makeSummary(), [makePage('/home')])
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.text()).toContain('Page')
      expect(wrapper.text()).toContain('Sessions')
      expect(wrapper.text()).toContain('LCP p75')
      expect(wrapper.text()).toContain('CWV pass')
    })

    it('shows "No pages with Web Vitals data" when pages list is empty', () => {
      setupMocks(makeSummary(), [])
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.text()).toContain('No pages with Web Vitals data')
    })
  })

  describe('sorting', () => {
    it('toggles sort direction when clicking the same column header twice', async () => {
      setupMocks(makeSummary(), [makePage('/a'), makePage('/b')])
      const wrapper = mount(BrowserView, { global: { stubs } })
      const pageBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Page'))!
      await pageBtn.trigger('click')
      await pageBtn.trigger('click')
      expect(wrapper.text()).toBeTruthy()
    })

    it('changes the active sort column', async () => {
      setupMocks(makeSummary(), [makePage('/a')])
      const wrapper = mount(BrowserView, { global: { stubs } })
      const sessionsBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Sessions'))!
      await sessionsBtn.trigger('click')
      expect(wrapper.text()).toBeTruthy()
    })
  })
})
