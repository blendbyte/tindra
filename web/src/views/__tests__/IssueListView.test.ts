import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

const pushMock = vi.fn()
const replaceMock = vi.fn()

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: pushMock, replace: replaceMock })),
  useRoute: vi.fn(() => ({ query: {} })),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(() => ({ mutate: vi.fn(), isPending: ref(false) })),
  useQueryClient: vi.fn(() => ({ invalidateQueries: vi.fn() })),
}))

vi.mock('@tanstack/vue-virtual', () => ({
  useWindowVirtualizer: vi.fn(() => ref({
    getVirtualItems: () => [],
    getTotalSize: () => 0,
    options: { scrollMargin: 0 },
  })),
}))

vi.mock('@/stores/projects', () => ({
  useProjectsStore: vi.fn(),
}))

vi.mock('@/stores/issueNav', () => ({
  useIssueNavStore: vi.fn(() => ({ set: vi.fn(), ids: [] })),
}))

vi.mock('@/composables/useToast', () => ({
  useToast: vi.fn(() => ({ show: vi.fn() })),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

vi.mock('@/utils/formatters', () => ({
  formatRel: vi.fn(() => '2m ago'),
}))

import IssueListView from '../IssueListView.vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'

const stubs = {
  RouterLink: { template: '<a><slot /></a>' },
  Icon: { template: '<span />' },
  FilterChip: { template: '<div />' },
  Sparkline: { template: '<span />' },
  BrandMark: { template: '<span />' },
  IgnoreButton: { template: '<div />' },
}

function setupMocks({ projects = [], selectedIds = [], issueData = undefined as unknown } = {}) {
  vi.mocked(useProjectsStore).mockReturnValue({
    projects,
    selectedIds,
    toggleProject: vi.fn(),
    setSelected: vi.fn(),
  } as any)

  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
    .mockReturnValueOnce({ data: ref(issueData), isFetching: ref(false), isError: ref(false), refetch: vi.fn() } as any)
    .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)
}

beforeEach(() => {
  vi.mocked(useQuery).mockReset()
  vi.mocked(useProjectsStore).mockReset()
  pushMock.mockReset()
  replaceMock.mockReset()
})

describe('IssueListView', () => {
  describe('empty states', () => {
    it('shows "No projects yet" when there are no projects', () => {
      setupMocks({ projects: [] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(wrapper.text()).toContain('No projects yet')
    })

    it('shows "No open issues" when issues exist but filtered view is empty', () => {
      // noOpenIssues = !noIssues && filtered.length === 0 && !isFiltered
      // Need allIssues > 0 but filtered = 0.
      // Use selectedIds that doesn't match any issue's project_id.
      setupMocks({
        projects: [{ id: 'proj-1', name: 'App' }],
        selectedIds: ['proj-999'],
        issueData: {
          issues: [{ id: 'iss-1', title: 'Err', level: 'error', status: 'open', project_id: 'proj-1', event_count: 1, user_count: 1, last_seen: '2024-01-01T00:00:00Z', first_seen: '2024-01-01T00:00:00Z', kind: 'error', release: null, assignee_id: null, ignore_until: null, ignore_count_limit: null, ignore_count: null }],
          total: 1,
          has_more: false,
        },
      })
      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(wrapper.text()).toContain('No open issues')
    })

    it('shows "All clear" when there are no issues at all', () => {
      setupMocks({
        projects: [{ id: '1', name: 'App' }],
        selectedIds: [],
        issueData: undefined,
      })
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [{ id: '1', name: 'App' }],
        selectedIds: [],
        toggleProject: vi.fn(),
        setSelected: vi.fn(),
      } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)

      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(wrapper.text()).toContain('All clear')
    })
  })

  describe('filter bar', () => {
    it('renders the search input', () => {
      setupMocks()
      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(wrapper.find('input[aria-label="Search issues"]').exists()).toBe(true)
    })

    it('renders the refresh button', () => {
      setupMocks()
      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(wrapper.find('.filterbar__refresh').exists()).toBe(true)
    })

    it('renders the export button', () => {
      setupMocks()
      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(wrapper.find('.export-menu__trigger').exists()).toBe(true)
    })
  })

  describe('error state', () => {
    it('shows error message when loading issues fails', () => {
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [{ id: '1', name: 'App' }],
        selectedIds: [],
        toggleProject: vi.fn(),
        setSelected: vi.fn(),
      } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false), isError: ref(true), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)

      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(wrapper.text()).toContain('Failed to load issues')
    })
  })

  describe('issue list', () => {
    it('renders a list header when issues are present', () => {
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [{ id: '1', name: 'App' }],
        selectedIds: [],
        toggleProject: vi.fn(),
        setSelected: vi.fn(),
      } as any)

      const issues = [
        {
          id: 'iss-1',
          title: 'TypeError: cannot read property',
          level: 'error',
          status: 'open',
          environment: 'production',
          project_id: '1',
          event_count: 42,
          user_count: 5,
          last_seen: '2024-01-01T00:00:00Z',
          first_seen: '2024-01-01T00:00:00Z',
          sparkline: [],
          kind: 'error',
        },
      ]

      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({
          data: ref({ issues, total: 1, has_more: false }),
          isFetching: ref(false),
          isError: ref(false),
          refetch: vi.fn(),
        } as any)
        .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)

      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(wrapper.find('.issuerow--header').exists()).toBe(true)
    })
  })
})
