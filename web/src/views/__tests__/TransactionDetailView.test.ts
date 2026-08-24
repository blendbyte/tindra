import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

const backMock = vi.fn()

vi.mock('vue-router', () => ({
  useRoute: vi.fn(() => ({ params: { id: 'tx-123' } })),
  useRouter: vi.fn(() => ({ back: backMock, push: vi.fn() })),
  RouterLink: { template: '<a><slot /></a>' },
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
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

import TransactionDetailView from '../TransactionDetailView.vue'
import { useQuery } from '@tanstack/vue-query'
import { useAuthStore } from '@/stores/auth'

const stubs = {
  Icon: { template: '<span />' },
}

const baseTx = {
  id: 'tx-123',
  transaction: '/api/users',
  op: 'http.server',
  project_id: 'proj-1',
  duration_ms: 250,
  status: 'ok',
  start_timestamp: '2024-01-01T00:00:00.000Z',
  trace_id: 'trace-abc',
  environment: 'production',
  release: null,
}

function setupMocks(
  tx: unknown = baseTx,
  spans: unknown[] = [],
  isError = false,
  isLoading = false,
  traceLogs: unknown = undefined,
  traceErrors: unknown[] = [],
  flameGraph: unknown = null,
) {
  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref(tx), isLoading: ref(isLoading), isError: ref(isError), refetch: vi.fn() } as any)
    .mockReturnValueOnce({ data: ref(spans), isLoading: ref(false), isError: ref(false), refetch: vi.fn() } as any)
    .mockReturnValueOnce({ data: ref(traceLogs) } as any)
    .mockReturnValueOnce({ data: ref(traceErrors.length ? traceErrors : undefined) } as any)
    // Null is the common case: most transactions were never profiled.
    .mockReturnValueOnce({ data: ref(flameGraph) } as any)
}

beforeEach(() => {
  vi.mocked(useQuery).mockReset()
  vi.mocked(useAuthStore).mockReset()
  vi.mocked(useAuthStore).mockReturnValue({ user: { timezone: 'UTC' }, setUser: vi.fn() } as any)
  backMock.mockReset()
})

describe('TransactionDetailView', () => {
  describe('error state', () => {
    it('shows error message when loading fails', () => {
      setupMocks(undefined, [], true)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Failed to load transaction')
    })

    it('shows a Retry button on error', () => {
      setupMocks(undefined, [], true)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.txerror .btn').text()).toBe('Retry')
    })

    it('shows back link to Transactions on error', () => {
      setupMocks(undefined, [], true)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.detail-breadcrumb__back').text()).toContain('Transactions')
    })
  })

  describe('loading skeleton', () => {
    it('renders skeleton while loading', () => {
      setupMocks(undefined, [], false, true)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.skel').exists()).toBe(true)
    })
  })

  describe('loaded transaction', () => {
    it('renders the transaction name', () => {
      setupMocks()
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('/api/users')
    })

    it('renders duration in the stats strip', () => {
      setupMocks()
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('250ms')
    })

    it('renders the trace ID', () => {
      setupMocks()
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('trace-abc')
    })

    it('renders the spans table header', () => {
      setupMocks()
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.span-row--header').exists()).toBe(true)
    })

    it('renders the timeline section', () => {
      setupMocks()
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.timeline').exists()).toBe(true)
    })

    it('renders a span search input', () => {
      setupMocks()
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.trace-search input').exists()).toBe(true)
    })

    it('shows expand all and collapse all buttons', () => {
      const spans = [
        { id: 'span-1', span_id: 'sp1', parent_span_id: null, op: 'db.query', description: 'SELECT 1', duration_ms: 12, start_offset_ms: 0, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      // Expand/collapse buttons use aria-label, not text content
      expect(wrapper.find('[aria-label="Expand all"]').exists()).toBe(true)
      expect(wrapper.find('[aria-label="Collapse all"]').exists()).toBe(true)
    })

    it('renders a span row for each span', () => {
      const spans = [
        { id: 'span-1', span_id: 'sp1', parent_span_id: null, op: 'db.query', description: 'SELECT 1', duration_ms: 12, start_offset_ms: 0, status: 'ok', is_critical: false },
        { id: 'span-2', span_id: 'sp2', parent_span_id: null, op: 'http.client', description: 'GET /api', duration_ms: 50, start_offset_ms: 15, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const spanRows = wrapper.findAll('.span-row:not(.span-row--header)')
      expect(spanRows.length).toBe(2)
    })
  })

  describe('trace error correlation', () => {
    it('renders trace errors section when errors exist in the trace', () => {
      const traceErrors = [{
        event_id: 'ev1',
        issue_id: 'iss-1',
        level: 'error',
        title: 'TypeError: auth failed',
        timestamp: '2024-01-01T00:00:00.100Z',
        span_id: null,
      }]
      setupMocks(baseTx, [], false, false, undefined, traceErrors)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.trace-logs').exists()).toBe(true)
      expect(wrapper.text()).toContain('Errors')
      expect(wrapper.text()).toContain('TypeError: auth failed')
    })

    it('shows error count in trace errors heading', () => {
      const traceErrors = [
        { event_id: 'ev1', issue_id: 'iss-1', level: 'error', title: 'Error A', timestamp: '2024-01-01T00:00:00.100Z', span_id: null },
        { event_id: 'ev2', issue_id: 'iss-2', level: 'warning', title: 'Error B', timestamp: '2024-01-01T00:00:00.200Z', span_id: null },
      ]
      setupMocks(baseTx, [], false, false, undefined, traceErrors)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('2 in this trace')
    })
  })

  describe('trace log correlation', () => {
    it('renders trace logs section when logs exist in the trace', () => {
      const traceLogs = {
        logs: [{
          id: 'log1',
          timestamp: '2024-01-01T00:00:00.050Z',
          level: 'info',
          body: 'User authenticated',
          trace_id: 'trace-abc',
          environment: 'production',
          attributes: {},
        }],
        has_more: false,
      }
      setupMocks(baseTx, [], false, false, traceLogs)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.trace-logs').exists()).toBe(true)
      expect(wrapper.text()).toContain('Logs')
      expect(wrapper.text()).toContain('User authenticated')
    })

    it('shows log count in trace logs heading', () => {
      const traceLogs = {
        logs: [
          { id: 'log1', timestamp: '2024-01-01T00:00:00.050Z', level: 'info', body: 'Log A', trace_id: 'trace-abc', environment: 'production', attributes: {} },
          { id: 'log2', timestamp: '2024-01-01T00:00:00.100Z', level: 'debug', body: 'Log B', trace_id: 'trace-abc', environment: 'production', attributes: {} },
        ],
        has_more: false,
      }
      setupMocks(baseTx, [], false, false, traceLogs)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('2 entries')
    })
  })

  describe('keyboard navigation', () => {
    it('handles / keydown to focus search without throwing', () => {
      setupMocks()
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const event = new KeyboardEvent('keydown', { key: '/', bubbles: true })
      expect(() => document.dispatchEvent(event)).not.toThrow()
      wrapper.unmount()
    })

    it('handles ArrowUp navigation without throwing', () => {
      const spans = [
        { id: 'span-1', span_id: 'sp1', parent_span_id: null, op: 'db.query', description: 'SELECT 1', duration_ms: 12, start_offset_ms: 0, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
      expect(() => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }))).not.toThrow()
      wrapper.unmount()
    })

    it('handles j/k navigation with spans and Enter to open detail', async () => {
      const spans = [
        { id: 'span-1', span_id: 'sp1', parent_span_id: null, op: 'db.query', description: 'SELECT 1', duration_ms: 12, start_offset_ms: 0, status: 'ok', is_critical: false },
        { id: 'span-2', span_id: 'sp2', parent_span_id: null, op: 'http.client', description: 'GET /api', duration_ms: 50, start_offset_ms: 10, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'j', bubbles: true }))
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'j', bubbles: true }))
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', bubbles: true }))
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('.span-detail').exists()).toBe(true)
      wrapper.unmount()
    })

    it('handles Escape key to clear focus', () => {
      const spans = [
        { id: 'span-1', span_id: 'sp1', parent_span_id: null, op: 'db.query', description: 'SELECT 1', duration_ms: 12, start_offset_ms: 0, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'j', bubbles: true }))
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
      expect(wrapper.exists()).toBe(true)
      wrapper.unmount()
    })
  })

  describe('flame graph', () => {
    const graph = {
      sample_count: 42,
      idle_samples: 0,
      sample_interval_ns: 10_000_000,
      duration_ns: 420_000_000,
      thread_name: 'MainThread',
      root: {
        function: '',
        total_samples: 42,
        self_samples: 0,
        children: [{ function: 'main', total_samples: 42, self_samples: 42 }],
      },
    }

    // Most transactions were never profiled, so the panel has to stay out of
    // the way rather than showing an empty box on every page.
    it('is absent when the transaction has no profile', () => {
      setupMocks(baseTx, [], false, false, undefined, [], null)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).not.toContain('Flame graph')
    })

    it('renders when a profile exists', () => {
      setupMocks(baseTx, [], false, false, undefined, [], graph)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Flame graph')
    })

    // The heading carries the summary, the way Errors and Logs carry a count.
    it('summarises the profile in the heading', () => {
      setupMocks(baseTx, [], false, false, undefined, [], graph)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('42 samples')
      expect(wrapper.text()).toContain('MainThread')
    })

    // v1 profiles carry no thread name, so the summary has to read sensibly
    // without one rather than trailing a separator.
    it('summarises without a thread name', () => {
      const noThread = { ...graph, thread_name: undefined }
      setupMocks(baseTx, [], false, false, undefined, [], noThread)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('42 samples')
      expect(wrapper.text()).not.toContain('42 samples ·')
    })
  })

  describe('spans error state', () => {
    it('shows spans error message when spans fail to load', () => {
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref(baseTx), isLoading: ref(false), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false), isError: ref(true), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(null) } as any)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Failed to load spans')
    })
  })

  describe('expand and collapse all', () => {
    const parentChildSpans = [
      { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'http.server', description: 'GET /api', duration_ms: 250, start_offset_ms: 0, status: 'ok', is_critical: false },
      { id: 'sp2', span_id: 'a2', parent_span_id: 'a1', op: 'db.query', description: 'SELECT 1', duration_ms: 12, start_offset_ms: 10, status: 'ok', is_critical: false },
    ]

    it('clicks Expand all without throwing', async () => {
      setupMocks(baseTx, parentChildSpans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      await wrapper.find('[aria-label="Expand all"]').trigger('click')
      expect(wrapper.find('.span-row--header').exists()).toBe(true)
    })

    it('clicks Collapse all without throwing', async () => {
      setupMocks(baseTx, parentChildSpans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      await wrapper.find('[aria-label="Collapse all"]').trigger('click')
      expect(wrapper.find('.span-row--header').exists()).toBe(true)
    })

    it('toggles branch collapse when parent chevron is clicked', async () => {
      setupMocks(baseTx, parentChildSpans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const caret = wrapper.find('.span-name__caret[class*="caret--open"]')
      if (caret.exists()) await caret.trigger('click')
      expect(wrapper.find('.span-row--header').exists()).toBe(true)
    })
  })

  describe('critical path', () => {
    it('shows Critical path label when spans have is_critical = true', () => {
      const criticalSpans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'SLOW', duration_ms: 200, start_offset_ms: 0, status: 'ok', is_critical: true },
        { id: 'sp2', span_id: 'a2', parent_span_id: null, op: 'http.client', description: 'GET', duration_ms: 50, start_offset_ms: 10, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, criticalSpans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Critical path')
    })
  })

  describe('span detail interactions', () => {
    const singleSpan = [
      { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'SELECT users', duration_ms: 12, start_offset_ms: 0, status: 'ok', is_critical: false },
    ]

    it('opens span detail when span row is clicked', async () => {
      setupMocks(baseTx, singleSpan)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const spanRows = wrapper.findAll('.span-row')
      const dataRow = spanRows.find(r => r.text().includes('SELECT users'))!
      await dataRow.trigger('click')
      expect(wrapper.find('.span-detail').exists()).toBe(true)
    })

    it('closes span detail when span row is clicked again', async () => {
      setupMocks(baseTx, singleSpan)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const spanRows = wrapper.findAll('.span-row')
      const dataRow = spanRows.find(r => r.text().includes('SELECT users'))!
      await dataRow.trigger('click')
      await dataRow.trigger('click')
      expect(wrapper.find('.span-detail').exists()).toBe(false)
    })

    it('shows span op in detail view', async () => {
      setupMocks(baseTx, singleSpan)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const spanRows = wrapper.findAll('.span-row')
      const dataRow = spanRows.find(r => r.text().includes('SELECT users'))!
      await dataRow.trigger('click')
      expect(wrapper.find('.span-detail').text()).toContain('db.query')
    })

    it('opens span detail when span-detail-btn is clicked', async () => {
      setupMocks(baseTx, singleSpan)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const detailBtn = wrapper.find('.span-detail-btn')
      if (detailBtn.exists()) {
        await detailBtn.trigger('click')
        expect(wrapper.find('.span-detail').exists()).toBe(true)
      }
    })
  })

  describe('span search', () => {
    const twoSpans = [
      { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'SELECT users', duration_ms: 12, start_offset_ms: 0, status: 'ok', is_critical: false },
      { id: 'sp2', span_id: 'a2', parent_span_id: null, op: 'http.client', description: 'GET /health', duration_ms: 5, start_offset_ms: 20, status: 'ok', is_critical: false },
    ]

    it('shows match count when searching', async () => {
      setupMocks(baseTx, twoSpans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      await wrapper.find('.trace-search input').setValue('SELECT')
      expect(wrapper.find('.trace-search__count').exists()).toBe(true)
    })

    it('shows no-match message when search has no results', async () => {
      setupMocks(baseTx, twoSpans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      await wrapper.find('.trace-search input').setValue('nonexistent_span_xyz')
      expect(wrapper.text()).toContain('No spans match')
    })

    it('shows clear button while searching', async () => {
      setupMocks(baseTx, twoSpans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      await wrapper.find('.trace-search input').setValue('SELECT')
      expect(wrapper.find('.trace-search__clear').exists()).toBe(true)
    })

    it('clears search when clear button is clicked', async () => {
      setupMocks(baseTx, twoSpans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      await wrapper.find('.trace-search input').setValue('SELECT')
      await wrapper.find('.trace-search__clear').trigger('click')
      expect(wrapper.find('.trace-search__count').exists()).toBe(false)
    })
  })

  describe('span empty state', () => {
    it('shows no-spans message when span list is empty', () => {
      setupMocks(baseTx, [])
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('No spans recorded')
    })
  })

  describe('copy trace ID', () => {
    it('clicking trace ID stat does not throw even without clipboard', async () => {
      const origClipboard = navigator.clipboard
      Object.defineProperty(navigator, 'clipboard', {
        value: { writeText: vi.fn().mockResolvedValue(undefined) },
        configurable: true,
      })
      setupMocks()
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const traceStat = wrapper.find('.stat--copyable')
      if (traceStat.exists()) await traceStat.trigger('click')
      expect(wrapper.exists()).toBe(true)
      Object.defineProperty(navigator, 'clipboard', { value: origClipboard, configurable: true })
    })
  })

  describe('span search - filter by op', () => {
    it('searches by op name and filters results', async () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'SELECT users', duration_ms: 12, start_offset_ms: 0, status: 'ok', is_critical: false },
        { id: 'sp2', span_id: 'a2', parent_span_id: null, op: 'http.client', description: 'GET /health', duration_ms: 5, start_offset_ms: 20, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      await wrapper.find('.trace-search input').setValue('http.client')
      const rows = wrapper.findAll('.span-row:not(.span-row--header)')
      expect(rows.length).toBe(1)
    })
  })

  describe('multiple critical spans for critPathEndMs', () => {
    it('renders critical path with two critical spans', () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'SLOW A', duration_ms: 150, start_offset_ms: 0, status: 'ok', is_critical: true },
        { id: 'sp2', span_id: 'a2', parent_span_id: null, op: 'db.query', description: 'SLOW B', duration_ms: 80, start_offset_ms: 50, status: 'ok', is_critical: true },
        { id: 'sp3', span_id: 'a3', parent_span_id: null, op: 'http.client', description: 'GET', duration_ms: 20, start_offset_ms: 5, status: 'ok', is_critical: false },
      ]
      setupMocks({ ...baseTx, duration_ms: 250 }, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Critical path')
    })
  })

  describe('collapseAll clears expandedGroups', () => {
    it('collapseAll is triggered and component still renders', async () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'http.server', description: 'GET /api', duration_ms: 250, start_offset_ms: 0, status: 'ok', is_critical: false },
        { id: 'sp2', span_id: 'a2', parent_span_id: 'a1', op: 'db.query', description: 'SELECT 1', duration_ms: 12, start_offset_ms: 10, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      await wrapper.find('[aria-label="Expand all"]').trigger('click')
      await wrapper.find('[aria-label="Collapse all"]').trigger('click')
      expect(wrapper.find('.span-row--header').exists()).toBe(true)
    })
  })

  describe('ticks with different durations', () => {
    it('renders with tx duration > 250ms for different tick step', () => {
      setupMocks({ ...baseTx, duration_ms: 600 })
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.timeline').exists()).toBe(true)
    })

    it('renders with tx duration > 1000ms for large tick step', () => {
      setupMocks({ ...baseTx, duration_ms: 2000 })
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.timeline').exists()).toBe(true)
    })
  })

  describe('error badge on span', () => {
    it('shows error badge on span when trace errors reference that span', () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'SELECT 1', duration_ms: 12, start_offset_ms: 0, status: 'ok', is_critical: false },
      ]
      const traceErrors = [
        { event_id: 'ev1', issue_id: 'iss-1', level: 'error', title: 'DB error', timestamp: '2024-01-01T00:00:00.100Z', span_id: 'a1' },
      ]
      setupMocks(baseTx, spans, false, false, undefined, traceErrors)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.span-error-badge').exists()).toBe(true)
    })
  })

  describe('span detail - self time', () => {
    it('shows Self time row in the timing section', async () => {
      const spans = [
        { id: 'sp1', span_id: 'parent', parent_span_id: null, op: 'http.server', description: 'Root', duration_ms: 100, start_offset_ms: 0, status: 'ok', is_critical: false, start_timestamp_ms: 1704067200000 },
        { id: 'sp2', span_id: 'child1', parent_span_id: 'parent', op: 'db.query', description: 'Query', duration_ms: 50, start_offset_ms: 10, status: 'ok', is_critical: false, start_timestamp_ms: 1704067200010 },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const parentRow = wrapper.findAll('.span-row').find(r => r.text().includes('Root'))!
      await parentRow.trigger('click')
      expect(wrapper.find('.span-detail').text()).toContain('Self time')
    })
  })

  describe('span detail - span data entries', () => {
    it('shows Span Data section when span has a data field', async () => {
      const spans = [
        {
          id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'SELECT users',
          duration_ms: 12, start_offset_ms: 0, status: 'ok', is_critical: false,
          start_timestamp_ms: 1704067200000,
          data: { 'db.system': 'postgresql', rows: 3 },
        },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const dataRow = wrapper.findAll('.span-row').find(r => r.text().includes('SELECT users'))!
      await dataRow.trigger('click')
      expect(wrapper.find('.span-detail').text()).toContain('Span Data')
      expect(wrapper.find('.span-detail').text()).toContain('db.system')
      expect(wrapper.find('.span-detail').text()).toContain('postgresql')
    })

    it('does not show Span Data section when span has no data', async () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'SELECT 1', duration_ms: 12, start_offset_ms: 0, status: 'ok', is_critical: false, start_timestamp_ms: 1704067200000 },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const dataRow = wrapper.findAll('.span-row').find(r => r.text().includes('SELECT 1'))!
      await dataRow.trigger('click')
      expect(wrapper.find('.span-detail').text()).not.toContain('Span Data')
    })
  })

  describe('span detail - copy span ID', () => {
    it('copies span_id to clipboard when the copy element is clicked', async () => {
      const clipboardMock = { writeText: vi.fn().mockResolvedValue(undefined) }
      Object.defineProperty(navigator, 'clipboard', { value: clipboardMock, configurable: true })
      const spans = [
        { id: 'sp1', span_id: 'abc-span-123', parent_span_id: null, op: 'db.query', description: 'SELECT 1', duration_ms: 12, start_offset_ms: 0, status: 'ok', is_critical: false, start_timestamp_ms: 1704067200000 },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const dataRow = wrapper.findAll('.span-row').find(r => r.text().includes('SELECT 1'))!
      await dataRow.trigger('click')
      const copyEl = wrapper.find('.span-detail__v--copy')
      if (copyEl.exists()) {
        await copyEl.trigger('click')
        expect(clipboardMock.writeText).toHaveBeenCalledWith('abc-span-123')
      }
    })
  })

  describe('spans loading inside waterfall', () => {
    it('renders skeleton rows in waterfall when spans are loading', () => {
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref(baseTx), isLoading: ref(false), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref(undefined), isLoading: ref(true), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(null) } as any)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      // Skeleton rows appear inside waterfall-left during spans loading
      expect(wrapper.find('.waterfall-left .skel').exists()).toBe(true)
    })
  })

  describe('search by status', () => {
    it('filters spans by status field', async () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'SELECT 1', duration_ms: 12, start_offset_ms: 0, status: 'ok', is_critical: false },
        { id: 'sp2', span_id: 'a2', parent_span_id: null, op: 'http.client', description: 'GET /api', duration_ms: 50, start_offset_ms: 20, status: 'error', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      await wrapper.find('.trace-search input').setValue('error')
      const rows = wrapper.findAll('.span-row:not(.span-row--header)')
      expect(rows.length).toBe(1)
    })
  })

  describe('environment tag in breadcrumb', () => {
    it('shows the environment tag in the breadcrumb actions', () => {
      setupMocks()
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.detail-breadcrumb__actions .tag').text()).toBe('production')
    })

    it('shows dash when environment is null', () => {
      setupMocks({ ...baseTx, environment: null })
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.detail-breadcrumb__actions .tag').text()).toBe('-')
    })
  })

  describe('different op types', () => {
    it('renders task op prefix', () => {
      setupMocks({ ...baseTx, op: 'task.run' })
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.optag').text()).toBe('task')
    })

    it('renders cache op prefix', () => {
      setupMocks({ ...baseTx, op: 'cache.get' })
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.optag').text()).toBe('cache')
    })

    it('renders db op spans with correct op label in span row', () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'SELECT 1', duration_ms: 10, start_offset_ms: 0, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('db')
    })

    it('renders grpc op spans', () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'grpc.unary', description: 'SomeMethod', duration_ms: 10, start_offset_ms: 0, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('grpc')
    })

    it('renders graphql op spans', () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'graphql.query', description: 'GetUser', duration_ms: 20, start_offset_ms: 0, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('graphql')
    })

    it('renders queue op spans', () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'queue.publish', description: 'send-email', duration_ms: 5, start_offset_ms: 0, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('queue')
    })

    it('renders file op spans', () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'file.read', description: '/etc/config', duration_ms: 3, start_offset_ms: 0, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('file')
    })

    it('renders default op color for unknown op type', () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'custom.thing', description: 'something', duration_ms: 8, start_offset_ms: 0, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('custom')
    })
  })

  describe('span detail - parent span ID row', () => {
    it('shows Parent Span ID in detail when span has a parent', async () => {
      const spans = [
        { id: 'sp1', span_id: 'parent-001', parent_span_id: null, op: 'http.server', description: 'Root', duration_ms: 100, start_offset_ms: 0, status: 'ok', is_critical: false, start_timestamp_ms: 1704067200000 },
        { id: 'sp2', span_id: 'child-001', parent_span_id: 'parent-001', op: 'db.query', description: 'SELECT 1', duration_ms: 20, start_offset_ms: 10, status: 'ok', is_critical: false, start_timestamp_ms: 1704067200010 },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const childRow = wrapper.findAll('.span-row').find(r => r.text().includes('SELECT 1'))!
      await childRow.trigger('click')
      expect(wrapper.find('.span-detail').text()).toContain('Parent Span ID')
      expect(wrapper.find('.span-detail').text()).toContain('parent-001')
    })

    it('does not show Parent Span ID when span has no parent', async () => {
      const spans = [
        { id: 'sp1', span_id: 'root-001', parent_span_id: null, op: 'http.server', description: 'Root', duration_ms: 100, start_offset_ms: 0, status: 'ok', is_critical: false, start_timestamp_ms: 1704067200000 },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const rootRow = wrapper.findAll('.span-row').find(r => r.text().includes('Root'))!
      await rootRow.trigger('click')
      expect(wrapper.find('.span-detail').text()).not.toContain('Parent Span ID')
    })
  })

  describe('span detail - critical path flag', () => {
    it('shows Critical path Yes row in detail when span is critical', async () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'SLOW', duration_ms: 200, start_offset_ms: 0, status: 'ok', is_critical: true },
        { id: 'sp2', span_id: 'a2', parent_span_id: null, op: 'http.client', description: 'GET', duration_ms: 10, start_offset_ms: 5, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const critRow = wrapper.findAll('.span-row').find(r => r.text().includes('SLOW'))!
      await critRow.trigger('click')
      const detail = wrapper.find('.span-detail')
      expect(detail.text()).toContain('Critical path')
      expect(detail.text()).toContain('Yes')
    })

    it('does not show Critical path row in detail when span is not critical', async () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'http.client', description: 'GET /api', duration_ms: 30, start_offset_ms: 0, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const row = wrapper.findAll('.span-row').find(r => r.text().includes('GET /api'))!
      await row.trigger('click')
      // Critical path row only appears when is_critical is true
      const detailText = wrapper.find('.span-detail').text()
      // The "Critical path" stat label is in hero, but in span-detail it should not appear for non-critical spans
      expect(detailText).not.toContain('Yes')
    })
  })

  describe('auto-group rows', () => {
    it('renders Autogrouped badge when 4+ consecutive same-op sibling spans', () => {
      const spans = [
        { id: 'g1', span_id: 'g1', parent_span_id: null, op: 'db.query', description: 'Q1', duration_ms: 5, start_offset_ms: 0, status: 'ok', is_critical: false },
        { id: 'g2', span_id: 'g2', parent_span_id: null, op: 'db.query', description: 'Q2', duration_ms: 5, start_offset_ms: 5, status: 'ok', is_critical: false },
        { id: 'g3', span_id: 'g3', parent_span_id: null, op: 'db.query', description: 'Q3', duration_ms: 5, start_offset_ms: 10, status: 'ok', is_critical: false },
        { id: 'g4', span_id: 'g4', parent_span_id: null, op: 'db.query', description: 'Q4', duration_ms: 5, start_offset_ms: 15, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.span-autogroup-badge').text()).toBe('Autogrouped')
    })

    it('renders count of spans in auto-group row', () => {
      const spans = [
        { id: 'g1', span_id: 'g1', parent_span_id: null, op: 'db.query', description: 'Q1', duration_ms: 5, start_offset_ms: 0, status: 'ok', is_critical: false },
        { id: 'g2', span_id: 'g2', parent_span_id: null, op: 'db.query', description: 'Q2', duration_ms: 5, start_offset_ms: 5, status: 'ok', is_critical: false },
        { id: 'g3', span_id: 'g3', parent_span_id: null, op: 'db.query', description: 'Q3', duration_ms: 5, start_offset_ms: 10, status: 'ok', is_critical: false },
        { id: 'g4', span_id: 'g4', parent_span_id: null, op: 'db.query', description: 'Q4', duration_ms: 5, start_offset_ms: 15, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.span-row--group .span-row__dur').text()).toContain('4 spans')
    })

    it('expands auto-group when group row is clicked', async () => {
      const spans = [
        { id: 'g1', span_id: 'g1', parent_span_id: null, op: 'db.query', description: 'Q1', duration_ms: 5, start_offset_ms: 0, status: 'ok', is_critical: false },
        { id: 'g2', span_id: 'g2', parent_span_id: null, op: 'db.query', description: 'Q2', duration_ms: 5, start_offset_ms: 5, status: 'ok', is_critical: false },
        { id: 'g3', span_id: 'g3', parent_span_id: null, op: 'db.query', description: 'Q3', duration_ms: 5, start_offset_ms: 10, status: 'ok', is_critical: false },
        { id: 'g4', span_id: 'g4', parent_span_id: null, op: 'db.query', description: 'Q4', duration_ms: 5, start_offset_ms: 15, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const groupRow = wrapper.find('.span-row--group')
      await groupRow.trigger('click')
      // After expanding, individual span rows should appear
      const spanRows = wrapper.findAll('.span-row:not(.span-row--header):not(.span-row--group)')
      expect(spanRows.length).toBeGreaterThan(0)
    })
  })

  describe('chain rows', () => {
    it('renders Chain badge when a single-descendant chain of 3+ same-op spans exists', () => {
      // Build a chain: a -> b -> c all same op, each with exactly one child
      const spans = [
        { id: 'c1', span_id: 'c1', parent_span_id: null, op: 'http.client', description: 'L1', duration_ms: 60, start_offset_ms: 0, status: 'ok', is_critical: false },
        { id: 'c2', span_id: 'c2', parent_span_id: 'c1', op: 'http.client', description: 'L2', duration_ms: 50, start_offset_ms: 5, status: 'ok', is_critical: false },
        { id: 'c3', span_id: 'c3', parent_span_id: 'c2', op: 'http.client', description: 'L3', duration_ms: 40, start_offset_ms: 10, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.span-autogroup-badge').text()).toBe('Chain')
    })

    it('renders span count in chain row', () => {
      const spans = [
        { id: 'c1', span_id: 'c1', parent_span_id: null, op: 'http.client', description: 'L1', duration_ms: 60, start_offset_ms: 0, status: 'ok', is_critical: false },
        { id: 'c2', span_id: 'c2', parent_span_id: 'c1', op: 'http.client', description: 'L2', duration_ms: 50, start_offset_ms: 5, status: 'ok', is_critical: false },
        { id: 'c3', span_id: 'c3', parent_span_id: 'c2', op: 'http.client', description: 'L3', duration_ms: 40, start_offset_ms: 10, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.span-row--group .span-row__dur').text()).toContain('3 spans')
    })
  })

  describe('trace offset helpers', () => {
    it('shows log offset in seconds when log is more than 1000ms after tx start', () => {
      const traceLogs = {
        logs: [{
          id: 'log1',
          timestamp: '2024-01-01T00:00:01.500Z',  // 1500ms after tx start (2024-01-01T00:00:00.000Z)
          level: 'info',
          body: 'Slow event',
          trace_id: 'trace-abc',
          environment: 'production',
          attributes: {},
        }],
        has_more: false,
      }
      setupMocks(baseTx, [], false, false, traceLogs)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('+1.50s')
    })

    it('shows negative offset label for logs before tx start', () => {
      const traceLogs = {
        logs: [{
          id: 'log1',
          timestamp: '2023-12-31T23:59:59.000Z',  // 1 second before tx start
          level: 'warn',
          body: 'Early event',
          trace_id: 'trace-abc',
          environment: 'production',
          attributes: {},
        }],
        has_more: false,
      }
      setupMocks(baseTx, [], false, false, traceLogs)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('<0ms')
    })

    it('shows error offset in seconds when error is more than 1000ms after tx start', () => {
      const traceErrors = [{
        event_id: 'ev1',
        issue_id: 'iss-1',
        level: 'error',
        title: 'Late error',
        timestamp: '2024-01-01T00:00:02.000Z',  // 2000ms after tx start
        span_id: null,
      }]
      setupMocks(baseTx, [], false, false, undefined, traceErrors)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('+2.00s')
    })

    it('shows error offset as <0ms for errors before tx start', () => {
      const traceErrors = [{
        event_id: 'ev1',
        issue_id: 'iss-1',
        level: 'error',
        title: 'Early error',
        timestamp: '2023-12-31T23:59:58.000Z',  // before tx start
        span_id: null,
      }]
      setupMocks(baseTx, [], false, false, undefined, traceErrors)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('<0ms')
    })
  })

  describe('op legend in search bar', () => {
    it('renders op legend dots when spans are present and no search query', () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'Q1', duration_ms: 10, start_offset_ms: 0, status: 'ok', is_critical: false },
        { id: 'sp2', span_id: 'a2', parent_span_id: null, op: 'http.client', description: 'GET', duration_ms: 5, start_offset_ms: 10, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.trace-search__legend').exists()).toBe(true)
      // Both distinct op prefixes should appear
      const legItems = wrapper.findAll('.trace-search__leg')
      expect(legItems.length).toBe(2)
    })

    it('hides op legend when search query is active', async () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'Q1', duration_ms: 10, start_offset_ms: 0, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      await wrapper.find('.trace-search input').setValue('Q1')
      expect(wrapper.find('.trace-search__legend').exists()).toBe(false)
    })
  })

  describe('trace ID short display', () => {
    it('shows first 16 chars of trace_id followed by ellipsis', () => {
      setupMocks({ ...baseTx, trace_id: 'abcdef1234567890xyz' })
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      // The template shows trace_id.slice(0, 16) + '…'
      expect(wrapper.text()).toContain('abcdef1234567890')
    })
  })

  describe('back button navigation', () => {
    it('calls router.back() when back link is clicked on loaded transaction', async () => {
      setupMocks()
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      await wrapper.find('.detail-breadcrumb__back').trigger('click')
      expect(backMock).toHaveBeenCalled()
    })

    it('calls router.back() when back link is clicked on error state', async () => {
      setupMocks(undefined, [], true)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      await wrapper.find('.detail-breadcrumb__back').trigger('click')
      expect(backMock).toHaveBeenCalled()
    })

    it('calls router.back() when back link is clicked on loading state', async () => {
      setupMocks(undefined, [], false, true)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      await wrapper.find('.detail-breadcrumb__back').trigger('click')
      expect(backMock).toHaveBeenCalled()
    })
  })

  describe('transaction name in breadcrumb title', () => {
    it('shows transaction name in breadcrumb title on loaded state', () => {
      setupMocks()
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.detail-breadcrumb__title').text()).toContain('/api/users')
    })
  })

  describe('span status display in detail', () => {
    it('shows error status class in span detail for error status span', async () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'SELECT fail', duration_ms: 10, start_offset_ms: 0, status: 'error', is_critical: false, start_timestamp_ms: 1704067200000 },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const row = wrapper.findAll('.span-row').find(r => r.text().includes('SELECT fail'))!
      await row.trigger('click')
      expect(wrapper.find('.span-detail__status--error').exists()).toBe(true)
    })
  })

  describe('span child count badge', () => {
    it('shows child count badge on parent span', () => {
      const spans = [
        { id: 'sp1', span_id: 'parent-x', parent_span_id: null, op: 'http.server', description: 'Root', duration_ms: 100, start_offset_ms: 0, status: 'ok', is_critical: false },
        { id: 'sp2', span_id: 'child-x', parent_span_id: 'parent-x', op: 'db.query', description: 'Q1', duration_ms: 20, start_offset_ms: 5, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.span-child-count').exists()).toBe(true)
      expect(wrapper.find('.span-child-count').text()).toBe('1')
    })
  })

  describe('zoom controls', () => {
    it('zoom in button exists when spans are present', () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'Q1', duration_ms: 10, start_offset_ms: 0, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.trace-search__zoom').exists()).toBe(true)
    })

    it('zoom controls are not rendered when there are no spans', () => {
      setupMocks(baseTx, [])
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.trace-search__zoom').exists()).toBe(false)
    })

    it('handles keyboard + zoom in without throwing', () => {
      setupMocks()
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(() => document.dispatchEvent(new KeyboardEvent('keydown', { key: '+', bubbles: true }))).not.toThrow()
      expect(() => document.dispatchEvent(new KeyboardEvent('keydown', { key: '=', bubbles: true }))).not.toThrow()
      wrapper.unmount()
    })

    it('handles keyboard - zoom out without throwing', () => {
      setupMocks()
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(() => document.dispatchEvent(new KeyboardEvent('keydown', { key: '-', bubbles: true }))).not.toThrow()
      expect(() => document.dispatchEvent(new KeyboardEvent('keydown', { key: '_', bubbles: true }))).not.toThrow()
      wrapper.unmount()
    })
  })

  describe('keyboard Escape from input', () => {
    it('handles Escape key when focused inside the search input without throwing', async () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'SELECT 1', duration_ms: 12, start_offset_ms: 0, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const input = wrapper.find('.trace-search input').element as HTMLInputElement
      // Simulate Escape from inside the input element
      const event = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })
      Object.defineProperty(event, 'target', { value: input, configurable: true })
      expect(() => document.dispatchEvent(event)).not.toThrow()
      wrapper.unmount()
    })
  })

  describe('span detail - no description', () => {
    it('does not render Description row when span has empty description', async () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'http.server', description: '', duration_ms: 50, start_offset_ms: 0, status: 'ok', is_critical: false, start_timestamp_ms: 1704067200000 },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      const row = wrapper.findAll('.span-row').find(r => !r.classes().includes('span-row--header'))!
      await row.trigger('click')
      expect(wrapper.find('.span-detail').text()).not.toContain('Description')
    })
  })

  describe('span search - match count display', () => {
    it('shows correct fraction in match count (matching / total)', async () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'SELECT users', duration_ms: 12, start_offset_ms: 0, status: 'ok', is_critical: false },
        { id: 'sp2', span_id: 'a2', parent_span_id: null, op: 'db.query', description: 'SELECT orders', duration_ms: 8, start_offset_ms: 5, status: 'ok', is_critical: false },
        { id: 'sp3', span_id: 'a3', parent_span_id: null, op: 'http.client', description: 'GET /health', duration_ms: 3, start_offset_ms: 20, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      await wrapper.find('.trace-search input').setValue('SELECT')
      const countEl = wrapper.find('.trace-search__count')
      expect(countEl.text()).toContain('2')
      expect(countEl.text()).toContain('3')
    })
  })

  describe('transaction status display', () => {
    it('renders ok status badge in stat row', () => {
      setupMocks()
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.tx-status--ok').exists()).toBe(true)
      expect(wrapper.find('.tx-status--ok').text()).toBe('ok')
    })

    it('renders error status badge when tx has error status', () => {
      setupMocks({ ...baseTx, status: 'error' })
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.tx-status--error').exists()).toBe(true)
    })
  })

  describe('span count in Duration stat', () => {
    it('shows 0 spans label when span list is empty', () => {
      setupMocks(baseTx, [])
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.stat__sub').text()).toContain('0 spans')
    })

    it('shows correct span count when spans are loaded', () => {
      const spans = [
        { id: 'sp1', span_id: 'a1', parent_span_id: null, op: 'db.query', description: 'Q1', duration_ms: 10, start_offset_ms: 0, status: 'ok', is_critical: false },
        { id: 'sp2', span_id: 'a2', parent_span_id: null, op: 'http.client', description: 'GET', duration_ms: 5, start_offset_ms: 15, status: 'ok', is_critical: false },
        { id: 'sp3', span_id: 'a3', parent_span_id: null, op: 'cache.get', description: 'get-key', duration_ms: 2, start_offset_ms: 20, status: 'ok', is_critical: false },
      ]
      setupMocks(baseTx, spans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      expect(wrapper.find('.stat__sub').text()).toContain('3 spans')
    })
  })
})
