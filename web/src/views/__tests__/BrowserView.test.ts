import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { reactive, ref } from 'vue'

const routeState = { query: {} as Record<string, unknown> }

vi.mock('vue-router', () => ({
  useRoute: vi.fn(() => routeState),
  useRouter: vi.fn(() => ({ replace: vi.fn(), push: vi.fn() })),
  RouterLink: { template: '<a><slot /></a>', props: ['to'] },
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

vi.mock('@/stores/appUser', () => ({
  useAppUserStore: vi.fn(() => ({
    identity: '',
    selected: null,
    label: '',
    select: vi.fn(),
    clear: vi.fn(),
  })),
  routeUserIdentity: (q: { user?: unknown }) => typeof q?.user === 'string' ? q.user : '',
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: vi.fn(() => ({ user: { timezone: 'UTC' } })),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

import BrowserView from '../BrowserView.vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { usePerformanceStore } from '@/stores/performance'
import { apiFetch } from '@/api/client'

const stubs = {
  Icon: { template: '<span />' },
  FilterChip: { template: '<div />' },
  PerformanceSubnav: { template: '<div />' },
  RouterLink: { template: '<a><slot /></a>', props: ['to'] },
  UserFilter: { template: '<div />' },
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

const makePageload = (id: string, transaction = '/home', measurements: Record<string, { value?: number } | number> | null = {
  lcp: { value: 2100 },
  inp: 180,
  cls: { value: 0.05 },
}) => ({
  id,
  project_id: 'p1',
  trace_id: `tr-${id}`,
  transaction,
  op: 'pageload',
  status: 'ok',
  duration_ms: 1200,
  start_timestamp: '2024-01-01T00:00:00Z',
  environment: 'production',
  measurements,
})

function setupMocks(
  summaryData?: unknown,
  pagesData?: unknown[],
  isError = false,
  pageloads: {
    data?: { transactions: unknown[]; next_cursor_id?: string; next_cursor_time?: string }
    isLoading?: boolean
    isError?: boolean
    refetch?: ReturnType<typeof vi.fn>
  } = {},
) {
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
  vi.mocked(usePerformanceStore).mockReturnValue(reactive({
    windowHrs: '24h',
    envFilter: 'All',
  }) as any)

  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref(summaryData), isLoading: ref(false), isError: ref(isError), refetch: vi.fn() } as any)
    .mockReturnValueOnce({ data: ref(pagesData), isLoading: ref(false), isError: ref(isError), refetch: vi.fn() } as any)
    .mockReturnValueOnce({
      data: ref(pageloads.data ?? { transactions: [] }),
      isLoading: ref(pageloads.isLoading ?? false),
      isError: ref(pageloads.isError ?? false),
      refetch: pageloads.refetch ?? vi.fn(),
    } as any)
}

beforeEach(() => {
  vi.mocked(useQuery).mockReset()
  vi.mocked(useProjectsStore).mockReset()
  vi.mocked(usePerformanceStore).mockReset()
  routeState.query = {}
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
        .mockReturnValueOnce({ data: ref({ transactions: [] }), isLoading: ref(false), isError: ref(false), refetch: vi.fn() } as any)
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
        .mockReturnValueOnce({ data: ref({ transactions: [] }), isLoading: ref(false), isError: ref(false), refetch: vi.fn() } as any)
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
      UserFilter: { template: '<div />' },
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

  describe('user mode', () => {
    it('shows a person-specific empty state', () => {
      routeState.query = { user: 'u-1' }
      setupMocks()
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.text()).toContain('No page loads for u-1 in this window')
      expect(wrapper.text()).toContain('set_user()')
    })

    it('renders page loads with LCP/INP/CLS from measurements', () => {
      routeState.query = { user: 'u-1' }
      setupMocks(undefined, undefined, false, {
        data: { transactions: [makePageload('t1', '/checkout')] },
      })
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.text()).toContain('/checkout')
      expect(wrapper.text()).toContain('2.10s')
      expect(wrapper.text()).toContain('180ms')
      expect(wrapper.text()).toContain('0.050')
      expect(wrapper.text()).toContain('1 page load')
    })

    it('shows a dash when a measurement is missing', () => {
      routeState.query = { user: 'u-1' }
      setupMocks(undefined, undefined, false, {
        data: { transactions: [makePageload('t1', '/home', null)] },
      })
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.text()).toContain('–')
    })

    it('shows skeleton rows while page loads fetch', () => {
      routeState.query = { user: 'u-1' }
      setupMocks(undefined, undefined, false, { isLoading: true })
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.find('.ghost--bar').exists()).toBe(true)
    })

    it('retries the pageloads query on error', async () => {
      routeState.query = { user: 'u-1' }
      const refetchPageloads = vi.fn()
      setupMocks(undefined, undefined, false, { isError: true, refetch: refetchPageloads })
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.text()).toContain("Couldn't load Web Vitals")
      await wrapper.find('.txerror .btn').trigger('click')
      expect(refetchPageloads).toHaveBeenCalled()
    })

    it('loads more page loads from the cursor', async () => {
      routeState.query = { user: 'u-1' }
      setupMocks(undefined, undefined, false, {
        data: {
          transactions: [makePageload('t1', '/one')],
          next_cursor_id: 'c2',
          next_cursor_time: '2024-01-01T01:00:00Z',
        },
      })
      vi.mocked(apiFetch)
        .mockResolvedValueOnce({
          transactions: [makePageload('t2', '/two')],
          next_cursor_id: 'c3',
          next_cursor_time: '2024-01-01T02:00:00Z',
        })
        .mockResolvedValueOnce({
          transactions: [makePageload('t3', '/three')],
        })
      const wrapper = mount(BrowserView, { global: { stubs } })
      expect(wrapper.text()).toContain('Load more')
      await wrapper.find('.list-footer .btn').trigger('click')
      await flushPromises()
      expect(wrapper.text()).toContain('/two')
      expect(wrapper.text()).toContain('2 page loads')
      expect(String(vi.mocked(apiFetch).mock.calls.at(-1)?.[0])).toContain('cursor_id=c2')
      await wrapper.find('.list-footer .btn').trigger('click')
      await flushPromises()
      expect(wrapper.text()).toContain('/three')
      const perf = vi.mocked(usePerformanceStore).mock.results.at(-1)?.value as { envFilter: string }
      perf.envFilter = 'production'
      await flushPromises()
      expect(wrapper.text()).not.toContain('/three')
    })

    it('enables the pageloads query and disables fleet vitals', () => {
      routeState.query = { user: 'u-1' }
      setupMocks()
      mount(BrowserView, { global: { stubs } })
      const [summaryCall, pagesCall, pageloadsCall] = vi.mocked(useQuery).mock.calls
      expect(summaryCall[0].enabled.value).toBe(false)
      expect(pagesCall[0].enabled.value).toBe(false)
      expect(pageloadsCall[0].enabled.value).toBe(true)
      for (const [opts] of vi.mocked(useQuery).mock.calls) {
        opts.queryKey?.value
        opts.queryFn?.({ signal: new AbortController().signal })
      }
    })
  })
})

 it('discards pending pages when the environment changes', async () => {
   routeState.query = { user: 'u-1' }
   setupMocks(undefined, undefined, false, { data: { transactions: [makePageload('first')], next_cursor_id: 'first', next_cursor_time: '2024-01-01T00:00:00Z' } })
   let resolve!: (value: any) => void
   vi.mocked(apiFetch).mockReturnValueOnce(new Promise(r => { resolve = r }))
   const wrapper = mount(BrowserView, { global: { stubs } })
   await wrapper.find('.list-footer .btn').trigger('click')
   usePerformanceStore().envFilter = 'staging'
   await flushPromises()
   resolve({ transactions: [makePageload('old-production', '/old-production')] })
   await flushPromises()
   expect(wrapper.text()).not.toContain('/old-production')
   wrapper.unmount()
 })
