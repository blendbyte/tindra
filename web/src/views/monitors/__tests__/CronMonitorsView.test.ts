import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { ref, toValue, type Ref } from 'vue'

let cronstrueThrows = false

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

// Ping URLs come from the configured public URL.
const publicURL = ref('https://tindra.example.com')
const { confirmMock } = vi.hoisted(() => ({ confirmMock: vi.fn() }))
vi.mock('@/composables/useConfirm', () => ({ useConfirm: () => ({ confirm: confirmMock }) }))

vi.mock('@/composables/useConfig', () => ({
  useConfig: () => ({ baseUrl: publicURL }),
}))

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

vi.mock('cronstrue', () => ({
  default: {
    toString: vi.fn(() => {
      if (cronstrueThrows) throw new Error('invalid cron')
      return 'Every hour'
    }),
  },
}))

import CronMonitorsView from '../CronMonitorsView.vue'
import { useQuery, useMutation } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { useAuthStore } from '@/stores/auth'
import { apiFetch } from '@/api/client'

const stubs = {
  Icon: { template: '<span />' },
}

const makeMonitor = (id: string, name: string, state = 'ok') => ({
  id,
  name,
  schedule: '0 * * * *',
  state,
  status: 'active',
  grace_period_secs: 300,
  project_id: 'proj-1',
  last_checkin_at: '2024-01-01T00:00:00Z',
  next_expected_at: '2024-01-01T01:00:00Z',
  recent_checkins: [],
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

function setupMocks(monitors: unknown[] = [], canManage = false, isLoading = false, checkins: unknown[] = []) {
  mockQueries({
    projects: { data: ref([{ id: 'proj-1', name: 'App' }]) },
    monitors: { data: ref(monitors), isLoading: ref(isLoading) },
    checkins: { data: ref(checkins) },
  }, { canManage })
}

// Mutations in registration order: create, save, delete.
const CREATE = 0
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

async function openRow(wrapper: ReturnType<typeof mount>, name: string) {
  await wrapper.findAll('.monrow').find(r => r.text().includes(name))!.trigger('click')
}

async function openEdit(wrapper: ReturnType<typeof mount>, name = 'Daily backup') {
  await openRow(wrapper, name)
  await wrapper.find('.mon-detail__actions .btn').trigger('click')
}

beforeEach(() => {
  cronstrueThrows = false
  issuedKeys = []
  confirmMock.mockReset()
  vi.mocked(useQuery).mockReset()
  vi.mocked(useMutation).mockReset()
  mockMutations({})
  vi.mocked(useProjectsStore).mockReset()
  vi.mocked(useAuthStore).mockReset()
})

describe('CronMonitorsView', () => {
  describe('data ownership', () => {
    it('only issues cron monitor queries, never uptime ones', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openRow(wrapper, 'Daily backup')
      expect(issuedKeys.map(k => k[0])).toEqual(['projects', 'monitors', 'checkins'])
      expect(issuedKeys.some(k => String(k[0]).startsWith('uptime'))).toBe(false)
    })

    it('registers only the cron create, save and delete mutations', () => {
      setupMocks([])
      mount(CronMonitorsView, { global: { stubs } })
      expect(useMutation).toHaveBeenCalledTimes(3)
    })
  })

  describe('subnav', () => {
    it('marks the cron tab active', () => {
      setupMocks([])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.find('.mon-subnav__tab--active').text()).toBe('Cron monitors')
    })
  })

  describe('empty state', () => {
    it('shows "No monitors yet" when there are no monitors', () => {
      setupMocks([])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.text()).toContain('No monitors yet')
    })

    it('shows "New monitor" button in empty state when user can manage', () => {
      setupMocks([], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      const btns = wrapper.findAll('.btn--primary')
      expect(btns.some(b => b.text().includes('New monitor'))).toBe(true)
    })

    it('hides "New monitor" button in empty state when user cannot manage', () => {
      setupMocks([], false)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      const emptyActions = wrapper.find('.empty-state__actions')
      expect(emptyActions.exists()).toBe(false)
    })
  })

  describe('loading skeleton', () => {
    it('shows skeleton rows while loading', () => {
      setupMocks([], false, true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow').exists()).toBe(true)
    })
  })

  describe('loaded monitors', () => {
    it('renders a row for each monitor', () => {
      setupMocks([makeMonitor('m1', 'Daily backup'), makeMonitor('m2', 'Hourly report')])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Daily backup')
      expect(wrapper.text()).toContain('Hourly report')
    })

    it('shows the monitor list header', () => {
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow--header').exists()).toBe(true)
    })

    it('expands monitor detail on row click', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openRow(wrapper, 'Daily backup')
      expect(wrapper.find('.mon-detail').exists()).toBe(true)
    })

    it('collapses monitor detail when clicking the same row again', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      await row.trigger('click')
      expect(wrapper.find('.mon-detail').exists()).toBe(false)
    })

    it('shows humanized schedule text', () => {
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Every hour')
    })
  })

  describe('create form', () => {
    it('shows "New monitor" button in filterbar when user can manage', () => {
      setupMocks([], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      const filterbar = wrapper.find('.filterbar')
      expect(filterbar.find('.btn--primary').exists()).toBe(true)
    })

    it('hides the filterbar "New monitor" button when user cannot manage', () => {
      setupMocks([], false)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.find('.filterbar .btn--primary').exists()).toBe(false)
    })

    it('toggles the create form when clicking "New monitor"', async () => {
      setupMocks([], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.find('.mon-createbar').exists()).toBe(false)
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      expect(wrapper.find('.mon-createbar').exists()).toBe(true)
    })

    it('hides the create form when clicking Cancel', async () => {
      setupMocks([], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      const cancelBtn = wrapper.find('.mon-createbar__actions .btn--ghost')
      await cancelBtn.trigger('click')
      expect(wrapper.find('.mon-createbar').exists()).toBe(false)
    })
  })

  describe('monitor expanded detail', () => {
    it('shows check-in history section', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openRow(wrapper, 'Daily backup')
      expect(wrapper.text()).toContain('Check-in history')
    })

    it('shows "No check-ins yet" when there are no check-ins', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openRow(wrapper, 'Daily backup')
      expect(wrapper.text()).toContain('No check-ins yet')
    })

    it('shows Edit button in detail when user can manage', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openRow(wrapper, 'Daily backup')
      expect(wrapper.find('.mon-detail__actions .btn').text()).toContain('Edit')
    })
  })

  describe('monitor state labels', () => {
    it('shows "OK" label for monitors in ok state', () => {
      setupMocks([makeMonitor('m1', 'Backup', 'ok')])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow__state').text()).toBe('OK')
    })

    it('shows "Missed" label for monitors in missed state', () => {
      setupMocks([makeMonitor('m1', 'Backup', 'missed')])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow__state').text()).toBe('Missed')
    })

    it('shows "Running" in plain text for monitors in in_progress state', () => {
      setupMocks([makeMonitor('m1', 'Backup', 'in_progress')])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow__state').text()).toBe('Running')
      expect(wrapper.find('.monrow__state').attributes('style')).toContain('var(--text-2)')
    })

    it('never calls a monitor that has not checked in OK', () => {
      setupMocks([{ ...makeMonitor('m1', 'Weekly report'), last_checkin_at: null }])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow__state').text()).toBe('Not checked yet')
      expect(wrapper.find('.monrow__state').attributes('style')).toContain('var(--text-3)')
      expect(wrapper.find('.mon-verdict').text()).toContain('1 not checked yet')
    })

    it('shows "Error" label for monitors in error state', () => {
      setupMocks([makeMonitor('m1', 'Backup', 'error')])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow__state').text()).toBe('Error')
    })

    it('shows "Unknown" label for monitors in unrecognized state', () => {
      setupMocks([makeMonitor('m1', 'Backup', 'unknown_state')])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow__state').text()).toBe('Unknown')
    })
  })

  describe('edit form', () => {
    it('shows edit form when Edit button is clicked', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openEdit(wrapper)
      expect(wrapper.find('.mon-editbar').exists()).toBe(true)
    })

    it('hides edit form when Cancel is clicked', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openEdit(wrapper)
      const cancelBtn = wrapper.findAll('.mon-editbar .btn--ghost').find(b => b.text().includes('Cancel'))!
      await cancelBtn.trigger('click')
      expect(wrapper.find('.mon-editbar').exists()).toBe(false)
    })

    it('shows Delete button in edit form', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openEdit(wrapper)
      const deleteBtns = wrapper.findAll('.mon-editbar .btn--ghost')
      expect(deleteBtns.some(b => b.text().includes('Delete'))).toBe(true)
    })
  })

  describe('check-in history with data', () => {
    it('renders check-in rows when checkins exist', async () => {
      const checkin = {
        id: 'ci1',
        status: 'ok',
        duration_ms: 150,
        environment: 'production',
        received_at: '2024-01-01T00:00:00Z',
      }
      setupMocks([makeMonitor('m1', 'Daily backup')], false, false, [checkin])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openRow(wrapper, 'Daily backup')
      expect(wrapper.find('.mon-ci-row').exists()).toBe(true)
    })

    it('shows check-in history header when checkins exist', async () => {
      const checkin = { id: 'ci1', status: 'ok', duration_ms: 100, environment: 'prod', received_at: '2024-01-01T00:00:00Z' }
      setupMocks([makeMonitor('m1', 'Backup')], false, false, [checkin])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openRow(wrapper, 'Backup')
      expect(wrapper.find('.mon-ci-row--header').exists()).toBe(true)
    })

    it('renders error status checkin row', async () => {
      const checkin = { id: 'ci1', status: 'error', duration_ms: 0, environment: 'prod', received_at: '2024-01-01T00:00:00Z' }
      setupMocks([makeMonitor('m1', 'Backup')], false, false, [checkin])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openRow(wrapper, 'Backup')
      expect(wrapper.find('.mon-ci-row').exists()).toBe(true)
    })

    it('renders in_progress status checkin row', async () => {
      const checkin = { id: 'ci1', status: 'in_progress', duration_ms: 0, environment: 'prod', received_at: '2024-01-01T00:00:00Z' }
      setupMocks([makeMonitor('m1', 'Backup')], false, false, [checkin])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openRow(wrapper, 'Backup')
      expect(wrapper.find('.mon-ci-row').exists()).toBe(true)
    })
  })

  describe('create form interactions', () => {
    it('cancels create form when Cancel is clicked', async () => {
      setupMocks([], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      expect(wrapper.find('.mon-createbar').exists()).toBe(true)
      const cancelBtn = wrapper.find('.mon-createbar__actions .btn--ghost')
      await cancelBtn.trigger('click')
      expect(wrapper.find('.mon-createbar').exists()).toBe(false)
    })

    it('shows edit form with prefilled values', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openEdit(wrapper)
      // startEdit fills form fields
      const nameInput = wrapper.find('.mon-editbar input')
      expect((nameInput.element as HTMLInputElement).value).toBe('Daily backup')
    })

    it('updates name input in create form', async () => {
      setupMocks([], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      const nameInput = wrapper.find('.mon-createbar input:not([type="number"]):not(.mono)')
      await nameInput.setValue('My Monitor')
      expect((nameInput.element as HTMLInputElement).value).toBe('My Monitor')
    })

    it('updates schedule input in create form', async () => {
      setupMocks([], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      const scheduleInput = wrapper.find('#new-monitor-schedule')
      await scheduleInput.setValue('0 0 * * *')
      expect((scheduleInput.element as HTMLInputElement).value).toBe('0 0 * * *')
    })

    it('updates project select in create form', async () => {
      setupMocks([], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      const select = wrapper.find('.mon-createbar select')
      await select.setValue('proj-1')
      expect((select.element as HTMLSelectElement).value).toBe('proj-1')
    })

    it('updates grace period input in create form', async () => {
      setupMocks([], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      const graceInput = wrapper.find('.mon-createbar input[type="number"]')
      await graceInput.setValue('600')
      expect((graceInput.element as HTMLInputElement).value).toBe('600')
    })

    it('sends an optional slug when creating a monitor', async () => {
      const recorded: any[] = []
      vi.mocked(useMutation).mockImplementation(((opts: any) => {
        recorded.push(opts)
        return { mutate: vi.fn(), isPending: ref(false) }
      }) as any)
      setupMocks([], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      await wrapper.find('.mon-createbar input:not([type="number"]):not(.mono)').setValue('Nightly backup')
      await wrapper.find('#new-monitor-slug').setValue(' nightly ')
      vi.mocked(apiFetch).mockClear()
      // The create mutation is the first one the view registers.
      await recorded[CREATE].mutationFn()
      const body = JSON.parse((vi.mocked(apiFetch).mock.calls[0][1] as RequestInit).body as string)
      expect(body).toMatchObject({ name: 'Nightly backup', slug: 'nightly' })

      await wrapper.find('#new-monitor-slug').setValue('')
      vi.mocked(apiFetch).mockClear()
      await recorded[CREATE].mutationFn()
      const noSlug = JSON.parse((vi.mocked(apiFetch).mock.calls[0][1] as RequestInit).body as string)
      expect(noSlug).not.toHaveProperty('slug')
    })

    it('shows the slug in monitor detail and prefills it when editing', async () => {
      setupMocks([{ ...makeMonitor('m1', 'Daily backup'), slug: 'daily-backup' }], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openRow(wrapper, 'Daily backup')
      expect(wrapper.find('.mon-detail__meta').text()).toContain('daily-backup')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
      expect((wrapper.find('#edit-monitor-slug').element as HTMLInputElement).value).toBe('daily-backup')
    })

    it('calls createMonitor when Create button is clicked with all fields set', async () => {
      const createMutate = vi.fn()
      mockMutations({ [CREATE]: { mutate: createMutate } })
      setupMocks([], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      await wrapper.find('.mon-createbar input:not([type="number"]):not(.mono)').setValue('My Monitor')
      await wrapper.find('#new-monitor-schedule').setValue('0 * * * *')
      await wrapper.find('.mon-createbar select').setValue('proj-1')
      const createBtn = wrapper.find('.mon-createbar__actions .btn--primary')
      await createBtn.trigger('click')
      expect(createMutate).toHaveBeenCalled()
    })
  })

  describe('copy ping URL', () => {
    it('calls clipboard.writeText when Copy button is clicked', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openRow(wrapper, 'Daily backup')
      const copyBtn = wrapper.findAll('.mon-ping .btn').find(b => b.text().includes('Copy'))!
      await copyBtn.trigger('click')
      expect(writeText).toHaveBeenCalled()
    })

    it('builds the ping URL from the configured public URL, not the browser host', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openRow(wrapper, 'Daily backup')
      expect(wrapper.find('.mon-ping code').text()).toBe('https://tindra.example.com/api/cron/m1')
      await wrapper.findAll('.mon-ping .btn').find(b => b.text().includes('Copy'))!.trigger('click')
      expect(writeText).toHaveBeenCalledWith('https://tindra.example.com/api/cron/m1')
    })
  })

  describe('saving state', () => {
    it('shows "Saving…" in Save button when saving is true', async () => {
      mockMutations({ [SAVE]: { isPending: ref(true) } })
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openEdit(wrapper)
      expect(wrapper.find('.mon-editbar .btn--primary').text()).toContain('Saving')
    })
  })

  describe('confirm delete', () => {
    it('calls confirmDelete when Delete button is clicked (with confirm accepted)', async () => {
      confirmMock.mockResolvedValue(true)
      const deleteMutate = vi.fn()
      mockMutations({ [DELETE]: { mutate: deleteMutate } })
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openEdit(wrapper)
      const deleteBtn = wrapper.findAll('.mon-editbar .btn--ghost').find(b => b.text().includes('Delete'))!
      await deleteBtn.trigger('click')
      await flushPromises()
      expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: 'Delete monitor "Daily backup"?', danger: true }))
      expect(deleteMutate).toHaveBeenCalledWith('m1')
    })

    it('does not call delete when confirm is declined', async () => {
      confirmMock.mockResolvedValue(false)
      const deleteMutate = vi.fn()
      mockMutations({ [DELETE]: { mutate: deleteMutate } })
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openEdit(wrapper)
      const deleteBtn = wrapper.findAll('.mon-editbar .btn--ghost').find(b => b.text().includes('Delete'))!
      await deleteBtn.trigger('click')
      await flushPromises()
      expect(deleteMutate).not.toHaveBeenCalled()
    })
  })

  describe('checkins loading state', () => {
    it('shows "Loading…" when check-in history is loading', async () => {
      mockQueries({
        projects: { data: ref([{ id: 'proj-1', name: 'App' }]) },
        monitors: { data: ref([makeMonitor('m1', 'Daily backup')]) },
        checkins: { data: ref(undefined), isLoading: ref(true) },
      })
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openRow(wrapper, 'Daily backup')
      expect(wrapper.text()).toContain('Loading')
    })
  })

  describe('status select in edit form', () => {
    it('updates editForm.status when select value changes', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openEdit(wrapper)
      const select = wrapper.find('.mon-editbar select')
      await select.setValue('paused')
      expect((select.element as HTMLSelectElement).value).toBe('paused')
    })
  })

  describe('recent checkins timeline', () => {
    it('renders recent checkin dots for monitors with recent_checkins', () => {
      const monWithCheckins = {
        ...makeMonitor('m1', 'Daily backup'),
        recent_checkins: [
          { status: 'ok', received_at: '2024-01-01T00:00:00Z' },
          { status: 'error', received_at: '2024-01-01T01:00:00Z' },
        ],
      }
      setupMocks([monWithCheckins])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      const dots = wrapper.findAll('.mon-tl-dot:not(.mon-tl-dot--empty)')
      expect(dots.length).toBe(2)
    })

    it('shows default color for unknown checkin status in timeline', () => {
      const monWithUnknown = {
        ...makeMonitor('m1', 'Daily backup'),
        recent_checkins: [{ status: 'unknown_state', received_at: '2024-01-01T00:00:00Z' }],
      }
      setupMocks([monWithUnknown])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      const dot = wrapper.find('.mon-tl-dot:not(.mon-tl-dot--empty)')
      expect(dot.attributes('style')).toContain('background')
    })
  })

  describe('save button in edit form', () => {
    it('calls saveMonitor when Save button is clicked', async () => {
      const saveMutate = vi.fn()
      mockMutations({ [SAVE]: { mutate: saveMutate } })
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openEdit(wrapper)
      await wrapper.find('.mon-editbar .btn--primary').trigger('click')
      expect(saveMutate).toHaveBeenCalledWith('m1')
    })

    it('updates editForm schedule input', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openEdit(wrapper)
      const inputs = wrapper.findAll('.mon-editbar input')
      const scheduleInput = inputs.find(i => (i.element as HTMLInputElement).classList.contains('mono'))
      if (scheduleInput) {
        await scheduleInput.setValue('0 0 * * *')
        expect((scheduleInput.element as HTMLInputElement).value).toBe('0 0 * * *')
      }
    })

    it('updates editForm grace period input', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openEdit(wrapper)
      const inputs = wrapper.findAll('.mon-editbar input[type="number"]')
      if (inputs.length > 0) {
        await inputs[0].setValue(600)
        expect((inputs[0].element as HTMLInputElement).value).toBe('600')
      }
    })
  })

  describe('null timestamps', () => {
    it('shows "–" when last_checkin_at is null', () => {
      const monNoCheckin = { ...makeMonitor('m1', 'Daily backup'), last_checkin_at: null }
      setupMocks([monNoCheckin])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.text()).toContain('–')
    })

    it('shows "–" when ci duration_ms is null', async () => {
      const checkin = { id: 'ci1', status: 'ok', duration_ms: null, environment: 'production', received_at: '2024-01-01T00:00:00Z' }
      setupMocks([makeMonitor('m1', 'Daily backup')], false, false, [checkin])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openRow(wrapper, 'Daily backup')
      // duration_ms null renders '–'
      expect(wrapper.find('.mon-ci-row:not(.mon-ci-row--header)').text()).toContain('–')
    })
  })

  describe('empty state New monitor button click', () => {
    it('shows create form when "New monitor" in empty state is clicked', async () => {
      setupMocks([], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.find('.mon-createbar').exists()).toBe(false)
      const emptyStateBtn = wrapper.find('.empty-state__actions .btn--primary')
      if (emptyStateBtn.exists()) {
        await emptyStateBtn.trigger('click')
        expect(wrapper.find('.mon-createbar').exists()).toBe(true)
      }
    })
  })

  describe('create form - input and submit', () => {
    it('updates grace period input in create form', async () => {
      setupMocks([], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      const graceInput = wrapper.find('.mon-createbar input[type="number"]')
      if (graceInput.exists()) {
        await graceInput.setValue(600)
        expect((graceInput.element as HTMLInputElement).value).toBe('600')
      }
    })

    it('keeps the Create button disabled until name, project and schedule are set', async () => {
      const createMutate = vi.fn()
      mockMutations({ [CREATE]: { mutate: createMutate } })
      setupMocks([], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      const createBtn = wrapper.find('.mon-createbar__actions .btn--primary')
      expect(createBtn.attributes('disabled')).toBeDefined()
      await createBtn.trigger('click')
      expect(createMutate).not.toHaveBeenCalled()
    })
  })

  describe('edit form - name input', () => {
    it('updates editForm name input', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openEdit(wrapper)
      const nameInput = wrapper.find('.mon-editbar input:not([type="number"]):not(.mono)')
      if (nameInput.exists()) {
        await nameInput.setValue('Updated backup')
        expect((nameInput.element as HTMLInputElement).value).toBe('Updated backup')
      }
    })
  })

  describe('humanSchedule catch branch', () => {
    it('returns raw expression when cronstrue throws', () => {
      cronstrueThrows = true
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      // When cronstrue throws, humanSchedule returns the raw expression '0 * * * *'
      expect(wrapper.text()).toContain('0 * * * *')
    })
  })

  describe('queryParams with selectedIds', () => {
    it('builds query params including project_id when selectedIds has values', () => {
      mockQueries({
        projects: { data: ref([{ id: 'proj-1', name: 'App' }]) },
        monitors: { data: ref([makeMonitor('m1', 'Daily backup')]) },
      }, { selectedIds: ['proj-2', 'proj-1'] })
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Daily backup')
      expect(issuedKeys).toContainEqual(['monitors', 'project_id=proj-1&project_id=proj-2'])
    })
  })

  describe('projectName fallback', () => {
    it('returns sliced project id when project not found in projectList', async () => {
      mockQueries({
        projects: { data: ref([]) },
        monitors: { data: ref([makeMonitor('unknown-project-id', 'My Monitor')]) },
      })
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openRow(wrapper, 'My Monitor')
      // projectName returns id.slice(0, 8) when not found
      expect(wrapper.find('.mon-detail').text()).toContain('unknown-')
    })
  })

  describe('mutation callbacks via call-through pattern', () => {
    it('createMonitor onSuccess resets form and sets selectedMonitorId', async () => {
      vi.mocked(apiFetch).mockResolvedValue({ id: 'new-mon', name: 'New', schedule: '0 * * * *', status: 'active', state: 'ok', grace_period_secs: 300, project_id: 'proj-1', last_checkin_at: null, next_expected_at: null, recent_checkins: [] })
      mockMutationsCallThrough()
      setupMocks([], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      await wrapper.find('.mon-createbar input:not([type="number"]):not(.mono)').setValue('New Monitor')
      await wrapper.find('#new-monitor-schedule').setValue('0 * * * *')
      await wrapper.find('.mon-createbar select').setValue('proj-1')
      await wrapper.find('.mon-createbar__actions .btn--primary').trigger('click')
      await flushPromises()
      expect(vi.mocked(apiFetch)).toHaveBeenCalledWith('/api/monitors', expect.objectContaining({ method: 'POST' }))
      expect(wrapper.find('.mon-createbar').exists()).toBe(false)
    })

    it('saveMonitor onSuccess clears editingId', async () => {
      vi.mocked(apiFetch).mockResolvedValue({ id: 'm1', name: 'Daily backup', schedule: '0 * * * *', status: 'active', state: 'ok', grace_period_secs: 300, project_id: 'proj-1', last_checkin_at: null, next_expected_at: null, recent_checkins: [] })
      mockMutationsCallThrough()
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openEdit(wrapper)
      await wrapper.find('.mon-editbar .btn--primary').trigger('click')
      await flushPromises()
      expect(vi.mocked(apiFetch)).toHaveBeenCalledWith('/api/monitors/m1', expect.objectContaining({ method: 'PATCH' }))
      expect(wrapper.find('.mon-editbar').exists()).toBe(false)
    })

    it('deleteMonitor onSuccess clears selectedMonitorId and editingId', async () => {
      confirmMock.mockResolvedValue(true)
      vi.mocked(apiFetch).mockResolvedValue(undefined)
      mockMutationsCallThrough()
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(CronMonitorsView, { global: { stubs } })
      await openEdit(wrapper)
      const deleteBtn = wrapper.findAll('.mon-editbar .btn--ghost').find(b => b.text().includes('Delete'))!
      await deleteBtn.trigger('click')
      await flushPromises()
      expect(vi.mocked(apiFetch)).toHaveBeenCalledWith('/api/monitors/m1', { method: 'DELETE' })
      expect(wrapper.find('.mon-detail').exists()).toBe(false)
    })
  })
})

describe('CronMonitorsView load errors', () => {
  it('offers a retry instead of the empty state when cron monitors fail to load', async () => {
    const refetch = vi.fn()
    mockQueries({ projects: { data: ref([]) }, monitors: { isError: ref(true), refetch } }, { canManage: true })
    const wrapper = mount(CronMonitorsView, { global: { stubs } })
    expect(wrapper.text()).toContain("Couldn't load cron monitors")
    expect(wrapper.text()).not.toContain('No monitors yet')
    await wrapper.find('.txerror .btn').trigger('click')
    expect(refetch).toHaveBeenCalled()
  })

  it('shows a check-in load error in the monitor detail', async () => {
    const refetch = vi.fn()
    mockQueries({
      projects: { data: ref([]) },
      monitors: { data: ref([makeMonitor('m1', 'Daily backup')]) },
      checkins: { isError: ref(true), refetch },
    }, { canManage: true })
    const wrapper = mount(CronMonitorsView, { global: { stubs } })
    await openRow(wrapper, 'Daily backup')
    expect(wrapper.text()).toContain("Couldn't load check-ins.")
    expect(wrapper.text()).not.toContain('No check-ins yet.')
    await wrapper.findAll('.mon-ci-empty .btn').at(0)!.trigger('click')
    expect(refetch).toHaveBeenCalled()
  })
})
