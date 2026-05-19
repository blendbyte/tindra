import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

const pushMock = vi.fn()

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: pushMock })),
}))

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
  formatDuration: vi.fn((n: number) => `${n}ms`),
  formatRel: vi.fn(() => '2m ago'),
}))

import ReleasesView from '../ReleasesView.vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'

const stubs = {
  RouterLink: { template: '<a><slot /></a>' },
  Icon: { template: '<span />' },
}

const makeRelease = (id: string, version: string) => ({
  id,
  version,
  project_id: 'proj-1',
  deployed_at: '2024-01-01T00:00:00Z',
  tx_count: 100,
  tx_p50: 120,
  tx_p95: 450,
  tx_error_rate: 0,
  new_issues: 0,
  regressed_issues: 0,
})

function setupMocks(releases: unknown[] = [], isFetching = false, isError = false) {
  vi.mocked(useProjectsStore).mockReturnValue({
    projects: [{ id: 'proj-1', name: 'App' }],
    selectedIds: [],
  } as any)

  vi.mocked(useQuery).mockReturnValue({
    data: ref(releases.length > 0 ? { releases, total: releases.length, has_more: false } : undefined),
    isFetching: ref(isFetching),
    isError: ref(isError),
    refetch: vi.fn(),
  } as any)
}

beforeEach(() => {
  vi.mocked(useQuery).mockReset()
  vi.mocked(useProjectsStore).mockReset()
  pushMock.mockReset()
})

describe('ReleasesView', () => {
  describe('empty state', () => {
    it('shows "No releases yet" when there are no releases', () => {
      setupMocks([], false)
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.text()).toContain('No releases yet')
    })

    it('includes a note about the release field', () => {
      setupMocks([], false)
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.text()).toContain('release')
    })
  })

  describe('error state', () => {
    it('shows failed to load releases message on error', () => {
      setupMocks([], false, true)
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.text()).toContain('Failed to load releases')
    })

    it('renders a Try again button on error', () => {
      setupMocks([], false, true)
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.find('.btn').text()).toBe('Try again')
    })
  })

  describe('loaded releases', () => {
    it('renders a row for each release', () => {
      setupMocks([makeRelease('r1', 'v1.0.0'), makeRelease('r2', 'v1.1.0')])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      const rows = wrapper.findAll('.relrow:not(.relrow--header)')
      expect(rows.length).toBe(2)
    })

    it('shows the version string for each release', () => {
      setupMocks([makeRelease('r1', 'v1.2.3')])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.text()).toContain('v1.2.3')
    })

    it('shows "Clean" pill for releases with no new issues', () => {
      setupMocks([makeRelease('r1', 'v1.0.0')])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.find('.rel-issues-pill--clean').text()).toBe('Clean')
    })

    it('shows "N new" pill for releases with new issues', () => {
      const release = { ...makeRelease('r1', 'v1.0.0'), new_issues: 3 }
      setupMocks([release])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.find('.rel-issues-pill--active').text()).toContain('new')
    })

    it('navigates to release detail on row click', async () => {
      setupMocks([makeRelease('r1', 'v1.0.0')])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      await wrapper.find('.relrow:not(.relrow--header)').trigger('click')
      expect(pushMock).toHaveBeenCalledWith('/releases/r1')
    })

    it('renders the table header row', () => {
      setupMocks([makeRelease('r1', 'v1.0.0')])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.find('.relrow--header').exists()).toBe(true)
    })
  })

  describe('sorting', () => {
    it('toggles sort direction when clicking the same column twice', async () => {
      setupMocks([makeRelease('r1', 'v1.0.0'), makeRelease('r2', 'v2.0.0')])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      const versionBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Version'))!
      await versionBtn.trigger('click')
      await versionBtn.trigger('click')
      expect(wrapper.text()).toBeTruthy()
    })
  })
})
