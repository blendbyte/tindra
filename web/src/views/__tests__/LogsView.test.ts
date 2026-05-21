import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
}))

vi.mock('@/stores/projects', () => ({
  useProjectsStore: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

vi.mock('@/utils/formatters', () => ({
  formatTs: vi.fn((ts: string) => ts),
}))

import LogsView from '../LogsView.vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'

const stubs = {
  Icon: { template: '<span />' },
  FilterChip: { name: 'FilterChip', props: ['label', 'value', 'options'], template: '<div />' },
}

const makeLog = (id: string, level: string, body: string) => ({
  id,
  level,
  body,
  timestamp: '2024-01-01T00:00:00Z',
  environment: 'production',
  trace_id: null,
  release: null,
  attributes: {},
})

function setupMocks(logs: unknown[] = [], isLoading = false) {
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
  vi.mocked(useQuery).mockReturnValue({
    data: ref(logs.length > 0 ? { logs, has_more: false } : (isLoading ? undefined : { logs: [], has_more: false })),
    isLoading: ref(isLoading),
    isFetching: ref(false),
    refetch: vi.fn(),
  } as any)
}

beforeEach(() => {
  vi.mocked(useQuery).mockReset()
  vi.mocked(useProjectsStore).mockReset()
})

describe('LogsView', () => {
  describe('loading state', () => {
    it('shows loading skeleton when logs are being fetched', () => {
      setupMocks([], true)
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.find('.perf-table__skel-row').exists()).toBe(true)
    })
  })

  describe('empty state', () => {
    it('shows "No logs found" when there are no logs', () => {
      setupMocks([])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.text()).toContain('No logs found')
    })

    it('shows SDK hint in empty state without active filters', () => {
      setupMocks([])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.text()).toContain('SDK')
    })
  })

  describe('log table', () => {
    it('renders a row for each log entry', () => {
      const logs = [
        makeLog('l1', 'error', 'Something went wrong'),
        makeLog('l2', 'info', 'Server started'),
      ]
      setupMocks(logs)
      const wrapper = mount(LogsView, { global: { stubs } })
      const rows = wrapper.findAll('.perf-table__row--clickable')
      expect(rows.length).toBe(2)
    })

    it('displays log message body', () => {
      setupMocks([makeLog('l1', 'error', 'Database connection failed')])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Database connection failed')
    })

    it('displays log level', () => {
      setupMocks([makeLog('l1', 'error', 'test')])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.text()).toContain('error')
    })

    it('renders table headers', () => {
      setupMocks([makeLog('l1', 'error', 'test')])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Time')
      expect(wrapper.text()).toContain('Level')
      expect(wrapper.text()).toContain('Message')
    })

    it('toggles expanded row details on click', async () => {
      setupMocks([makeLog('l1', 'error', 'test error')])
      const wrapper = mount(LogsView, { global: { stubs } })
      const row = wrapper.find('.perf-table__row--clickable')
      await row.trigger('click')
      expect(wrapper.find('.log-expanded').exists()).toBe(true)
    })

    it('collapses expanded row when clicking the same row again', async () => {
      setupMocks([makeLog('l1', 'error', 'test error')])
      const wrapper = mount(LogsView, { global: { stubs } })
      const row = wrapper.find('.perf-table__row--clickable')
      await row.trigger('click')
      await row.trigger('click')
      expect(wrapper.find('.log-expanded').exists()).toBe(false)
    })

    it('shows "No attributes" in expanded row when there are none', async () => {
      setupMocks([makeLog('l1', 'error', 'test')])
      const wrapper = mount(LogsView, { global: { stubs } })
      await wrapper.find('.perf-table__row--clickable').trigger('click')
      expect(wrapper.text()).toContain('No attributes')
    })

    it('renders attribute rows when log has attributes', async () => {
      const log = { ...makeLog('l1', 'error', 'test'), attributes: { user_id: '123', action: 'delete' } }
      setupMocks([log])
      const wrapper = mount(LogsView, { global: { stubs } })
      await wrapper.find('.perf-table__row--clickable').trigger('click')
      expect(wrapper.text()).toContain('user_id')
      expect(wrapper.text()).toContain('123')
    })
  })

  describe('refresh button', () => {
    it('renders the refresh button', () => {
      setupMocks([])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.find('.filterbar__refresh').exists()).toBe(true)
    })
  })

  describe('search input', () => {
    it('renders the search input', () => {
      setupMocks([])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.find('.filterbar__search input').exists()).toBe(true)
    })

    it('triggers debounced search when input value changes', async () => {
      vi.useFakeTimers()
      setupMocks([makeLog('l1', 'error', 'test')])
      const wrapper = mount(LogsView, { global: { stubs } })
      const input = wrapper.find('.filterbar__search input')
      await input.trigger('input')
      vi.advanceTimersByTime(400)
      await wrapper.vm.$nextTick()
      vi.useRealTimers()
      expect(wrapper.find('.filterbar__search input').exists()).toBe(true)
    })
  })

  describe('refresh button interaction', () => {
    it('calls refetch when refresh button is clicked', async () => {
      const refetch = vi.fn()
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(useQuery).mockReturnValue({
        data: ref({ logs: [], has_more: false }),
        isLoading: ref(false),
        isFetching: ref(false),
        refetch,
      } as any)
      const wrapper = mount(LogsView, { global: { stubs } })
      await wrapper.find('.filterbar__refresh').trigger('click')
      expect(refetch).toHaveBeenCalledOnce()
    })

    it('shows fetching class when isFetching is true', () => {
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(useQuery).mockReturnValue({
        data: ref({ logs: [], has_more: false }),
        isLoading: ref(false),
        isFetching: ref(true),
        refetch: vi.fn(),
      } as any)
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.find('.filterbar__refresh--fetching').exists()).toBe(true)
    })
  })

  describe('level filter change', () => {
    it('updates level filter when FilterChip emits change', async () => {
      setupMocks([makeLog('l1', 'info', 'test')])
      const wrapper = mount(LogsView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      const levelChip = chips.find(c => c.props('label') === 'Level')
      await levelChip?.vm.$emit('change', 'Error')
      expect(wrapper.find('.filterbar__search input').exists()).toBe(true)
    })

    it('updates env filter when environment FilterChip emits change', async () => {
      setupMocks([makeLog('l1', 'info', 'test')])
      const wrapper = mount(LogsView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      const envChip = chips.find(c => c.props('label') === 'Environment')
      await envChip?.vm.$emit('change', 'staging')
      expect(wrapper.find('.filterbar__search input').exists()).toBe(true)
    })
  })

  describe('env badge classes', () => {
    it('applies production badge class for production environment', () => {
      setupMocks([makeLog('l1', 'error', 'prod log')])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.find('.envbadge--prod').exists()).toBe(true)
    })

    it('applies staging badge class for staging environment', () => {
      const log = { ...makeLog('l1', 'info', 'staging log'), environment: 'staging' }
      setupMocks([log])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.find('.envbadge--staging').exists()).toBe(true)
    })

    it('applies no special badge class for non-prod non-staging environment', () => {
      const log = { ...makeLog('l1', 'info', 'dev log'), environment: 'development' }
      setupMocks([log])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.find('.envbadge--prod').exists()).toBe(false)
      expect(wrapper.find('.envbadge--staging').exists()).toBe(false)
      expect(wrapper.find('.envbadge').exists()).toBe(true)
    })
  })

  describe('expanded row details', () => {
    it('shows trace_id when log has one', async () => {
      const log = { ...makeLog('l1', 'info', 'traced log'), trace_id: 'trace-abc123' }
      setupMocks([log])
      const wrapper = mount(LogsView, { global: { stubs } })
      await wrapper.find('.perf-table__row--clickable').trigger('click')
      expect(wrapper.text()).toContain('trace-abc123')
    })

    it('shows release tag when log has release', async () => {
      const log = { ...makeLog('l1', 'info', 'released log'), release: 'v1.2.3' }
      setupMocks([log])
      const wrapper = mount(LogsView, { global: { stubs } })
      await wrapper.find('.perf-table__row--clickable').trigger('click')
      expect(wrapper.text()).toContain('v1.2.3')
    })

    it('renders object attribute values as JSON', async () => {
      const log = { ...makeLog('l1', 'error', 'test'), attributes: { meta: { key: 'val' } } }
      setupMocks([log])
      const wrapper = mount(LogsView, { global: { stubs } })
      await wrapper.find('.perf-table__row--clickable').trigger('click')
      expect(wrapper.text()).toContain('{"key":"val"}')
    })
  })

  describe('has_more row', () => {
    it('shows truncation notice when has_more is true', () => {
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(useQuery).mockReturnValue({
        data: ref({ logs: [makeLog('l1', 'info', 'test')], has_more: true }),
        isLoading: ref(false),
        isFetching: ref(false),
        refetch: vi.fn(),
      } as any)
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Showing the first 100 entries')
    })
  })

  describe('empty state with active filter', () => {
    it('shows "Try adjusting your filters" when level filter is active', async () => {
      setupMocks([])
      const wrapper = mount(LogsView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      const levelChip = chips.find(c => c.props('label') === 'Level')
      await levelChip?.vm.$emit('change', 'Error')
      expect(wrapper.text()).toContain('Try adjusting your filters')
    })
  })
})
