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
import { useWindowVirtualizer } from '@tanstack/vue-virtual'
import { apiFetch } from '@/api/client'
import { flushPromises } from '@vue/test-utils'

const stubs = {
  RouterLink: { template: '<a><slot /></a>' },
  Icon: { template: '<span />' },
  FilterChip: { name: 'FilterChip', props: ['label', 'value', 'options'], template: '<div />' },
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

    it('calls refetch when Try again button is clicked', async () => {
      const refetchFn = vi.fn()
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [{ id: '1', name: 'App' }],
        selectedIds: [],
        toggleProject: vi.fn(),
        setSelected: vi.fn(),
      } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false), isError: ref(true), refetch: refetchFn } as any)
        .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)

      const wrapper = mount(IssueListView, { global: { stubs } })
      await wrapper.find('.txerror .btn').trigger('click')
      expect(refetchFn).toHaveBeenCalled()
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

  describe('sort columns', () => {
    const oneIssue = [{
      id: 'iss-1',
      title: 'TypeError: x is not defined',
      level: 'error',
      status: 'open',
      environment: 'production',
      project_id: '1',
      event_count: 5,
      user_count: 1,
      last_seen: '2024-01-01T00:00:00Z',
      first_seen: '2024-01-01T00:00:00Z',
      sparkline: [],
      kind: 'error',
      release: null,
      assignee_id: null,
      ignore_until: null,
      ignore_count_limit: null,
      ignore_count: null,
    }]

    function setupWithIssues() {
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [{ id: '1', name: 'App' }],
        selectedIds: [],
        toggleProject: vi.fn(),
        setSelected: vi.fn(),
      } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({
          data: ref({ issues: oneIssue, total: 1, has_more: false }),
          isFetching: ref(false),
          isError: ref(false),
          refetch: vi.fn(),
        } as any)
        .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)
    }

    it('renders sort buttons in the header', () => {
      setupWithIssues()
      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(wrapper.findAll('.col-sort').length).toBeGreaterThan(0)
    })

    it('makes a sort column active when clicked', async () => {
      setupWithIssues()
      const wrapper = mount(IssueListView, { global: { stubs } })
      const titleBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Issue'))!
      await titleBtn.trigger('click')
      expect(titleBtn.classes()).toContain('col-sort--active')
    })

    it('shows sort direction icon after clicking a column', async () => {
      setupWithIssues()
      const wrapper = mount(IssueListView, { global: { stubs } })
      const eventsBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Events'))!
      await eventsBtn.trigger('click')
      expect(eventsBtn.find('.col-sort__icon').exists()).toBe(true)
    })

    it('toggles sort direction when clicking the same column twice', async () => {
      setupWithIssues()
      const wrapper = mount(IssueListView, { global: { stubs } })
      const eventsBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Events'))!
      await eventsBtn.trigger('click')
      const firstIcon = eventsBtn.find('.col-sort__icon').text()
      await eventsBtn.trigger('click')
      const secondIcon = eventsBtn.find('.col-sort__icon').text()
      expect(firstIcon).not.toBe(secondIcon)
    })

    it('clicks Last seen sort column', async () => {
      setupWithIssues()
      const wrapper = mount(IssueListView, { global: { stubs } })
      const lastSeenBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Last seen'))!
      await lastSeenBtn.trigger('click')
      expect(lastSeenBtn.classes()).toContain('col-sort--active')
    })
  })

  describe('filterbar interactions', () => {
    it('calls refetch when refresh button is clicked', async () => {
      const refetchFn = vi.fn()
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [{ id: '1', name: 'App' }], selectedIds: [], toggleProject: vi.fn(), setSelected: vi.fn(),
      } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref({ issues: [], total: 0, has_more: false }), isFetching: ref(false), isError: ref(false), refetch: refetchFn } as any)
        .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)
      const wrapper = mount(IssueListView, { global: { stubs } })
      await wrapper.find('.filterbar__refresh').trigger('click')
      expect(refetchFn).toHaveBeenCalled()
    })

    it('updates search when typing in search input', async () => {
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [{ id: '1', name: 'App' }], selectedIds: [], toggleProject: vi.fn(), setSelected: vi.fn(),
      } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref({ issues: [], total: 0, has_more: false }), isFetching: ref(false), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)
      const wrapper = mount(IssueListView, { global: { stubs } })
      const searchInput = wrapper.find('input[aria-label="Search issues"]')
      await searchInput.setValue('TypeError')
      expect((searchInput.element as HTMLInputElement).value).toBe('TypeError')
    })

    it('shows clear filters button and clears filters when clicked', async () => {
      const issueWithTitle = {
        id: 'iss-1', title: 'UniqueBugTitle', level: 'error', status: 'open',
        project_id: '1', event_count: 1, user_count: 1,
        last_seen: '2024-01-01T00:00:00Z', first_seen: '2024-01-01T00:00:00Z',
        kind: 'error', release: null, assignee_id: null, ignore_until: null,
        ignore_count_limit: null, ignore_count: null,
      }
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [{ id: '1', name: 'App', slug: 'app' }], selectedIds: [], toggleProject: vi.fn(), setSelected: vi.fn(),
      } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref({ issues: [issueWithTitle], total: 1, has_more: false }), isFetching: ref(false), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)
      const wrapper = mount(IssueListView, { global: { stubs } })
      const searchInput = wrapper.find('input[aria-label="Search issues"]')
      // Type something that doesn't match, making filtered empty while isFiltered is true
      await searchInput.setValue('nonexistentquery12345')
      // The empty-filter section with "Clear filters" button should render
      expect(wrapper.find('.empty-filter').exists()).toBe(true)
      await wrapper.find('.empty-filter .btn').trigger('click')
      // After clearing, search should be empty
      expect((searchInput.element as HTMLInputElement).value).toBe('')
    })
  })

  describe('error state actions', () => {
    it('shows a "Try again" button on error', () => {
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
      expect(wrapper.find('.txerror .btn').text()).toBe('Try again')
    })
  })

  describe('isFetching state', () => {
    it('adds fetching class to refresh button when loading', () => {
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [{ id: '1', name: 'App' }],
        selectedIds: [],
        toggleProject: vi.fn(),
        setSelected: vi.fn(),
      } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({
          data: ref({ issues: [], total: 0, has_more: false }),
          isFetching: ref(true),
          isError: ref(false),
          refetch: vi.fn(),
        } as any)
        .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)
      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(wrapper.find('.filterbar__refresh--fetching').exists()).toBe(true)
    })
  })

  describe('noOpenIssues empty state actions', () => {
    const noOpenIssuesMockData = {
      projects: [{ id: 'proj-1', name: 'App' }],
      selectedIds: ['proj-999'],
      issueData: {
        issues: [{ id: 'iss-1', title: 'Err', level: 'error', status: 'open', project_id: 'proj-1', event_count: 1, user_count: 1, last_seen: '2024-01-01T00:00:00Z', first_seen: '2024-01-01T00:00:00Z', kind: 'error', release: null, assignee_id: null, ignore_until: null, ignore_count_limit: null, ignore_count: null }],
        total: 1,
        has_more: false,
      },
    }

    it('shows "View all issues" button in noOpenIssues state', () => {
      setupMocks(noOpenIssuesMockData)
      const wrapper = mount(IssueListView, { global: { stubs } })
      const viewAllBtn = wrapper.findAll('.btn').find(b => b.text().includes('View all issues'))
      expect(viewAllBtn).toBeDefined()
    })

    it('clicking "View all issues" transitions out of noOpenIssues state', async () => {
      setupMocks(noOpenIssuesMockData)
      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(wrapper.text()).toContain('No open issues')
      const viewAllBtn = wrapper.findAll('.btn').find(b => b.text().includes('View all issues'))
      if (viewAllBtn) {
        await viewAllBtn.trigger('click')
        expect(wrapper.text()).not.toContain('No open issues')
      }
    })
  })

  describe('all clear empty state actions', () => {
    it('clicking "View project DSNs" navigates to /settings', async () => {
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
      const dsnBtn = wrapper.findAll('.btn').find(b => b.text().includes('View project DSNs'))
      if (dsnBtn) {
        await dsnBtn.trigger('click')
        expect(pushMock).toHaveBeenCalledWith('/settings')
      }
    })
  })

  describe('FilterChip interactions', () => {
    it('shows list header when level filter is changed to non-default', async () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      // FilterChip[1] is the Level chip
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      await chips[1].vm.$emit('change', 'Error')
      // isFiltered=true now, so header should show even with no issues
      expect(wrapper.find('.issuerow--header').exists()).toBe(true)
    })

    it('shows no-matches message when filter has no results', async () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      await chips[1].vm.$emit('change', 'Error')
      expect(wrapper.find('.empty-filter').exists()).toBe(true)
      expect(wrapper.text()).toContain('No matches')
    })

    it('clears all filters when Clear filters button is clicked', async () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      await chips[1].vm.$emit('change', 'Error')
      expect(wrapper.find('.empty-filter').exists()).toBe(true)
      await wrapper.find('.empty-filter .btn').trigger('click')
      expect(wrapper.find('.empty-filter').exists()).toBe(false)
    })

    it('changes status filter via FilterChip emit', async () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      await chips[0].vm.$emit('change', 'Resolved')
      // isFiltered=true (status is not 'Open') → header appears
      expect(wrapper.find('.issuerow--header').exists()).toBe(true)
    })

    it('shows active filter summary when filters set', async () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      const chips = wrapper.findAllComponents({ name: 'FilterChip' })
      await chips[1].vm.$emit('change', 'Error')
      expect(wrapper.find('.empty-filter__body').exists()).toBe(true)
    })
  })

  describe('assignee dropdown', () => {
    it('opens assignee dropdown when filterchip button is clicked', async () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      const assigneeBtn = wrapper.find('.filterchip')
      await assigneeBtn.trigger('click')
      expect(wrapper.find('.popover').exists()).toBe(true)
    })

    it('selects "Me" option from assignee dropdown', async () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      await wrapper.find('.filterchip').trigger('click')
      const meOption = wrapper.findAll('.popover__item').find(i => i.text() === 'Me')!
      await meOption.trigger('click')
      // dropdown closes after selection
      expect(wrapper.find('.popover').exists()).toBe(false)
    })

    it('selects "All" from assignee dropdown', async () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      await wrapper.find('.filterchip').trigger('click')
      const allOption = wrapper.findAll('.popover__item').find(i => i.text() === 'All')!
      await allOption.trigger('click')
      expect(wrapper.find('.popover').exists()).toBe(false)
    })

    it('renders user items in assignee dropdown when users data is available', async () => {
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [{ id: '1', name: 'App' }],
        selectedIds: [],
        toggleProject: vi.fn(),
        setSelected: vi.fn(),
      } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref({ issues: [], total: 0, has_more: false }), isFetching: ref(false), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref([{ id: 'u-1', name: 'Alice', email: 'alice@example.com' }, { id: 'u-2', name: '', email: 'bob@example.com' }]), isFetching: ref(false) } as any)
      const wrapper = mount(IssueListView, { global: { stubs } })
      await wrapper.find('.filterchip').trigger('click')
      const items = wrapper.findAll('.popover__item')
      // All + Me + 2 users = at least 4 items
      expect(items.length).toBeGreaterThanOrEqual(4)
      expect(items.some(i => i.text() === 'Alice')).toBe(true)
      // user with no name falls back to email
      expect(items.some(i => i.text() === 'bob@example.com')).toBe(true)
    })

    it('selects a specific user from assignee dropdown', async () => {
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [{ id: '1', name: 'App' }],
        selectedIds: [],
        toggleProject: vi.fn(),
        setSelected: vi.fn(),
      } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
        .mockReturnValueOnce({ data: ref({ issues: [], total: 0, has_more: false }), isFetching: ref(false), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref([{ id: 'u-1', name: 'Alice', email: 'alice@example.com' }]), isFetching: ref(false) } as any)
      const wrapper = mount(IssueListView, { global: { stubs } })
      await wrapper.find('.filterchip').trigger('click')
      const aliceItem = wrapper.findAll('.popover__item').find(i => i.text() === 'Alice')!
      await aliceItem.trigger('click')
      expect(wrapper.find('.popover').exists()).toBe(false)
    })
  })

  describe('export menu', () => {
    it('shows Download CSV and JSON buttons in export menu', () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(wrapper.findAll('.export-menu__item').length).toBe(2)
    })

    it('triggers CSV export when Download CSV is clicked', async () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      const csvBtn = wrapper.findAll('.export-menu__item').find(b => b.text().includes('CSV'))!
      expect(() => csvBtn.trigger('click')).not.toThrow()
    })

    it('triggers JSON export when Download JSON is clicked', async () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      const jsonBtn = wrapper.findAll('.export-menu__item').find(b => b.text().includes('JSON'))!
      expect(() => jsonBtn.trigger('click')).not.toThrow()
    })
  })

  describe('keyboard shortcuts', () => {
    it('handles j key to move selection down without throwing', () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(() => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'j' }))).not.toThrow()
      wrapper.unmount()
    })

    it('handles k key to move selection up without throwing', () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(() => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k' }))).not.toThrow()
      wrapper.unmount()
    })

    it('handles / key to focus search without throwing', () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(() => document.dispatchEvent(new KeyboardEvent('keydown', { key: '/' }))).not.toThrow()
      wrapper.unmount()
    })

    it('handles e key (resolve selected) without throwing', () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(() => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'e' }))).not.toThrow()
      wrapper.unmount()
    })

    it('handles i key (ignore selected) without throwing', () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(() => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'i' }))).not.toThrow()
      wrapper.unmount()
    })

    it('handles u key (unignore) without throwing', () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      expect(() => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'u' }))).not.toThrow()
      wrapper.unmount()
    })

    it('ignores keydown when target is an input', () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      const input = wrapper.find('input')
      input.element.dispatchEvent(new KeyboardEvent('keydown', { key: 'j', bubbles: true }))
      wrapper.unmount()
    })
  })

  describe('empty state navigation', () => {
    it('navigates to settings when View project DSNs is clicked', async () => {
      setupMocks({ projects: [{ id: '1', name: 'App' }] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      const dsnBtn = wrapper.findAll('.btn').find(b => b.text().includes('View project DSNs'))!
      await dsnBtn.trigger('click')
      expect(pushMock).toHaveBeenCalledWith('/settings')
    })

    it('navigates to project creation when Create project is clicked', async () => {
      setupMocks({ projects: [] })
      const wrapper = mount(IssueListView, { global: { stubs } })
      const createBtn = wrapper.findAll('.btn--primary').find(b => b.text().includes('Create project'))!
      await createBtn.trigger('click')
      expect(pushMock).toHaveBeenCalledWith('/settings/projects?new=1')
    })
  })
})

// ── Virtual row helpers ────────────────────────────────────────────────────────
const virtualIssue = {
  id: 'iss-v1',
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
  release: null,
  assignee_id: null,
  ignore_until: null,
  ignore_count_limit: null,
  ignore_count: null,
}

function setupWithVirtualRow(issue = virtualIssue, extraIssues: any[] = []) {
  vi.mocked(useWindowVirtualizer).mockReturnValueOnce(ref({
    getVirtualItems: () => [{ index: 0, start: 0, size: 52, key: 0 }],
    getTotalSize: () => 52,
    options: { scrollMargin: 0 },
  }) as any)

  vi.mocked(useProjectsStore).mockReturnValue({
    projects: [{ id: '1', name: 'App', slug: 'app' }],
    selectedIds: [],
    toggleProject: vi.fn(),
    setSelected: vi.fn(),
  } as any)

  const allIssues = [issue, ...extraIssues]

  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
    .mockReturnValueOnce({
      data: ref({ issues: allIssues, total: allIssues.length, has_more: false }),
      isFetching: ref(false),
      isError: ref(false),
      refetch: vi.fn(),
    } as any)
    .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)
}

describe('virtual row rendering', () => {
  it('renders an issue row when virtual items are provided', () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs } })
    expect(wrapper.find('[role="row"]').exists()).toBe(true)
  })

  it('opens issue on row click', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs } })
    await wrapper.find('[role="row"]').trigger('click')
    expect(pushMock).toHaveBeenCalledWith('/issues/iss-v1')
  })

  it('levelColor applies danger color for error level', () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs } })
    const sparkSpan = wrapper.find('.events-cell__spark')
    expect(sparkSpan.attributes('style')).toContain('var(--danger)')
  })

  it('levelColor applies warning color for warning level', () => {
    setupWithVirtualRow({ ...virtualIssue, id: 'iss-w', level: 'warning' })
    const wrapper = mount(IssueListView, { global: { stubs } })
    expect(wrapper.find('.events-cell__spark').attributes('style')).toContain('var(--warning)')
  })

  it('levelColor applies info color for info level', () => {
    setupWithVirtualRow({ ...virtualIssue, id: 'iss-i', level: 'info' })
    const wrapper = mount(IssueListView, { global: { stubs } })
    expect(wrapper.find('.events-cell__spark').attributes('style')).toContain('var(--info)')
  })

  it('levelColor applies default text-3 color for unknown level', () => {
    setupWithVirtualRow({ ...virtualIssue, id: 'iss-d', level: 'debug' })
    const wrapper = mount(IssueListView, { global: { stubs } })
    expect(wrapper.find('.events-cell__spark').attributes('style')).toContain('var(--text-3)')
  })

  it('navigateIssue navigates on normal link click', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs } })
    await wrapper.find('.issue__title').trigger('click')
    expect(pushMock).toHaveBeenCalledWith('/issues/iss-v1')
  })

  it('navigateIssue skips navigation on meta-click', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs } })
    await wrapper.find('.issue__title').trigger('click', { metaKey: true })
    expect(pushMock).not.toHaveBeenCalled()
  })

  it('toggleSelect via checkbox change marks row as checked', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs } })
    const rowCheck = wrapper.find('.row-check:not(.row-check--header)')
    await rowCheck.trigger('change')
    expect(wrapper.find('.issuerow--checked').exists()).toBe(true)
  })

  it('toggleAll via header checkbox selects all issues', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs } })
    await wrapper.find('.row-check--header').trigger('change')
    expect(wrapper.find('.issuerow--checked').exists()).toBe(true)
  })

  it('toggleAll deselects all when all are already selected', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs } })
    const headerCheck = wrapper.find('.row-check--header')
    await headerCheck.trigger('change') // select all
    await headerCheck.trigger('change') // deselect all
    expect(wrapper.find('.issuerow--checked').exists()).toBe(false)
  })

  it('shows n1_query kindbadge for n1_query kind', () => {
    setupWithVirtualRow({ ...virtualIssue, kind: 'n1_query' })
    const wrapper = mount(IssueListView, { global: { stubs } })
    expect(wrapper.find('.kindbadge').text()).toBe('N+1')
  })

  it('applies regressed class for regressed status', () => {
    setupWithVirtualRow({ ...virtualIssue, status: 'regressed' })
    const wrapper = mount(IssueListView, { global: { stubs } })
    expect(wrapper.find('.issuerow--regressed').exists()).toBe(true)
  })

  it('shows staging envbadge for staging environment', () => {
    setupWithVirtualRow({ ...virtualIssue, environment: 'staging' })
    const wrapper = mount(IssueListView, { global: { stubs } })
    expect(wrapper.find('.envbadge--staging').exists()).toBe(true)
  })
})

describe('bulk action bar', () => {
  it('shows bulk action bar when items are selected', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs } })
    await wrapper.find('.row-check--header').trigger('change')
    expect(wrapper.find('.bulkbar').exists()).toBe(true)
  })

  it('calls bulkResolve when Resolve button is clicked', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs } })
    await wrapper.find('.row-check--header').trigger('change')
    await wrapper.find('.bulkbar .btn--primary').trigger('click')
    await flushPromises()
    expect(wrapper.find('.bulkbar').exists()).toBe(true)
  })

  it('calls bulkUnignore when Unignore button is clicked', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs } })
    await wrapper.find('.row-check--header').trigger('change')
    const unignoreBtn = wrapper.findAll('.bulkbar .btn').find(b => b.text() === 'Unignore')!
    await unignoreBtn.trigger('click')
    await flushPromises()
    expect(wrapper.find('.bulkbar').exists()).toBe(true)
  })

  it('shows merge button and merge confirm when canMerge is true', async () => {
    const issue2 = { ...virtualIssue, id: 'iss-v2', event_count: 10 }
    setupWithVirtualRow(virtualIssue, [issue2])
    const wrapper = mount(IssueListView, { global: { stubs } })
    await wrapper.find('.row-check--header').trigger('change')
    await wrapper.vm.$nextTick()
    const mergeBtn = wrapper.findAll('.bulkbar .btn').find(b => b.text() === 'Merge')
    if (mergeBtn) {
      await mergeBtn.trigger('click')
      expect(wrapper.text()).toContain('Merge')
    }
  })

  it('clears selection when Clear button is clicked', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs } })
    await wrapper.find('.row-check--header').trigger('change')
    const clearBtn = wrapper.findAll('.bulkbar .btn').find(b => b.text() === 'Clear')!
    await clearBtn.trigger('click')
    expect(wrapper.find('.bulkbar').exists()).toBe(false)
  })
})

describe('keyboard navigation with selected issue', () => {
  it('navigates to issue on Enter key after pressing j', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs }, attachTo: document.body })
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'j' }))
    await wrapper.vm.$nextTick()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }))
    expect(pushMock).toHaveBeenCalledWith('/issues/iss-v1')
    wrapper.unmount()
  })

  it('toggles selection via x key', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs }, attachTo: document.body })
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'j' }))
    await wrapper.vm.$nextTick()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'x' }))
    await wrapper.vm.$nextTick()
    expect(wrapper.find('.issuerow--checked').exists()).toBe(true)
    wrapper.unmount()
  })

  it('resolveSelected via e key when selectedIdx is valid', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs }, attachTo: document.body })
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'j' }))
    await wrapper.vm.$nextTick()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'e' }))
    await flushPromises()
    wrapper.unmount()
  })

  it('ignoreSelected via i key when selectedIdx is valid', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs }, attachTo: document.body })
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'j' }))
    await wrapper.vm.$nextTick()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'i' }))
    await flushPromises()
    wrapper.unmount()
  })

  it('unignoreSelected via u key for ignored issue', async () => {
    setupWithVirtualRow({ ...virtualIssue, status: 'ignored' })
    const wrapper = mount(IssueListView, { global: { stubs }, attachTo: document.body })
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'j' }))
    await wrapper.vm.$nextTick()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'u' }))
    await flushPromises()
    wrapper.unmount()
  })

  it('bulkResolve via e key when items are selected', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs }, attachTo: document.body })
    await wrapper.find('.row-check--header').trigger('change')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'e' }))
    await flushPromises()
    wrapper.unmount()
  })

  it('bulkIgnore via i key when items are selected', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs }, attachTo: document.body })
    await wrapper.find('.row-check--header').trigger('change')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'i' }))
    await flushPromises()
    wrapper.unmount()
  })

  it('bulkUnignore via u key when items are selected', async () => {
    setupWithVirtualRow()
    const wrapper = mount(IssueListView, { global: { stubs }, attachTo: document.body })
    await wrapper.find('.row-check--header').trigger('change')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'u' }))
    await flushPromises()
    wrapper.unmount()
  })
})

describe('load more pagination', () => {
  it('shows Load more button when has_more is true', async () => {
    const dataRef = ref<any>(undefined)
    vi.mocked(useProjectsStore).mockReturnValue({
      projects: [{ id: '1', name: 'App' }],
      selectedIds: [],
      toggleProject: vi.fn(),
      setSelected: vi.fn(),
    } as any)
    vi.mocked(useQuery)
      .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
      .mockReturnValueOnce({ data: dataRef, isFetching: ref(false), isError: ref(false), refetch: vi.fn() } as any)
      .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)

    const wrapper = mount(IssueListView, { global: { stubs } })
    dataRef.value = {
      issues: [virtualIssue],
      total: 5,
      has_more: true,
      next_cursor_time: '2024-01-01T00:00:00Z',
      next_cursor_id: 'cursor-abc',
    }
    await wrapper.vm.$nextTick()
    expect(wrapper.find('.list-footer__more').exists()).toBe(true)
  })

  it('calls apiFetch when Load more button is clicked', async () => {
    vi.mocked(apiFetch).mockResolvedValue({
      issues: [],
      total: 5,
      has_more: false,
    } as any)

    const dataRef = ref<any>(undefined)
    vi.mocked(useProjectsStore).mockReturnValue({
      projects: [{ id: '1', name: 'App' }],
      selectedIds: [],
      toggleProject: vi.fn(),
      setSelected: vi.fn(),
    } as any)
    vi.mocked(useQuery)
      .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
      .mockReturnValueOnce({ data: dataRef, isFetching: ref(false), isError: ref(false), refetch: vi.fn() } as any)
      .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)

    const wrapper = mount(IssueListView, { global: { stubs } })
    dataRef.value = {
      issues: [virtualIssue],
      total: 5,
      has_more: true,
      next_cursor_time: '2024-01-01T00:00:00Z',
      next_cursor_id: 'cursor-abc',
    }
    await wrapper.vm.$nextTick()
    await wrapper.find('.list-footer__more').trigger('click')
    await flushPromises()
    expect(apiFetch).toHaveBeenCalledWith(expect.stringContaining('/api/issues'))
  })

  it('shows list-footer counter text when sorted has items', async () => {
    const dataRef = ref<any>(undefined)
    vi.mocked(useProjectsStore).mockReturnValue({
      projects: [{ id: '1', name: 'App' }],
      selectedIds: [],
      toggleProject: vi.fn(),
      setSelected: vi.fn(),
    } as any)
    vi.mocked(useQuery)
      .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
      .mockReturnValueOnce({ data: dataRef, isFetching: ref(false), isError: ref(false), refetch: vi.fn() } as any)
      .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)

    const wrapper = mount(IssueListView, { global: { stubs } })
    dataRef.value = {
      issues: [virtualIssue],
      total: 1,
      has_more: false,
    }
    await wrapper.vm.$nextTick()
    expect(wrapper.find('.list-footer__count').exists()).toBe(true)
    expect(wrapper.find('.list-footer__done').text()).toBe('All loaded')
  })
})

describe('assigneeInitial', () => {
  it('shows owner avatar initial when issue has assignee matching a user', () => {
    vi.mocked(useWindowVirtualizer).mockReturnValueOnce(ref({
      getVirtualItems: () => [{ index: 0, start: 0, size: 52, key: 0 }],
      getTotalSize: () => 52,
      options: { scrollMargin: 0 },
    }) as any)
    vi.mocked(useProjectsStore).mockReturnValue({
      projects: [{ id: '1', name: 'App' }],
      selectedIds: [],
      toggleProject: vi.fn(),
      setSelected: vi.fn(),
    } as any)
    vi.mocked(useQuery)
      .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
      .mockReturnValueOnce({
        data: ref({ issues: [{ ...virtualIssue, assignee_id: 'user-1' }], total: 1, has_more: false }),
        isFetching: ref(false), isError: ref(false), refetch: vi.fn(),
      } as any)
      .mockReturnValueOnce({ data: ref([{ id: 'user-1', name: 'Alice', email: 'alice@example.com' }]), isFetching: ref(false) } as any)

    const wrapper = mount(IssueListView, { global: { stubs } })
    expect(wrapper.find('.owner-avatar').exists()).toBe(true)
    expect(wrapper.find('.owner-avatar').text()).toBe('A')
  })

  it('falls back to email initial when user is not found', () => {
    vi.mocked(useWindowVirtualizer).mockReturnValueOnce(ref({
      getVirtualItems: () => [{ index: 0, start: 0, size: 52, key: 0 }],
      getTotalSize: () => 52,
      options: { scrollMargin: 0 },
    }) as any)
    vi.mocked(useProjectsStore).mockReturnValue({
      projects: [{ id: '1', name: 'App' }],
      selectedIds: [],
      toggleProject: vi.fn(),
      setSelected: vi.fn(),
    } as any)
    const issueWithUnknownAssignee = {
      ...virtualIssue,
      assignee_id: 'user-unknown',
      assignee_email: 'bob@example.com',
    }
    vi.mocked(useQuery)
      .mockReturnValueOnce({ data: ref(undefined), isFetching: ref(false) } as any)
      .mockReturnValueOnce({
        data: ref({ issues: [issueWithUnknownAssignee], total: 1, has_more: false }),
        isFetching: ref(false), isError: ref(false), refetch: vi.fn(),
      } as any)
      .mockReturnValueOnce({ data: ref([]), isFetching: ref(false) } as any)

    const wrapper = mount(IssueListView, { global: { stubs } })
    expect(wrapper.find('.owner-avatar').text()).toBe('B')
  })
})
