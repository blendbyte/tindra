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

import TransactionDetailView from '../TransactionDetailView.vue'
import { useQuery } from '@tanstack/vue-query'

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

function setupMocks(tx: unknown = baseTx, spans: unknown[] = [], isError = false, isLoading = false) {
  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref(tx), isLoading: ref(isLoading), isError: ref(isError), refetch: vi.fn() } as any)
    .mockReturnValueOnce({ data: ref(spans), isLoading: ref(false), isError: ref(false), refetch: vi.fn() } as any)
    .mockReturnValueOnce({ data: ref(undefined) } as any)
    .mockReturnValueOnce({ data: ref(undefined) } as any)
}

beforeEach(() => {
  vi.mocked(useQuery).mockReset()
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
})
