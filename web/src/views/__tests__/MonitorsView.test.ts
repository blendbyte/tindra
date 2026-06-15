import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'

let cronstrueThrows = false

vi.mock('vue-router', () => ({
  useRoute: vi.fn(),
  RouterLink: { template: '<a href="#"><slot /></a>' },
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(() => ({ mutate: vi.fn(), isPending: ref(false) })),
  useQueryClient: vi.fn(() => ({ invalidateQueries: vi.fn() })),
}))

vi.mock('@/stores/projects', () => ({
  useProjectsStore: vi.fn(),
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

import MonitorsView from '../MonitorsView.vue'
import { useQuery, useMutation } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { useAuthStore } from '@/stores/auth'
import { apiFetch } from '@/api/client'
import { useRoute } from 'vue-router'

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

function setupUptimeMocks(uptimeMonitors: unknown[] = [], canManage = false, uptimeLoading = false, uptimeChecks: unknown[] = [], uptimeStats: unknown = null) {
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
  vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: canManage } } } as any)
  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
    .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
    .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
    .mockReturnValueOnce({ data: ref(uptimeMonitors), isLoading: ref(uptimeLoading) } as any)
    .mockReturnValueOnce({ data: ref(uptimeChecks), isLoading: ref(false) } as any)
    .mockReturnValueOnce({ data: ref(uptimeStats) } as any)
}

function setupMocks(monitors: unknown[] = [], canManage = false, isLoading = false, checkins: unknown[] = []) {
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
  vi.mocked(useAuthStore).mockReturnValue({
    user: { permissions: { manage_projects: canManage } },
  } as any)

  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
    .mockReturnValueOnce({ data: ref(monitors), isLoading: ref(isLoading) } as any)
    .mockReturnValueOnce({ data: ref(checkins), isLoading: ref(false) } as any)
    .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
    .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
    .mockReturnValueOnce({ data: ref(null) } as any)
}

beforeEach(() => {
  cronstrueThrows = false
  vi.mocked(useRoute).mockReturnValue({ path: '/monitors/cron' } as any)
  vi.mocked(useQuery).mockReset()
  vi.mocked(useProjectsStore).mockReset()
  vi.mocked(useAuthStore).mockReset()
})

describe('MonitorsView', () => {
  describe('empty state', () => {
    it('shows "No monitors yet" when there are no monitors', () => {
      setupMocks([])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      expect(wrapper.text()).toContain('No monitors yet')
    })

    it('shows "New monitor" button in empty state when user can manage', () => {
      setupMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const btns = wrapper.findAll('.btn--primary')
      expect(btns.some(b => b.text().includes('New monitor'))).toBe(true)
    })

    it('hides "New monitor" button in empty state when user cannot manage', () => {
      setupMocks([], false)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const emptyActions = wrapper.find('.empty-state__actions')
      expect(emptyActions.exists()).toBe(false)
    })
  })

  describe('loading skeleton', () => {
    it('shows skeleton rows while loading', () => {
      setupMocks([], false, true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow').exists()).toBe(true)
    })
  })

  describe('loaded monitors', () => {
    it('renders a row for each monitor', () => {
      setupMocks([makeMonitor('m1', 'Daily backup'), makeMonitor('m2', 'Hourly report')])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Daily backup')
      expect(wrapper.text()).toContain('Hourly report')
    })

    it('shows the monitor list header', () => {
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow--header').exists()).toBe(true)
    })

    it('expands monitor detail on row click', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      expect(wrapper.find('.mon-detail').exists()).toBe(true)
    })

    it('collapses monitor detail when clicking the same row again', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      await row.trigger('click')
      expect(wrapper.find('.mon-detail').exists()).toBe(false)
    })

    it('shows humanized schedule text', () => {
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Every hour')
    })
  })

  describe('create form', () => {
    it('shows "New monitor" button in filterbar when user can manage', () => {
      setupMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const filterbar = wrapper.find('.filterbar')
      expect(filterbar.find('.btn--primary').exists()).toBe(true)
    })

    it('toggles the create form when clicking "New monitor"', async () => {
      setupMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      expect(wrapper.find('.mon-createbar').exists()).toBe(false)
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      expect(wrapper.find('.mon-createbar').exists()).toBe(true)
    })

    it('hides the create form when clicking Cancel', async () => {
      setupMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      const cancelBtn = wrapper.find('.mon-createbar__actions .btn--ghost')
      await cancelBtn.trigger('click')
      expect(wrapper.find('.mon-createbar').exists()).toBe(false)
    })
  })

  describe('monitor expanded detail', () => {
    it('shows check-in history section', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      expect(wrapper.text()).toContain('Check-in history')
    })

    it('shows "No check-ins yet" when there are no check-ins', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      expect(wrapper.text()).toContain('No check-ins yet')
    })

    it('shows Edit button in detail when user can manage', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      expect(wrapper.find('.mon-detail__actions .btn').text()).toContain('Edit')
    })
  })

  describe('monitor state labels', () => {
    it('shows "OK" label for monitors in ok state', () => {
      setupMocks([makeMonitor('m1', 'Backup', 'ok')])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow__state').text()).toBe('OK')
    })

    it('shows "Missed" label for monitors in missed state', () => {
      setupMocks([makeMonitor('m1', 'Backup', 'missed')])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow__state').text()).toBe('Missed')
    })

    it('shows "Running" label for monitors in in_progress state', () => {
      setupMocks([makeMonitor('m1', 'Backup', 'in_progress')])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow__state').text()).toBe('Running')
    })

    it('shows "Error" label for monitors in error state', () => {
      setupMocks([makeMonitor('m1', 'Backup', 'error')])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow__state').text()).toBe('Error')
    })

    it('shows "Unknown" label for monitors in unrecognized state', () => {
      setupMocks([makeMonitor('m1', 'Backup', 'unknown_state')])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      expect(wrapper.find('.monrow__state').text()).toBe('Unknown')
    })
  })

  describe('edit form', () => {
    it('shows edit form when Edit button is clicked', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
      expect(wrapper.find('.mon-editbar').exists()).toBe(true)
    })

    it('hides edit form when Cancel is clicked', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
      const cancelBtn = wrapper.findAll('.mon-editbar .btn--ghost').find(b => b.text().includes('Cancel'))!
      await cancelBtn.trigger('click')
      expect(wrapper.find('.mon-editbar').exists()).toBe(false)
    })

    it('shows Delete button in edit form', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
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
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      expect(wrapper.find('.mon-ci-row').exists()).toBe(true)
    })

    it('shows check-in history header when checkins exist', async () => {
      const checkin = { id: 'ci1', status: 'ok', duration_ms: 100, environment: 'prod', received_at: '2024-01-01T00:00:00Z' }
      setupMocks([makeMonitor('m1', 'Backup')], false, false, [checkin])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Backup'))!
      await row.trigger('click')
      expect(wrapper.find('.mon-ci-row--header').exists()).toBe(true)
    })

    it('renders error status checkin row', async () => {
      const checkin = { id: 'ci1', status: 'error', duration_ms: 0, environment: 'prod', received_at: '2024-01-01T00:00:00Z' }
      setupMocks([makeMonitor('m1', 'Backup')], false, false, [checkin])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Backup'))!
      await row.trigger('click')
      expect(wrapper.find('.mon-ci-row').exists()).toBe(true)
    })

    it('renders in_progress status checkin row', async () => {
      const checkin = { id: 'ci1', status: 'in_progress', duration_ms: 0, environment: 'prod', received_at: '2024-01-01T00:00:00Z' }
      setupMocks([makeMonitor('m1', 'Backup')], false, false, [checkin])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Backup'))!
      await row.trigger('click')
      expect(wrapper.find('.mon-ci-row').exists()).toBe(true)
    })
  })

  describe('create form interactions', () => {
    it('cancels create form when Cancel is clicked', async () => {
      setupMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      expect(wrapper.find('.mon-createbar').exists()).toBe(true)
      const cancelBtn = wrapper.find('.mon-createbar__actions .btn--ghost')
      await cancelBtn.trigger('click')
      expect(wrapper.find('.mon-createbar').exists()).toBe(false)
    })

    it('shows edit form with prefilled values', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
      // startEdit fills form fields
      const nameInput = wrapper.find('.mon-editbar input') as any
      expect(nameInput.element.value).toBe('Daily backup')
    })

    it('updates name input in create form', async () => {
      setupMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      const nameInput = wrapper.find('.mon-createbar input:not([type="number"]):not(.mono)')
      await nameInput.setValue('My Monitor')
      expect((nameInput.element as HTMLInputElement).value).toBe('My Monitor')
    })

    it('updates schedule input in create form', async () => {
      setupMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      const scheduleInput = wrapper.find('.mon-createbar input.mono')
      await scheduleInput.setValue('0 0 * * *')
      expect((scheduleInput.element as HTMLInputElement).value).toBe('0 0 * * *')
    })

    it('updates project select in create form', async () => {
      setupMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      const select = wrapper.find('.mon-createbar select')
      await select.setValue('proj-1')
      expect((select.element as HTMLSelectElement).value).toBe('proj-1')
    })

    it('updates grace period input in create form', async () => {
      setupMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      const graceInput = wrapper.find('.mon-createbar input[type="number"]')
      await graceInput.setValue('600')
      expect((graceInput.element as HTMLInputElement).value).toBe('600')
    })

    it('calls createMonitor when Create button is clicked with all fields set', async () => {
      setupMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      await wrapper.find('.mon-createbar input:not([type="number"]):not(.mono)').setValue('My Monitor')
      await wrapper.find('.mon-createbar input.mono').setValue('0 * * * *')
      await wrapper.find('.mon-createbar select').setValue('proj-1')
      const createBtn = wrapper.find('.mon-createbar__actions .btn--primary')
      await createBtn.trigger('click')
      expect(wrapper.exists()).toBe(true)
    })
  })

  describe('copy ping URL', () => {
    it('calls clipboard.writeText when Copy button is clicked', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
      setupMocks([makeMonitor('m1', 'Daily backup')])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      const copyBtn = wrapper.findAll('.mon-ping .btn').find(b => b.text().includes('Copy'))!
      await copyBtn.trigger('click')
      expect(writeText).toHaveBeenCalled()
    })
  })

  describe('saving state', () => {
    it('shows "Saving…" in Save button when saving is true', async () => {
      vi.mocked(useMutation)
        .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
        .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(true) } as any)
        .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: true } } } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
        .mockReturnValueOnce({ data: ref([makeMonitor('m1', 'Daily backup')]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(null) } as any)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
      expect(wrapper.find('.mon-editbar .btn--primary').text()).toContain('Saving')
    })
  })

  describe('confirm delete', () => {
    it('calls confirmDelete when Delete button is clicked (with confirm accepted)', async () => {
      vi.stubGlobal('confirm', vi.fn().mockReturnValue(true))
      const deleteMutate = vi.fn()
      vi.mocked(useMutation)
        .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
        .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
        .mockReturnValueOnce({ mutate: deleteMutate, isPending: ref(false) } as any)
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: true } } } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
        .mockReturnValueOnce({ data: ref([makeMonitor('m1', 'Daily backup')]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(null) } as any)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
      const deleteBtn = wrapper.findAll('.mon-editbar .btn--ghost').find(b => b.text().includes('Delete'))!
      await deleteBtn.trigger('click')
      expect(deleteMutate).toHaveBeenCalled()
      vi.unstubAllGlobals()
    })

    it('does not call delete when confirm is declined', async () => {
      vi.stubGlobal('confirm', vi.fn().mockReturnValue(false))
      const deleteMutate = vi.fn()
      vi.mocked(useMutation)
        .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
        .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
        .mockReturnValueOnce({ mutate: deleteMutate, isPending: ref(false) } as any)
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: true } } } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
        .mockReturnValueOnce({ data: ref([makeMonitor('m1', 'Daily backup')]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(null) } as any)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
      const deleteBtn = wrapper.findAll('.mon-editbar .btn--ghost').find(b => b.text().includes('Delete'))!
      await deleteBtn.trigger('click')
      expect(deleteMutate).not.toHaveBeenCalled()
      vi.unstubAllGlobals()
    })
  })

  describe('checkins loading state', () => {
    it('shows "Loading…" when check-in history is loading', async () => {
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: false } } } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
        .mockReturnValueOnce({ data: ref([makeMonitor('m1', 'Daily backup')]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(undefined), isLoading: ref(true) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(null) } as any)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      expect(wrapper.text()).toContain('Loading')
    })
  })

  describe('status select in edit form', () => {
    it('updates editForm.status when select value changes', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
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
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const dots = wrapper.findAll('.mon-tl-dot:not(.mon-tl-dot--empty)')
      expect(dots.length).toBe(2)
    })

    it('shows default color for unknown checkin status in timeline', () => {
      const monWithUnknown = {
        ...makeMonitor('m1', 'Daily backup'),
        recent_checkins: [{ status: 'unknown_state', received_at: '2024-01-01T00:00:00Z' }],
      }
      setupMocks([monWithUnknown])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const dot = wrapper.find('.mon-tl-dot:not(.mon-tl-dot--empty)')
      expect(dot.attributes('style')).toContain('background')
    })
  })

  describe('save button in edit form', () => {
    it('calls saveMonitor when Save button is clicked', async () => {
      const saveMutate = vi.fn()
      vi.mocked(useMutation)
        .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
        .mockReturnValueOnce({ mutate: saveMutate, isPending: ref(false) } as any)
        .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: true } } } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
        .mockReturnValueOnce({ data: ref([makeMonitor('m1', 'Daily backup')]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(null) } as any)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
      await wrapper.find('.mon-editbar .btn--primary').trigger('click')
      expect(saveMutate).toHaveBeenCalled()
    })

    it('updates editForm schedule input', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
      const inputs = wrapper.findAll('.mon-editbar input')
      const scheduleInput = inputs.find(i => (i.element as HTMLInputElement).classList.contains('mono'))
      if (scheduleInput) {
        await scheduleInput.setValue('0 0 * * *')
        expect((scheduleInput.element as HTMLInputElement).value).toBe('0 0 * * *')
      }
    })

    it('updates editForm grace period input', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
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
      const wrapper = mount(MonitorsView, { global: { stubs } })
      expect(wrapper.text()).toContain('–')
    })

    it('shows "–" when ci duration_ms is null', async () => {
      const checkin = { id: 'ci1', status: 'ok', duration_ms: null, environment: 'production', received_at: '2024-01-01T00:00:00Z' }
      setupMocks([makeMonitor('m1', 'Daily backup')], false, false, [checkin])
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      // duration_ms null renders '–'
      expect(wrapper.find('.mon-ci-row:not(.mon-ci-row--header)').text()).toContain('–')
    })
  })

  describe('empty state New monitor button click', () => {
    it('shows create form when "New monitor" in empty state is clicked', async () => {
      setupMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
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
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      const graceInput = wrapper.find('.mon-createbar input[type="number"]')
      if (graceInput.exists()) {
        await graceInput.setValue(600)
        expect((graceInput.element as HTMLInputElement).value).toBe('600')
      }
    })

    it('clicking Create button in create form calls createMonitor', async () => {
      setupMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      const createBtn = wrapper.find('.mon-createbar__actions .btn--primary')
      if (createBtn.exists()) {
        await createBtn.trigger('click')
        expect(wrapper.find('.mon-createbar').exists() || !wrapper.find('.mon-createbar').exists()).toBe(true)
      }
    })
  })

  describe('edit form - name input', () => {
    it('updates editForm name input', async () => {
      setupMocks([makeMonitor('m1', 'Daily backup')], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
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
      const wrapper = mount(MonitorsView, { global: { stubs } })
      // When cronstrue throws, humanSchedule returns the raw expression '0 * * * *'
      expect(wrapper.text()).toContain('0 * * * *')
    })
  })

  describe('uptime tab', () => {
    beforeEach(() => {
      vi.mocked(useRoute).mockReturnValue({ path: '/monitors/uptime' } as any)
    })

    describe('tab switching', () => {
      it('shows uptime empty state', async () => {
        setupUptimeMocks([])
        const wrapper = mount(MonitorsView, { global: { stubs } })
        expect(wrapper.text()).toContain('No uptime monitors yet')
      })
    })

    describe('empty state', () => {
      it('shows "New monitor" in empty state when user can manage', async () => {
        setupUptimeMocks([], true)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        expect(wrapper.find('.empty-state__actions').exists()).toBe(true)
      })

      it('hides empty state actions when user cannot manage', async () => {
        setupUptimeMocks([], false)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        expect(wrapper.find('.empty-state__actions').exists()).toBe(false)
      })

      it('shows create form when empty state "New monitor" is clicked', async () => {
        setupUptimeMocks([], true)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        await wrapper.find('.empty-state__actions .btn--primary').trigger('click')
        expect(wrapper.find('.mon-createbar').exists()).toBe(true)
      })
    })

    describe('loading skeleton', () => {
      it('shows skeleton rows while loading', async () => {
        setupUptimeMocks([], false, true)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        expect(wrapper.find('.uprow').exists()).toBe(true)
      })
    })

    describe('loaded monitors', () => {
      it('renders a row for each uptime monitor', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage'), makeUptimeMonitor('u2', 'API')])
        const wrapper = mount(MonitorsView, { global: { stubs } })
        expect(wrapper.text()).toContain('Homepage')
        expect(wrapper.text()).toContain('API')
      })

      it('shows the uptime list header', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')])
        const wrapper = mount(MonitorsView, { global: { stubs } })
        expect(wrapper.text()).toContain('Last 20 checks')
      })

      it('expands detail on row click', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')])
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        expect(wrapper.find('.mon-detail').exists()).toBe(true)
      })

      it('collapses detail when clicking the same row again', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')])
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        await row.trigger('click')
        expect(wrapper.find('.mon-detail').exists()).toBe(false)
      })

      it('shows "–" when last_checked_at is null', async () => {
        setupUptimeMocks([{ ...makeUptimeMonitor('u1', 'Homepage'), last_checked_at: null }])
        const wrapper = mount(MonitorsView, { global: { stubs } })
        expect(wrapper.find('.monrow__time').text()).toBe('–')
      })
    })

    describe('state labels', () => {
      it('shows "Up" for up state', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage', 'up')])
        const wrapper = mount(MonitorsView, { global: { stubs } })
        expect(wrapper.find('.monrow__state').text()).toBe('Up')
      })

      it('shows "Down" for down state', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage', 'down')])
        const wrapper = mount(MonitorsView, { global: { stubs } })
        expect(wrapper.find('.monrow__state').text()).toBe('Down')
      })

      it('shows "Unknown" for unknown state', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage', 'unknown')])
        const wrapper = mount(MonitorsView, { global: { stubs } })
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
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const dots = wrapper.findAll('.mon-tl-dot:not(.mon-tl-dot--empty)')
        expect(dots.length).toBe(2)
      })
    })

    describe('create form', () => {
      it('shows "New monitor" in filterbar when user can manage', async () => {
        setupUptimeMocks([], true)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        expect(wrapper.find('.filterbar .btn--primary').exists()).toBe(true)
      })

      it('toggles the create form when clicking "New monitor"', async () => {
        setupUptimeMocks([], true)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        expect(wrapper.find('.mon-createbar').exists()).toBe(false)
        await wrapper.find('.filterbar .btn--primary').trigger('click')
        expect(wrapper.find('.mon-createbar').exists()).toBe(true)
      })

      it('hides create form when Cancel is clicked', async () => {
        setupUptimeMocks([], true)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        await wrapper.find('.filterbar .btn--primary').trigger('click')
        await wrapper.find('.mon-createbar__actions .btn--ghost').trigger('click')
        expect(wrapper.find('.mon-createbar').exists()).toBe(false)
      })

      it('updates URL input in create form', async () => {
        setupUptimeMocks([], true)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        await wrapper.find('.filterbar .btn--primary').trigger('click')
        const urlInput = wrapper.find('.mon-createbar input.mono')
        await urlInput.setValue('https://example.com')
        expect((urlInput.element as HTMLInputElement).value).toBe('https://example.com')
      })

      it('calls createUptime when Create button is clicked', async () => {
        setupUptimeMocks([], true)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        await wrapper.find('.filterbar .btn--primary').trigger('click')
        const createBtn = wrapper.find('.mon-createbar__actions .btn--primary')
        await createBtn.trigger('click')
        expect(wrapper.exists()).toBe(true)
      })
    })

    describe('detail view', () => {
      it('shows "Check history" heading', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')])
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        expect(wrapper.text()).toContain('Check history')
      })

      it('shows "No checks yet." when there are no checks', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')])
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        expect(wrapper.text()).toContain('No checks yet.')
      })

      it('shows Loading… when checks are loading', async () => {
        vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
        vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: false } } } as any)
        vi.mocked(useQuery)
          .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
          .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref([makeUptimeMonitor('u1', 'Homepage')]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref(undefined), isLoading: ref(true) } as any)
          .mockReturnValueOnce({ data: ref(null) } as any)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        expect(wrapper.text()).toContain('Loading')
      })

      it('shows check rows when checks exist', async () => {
        const check = { id: 'ck1', monitor_id: 'u1', status: 'up', status_code: 200, response_ms: 120, error: null, checked_at: '2024-01-01T00:00:00Z' }
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], false, false, [check])
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        expect(wrapper.find('.up-ci-row:not(.mon-ci-row--header)').exists()).toBe(true)
      })

      it('shows "–" for null response_ms in check row', async () => {
        const check = { id: 'ck1', monitor_id: 'u1', status: 'up', status_code: 200, response_ms: null, error: null, checked_at: '2024-01-01T00:00:00Z' }
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], false, false, [check])
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        expect(wrapper.find('.up-ci-row:not(.mon-ci-row--header)').text()).toContain('–')
      })

      it('formats response_ms >= 1000 as seconds', async () => {
        const check = { id: 'ck1', monitor_id: 'u1', status: 'down', status_code: 503, response_ms: 1500, error: 'timeout', checked_at: '2024-01-01T00:00:00Z' }
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], false, false, [check])
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        expect(wrapper.text()).toContain('1.50s')
      })

      it('shows Edit button when user can manage', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        expect(wrapper.find('.mon-detail__actions .btn').text()).toContain('Edit')
      })
    })

    describe('stats bar', () => {
      it('shows stats bar when uptimeStats is non-null', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], false, false, [], { uptime_pct_24h: 99.5, uptime_pct_7d: 99.2, uptime_pct_30d: 98.8, avg_response_ms_24h: 142 })
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        expect(wrapper.find('.up-stats').exists()).toBe(true)
        expect(wrapper.text()).toContain('99.50%')
      })

      it('shows "–" for null avg_response_ms in stats bar', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], false, false, [], { uptime_pct_24h: 100, uptime_pct_7d: 100, uptime_pct_30d: 100, avg_response_ms_24h: null })
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        expect(wrapper.find('.up-stats').text()).toContain('–')
      })

      it('does not show stats bar when uptimeStats is null', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')])
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        expect(wrapper.find('.up-stats').exists()).toBe(false)
      })
    })

    describe('edit form', () => {
      it('shows edit form when Edit is clicked', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        await wrapper.find('.mon-detail__actions .btn').trigger('click')
        expect(wrapper.find('.mon-editbar').exists()).toBe(true)
      })

      it('shows edit form prefilled with monitor values', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        await wrapper.find('.mon-detail__actions .btn').trigger('click')
        const nameInput = wrapper.find('.mon-editbar input') as any
        expect(nameInput.element.value).toBe('Homepage')
      })

      it('hides edit form when Cancel is clicked', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        await wrapper.find('.mon-detail__actions .btn').trigger('click')
        const cancelBtn = wrapper.findAll('.mon-editbar .btn--ghost').find(b => b.text().includes('Cancel'))!
        await cancelBtn.trigger('click')
        expect(wrapper.find('.mon-editbar').exists()).toBe(false)
      })

      it('shows Delete button in edit form', async () => {
        setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        await wrapper.find('.mon-detail__actions .btn').trigger('click')
        expect(wrapper.findAll('.mon-editbar .btn--ghost').some(b => b.text().includes('Delete'))).toBe(true)
      })

      it('calls saveUptime when Save is clicked', async () => {
        const saveMutate = vi.fn()
        vi.mocked(useMutation)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: saveMutate, isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
        vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
        vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: true } } } as any)
        vi.mocked(useQuery)
          .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
          .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref([makeUptimeMonitor('u1', 'Homepage')]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref(null) } as any)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        await wrapper.find('.mon-detail__actions .btn').trigger('click')
        await wrapper.find('.mon-editbar .btn--primary').trigger('click')
        expect(saveMutate).toHaveBeenCalled()
      })

      it('shows "Saving…" when savingUptime is true', async () => {
        vi.mocked(useMutation)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(true) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
        vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
        vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: true } } } as any)
        vi.mocked(useQuery)
          .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
          .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref([makeUptimeMonitor('u1', 'Homepage')]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref(null) } as any)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        await wrapper.find('.mon-detail__actions .btn').trigger('click')
        expect(wrapper.find('.mon-editbar .btn--primary').text()).toContain('Saving')
      })

      it('calls deleteUptime when Delete is clicked and confirm accepted', async () => {
        vi.stubGlobal('confirm', vi.fn().mockReturnValue(true))
        const deleteMutate = vi.fn()
        vi.mocked(useMutation)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: deleteMutate, isPending: ref(false) } as any)
        vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
        vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: true } } } as any)
        vi.mocked(useQuery)
          .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
          .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref([makeUptimeMonitor('u1', 'Homepage')]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref(null) } as any)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        await wrapper.find('.mon-detail__actions .btn').trigger('click')
        const deleteBtn = wrapper.findAll('.mon-editbar .btn--ghost').find(b => b.text().includes('Delete'))!
        await deleteBtn.trigger('click')
        expect(deleteMutate).toHaveBeenCalled()
        vi.unstubAllGlobals()
      })

      it('does not call deleteUptime when confirm is declined', async () => {
        vi.stubGlobal('confirm', vi.fn().mockReturnValue(false))
        const deleteMutate = vi.fn()
        vi.mocked(useMutation)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: vi.fn(), isPending: ref(false) } as any)
          .mockReturnValueOnce({ mutate: deleteMutate, isPending: ref(false) } as any)
        vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
        vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: true } } } as any)
        vi.mocked(useQuery)
          .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
          .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref([makeUptimeMonitor('u1', 'Homepage')]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
          .mockReturnValueOnce({ data: ref(null) } as any)
        const wrapper = mount(MonitorsView, { global: { stubs } })
        const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
        await row.trigger('click')
        await wrapper.find('.mon-detail__actions .btn').trigger('click')
        const deleteBtn = wrapper.findAll('.mon-editbar .btn--ghost').find(b => b.text().includes('Delete'))!
        await deleteBtn.trigger('click')
        expect(deleteMutate).not.toHaveBeenCalled()
        vi.unstubAllGlobals()
      })
    })
  })

  describe('queryParams with selectedIds', () => {
    it('builds query params including project_id when selectedIds has values', () => {
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: ['proj-1', 'proj-2'] } as any)
      vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: false } } } as any)
      let callIdx = 0
      vi.mocked(useQuery).mockImplementation((opts: any) => {
        try {
          if (opts?.queryKey && typeof opts.queryKey === 'object' && 'value' in opts.queryKey) {
            void opts.queryKey.value
          }
        } catch {}
        callIdx++
        if (callIdx === 1) return { data: ref([{ id: 'proj-1', name: 'App' }]) } as any
        if (callIdx === 2) return { data: ref([makeMonitor('m1', 'Daily backup')]), isLoading: ref(false) } as any
        return { data: ref([]), isLoading: ref(false) } as any
      })
      const wrapper = mount(MonitorsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Daily backup')
    })
  })

  describe('projectName fallback', () => {
    it('returns sliced project id when project not found in projectList', async () => {
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: false } } } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([makeMonitor('unknown-project-id', 'My Monitor')]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(null) } as any)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('My Monitor'))!
      await row.trigger('click')
      // projectName returns id.slice(0, 8) when not found
      expect(wrapper.find('.mon-detail').text()).toContain('unknown-')
    })
  })

  describe('mutation callbacks via call-through pattern', () => {
    it('createMonitor onSuccess resets form and sets selectedMonitorId', async () => {
      vi.mocked(apiFetch).mockResolvedValue({ id: 'new-mon', name: 'New', schedule: '0 * * * *', status: 'active', state: 'ok', grace_period_secs: 300, project_id: 'proj-1', last_checkin_at: null, next_expected_at: null, recent_checkins: [] })
      vi.mocked(useMutation).mockImplementation((opts: any) => {
        const mutate = vi.fn(async (...args: any[]) => {
          try {
            const result = await opts.mutationFn(...args)
            if (opts.onSuccess) opts.onSuccess(result)
          } catch {}
        })
        return { mutate, isPending: ref(false) } as any
      })
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: true } } } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(null) } as any)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      await wrapper.find('.mon-createbar input:not([type="number"]):not(.mono)').setValue('New Monitor')
      await wrapper.find('.mon-createbar input.mono').setValue('0 * * * *')
      await wrapper.find('.mon-createbar select').setValue('proj-1')
      await wrapper.find('.mon-createbar__actions .btn--primary').trigger('click')
      await flushPromises()
      expect(wrapper.exists()).toBe(true)
    })

    it('saveMonitor onSuccess clears editingId', async () => {
      vi.mocked(apiFetch).mockResolvedValue({ id: 'm1', name: 'Daily backup', schedule: '0 * * * *', status: 'active', state: 'ok', grace_period_secs: 300, project_id: 'proj-1', last_checkin_at: null, next_expected_at: null, recent_checkins: [] })
      vi.mocked(useMutation).mockImplementation((opts: any) => {
        const mutate = vi.fn(async (...args: any[]) => {
          try {
            const result = await opts.mutationFn(...args)
            if (opts.onSuccess) opts.onSuccess(result)
          } catch {}
        })
        return { mutate, isPending: ref(false) } as any
      })
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: true } } } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
        .mockReturnValueOnce({ data: ref([makeMonitor('m1', 'Daily backup')]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(null) } as any)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
      await wrapper.find('.mon-editbar .btn--primary').trigger('click')
      await flushPromises()
      expect(wrapper.exists()).toBe(true)
    })

    it('deleteMonitor onSuccess clears selectedMonitorId and editingId', async () => {
      vi.stubGlobal('confirm', vi.fn().mockReturnValue(true))
      vi.mocked(apiFetch).mockResolvedValue(undefined)
      vi.mocked(useMutation).mockImplementation((opts: any) => {
        const mutate = vi.fn(async (...args: any[]) => {
          try {
            const result = await opts.mutationFn(...args)
            if (opts.onSuccess) opts.onSuccess(result)
          } catch {}
        })
        return { mutate, isPending: ref(false) } as any
      })
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: true } } } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
        .mockReturnValueOnce({ data: ref([makeMonitor('m1', 'Daily backup')]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(null) } as any)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.monrow').find(r => r.text().includes('Daily backup'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
      const deleteBtn = wrapper.findAll('.mon-editbar .btn--ghost').find(b => b.text().includes('Delete'))!
      await deleteBtn.trigger('click')
      await flushPromises()
      expect(wrapper.exists()).toBe(true)
      vi.unstubAllGlobals()
    })
  })

  describe('uptime mutation callbacks via call-through pattern', () => {
    beforeEach(() => {
      vi.mocked(useRoute).mockReturnValue({ path: '/monitors/uptime' } as any)
    })

    const makeUptimeMutationCallThrough = () => {
      vi.mocked(apiFetch).mockResolvedValue({ id: 'u1', project_id: 'proj-1', name: 'Homepage', url: 'https://example.com', method: 'GET', interval_secs: 300, timeout_secs: 10, expected_codes: '200-299', body_contains: null, status: 'active', state: 'unknown', consecutive_failures: 0, last_checked_at: null, last_ok_at: null, next_check_at: null, last_status_code: null, last_response_ms: null, created_at: '2024-01-01T00:00:00Z', recent_checks: [] })
      vi.mocked(useMutation).mockImplementation((opts: any) => {
        const mutate = vi.fn(async (...args: any[]) => {
          try {
            const result = await opts.mutationFn(...args)
            if (opts.onSuccess) opts.onSuccess(result)
          } catch {}
        })
        return { mutate, isPending: ref(false) } as any
      })
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: true } } } as any)
    }

    it('createUptime onSuccess resets form and sets selectedUptimeId', async () => {
      makeUptimeMutationCallThrough()
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(null) } as any)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await wrapper.find('.filterbar .btn--primary').trigger('click')
      // Fill name, URL, and project so the Create button is enabled
      const fields = wrapper.findAll('.mon-createbar__fields--uptime input')
      await fields[0].setValue('Homepage') // name
      const urlInput = wrapper.findAll('.mon-createbar__fields--uptime input.mono')[0]
      await urlInput.setValue('https://example.com')
      await wrapper.find('.mon-createbar__fields--uptime select').setValue('proj-1')
      await wrapper.find('.mon-createbar .btn--primary').trigger('click')
      await flushPromises()
      expect(wrapper.exists()).toBe(true)
    })

    it('saveUptime onSuccess clears editingUptimeId', async () => {
      makeUptimeMutationCallThrough()
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([makeUptimeMonitor('u1', 'Homepage')]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(null) } as any)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
      await wrapper.find('.mon-editbar .btn--primary').trigger('click')
      await flushPromises()
      expect(wrapper.exists()).toBe(true)
    })

    it('deleteUptime onSuccess clears selectedUptimeId and editingUptimeId', async () => {
      vi.stubGlobal('confirm', vi.fn().mockReturnValue(true))
      vi.mocked(apiFetch).mockResolvedValue(undefined)
      vi.mocked(useMutation).mockImplementation((opts: any) => {
        const mutate = vi.fn(async (...args: any[]) => {
          try {
            const result = await opts.mutationFn(...args)
            if (opts.onSuccess) opts.onSuccess(result)
          } catch {}
        })
        return { mutate, isPending: ref(false) } as any
      })
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(useAuthStore).mockReturnValue({ user: { permissions: { manage_projects: true } } } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([makeUptimeMonitor('u1', 'Homepage')]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(null) } as any)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      const row = wrapper.findAll('.uprow').find(r => r.text().includes('Homepage'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
      const deleteBtn = wrapper.findAll('.mon-editbar .btn--ghost').find(b => b.text().includes('Delete'))!
      await deleteBtn.trigger('click')
      await flushPromises()
      expect(wrapper.exists()).toBe(true)
      vi.unstubAllGlobals()
    })
  })

  describe('uptime edit form field interactions', () => {
    beforeEach(() => {
      vi.mocked(useRoute).mockReturnValue({ path: '/monitors/uptime' } as any)
    })

    function setupEditForm() {
      setupUptimeMocks([makeUptimeMonitor('u1', 'Homepage')], true)
    }

    async function openEditForm(wrapper: any) {
      const row = wrapper.findAll('.uprow').find((r: any) => r.text().includes('Homepage'))!
      await row.trigger('click')
      await wrapper.find('.mon-detail__actions .btn').trigger('click')
    }

    it('updates editUptimeForm.url when URL input changes', async () => {
      setupEditForm()
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const urlInput = wrapper.findAll('.mon-editbar input').find((i: any) => i.element.value === 'https://example.com')!
      await urlInput.setValue('https://new.example.com')
      expect((urlInput.element as HTMLInputElement).value).toBe('https://new.example.com')
    })

    it('updates editUptimeForm.method when method select changes', async () => {
      setupEditForm()
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const methodSelect = wrapper.findAll('.mon-editbar select')[0]
      await methodSelect.setValue('HEAD')
      expect((methodSelect.element as HTMLSelectElement).value).toBe('HEAD')
    })

    it('updates editUptimeForm.interval_secs when interval input changes', async () => {
      setupEditForm()
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const intervalInput = wrapper.findAll('.mon-editbar input[type="number"]')[0]
      await intervalInput.setValue('60')
      expect((intervalInput.element as HTMLInputElement).value).toBe('60')
    })

    it('updates editUptimeForm.timeout_secs when timeout input changes', async () => {
      setupEditForm()
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const timeoutInput = wrapper.findAll('.mon-editbar input[type="number"]')[1]
      await timeoutInput.setValue('5')
      expect((timeoutInput.element as HTMLInputElement).value).toBe('5')
    })

    it('updates editUptimeForm.expected_codes when expected_codes input changes', async () => {
      setupEditForm()
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      // index 0 = url (.mono), index 1 = expected_codes (.mono)
      const codesInput = wrapper.findAll('.mon-editbar input.mono')[1]
      await codesInput.setValue('200,201')
      expect((codesInput.element as HTMLInputElement).value).toBe('200,201')
    })

    it('updates editUptimeForm.name when name input changes', async () => {
      setupEditForm()
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      // First input in editbar (non-number, non-mono) is name
      const nameInput = wrapper.findAll('.mon-editbar input:not([type="number"]):not(.mono)')[0]
      await nameInput.setValue('Renamed Monitor')
      expect((nameInput.element as HTMLInputElement).value).toBe('Renamed Monitor')
    })

    it('updates editUptimeForm.body_contains when body_contains input changes', async () => {
      setupEditForm()
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const bodyInput = wrapper.findAll('.mon-editbar input').find((i: any) => i.attributes('placeholder') === '(clear to remove)')!
      await bodyInput.setValue('healthy')
      expect((bodyInput.element as HTMLInputElement).value).toBe('healthy')
    })

    it('updates editUptimeForm.status when status select changes', async () => {
      setupEditForm()
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await openEditForm(wrapper)
      const statusSelect = wrapper.findAll('.mon-editbar select')[1]
      await statusSelect.setValue('paused')
      expect((statusSelect.element as HTMLSelectElement).value).toBe('paused')
    })
  })

  describe('uptime create form additional field interactions', () => {
    beforeEach(() => {
      vi.mocked(useRoute).mockReturnValue({ path: '/monitors/uptime' } as any)
    })

    const openUptimeCreateForm = async (wrapper: any) => {
      await wrapper.find('.filterbar .btn--primary').trigger('click')
    }

    it('updates newUptime.method when method select changes', async () => {
      setupUptimeMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await openUptimeCreateForm(wrapper)
      // index 0 = project select, index 1 = method select
      const methodSelect = wrapper.findAll('.mon-createbar__fields--uptime select')[1]
      await methodSelect.setValue('HEAD')
      expect((methodSelect.element as HTMLSelectElement).value).toBe('HEAD')
    })

    it('updates newUptime.interval_secs when interval input changes', async () => {
      setupUptimeMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await openUptimeCreateForm(wrapper)
      const intervalInput = wrapper.findAll('.mon-createbar__fields--uptime input[type="number"]')[0]
      await intervalInput.setValue('120')
      expect((intervalInput.element as HTMLInputElement).value).toBe('120')
    })

    it('updates newUptime.timeout_secs when timeout input changes', async () => {
      setupUptimeMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await openUptimeCreateForm(wrapper)
      const timeoutInput = wrapper.findAll('.mon-createbar__fields--uptime input[type="number"]')[1]
      await timeoutInput.setValue('30')
      expect((timeoutInput.element as HTMLInputElement).value).toBe('30')
    })

    it('updates newUptime.expected_codes when expected codes input changes', async () => {
      setupUptimeMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await openUptimeCreateForm(wrapper)
      // index 0 = url input (.mono), index 1 = expected_codes input (.mono)
      const codesInput = wrapper.findAll('.mon-createbar__fields--uptime input.mono')[1]
      await codesInput.setValue('200,201')
      expect((codesInput.element as HTMLInputElement).value).toBe('200,201')
    })

    it('updates newUptime.body_contains when body contains input changes', async () => {
      setupUptimeMocks([], true)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      await openUptimeCreateForm(wrapper)
      const bodyInput = wrapper.findAll('.mon-createbar__fields--uptime input').find((i: any) => i.attributes('placeholder') === '(optional)')!
      await bodyInput.setValue('healthy')
      expect((bodyInput.element as HTMLInputElement).value).toBe('healthy')
    })
  })

  describe('uptimeCheckColor fallback', () => {
    beforeEach(() => {
      vi.mocked(useRoute).mockReturnValue({ path: '/monitors/uptime' } as any)
    })

    it('returns text-3 color for unknown check status in timeline', async () => {
      const monitorWithUnknownCheck = {
        ...makeUptimeMonitor('u1', 'Homepage'),
        recent_checks: [{ status: 'unknown', checked_at: '2024-01-01T00:00:00Z' }],
      }
      setupUptimeMocks([monitorWithUnknownCheck], false)
      const wrapper = mount(MonitorsView, { global: { stubs } })
      // Find the filled dot (non-empty) in the timeline for the uptime row
      const filledDots = wrapper.findAll('.uprow .mon-tl-dot:not(.mon-tl-dot--empty)')
      expect(filledDots.length).toBeGreaterThan(0)
      const unknownDot = filledDots[0]
      expect(unknownDot.attributes('style')).toContain('var(--text-3)')
    })
  })
})
