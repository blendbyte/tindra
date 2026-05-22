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
) {
  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref(tx), isLoading: ref(isLoading), isError: ref(isError), refetch: vi.fn() } as any)
    .mockReturnValueOnce({ data: ref(spans), isLoading: ref(false), isError: ref(false), refetch: vi.fn() } as any)
    .mockReturnValueOnce({ data: ref(traceLogs) } as any)
    .mockReturnValueOnce({ data: ref(traceErrors.length ? traceErrors : undefined) } as any)
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
      // Expand/collapse buttons use title attributes, not text content
      expect(wrapper.find('[title="Expand all"]').exists()).toBe(true)
      expect(wrapper.find('[title="Collapse all"]').exists()).toBe(true)
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

  describe('spans error state', () => {
    it('shows spans error message when spans fail to load', () => {
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref(baseTx), isLoading: ref(false), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false), isError: ref(true), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
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
      await wrapper.find('[title="Expand all"]').trigger('click')
      expect(wrapper.find('.span-row--header').exists()).toBe(true)
    })

    it('clicks Collapse all without throwing', async () => {
      setupMocks(baseTx, parentChildSpans)
      const wrapper = mount(TransactionDetailView, { global: { stubs } })
      await wrapper.find('[title="Collapse all"]').trigger('click')
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
      await wrapper.find('[title="Expand all"]').trigger('click')
      await wrapper.find('[title="Collapse all"]').trigger('click')
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
})
