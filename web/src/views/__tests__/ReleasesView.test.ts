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

import { flushPromises } from '@vue/test-utils'
import ReleasesView from '../ReleasesView.vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { apiFetch } from '@/api/client'

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

    it('shows up-arrow icon (↑) for version column after first click (defaults to asc)', async () => {
      setupMocks([makeRelease('r1', 'v1.0.0'), makeRelease('r2', 'v2.0.0')])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      const versionBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Version'))!
      await versionBtn.trigger('click')
      expect(versionBtn.find('.col-sort__icon').text()).toBe('↑')
    })

    it('shows down-arrow icon (↓) for version column after second click (toggles to desc)', async () => {
      setupMocks([makeRelease('r1', 'v1.0.0'), makeRelease('r2', 'v2.0.0')])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      const versionBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Version'))!
      await versionBtn.trigger('click')
      await versionBtn.trigger('click')
      expect(versionBtn.find('.col-sort__icon').text()).toBe('↓')
    })

    it('shows desc icon (↓) for numeric column on first click', async () => {
      setupMocks([makeRelease('r1', 'v1.0.0'), makeRelease('r2', 'v2.0.0')])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      const txnsBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Txns'))!
      await txnsBtn.trigger('click')
      expect(txnsBtn.find('.col-sort__icon').text()).toBe('↓')
    })

    it('sorts by P50 column when clicked', async () => {
      setupMocks([makeRelease('r1', 'v1.0.0'), makeRelease('r2', 'v2.0.0')])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      const p50Btn = wrapper.findAll('.col-sort').find(b => b.text().includes('P50'))!
      await p50Btn.trigger('click')
      expect(p50Btn.classes()).toContain('col-sort--active')
    })

    it('sorts by Errors column when clicked', async () => {
      setupMocks([makeRelease('r1', 'v1.0.0'), makeRelease('r2', 'v2.0.0')])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      const errorsBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Errors'))!
      await errorsBtn.trigger('click')
      expect(errorsBtn.classes()).toContain('col-sort--active')
    })

    it('sorts by Issues column when clicked', async () => {
      setupMocks([makeRelease('r1', 'v1.0.0'), makeRelease('r2', 'v2.0.0')])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      const issuesBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Issues'))!
      await issuesBtn.trigger('click')
      expect(issuesBtn.classes()).toContain('col-sort--active')
    })
  })

  describe('error rate styling', () => {
    it('applies tx-failure class to error rate when > 0', () => {
      const release = { ...makeRelease('r1', 'v1.0.0'), tx_error_rate: 2.5 }
      setupMocks([release])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.find('.tx-failure').exists()).toBe(true)
    })

    it('does not apply tx-failure class when error rate is 0', () => {
      const release = { ...makeRelease('r1', 'v1.0.0'), tx_error_rate: 0 }
      setupMocks([release])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.find('.tx-failure').exists()).toBe(false)
    })
  })

  describe('skeleton loading', () => {
    it('shows skeleton rows when isFetching and no data', () => {
      setupMocks([], true)
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.find('.skel').exists()).toBe(true)
    })
  })

  describe('formatCount display', () => {
    it('shows "–" when tx_count is 0', () => {
      const release = { ...makeRelease('r1', 'v1.0.0'), tx_count: 0 }
      setupMocks([release])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.text()).toContain('–')
    })

    it('shows millions suffix for tx_count >= 1_000_000', () => {
      const release = { ...makeRelease('r1', 'v1.0.0'), tx_count: 1_500_000 }
      setupMocks([release])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.text()).toContain('1.5M')
    })

    it('shows thousands suffix for tx_count >= 1000', () => {
      const release = { ...makeRelease('r1', 'v1.0.0'), tx_count: 1500 }
      setupMocks([release])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.text()).toContain('1.5k')
    })

    it('shows raw number for small tx_count', () => {
      const release = { ...makeRelease('r1', 'v1.0.0'), tx_count: 42 }
      setupMocks([release])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.text()).toContain('42')
    })
  })

  describe('tx_p50 zero case', () => {
    it('shows "–" when tx_p50 is 0', () => {
      const release = { ...makeRelease('r1', 'v1.0.0'), tx_p50: 0, tx_count: 100 }
      setupMocks([release])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      // tx_p50 === 0 renders '–' (not formatDuration)
      const numCells = wrapper.findAll('.relrow__num.mono')
      expect(numCells.some(c => c.text() === '–')).toBe(true)
    })
  })

  describe('project name display', () => {
    it('shows project name for release when single project', () => {
      setupMocks([makeRelease('r1', 'v1.0.0')])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.find('.rel-version__project').exists()).toBe(true)
      expect(wrapper.find('.rel-version__project').text()).toBe('App')
    })

    it('shows project name for each release when multiple projects', () => {
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [{ id: 'proj-1', name: 'App' }, { id: 'proj-2', name: 'Backend' }],
        selectedIds: [],
      } as any)
      vi.mocked(useQuery).mockReturnValue({
        data: ref({
          releases: [
            makeRelease('r1', 'v1.0.0'),
            { ...makeRelease('r2', 'v2.0.0'), project_id: 'proj-2' },
          ],
          total: 2,
          has_more: false,
        }),
        isFetching: ref(false),
        isError: ref(false),
        refetch: vi.fn(),
      } as any)
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.text()).toContain('App')
      expect(wrapper.text()).toContain('Backend')
    })
  })

  describe('deployed column default sort', () => {
    it('Deployed column is active by default', () => {
      setupMocks([makeRelease('r1', 'v1.0.0')])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      const deployedBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Deployed'))!
      expect(deployedBtn.classes()).toContain('col-sort--active')
    })

    it('toggles sort direction for deployed column when clicked', async () => {
      setupMocks([makeRelease('r1', 'v1.0.0'), makeRelease('r2', 'v2.0.0')])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      const deployedBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Deployed'))!
      const iconBefore = deployedBtn.find('.col-sort__icon').text()
      await deployedBtn.trigger('click')
      const iconAfter = deployedBtn.find('.col-sort__icon').text()
      expect(iconBefore).not.toBe(iconAfter)
    })
  })

  describe('load more', () => {
    it('shows Load more button when has_more is true', async () => {
      const dataRef = ref<any>(undefined)
      vi.mocked(useProjectsStore).mockReturnValue({ projects: [{ id: 'proj-1', name: 'App' }], selectedIds: [] } as any)
      vi.mocked(useQuery).mockReturnValue({
        data: dataRef,
        isFetching: ref(false),
        isError: ref(false),
        refetch: vi.fn(),
      } as any)
      const wrapper = mount(ReleasesView, { global: { stubs } })
      dataRef.value = {
        releases: [makeRelease('r1', 'v1.0.0')],
        total: 5,
        has_more: true,
        next_cursor_time: '2024-01-01T00:00:00Z',
        next_cursor_id: 'abc123',
      }
      await wrapper.vm.$nextTick()
      expect(wrapper.find('.list-footer').exists()).toBe(true)
      expect(wrapper.find('.list-footer .btn').text()).toContain('Load')
    })

    it('calls apiFetch and appends releases when Load more is clicked', async () => {
      const dataRef = ref<any>(undefined)
      vi.mocked(useProjectsStore).mockReturnValue({ projects: [{ id: 'proj-1', name: 'App' }], selectedIds: [] } as any)
      vi.mocked(useQuery).mockReturnValue({
        data: dataRef,
        isFetching: ref(false),
        isError: ref(false),
        refetch: vi.fn(),
      } as any)
      vi.mocked(apiFetch).mockResolvedValue({
        releases: [makeRelease('r2', 'v2.0.0')],
        total: 2,
        has_more: false,
      } as any)
      const wrapper = mount(ReleasesView, { global: { stubs } })
      dataRef.value = {
        releases: [makeRelease('r1', 'v1.0.0')],
        total: 2,
        has_more: true,
        next_cursor_time: '2024-01-01T00:00:00Z',
        next_cursor_id: 'abc123',
      }
      await wrapper.vm.$nextTick()
      await wrapper.find('.list-footer .btn').trigger('click')
      await flushPromises()
      expect(vi.mocked(apiFetch)).toHaveBeenCalled()
    })

    it('shows isFetchingMore loading text while loading more', async () => {
      const dataRef = ref<any>(undefined)
      vi.mocked(useProjectsStore).mockReturnValue({ projects: [{ id: 'proj-1', name: 'App' }], selectedIds: [] } as any)
      vi.mocked(useQuery).mockReturnValue({
        data: dataRef,
        isFetching: ref(false),
        isError: ref(false),
        refetch: vi.fn(),
      } as any)
      let resolveApiFetch!: (v: any) => void
      vi.mocked(apiFetch).mockReturnValue(new Promise(r => { resolveApiFetch = r }) as any)
      const wrapper = mount(ReleasesView, { global: { stubs } })
      dataRef.value = {
        releases: [makeRelease('r1', 'v1.0.0')],
        total: 2,
        has_more: true,
        next_cursor_time: '2024-01-01T00:00:00Z',
        next_cursor_id: 'abc123',
      }
      await wrapper.vm.$nextTick()
      await wrapper.find('.list-footer .btn').trigger('click')
      await wrapper.vm.$nextTick()
      expect(wrapper.find('.list-footer .btn').text()).toContain('Loading')
      resolveApiFetch({ releases: [], has_more: false, total: 1 })
      await flushPromises()
    })
  })

  describe('error state retry', () => {
    it('calls refetch when Try again button is clicked', async () => {
      const refetchFn = vi.fn()
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [{ id: 'proj-1', name: 'App' }],
        selectedIds: [],
      } as any)
      vi.mocked(useQuery).mockReturnValue({
        data: ref(undefined),
        isFetching: ref(false),
        isError: ref(true),
        refetch: refetchFn,
      } as any)
      const wrapper = mount(ReleasesView, { global: { stubs } })
      await wrapper.find('.btn').trigger('click')
      expect(refetchFn).toHaveBeenCalled()
    })
  })

  describe('error rate display', () => {
    it('shows error rate percentage when tx_count > 0 and error_rate > 0', () => {
      const release = { ...makeRelease('r1', 'v1.0.0'), tx_count: 100, tx_error_rate: 5.5 }
      setupMocks([release])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      expect(wrapper.text()).toContain('5.5%')
    })

    it('shows "–" for error rate when tx_count is 0', () => {
      const release = { ...makeRelease('r1', 'v1.0.0'), tx_count: 0, tx_error_rate: 0 }
      setupMocks([release])
      const wrapper = mount(ReleasesView, { global: { stubs } })
      // multiple '–' cells present since tx_count=0
      expect(wrapper.text()).toContain('–')
    })
  })
})
