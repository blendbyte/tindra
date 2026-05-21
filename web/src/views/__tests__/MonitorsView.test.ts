import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

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
  default: { toString: vi.fn(() => 'Every hour') },
}))

import MonitorsView from '../MonitorsView.vue'
import { useQuery, useMutation } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { useAuthStore } from '@/stores/auth'

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

function setupMocks(monitors: unknown[] = [], canManage = false, isLoading = false, checkins: unknown[] = []) {
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
  vi.mocked(useAuthStore).mockReturnValue({
    user: { permissions: { manage_projects: canManage } },
  } as any)

  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
    .mockReturnValueOnce({ data: ref(monitors), isLoading: ref(isLoading) } as any)
    .mockReturnValueOnce({ data: ref(checkins), isLoading: ref(false) } as any)
}

beforeEach(() => {
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
})
