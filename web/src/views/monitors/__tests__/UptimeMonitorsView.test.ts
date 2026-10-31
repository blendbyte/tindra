import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { ref, toValue, type Ref } from 'vue'

vi.mock('vue-router', () => ({
  RouterLink: { template: '<a href="#"><slot /></a>' },
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(),
  useQueryClient: vi.fn(() => ({ invalidateQueries: vi.fn() })),
}))

vi.mock('@/stores/projects', () => ({
  useProjectsStore: vi.fn(),
}))

const { confirmMock } = vi.hoisted(() => ({ confirmMock: vi.fn() }))
vi.mock('@/composables/useConfirm', () => ({ useConfirm: () => ({ confirm: confirmMock }) }))

vi.mock('@/stores/auth', () => ({
  useAuthStore: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

vi.mock('@/utils/formatters', () => ({
  formatRel: vi.fn(() => '2m ago'),
  formatDuration: vi.fn((n: number) => `${n}ms`),
}))

import UptimeMonitorsView from '../UptimeMonitorsView.vue'
import { useQuery, useMutation } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { useAuthStore } from '@/stores/auth'
import { apiFetch } from '@/api/client'

const stubs = {
  Icon: { template: '<span />' },
}

const makeUptimeMonitor = (id: string, name: string, state = 'unknown') => ({
  id,
  project_id: 'proj-1',
  name,
  url: 'https://example.com',
  method: 'GET',
  interval_secs: 300,
  timeout_secs: 10,
  expected_codes: '200-299',
  body_contains: null,
  status: 'active',
  state,
  consecutive_failures: 0,
  last_checked_at: '2024-01-01T00:00:00Z',
  last_ok_at: null,
  next_check_at: null,
  last_status_code: null,
  last_response_ms: null,
  created_at: '2024-01-01T00:00:00Z',
  recent_checks: [],
})

// Queries are answered by the first element of their key, so tests do not
// depend on the order in which the view registers them. Every key the view
// asks for is recorded in issuedKeys.
type QueryResult = { data?: Ref<unknown>; isLoading?: Ref<boolean>; isError?: Ref<boolean>; refetch?: () => void }
let issuedKeys: unknown[][] = []

function mockQueries(results: Record<string, QueryResult>, { canManage = false, selectedIds = [] as string[] } = {}) {
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds } as any)
  vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: canManage } } } as any)
  vi.mocked(useQuery).mockImplementation(((opts: { queryKey: unknown }) => {
    const key = toValue(opts.queryKey) as unknown[]
    issuedKeys.push(key)
    return { data: ref(undefined), isLoading: ref(false), isError: ref(false), refetch: vi.fn(), ...results[String(key[0])] }
  }) as any)
}

function setupUptimeMocks(uptimeMonitors: unknown[] = [], canManage = false, uptimeLoading = false, uptimeChecks: unknown[] = [], uptimeStats: unknown = null) {
  mockQueries({
    'projects': { data: ref([{ id: 'proj-1', name: 'App' }]) },
    'uptime-monitors': { data: ref(uptimeMonitors), isLoading: ref(uptimeLoading) },
    'uptime-checks': { data: ref(uptimeChecks) },
    'uptime-stats': { data: ref(uptimeStats) },
  }, { canManage })
}

// Mutations in registration order: create, save, delete.
const SAVE = 1
const DELETE = 2

function mockMutations(overrides: Record<number, Record<string, unknown>>) {
  let index = 0
  vi.mocked(useMutation).mockImplementation((() => ({ mutate: vi.fn(), isPending: ref(false), ...overrides[index++] })) as any)
}

// Runs each mutation for real so onSuccess side effects can be observed.
function mockMutationsCallThrough() {
  vi.mocked(useMutation).mockImplementation(((opts: any) => {
    const mutate = vi.fn(async (...args: any[]) => {
      try {
        const result = await opts.mutationFn(...args)
        if (opts.onSuccess) opts.onSuccess(result)
      } catch {}
    })
    return { mutate, isPending: ref(false) }
  }) as any)
}

async function openRow(wrapper: ReturnType<typeof mount>, name = 'Homepage') {
  await wrapper.findAll('.uprow').find(r => r.text().includes(name))!.trigger('click')
}

async function openEditForm(wrapper: ReturnType<typeof mount>) {
  await openRow(wrapper)
  await wrapper.find('.mon-detail__actions .btn').trigger('click')
}

async function openUptimeCreateForm(wrapper: ReturnType<typeof mount>) {
  await wrapper.find('.filterbar .btn--primary').trigger('click')
}

beforeEach(() => {
  issuedKeys = []
  confirmMock.mockReset()
  vi.mocked(useQuery).mockReset()
  vi.mocked(useMutation).mockReset()
  mockMutations({})
  vi.mocked(useProjectsStore).mockReset()
  vi.mocked(useAuthStore).mockReset()
})

describe('UptimeMonitorsView', () => {
  describe('data ownership', () => {
    it('only issues uptime monitor queries, never cron ones', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openRow(wrapper)
      expect(issuedKeys.map(k => k[0])).toEqual(['projects', 'uptime-monitors', 'uptime-checks', 'uptime-stats'])
      expect(issuedKeys.some(k => k[0] === 'monitors' || k[0] === 'checkins')).toBe(false)
    })

    it('registers only the uptime create, save and delete mutations', () => {
      setupUptimeMocks([])
      mount(UptimeMonitorsView, { global: { stubs } })
      expect(useMutation).toHaveBeenCalledTimes(3)
    })

    it('scopes the monitor list to the selected projects', () => {
      mockQueries({ projects: { data: ref([]) } }, { selectedIds: ['proj-2', 'proj-1'] })
      mount(UptimeMonitorsView, { global: { stubs } })
      expect(issuedKeys).toContainEqual(['uptime-monitors', 'project_id=proj-1&project_id=proj-2'])
    })
  })

  describe('tab switching', () => {
    it('shows uptime empty state', async () => {
      setupUptimeMocks([])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      expect(wrapper.text()).toContain('No uptime monitors yet')
    })

    it('marks the uptime tab active', () => {
      setupUptimeMocks([])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      expect(wrapper.find('.mon-subnav__tab--active').text()).toBe('Uptime monitors')
    })
  })

  describe('empty state', () => {
    it('shows "New monitor" in empty state when user can manage', async () => {
      setupUptimeMocks([], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      expect(wrapper.find('.empty-state__actions').exists()).toBe(true)
    })

    it('hides empty state actions when user cannot manage', async () => {
      setupUptimeMocks([], false)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      expect(wrapper.find('.empty-state__actions').exists()).toBe(false)
    })

    it('shows create form when empty state "New monitor" is clicked', async () => {
      setupUptimeMocks([], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await wrapper.find('.empty-state__actions .btn--primary').trigger('click')
      expect(wrapper.find('.mon-createbar').exists()).toBe(true)
    })
  })

  describe('loading skeleton', () => {
    it('shows skeleton rows while loading', async () => {
      setupUptimeMocks([], false, true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      expect(wrapper.find('.uprow').exists()).toBe(true)
    })
  })

  describe('loaded monitors', () => {
    it('renders a row for each uptime monitor', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage'), makeUptimeMonitor('u2', 'API')])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Homepage')
      expect(wrapper.text()).toContain('API')
    })

    it('shows the uptime list header', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Last 20 checks')
    })

    it('expands detail on row click', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openRow(wrapper)
      expect(wrapper.find('.mon-detail').exists()).toBe(true)
    })

    it('collapses detail when clicking the same row again', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
      await row.trigger('click')
      await row.trigger('click')
      expect(wrapper.find('.mon-detail').exists()).toBe(false)
    })

    it('shows "–" when last_checked_at is null', async () => {
      setupUptimeMocks([{ ...makeUptimeMonitor('u1', 'Homepage'), last_checked_at: null }])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow__time').text()).toBe('–')
    })
  })

  describe('state labels', () => {
    it('shows "Up" for up state', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage', 'up')])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow__state').text()).toBe('Up')
    })

    it('shows "Down" for down state', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage', 'down')])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow__state').text()).toBe('Down')
    })

    it('shows "Unknown" for unknown state', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage', 'unknown')])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow__state').text()).toBe('Unknown')
    })
  })

  describe('recent_checks timeline', () => {
    it('renders timeline dots for recent checks', async () => {
      const mon = { ...makeUptimeMonitor('u1', 'Homepage'), recent_checks: [
        { status: 'up', checked_at: '2024-01-01T00:00:00Z' },
        { status: 'down', checked_at: '2024-01-01T00:05:00Z' },
      ]}
      setupUptimeMocks([mon])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      const dots = wrapper.findAll('.mon-tl-dot:not(.mon-tl-dot--empty)')
      expect(dots.length).toBe(2)
    })
  })

  describe('create form', () => {
    it('shows "New monitor" in filterbar when user can manage', async () => {
      setupUptimeMocks([], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      expect(wrapper.find('.filterbar .btn--primary').exists()).toBe(true)
    })

    it('hides the filterbar "New monitor" button when user cannot manage', () => {
      setupUptimeMocks([], false)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      expect(wrapper.find('.filterbar .btn--primary').exists()).toBe(false)
    })

    it('toggles the create form when clicking "New monitor"', async () => {
      setupUptimeMocks([], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      expect(wrapper.find('.mon-createbar').exists()).toBe(false)
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      expect(wrapper.find('.mon-createbar').exists()).toBe(true)
    })

    it('hides create form when Cancel is clicked', async () => {
      setupUptimeMocks([], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      await wrapper.find('.mon-createbar__actions .btn--ghost').trigger('click')
      expect(wrapper.find('.mon-createbar').exists()).toBe(false)
    })

    it('updates URL input in create form', async () => {
      setupUptimeMocks([], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      const urlInput = wrapper.find('.mon-createbar input.mono')
      await urlInput.setValue('https://example.com')
      expect((urlInput.element as HTMLInputElement).value).toBe('https://example.com')
    })

    it('keeps the Create button disabled until name, URL and project are set', async () => {
      const createMutate = vi.fn()
      mockMutations({ 0: { mutate: createMutate } })
      setupUptimeMocks([], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      const createBtn = wrapper.find('.mon-createbar__actions .btn--primary')
      expect(createBtn.attributes('disabled')).toBeDefined()
      await createBtn.trigger('click')
      expect(createMutate).not.toHaveBeenCalled()
    })
  })

  describe('detail view', () => {
    it('shows "Check history" heading', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openRow(wrapper)
      expect(wrapper.text()).toContain('Check history')
    })

    it('shows "No checks yet." when there are no checks', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openRow(wrapper)
      expect(wrapper.text()).toContain('No checks yet.')
    })

    it('shows Loading… when checks are loading', async () => {
      mockQueries({
        'projects': { data: ref([{ id: 'proj-1', name: 'App' }]) },
        'uptime-monitors': { data: ref([makeUptimeMonitor('u1', 'Homepage')]) },
        'uptime-checks': { data: ref(undefined), isLoading: ref(true) },
        'uptime-stats': { data: ref(null) },
      })
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openRow(wrapper)
      expect(wrapper.text()).toContain('Loading')
    })

    it('shows check rows when checks exist', async () => {
      const check = { id: 'ck1', monitor_id: 'u1', status: 'up', status_code: 200, response_ms: 120, error: null, checked_at: '2024-01-01T00:00:00Z' }
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], false, false, [check])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openRow(wrapper)
      expect(wrapper.find('.up-ci-row:not(.mon-ci-row--header)').exists()).toBe(true)
    })

    it('shows "–" for null response_ms in check row', async () => {
      const check = { id: 'ck1', monitor_id: 'u1', status: 'up', status_code: 200, response_ms: null, error: null, checked_at: '2024-01-01T00:00:00Z' }
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], false, false, [check])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openRow(wrapper)
      expect(wrapper.find('.up-ci-row:not(.mon-ci-row--header)').text()).toContain('–')
    })

    it('formats response_ms >= 1000 as seconds', async () => {
      const check = { id: 'ck1', monitor_id: 'u1', status: 'down', status_code: 503, response_ms: 1500, error: 'timeout', checked_at: '2024-01-01T00:00:00Z' }
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], false, false, [check])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openRow(wrapper)
      expect(wrapper.text()).toContain('1.50s')
    })

    it('shows Edit button when user can manage', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openRow(wrapper)
      expect(wrapper.find('.mon-detail__actions .btn').text()).toContain('Edit')
    })
  })

  describe('stats bar', () => {
    it('shows stats bar when uptimeStats is non-null', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], false, false, [], { uptime_pct_24h: 99.5, uptime_pct_7d: 99.2, uptime_pct_30d: 98.8, avg_response_ms_24h: 142 })
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openRow(wrapper)
      expect(wrapper.find('.up-stats').exists()).toBe(true)
      expect(wrapper.text()).toContain('99.50%')
    })

    it('shows "–" for null avg_response_ms in stats bar', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], false, false, [], { uptime_pct_24h: 100, uptime_pct_7d: 100, uptime_pct_30d: 100, avg_response_ms_24h: null })
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openRow(wrapper)
      expect(wrapper.find('.up-stats').text()).toContain('–')
    })

    it('does not show stats bar when uptimeStats is null', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')])
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openRow(wrapper)
      expect(wrapper.find('.up-stats').exists()).toBe(false)
    })
  })

  describe('edit form', () => {
    it('shows edit form when Edit is clicked', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      expect(wrapper.find('.mon-editbar').exists()).toBe(true)
    })

    it('shows edit form prefilled with monitor values', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const nameInput = wrapper.find('.mon-editbar input')
      expect((nameInput.element as HTMLInputElement).value).toBe('Homepage')
    })

    it('hides edit form when Cancel is clicked', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const cancelBtn = wrapper.findAll('.mon-editbar .btn--ghost').find(b => b.text().includes('Cancel'))!
      await cancelBtn.trigger('click')
      expect(wrapper.find('.mon-editbar').exists()).toBe(false)
    })

    it('shows Delete button in edit form', async () => {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      expect(wrapper.findAll('.mon-editbar .btn--ghost').some(b => b.text().includes('Delete'))).toBe(true)
    })

    it('calls saveUptime when Save is clicked', async () => {
      const saveMutate = vi.fn()
      mockMutations({ [SAVE]: { mutate: saveMutate } })
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      await wrapper.find('.mon-editbar .btn--primary').trigger('click')
      expect(saveMutate).toHaveBeenCalledWith('u1')
    })

    it('shows "Saving…" when savingUptime is true', async () => {
      mockMutations({ [SAVE]: { isPending: ref(true) } })
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      expect(wrapper.find('.mon-editbar .btn--primary').text()).toContain('Saving')
    })

    it('calls deleteUptime when Delete is clicked and confirm accepted', async () => {
      confirmMock.mockResolvedValue(true)
      const deleteMutate = vi.fn()
      mockMutations({ [DELETE]: { mutate: deleteMutate } })
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const deleteBtn = wrapper.findAll('.mon-editbar .btn--ghost').find(b => b.text().includes('Delete'))!
      await deleteBtn.trigger('click')
      await flushPromises()
      expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: 'Delete uptime monitor "Homepage"?', danger: true }))
      expect(deleteMutate).toHaveBeenCalledWith('u1')
    })

    it('does not call deleteUptime when confirm is declined', async () => {
      confirmMock.mockResolvedValue(false)
      const deleteMutate = vi.fn()
      mockMutations({ [DELETE]: { mutate: deleteMutate } })
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const deleteBtn = wrapper.findAll('.mon-editbar .btn--ghost').find(b => b.text().includes('Delete'))!
      await deleteBtn.trigger('click')
      await flushPromises()
      expect(deleteMutate).not.toHaveBeenCalled()
    })
  })

  describe('uptime mutation callbacks via call-through pattern', () => {
    const uptimeResponse = { id: 'u1', project_id: 'proj-1', name: 'Homepage', url: 'https://example.com', method: 'GET', interval_secs: 300, timeout_secs: 10, expected_codes: '200-299', body_contains: null, status: 'active', state: 'unknown', consecutive_failures: 0, last_checked_at: null, last_ok_at: null, next_check_at: null, last_status_code: null, last_response_ms: null, created_at: '2024-01-01T00:00:00Z', recent_checks: [] }

    it('createUptime onSuccess resets form and sets selectedUptimeId', async () => {
      vi.mocked(apiFetch).mockResolvedValue(uptimeResponse)
      mockMutationsCallThrough()
      setupUptimeMocks([], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      // Fill name, URL, and project so the Create button is enabled
      const fields = wrapper.findAll('.mon-createbar__fields--uptime input')
      await fields[0].setValue('Homepage') // name
      const urlInput = wrapper.findAll('.mon-createbar__fields--uptime input.mono')[0]
      await urlInput.setValue('https://example.com')
      await wrapper.find('.mon-createbar__fields--uptime select').setValue('proj-1')
      await wrapper.find('.mon-createbar .btn--primary').trigger('click')
      await flushPromises()
      expect(vi.mocked(apiFetch)).toHaveBeenCalledWith('/api/uptime-monitors', expect.objectContaining({ method: 'POST' }))
      expect(wrapper.find('.mon-createbar').exists()).toBe(false)
    })

    it('saveUptime onSuccess clears editingUptimeId', async () => {
      vi.mocked(apiFetch).mockResolvedValue(uptimeResponse)
      mockMutationsCallThrough()
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      await wrapper.find('.mon-editbar .btn--primary').trigger('click')
      await flushPromises()
      expect(vi.mocked(apiFetch)).toHaveBeenCalledWith('/api/uptime-monitors/u1', expect.objectContaining({ method: 'PATCH' }))
      expect(wrapper.find('.mon-editbar').exists()).toBe(false)
    })

    it('deleteUptime onSuccess clears selectedUptimeId and editingUptimeId', async () => {
      confirmMock.mockResolvedValue(true)
      vi.mocked(apiFetch).mockResolvedValue(undefined)
      mockMutationsCallThrough()
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const deleteBtn = wrapper.findAll('.mon-editbar .btn--ghost').find(b => b.text().includes('Delete'))!
      await deleteBtn.trigger('click')
      await flushPromises()
      expect(vi.mocked(apiFetch)).toHaveBeenCalledWith('/api/uptime-monitors/u1', { method: 'DELETE' })
      expect(wrapper.find('.mon-detail').exists()).toBe(false)
    })
  })

  describe('uptime edit form field interactions', () => {
    function setupEditForm() {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
    }

    it('updates editUptimeForm.url when URL input changes', async () => {
      setupEditForm()
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const urlInput = wrapper.findAll('.mon-editbar input').find(i => (i.element as HTMLInputElement).value === 'https://example.com')!
      await urlInput.setValue('https://new.example.com')
      expect((urlInput.element as HTMLInputElement).value).toBe('https://new.example.com')
    })

    it('updates editUptimeForm.method when method select changes', async () => {
      setupEditForm()
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const methodSelect = wrapper.findAll('.mon-editbar select')[0]
      await methodSelect.setValue('HEAD')
      expect((methodSelect.element as HTMLSelectElement).value).toBe('HEAD')
    })

    it('updates editUptimeForm.interval_secs when interval input changes', async () => {
      setupEditForm()
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const intervalInput = wrapper.findAll('.mon-editbar input[type="number"]')[0]
      await intervalInput.setValue('60')
      expect((intervalInput.element as HTMLInputElement).value).toBe('60')
    })

    it('updates editUptimeForm.timeout_secs when timeout input changes', async () => {
      setupEditForm()
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const timeoutInput = wrapper.findAll('.mon-editbar input[type="number"]')[1]
      await timeoutInput.setValue('5')
      expect((timeoutInput.element as HTMLInputElement).value).toBe('5')
    })

    it('updates editUptimeForm.expected_codes when expected_codes input changes', async () => {
      setupEditForm()
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      // index 0 = url (.mono), index 1 = expected_codes (.mono)
      const codesInput = wrapper.findAll('.mon-editbar input.mono')[1]
      await codesInput.setValue('200,201')
      expect((codesInput.element as HTMLInputElement).value).toBe('200,201')
    })

    it('updates editUptimeForm.name when name input changes', async () => {
      setupEditForm()
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      // First input in editbar (non-number, non-mono) is name
      const nameInput = wrapper.findAll('.mon-editbar input:not([type="number"]):not(.mono)')[0]
      await nameInput.setValue('Renamed Monitor')
      expect((nameInput.element as HTMLInputElement).value).toBe('Renamed Monitor')
    })

    it('updates editUptimeForm.body_contains when body_contains input changes', async () => {
      setupEditForm()
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const bodyInput = wrapper.findAll('.mon-editbar input').find(i => i.attributes('placeholder') === '(clear to remove)')!
      await bodyInput.setValue('healthy')
      expect((bodyInput.element as HTMLInputElement).value).toBe('healthy')
    })

    it('updates editUptimeForm.status when status select changes', async () => {
      setupEditForm()
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const statusSelect = wrapper.findAll('.mon-editbar select')[1]
      await statusSelect.setValue('paused')
      expect((statusSelect.element as HTMLSelectElement).value).toBe('paused')
    })
  })

  describe('uptime create form additional field interactions', () => {
    it('updates newUptime.method when method select changes', async () => {
      setupUptimeMocks([], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openUptimeCreateForm(wrapper)
      // index 0 = project select, index 1 = method select
      const methodSelect = wrapper.findAll('.mon-createbar__fields--uptime select')[1]
      await methodSelect.setValue('HEAD')
      expect((methodSelect.element as HTMLSelectElement).value).toBe('HEAD')
    })

    it('updates newUptime.interval_secs when interval input changes', async () => {
      setupUptimeMocks([], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openUptimeCreateForm(wrapper)
      const intervalInput = wrapper.findAll('.mon-createbar__fields--uptime input[type="number"]')[0]
      await intervalInput.setValue('120')
      expect((intervalInput.element as HTMLInputElement).value).toBe('120')
    })

    it('updates newUptime.timeout_secs when timeout input changes', async () => {
      setupUptimeMocks([], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openUptimeCreateForm(wrapper)
      const timeoutInput = wrapper.findAll('.mon-createbar__fields--uptime input[type="number"]')[1]
      await timeoutInput.setValue('30')
      expect((timeoutInput.element as HTMLInputElement).value).toBe('30')
    })

    it('updates newUptime.expected_codes when expected codes input changes', async () => {
      setupUptimeMocks([], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openUptimeCreateForm(wrapper)
      // index 0 = url input (.mono), index 1 = expected_codes input (.mono)
      const codesInput = wrapper.findAll('.mon-createbar__fields--uptime input.mono')[1]
      await codesInput.setValue('200,201')
      expect((codesInput.element as HTMLInputElement).value).toBe('200,201')
    })

    it('updates newUptime.body_contains when body contains input changes', async () => {
      setupUptimeMocks([], true)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      await openUptimeCreateForm(wrapper)
      const bodyInput = wrapper.findAll('.mon-createbar__fields--uptime input').find(i => i.attributes('placeholder') === '(optional)')!
      await bodyInput.setValue('healthy')
      expect((bodyInput.element as HTMLInputElement).value).toBe('healthy')
    })
  })

  describe('uptimeCheckColor fallback', () => {
    it('returns text-3 color for unknown check status in timeline', async () => {
      const monitorWithUnknownCheck = {
        ...makeUptimeMonitor('u1', 'Homepage'),
        recent_checks: [{ status: 'unknown', checked_at: '2024-01-01T00:00:00Z' }],
      }
      setupUptimeMocks([monitorWithUnknownCheck], false)
      const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
      // Find the filled dot (non-empty) in the timeline for the uptime row
      const filledDots = wrapper.findAll('.uprow .mon-tl-dot:not(.mon-tl-dot--empty)')
      expect(filledDots.length).toBeGreaterThan(0)
      const unknownDot = filledDots[0]
      expect(unknownDot.attributes('style')).toContain('var(--text-3)')
    })
  })
})

describe('UptimeMonitorsView load errors', () => {
  it('offers a retry when uptime monitors fail to load', async () => {
    const refetch = vi.fn()
    mockQueries({ 'projects': { data: ref([]) }, 'uptime-monitors': { isError: ref(true), refetch } }, { canManage: true })
    const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
    expect(wrapper.text()).toContain("Couldn't load uptime monitors")
    expect(wrapper.text()).not.toContain('No uptime monitors yet')
    await wrapper.find('.txerror .btn').trigger('click')
    expect(refetch).toHaveBeenCalled()
  })

  it('shows a check load error in the uptime monitor detail', async () => {
    const refetch = vi.fn()
    mockQueries({
      'projects': { data: ref([]) },
      'uptime-monitors': { data: ref([makeUptimeMonitor('u1', 'Homepage')]) },
      'uptime-checks': { isError: ref(true), refetch },
    }, { canManage: true })
    const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
    await openRow(wrapper)
    expect(wrapper.text()).toContain("Couldn't load checks.")
    expect(wrapper.text()).not.toContain('No checks yet.')
    await wrapper.findAll('.mon-ci-empty .btn').at(0)!.trigger('click')
    expect(refetch).toHaveBeenCalled()
  })
})

describe('UptimeMonitorsView triage order', () => {
  it('states how many monitors are down and lists them first with their outage length', () => {
    const down = { ...makeUptimeMonitor('u2', 'Gateway', 'down'), went_down_at: new Date(Date.now() - 65 * 60_000).toISOString() }
    setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage', 'up'), down, { ...makeUptimeMonitor('u3', 'Old', 'up'), status: 'paused' }])
    const wrapper = mount(UptimeMonitorsView, { global: { stubs } })
    expect(wrapper.find('.mon-verdict').text().replace(/\s+/g, ' ')).toBe('1 monitor is down. 1 up, 1 paused.')
    const names = wrapper.findAll('.monrow:not(.monrow--header) .monrow__name').map(n => n.text())
    expect(names).toEqual(['Gateway', 'Homepage', 'Old'])
    expect(wrapper.find('.monrow--failing .monrow__since').text()).toBe('for 1h 5m')
  })
})
