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
import { useQuery } from '@tanstack/vue-query'
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

function setupMocks(monitors: unknown[] = [], canManage = false, isLoading = false) {
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
  vi.mocked(useAuthStore).mockReturnValue({
    user: { permissions: { manage_projects: canManage } },
  } as any)

  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref([{ id: 'proj-1', name: 'App' }]) } as any)
    .mockReturnValueOnce({ data: ref(monitors), isLoading: ref(isLoading) } as any)
    .mockReturnValueOnce({ data: ref([]), isLoading: ref(false) } as any)
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
})
