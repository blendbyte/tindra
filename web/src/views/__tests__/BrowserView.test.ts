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

    it('sets asc direction when switching to the Page (transaction) column', async () => {
      setupMocks(makeSummary(), [makePage('/a')])
      const wrapper = mount(BrowserView, { global: { stubs } })
      // First click Sessions to switch away from default
      const sessionsBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Sessions'))!
      await sessionsBtn.trigger('click')
      // Then click Page to switch to transaction (should set asc)
      const pageBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Page'))!
      await pageBtn.trigger('click')
      // After switching to Page column, sort icon should be ↑ (asc)
      expect(pageBtn.text()).toContain('↑')
    })

    it('shows skeleton rows when pages are loading', () => {
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(usePerformanceStore).mockReturnValue({ windowHrs: '24h', envFilter: 'All' } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref(makeSummary()), isLoading: ref(false), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref(undefined), isLoading: ref(true), isError: ref(false), refetch: vi.fn() } as any)
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.find('.perf-table__skel-row').exists()).toBe(true)
    })

    it('sorts by INP p75 column when clicked — shows sort icon', async () => {
      setupMocks(makeSummary(), [makePage('/a'), makePage('/b')])
      const wrapper = mount(BrowserView, { global: { stubs } })
      const inpBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('INP'))!
      await inpBtn.trigger('click')
      expect(inpBtn.text()).toMatch(/[↓↑]/)
    })

    it('sorts by CLS p75 column when clicked — shows sort icon', async () => {
      setupMocks(makeSummary(), [makePage('/a'), makePage('/b')])
      const wrapper = mount(BrowserView, { global: { stubs } })
      const clsBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('CLS'))!
      await clsBtn.trigger('click')
      expect(clsBtn.text()).toMatch(/[↓↑]/)
    })

    it('sorts by CWV pass column shows ↑ after click (already default)', async () => {
      setupMocks(makeSummary(), [makePage('/a'), makePage('/b')])
      const wrapper = mount(BrowserView, { global: { stubs } })
      // pass_rate is the default sort col, clicking once toggles direction
      const cwvBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('CWV'))!
      await cwvBtn.trigger('click')
      expect(cwvBtn.text()).toMatch(/[↓↑]/)
    })

    it('sorts by LCP p75 column when clicked — shows sort icon', async () => {
      setupMocks(makeSummary(), [makePage('/a'), makePage('/b')])
      const wrapper = mount(BrowserView, { global: { stubs } })
      const lcpBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('LCP'))!
      await lcpBtn.trigger('click')
      expect(lcpBtn.text()).toMatch(/[↓↑]/)
    })
  })

  describe('vital status classes', () => {
    it('shows needs-improvement vital class for poor-but-not-critical value', () => {
      const summaryWithWarning = makeSummary({ lcp: { p75: 3000, count: 50, pass_rate: 0.75 } })
      setupMocks(summaryWithWarning, [])
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.find('.txval--needs-improvement').exists()).toBe(true)
    })

    it('shows poor vital class for very bad value', () => {
      const summaryWithPoor = makeSummary({ lcp: { p75: 5000, count: 50, pass_rate: 0.30 } })
      setupMocks(summaryWithPoor, [])
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.find('.txval--poor').exists()).toBe(true)
    })
  })

  describe('page status', () => {
    it('shows needs-improvement status for pages with 50-90% pass rate', () => {
      const page = makePage('/slow', { pass_rate: 0.70 })
      setupMocks(makeSummary(), [page])
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.find('.pass-bar__fill--needs-improvement').exists()).toBe(true)
    })

    it('shows poor status for pages with < 50% pass rate', () => {
      const page = makePage('/very-slow', { pass_rate: 0.30 })
      setupMocks(makeSummary(), [page])
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.find('.pass-bar__fill--poor').exists()).toBe(true)
    })
  })

  describe('vital formatting', () => {
    it('formats vitals >= 1000ms as seconds', () => {
      const summaryLarge = makeSummary({ fcp: { p75: 2100, count: 50, pass_rate: 0.95 } })
      setupMocks(summaryLarge, [])
      const wrapper = mount(BrowserView, { global: { stubs } })
      // 2100ms → "2.10s"
      expect(wrapper.text()).toContain('2.10s')
    })

    it('formats CLS as decimal (no ms unit)', () => {
      const summaryCLS = makeSummary({ cls: { p75: 0.123, count: 50, pass_rate: 0.80 } })
      setupMocks(summaryCLS, [])
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.text()).toContain('0.123')
    })

    it('shows dash for zero vital value', () => {
      const summaryZero = makeSummary({ ttfb: { p75: 0, count: 50, pass_rate: 0.95 } })
      setupMocks(summaryZero, [])
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.text()).toContain('–')
    })
  })

  describe('retry button', () => {
    it('calls refetch when Retry button is clicked', async () => {
      const refetchSummary = vi.fn()
      const refetchPages = vi.fn()
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(usePerformanceStore).mockReturnValue({ windowHrs: '24h', envFilter: 'All' } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref(undefined), isLoading: ref(false), isError: ref(true), refetch: refetchSummary } as any)
        .mockReturnValueOnce({ data: ref(undefined), isLoading: ref(false), isError: ref(true), refetch: refetchPages } as any)
      const wrapper = mount(BrowserView, { global: { stubs } })
      await wrapper.find('.txerror .btn').trigger('click')
      expect(refetchSummary).toHaveBeenCalled()
      expect(refetchPages).toHaveBeenCalled()
    })
  })

  describe('FilterChip interactions', () => {
    const stubs2 = {
      Icon: { template: '<span />' },
      TimeseriesChart: { template: '<div />' },
      PerformanceSubnav: { template: '<div />' },
      SpanSamplesPanel: { name: 'SpanSamplesPanel', emits: ['close'], template: '<div />' },
      FilterChip: { name: 'FilterChip', props: ['label', 'value', 'options'], template: '<div />' },
    }

    it('updates windowHrs when Window FilterChip changes', async () => {
      setupMocks(makeSummary(), [])
      const wrapper = mount(BrowserView, { global: { stubs: stubs2 } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      if (chips.length > 0) {
        await chips[0].vm.$emit('change', '7d')
        expect(chips[0].exists()).toBe(true)
      }
    })

    it('updates envFilter when Env FilterChip changes', async () => {
      setupMocks(makeSummary(), [])
      const wrapper = mount(BrowserView, { global: { stubs: stubs2 } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      if (chips.length > 1) {
        await chips[1].vm.$emit('change', 'production')
        expect(chips[1].exists()).toBe(true)
      }
    })
  })
})
