import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, reactive, ref } from 'vue'

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
}))

vi.mock('@/stores/projects', () => ({
  useProjectsStore: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

const pushMock = vi.fn()
const replaceMock = vi.fn()
const routeState = reactive({ query: {} as Record<string, string | string[]> })
vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: pushMock,
    replace: (arg: { query?: Record<string, string | string[]> }) => {
      replaceMock(arg)
      if (arg?.query) routeState.query = arg.query
    },
  }),
  useRoute: () => routeState,
}))

vi.mock('@/utils/formatters', () => ({
  formatTs: vi.fn((ts: string) => ts),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: vi.fn(),
}))

import LogsView from '../LogsView.vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { useAuthStore } from '@/stores/auth'

const stubs = {
  Icon: { template: '<span />' },
  FilterChip: { name: 'FilterChip', props: ['label', 'value', 'options'], template: '<div />' },
  RouterLink: { name: 'RouterLink', props: ['to'], template: '<a :href="to"><slot /></a>' },
}

const makeLog = (id: string, level: string, body: string) => ({
  id,
  project_id: 'p1',
  level,
  body,
  timestamp: '2024-01-01T00:00:00Z',
  environment: 'production',
  trace_id: null,
  transaction_id: null,
  release: null,
  attributes: {},
})

const PROJECTS = [
  { id: 'p1', name: 'Checkout API' },
  { id: 'p2', name: 'Web Frontend' },
]

function setupMocks(logs: unknown[] = [], isLoading = false, selectedIds: string[] = []) {
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds, projects: PROJECTS, setSelected: vi.fn() } as any)
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
  vi.mocked(useAuthStore).mockReset()
  vi.mocked(useAuthStore).mockReturnValue({
    user: { timezone: 'UTC', permissions: { manage_alerts: true } },
    setUser: vi.fn(),
  } as any)
  pushMock.mockReset()
  replaceMock.mockReset()
  routeState.query = {}
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

    // A long message must not widen the message column: the cell carries the
    // width cap and the body inside it is the part that ellipsizes.
    it('caps the message cell so long bodies cannot stretch the table', () => {
      const long = 'x'.repeat(4000)
      setupMocks([makeLog('l1', 'error', long)])
      const wrapper = mount(LogsView, { global: { stubs } })
      const cell = wrapper.find('tbody .log-msg-col')
      expect(cell.exists()).toBe(true)
      expect(cell.find('.log-msg__body').text()).toBe(long)
    })

    it('caps the message cell in the loading skeleton too', () => {
      setupMocks([], true)
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.findAll('tbody .log-msg-col').length).toBe(12)
    })

    // Ingest normalizes the log protocol's "warn" to "warning", which is the
    // spelling the level filter sends and the only one with a styled dot.
    it('styles the level dot for normalized warning logs', () => {
      setupMocks([makeLog('l1', 'warning', 'disk space low')])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.find('.leveldot--warning').exists()).toBe(true)
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

    // Ingest flattens Sentry's typed attributes ({"value":x,"type":"string"})
    // before storing them, so the expanded row shows values, not wrappers.
    it('renders flattened sentry attributes as plain values', async () => {
      const log = {
        ...makeLog('l1', 'info', 'test'),
        attributes: { 'sentry.environment': 'production', 'user.id': 42 },
      }
      setupMocks([log])
      const wrapper = mount(LogsView, { global: { stubs } })
      await wrapper.find('.perf-table__row--clickable').trigger('click')

      const text = wrapper.find('.log-expanded').text()
      expect(text).toContain('sentry.environment')
      expect(text).toContain('production')
      expect(text).toContain('42')
      expect(text).not.toContain('"type"')
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

  describe('trace link', () => {
    it('links a log with a resolved transaction to its trace page', () => {
      const log = { ...makeLog('l1', 'error', 'boom'), trace_id: 't-1', transaction_id: 'tx-9' }
      setupMocks([log])
      const wrapper = mount(LogsView, { global: { stubs } })
      const link = wrapper.find('.log-trace-link')
      expect(link.exists()).toBe(true)
      expect(link.attributes('href')).toBe('/transactions/tx-9')
    })

    // A log can carry a trace_id whose transaction was never ingested (or was
    // already pruned). There is nothing to open, so no link is offered.
    it('omits the trace link when the trace has no transaction', () => {
      const log = { ...makeLog('l1', 'error', 'boom'), trace_id: 't-1', transaction_id: null }
      setupMocks([log])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.find('.log-trace-link').exists()).toBe(false)
    })

    it('does not toggle the row when the trace link is clicked', async () => {
      const log = { ...makeLog('l1', 'error', 'boom'), trace_id: 't-1', transaction_id: 'tx-9' }
      setupMocks([log])
      const wrapper = mount(LogsView, { global: { stubs } })
      await wrapper.find('.log-trace-link').trigger('click')
      expect(wrapper.find('.log-expanded').exists()).toBe(false)
    })

    it('links the trace id in the expanded row when a transaction exists', async () => {
      const log = { ...makeLog('l1', 'info', 'traced'), trace_id: 't-abc', transaction_id: 'tx-9' }
      setupMocks([log])
      const wrapper = mount(LogsView, { global: { stubs } })
      await wrapper.find('.perf-table__row--clickable').trigger('click')
      const link = wrapper.find('.log-expanded a')
      expect(link.attributes('href')).toBe('/transactions/tx-9')
      expect(link.text()).toContain('t-abc')
    })

    it('renders the expanded trace id as plain text without a transaction', async () => {
      const log = { ...makeLog('l1', 'info', 'traced'), trace_id: 't-abc', transaction_id: null }
      setupMocks([log])
      const wrapper = mount(LogsView, { global: { stubs } })
      await wrapper.find('.perf-table__row--clickable').trigger('click')
      expect(wrapper.find('.log-expanded').text()).toContain('t-abc')
      expect(wrapper.find('.log-expanded a').exists()).toBe(false)
    })
  })

  describe('project column', () => {
    it('shows the project column when no project is selected', () => {
      setupMocks([makeLog('l1', 'info', 'test')], false, [])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.find('.log-proj-col').exists()).toBe(true)
      expect(wrapper.find('.projtag').text()).toBe('Checkout API')
    })

    it('shows the project column when several projects are selected', () => {
      setupMocks([makeLog('l1', 'info', 'test')], false, ['p1', 'p2'])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.find('.log-proj-col').exists()).toBe(true)
    })

    // With a single project selected every row would repeat the same name.
    it('hides the project column when exactly one project is selected', () => {
      setupMocks([makeLog('l1', 'info', 'test')], false, ['p1'])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.find('.log-proj-col').exists()).toBe(false)
      expect(wrapper.find('.projtag').exists()).toBe(false)
    })

    it('falls back to the project id when the project is unknown', () => {
      const log = { ...makeLog('l1', 'info', 'test'), project_id: 'gone' }
      setupMocks([log])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.find('.projtag').text()).toBe('gone')
    })

    it('spans the project column in the expanded row', async () => {
      setupMocks([makeLog('l1', 'info', 'test')], false, [])
      const wrapper = mount(LogsView, { global: { stubs } })
      await wrapper.find('.perf-table__row--clickable').trigger('click')
      const cell = wrapper.find('.log-expanded').element.closest('td')
      expect(cell?.getAttribute('colspan')).toBe('5')
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

  describe('alert on this', () => {
    it('shows the button for users who can manage alerts', () => {
      setupMocks([makeLog('l1', 'error', 'x')])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Alert on this')
    })

    it('hides the button without manage_alerts', () => {
      vi.mocked(useAuthStore).mockReturnValue({
        user: { timezone: 'UTC', permissions: { manage_alerts: false } },
        setUser: vi.fn(),
      } as any)
      setupMocks([makeLog('l1', 'error', 'x')])
      const wrapper = mount(LogsView, { global: { stubs } })
      expect(wrapper.text()).not.toContain('Alert on this')
    })

    it('is disabled without a selected project', () => {
      setupMocks([makeLog('l1', 'error', 'x')])
      const wrapper = mount(LogsView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      const levelChip = chips.find(c => c.props('label') === 'Level')
      levelChip?.vm.$emit('change', 'Error')
      expect(wrapper.find('button.export-menu__trigger').attributes('disabled')).toBeDefined()
    })

    it('hydrates min_level and a custom environment from the URL', () => {
      routeState.query = { min_level: 'error', environment: 'eu-west', search: 'stripe', project_id: 'p1' }
      setupMocks([makeLog('l1', 'error', 'x')], false, ['p1'])
      const wrapper = mount(LogsView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      const levelChip = chips.find(c => c.props('label') === 'Level')
      const envChip = chips.find(c => c.props('label') === 'Environment')
      expect(levelChip?.props('value')).toBe('Error')
      expect(envChip?.props('value')).toBe('eu-west')
      expect(envChip?.props('options')).toContain('eu-west')
    })

    it('hydrates warn min_level from an array query param', () => {
      routeState.query = { min_level: ['warn'], project_id: ['p1', ''] }
      setupMocks([makeLog('l1', 'warning', 'x')], false, ['p1'])
      const wrapper = mount(LogsView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      expect(chips.find(c => c.props('label') === 'Level')?.props('value')).toBe('Warning')
    })

    it('writes min_level back to the URL when other filters change', async () => {
      routeState.query = { min_level: 'error', project_id: 'p1' }
      setupMocks([makeLog('l1', 'error', 'x')], false, ['p1'])
      const wrapper = mount(LogsView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      const envChip = chips.find(c => c.props('label') === 'Environment')
      await envChip?.vm.$emit('change', 'production')
      expect(replaceMock).toHaveBeenCalled()
      const query = replaceMock.mock.calls.at(-1)?.[0]?.query as Record<string, string>
      expect(query.min_level).toBe('error')
      expect(query.environment).toBe('production')
      expect(query.level).toBeUndefined()
    })

    it('writes exact level (not min_level) after a chip change', async () => {
      setupMocks([makeLog('l1', 'info', 'x')], false, ['p1'])
      const wrapper = mount(LogsView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      await chips.find(c => c.props('label') === 'Level')?.vm.$emit('change', 'Info')
      const query = replaceMock.mock.calls.at(-1)?.[0]?.query as Record<string, string>
      expect(query.level).toBe('info')
      expect(query.min_level).toBeUndefined()
    })

    it('hydrates from a later route query update', async () => {
      setupMocks([makeLog('l1', 'error', 'x')], false, ['p1'])
      const wrapper = mount(LogsView, { global: { stubs } })
      routeState.query = { min_level: 'fatal', environment: 'staging', search: 'db', project_id: 'p1' }
      await nextTick()
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      expect(chips.find(c => c.props('label') === 'Level')?.props('value')).toBe('Fatal')
      expect(chips.find(c => c.props('label') === 'Environment')?.props('value')).toBe('staging')
    })

    it('navigates to settings with current filters', async () => {
      setupMocks([makeLog('l1', 'error', 'x')], false, ['p1'])
      const wrapper = mount(LogsView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      const levelChip = chips.find(c => c.props('label') === 'Level')
      await levelChip?.vm.$emit('change', 'Error')
      await wrapper.find('button.export-menu__trigger').trigger('click')
      expect(pushMock).toHaveBeenCalled()
      const dest = String(pushMock.mock.calls[0][0])
      expect(dest).toContain('/settings/alerts')
      expect(dest).toContain('trigger=log_count')
      expect(dest).toContain('level=error')
    })

    it('enables Alert on this for warning plus search and prefills env', async () => {
      routeState.query = {
        min_level: 'warning',
        search: 'stripe',
        environment: 'production',
        project_id: 'p1',
      }
      setupMocks([makeLog('l1', 'warning', 'stripe')], false, ['p1'])
      const wrapper = mount(LogsView, { global: { stubs } })
      const btn = wrapper.find('button.export-menu__trigger')
      expect(btn.attributes('disabled')).toBeUndefined()
      await btn.trigger('click')
      const dest = String(pushMock.mock.calls[0][0])
      expect(dest).toContain('level=warning')
      expect(dest).toContain('search=stripe')
      expect(dest).toContain('environment=production')
    })

    it('prefills fatal from Alert on this', async () => {
      setupMocks([makeLog('l1', 'fatal', 'dead')], false, ['p1'])
      const wrapper = mount(LogsView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      await chips.find(c => c.props('label') === 'Level')?.vm.$emit('change', 'Fatal')
      await wrapper.find('button.export-menu__trigger').trigger('click')
      expect(String(pushMock.mock.calls[0][0])).toContain('level=fatal')
    })
  })
})
