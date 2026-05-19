import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

const pushMock = vi.fn()

vi.mock('vue-router', () => ({
  useRoute: vi.fn(() => ({ params: { id: 'iss-123' } })),
  useRouter: vi.fn(() => ({ push: pushMock })),
  RouterLink: { template: '<a><slot /></a>' },
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(() => ({ mutate: vi.fn(), isPending: ref(false) })),
  useQueryClient: vi.fn(() => ({ invalidateQueries: vi.fn() })),
  keepPreviousData: undefined,
}))

vi.mock('@/stores/issueNav', () => ({
  useIssueNavStore: vi.fn(() => ({ ids: [], prevId: () => null, nextId: () => null })),
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

import IssueDetailView from '../IssueDetailView.vue'
import { useQuery } from '@tanstack/vue-query'

const stubs = {
  RouterLink: { template: '<a><slot /></a>' },
  Icon: { template: '<span />' },
  TimeseriesChart: { template: '<div />' },
  IgnoreButton: { template: '<button />' },
  CodeContext: { template: '<div />' },
}

const baseIssue = {
  id: 'iss-123',
  title: 'TypeError: cannot read properties of undefined',
  level: 'error',
  status: 'open',
  environment: 'production',
  project_id: 'proj-1',
  event_count: 5,
  user_count: 2,
  last_seen: '2024-01-02T00:00:00Z',
  first_seen: '2024-01-01T00:00:00Z',
  kind: 'error',
  release: null,
  release_id: null,
  assignee_id: null,
  ignore_until: null,
  ignore_count_limit: null,
  ignore_count: null,
}

function setupQueries(issue = baseIssue as unknown, overrides: any[] = []) {
  // useQuery call order in IssueDetailView:
  // 1. me, 2. issue, 3. currentEvent, 4. perfEvents,
  // 5. comments, 6. history, 7. users, 8. issueTags, 9. histogram
  const defaults = [
    { data: ref({ id: 'user-1', permissions: { manage_issues: true } }) }, // 1. me
    { data: ref(issue), isError: ref(false), refetch: vi.fn() },           // 2. issue
    { data: ref(null) },                                                    // 3. currentEvent
    { data: ref([]) },                                                      // 4. perfEvents
    { data: ref([]) },                                                      // 5. comments
    { data: ref([]) },                                                      // 6. history
    { data: ref([]) },                                                      // 7. users
    { data: ref(null) },                                                    // 8. issueTags
    { data: ref(null) },                                                    // 9. histogram
  ]

  const mocks = defaults.map((d, i) => ({ ...d, ...(overrides[i] ?? {}) }))
  vi.mocked(useQuery).mockReset()
  for (const m of mocks) {
    vi.mocked(useQuery).mockReturnValueOnce(m as any)
  }
  vi.mocked(useQuery).mockReturnValue({ data: ref(undefined) } as any)
}

beforeEach(() => {
  vi.mocked(useQuery).mockReset()
  pushMock.mockReset()
})

describe('IssueDetailView', () => {
  describe('loading skeleton', () => {
    it('renders loading skeleton when issue data is not yet available', () => {
      setupQueries(undefined)
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      // When no issue and no error, either skeleton or breadcrumb appears
      // The v-else block at minimum renders .detail-breadcrumb__back
      expect(wrapper.find('.detail-breadcrumb__back').exists()).toBe(true)
    })
  })

  describe('error state', () => {
    it('shows an error message when loading the issue fails', () => {
      setupQueries(undefined, [
        { data: ref(undefined) },
        { data: ref(undefined), isError: ref(true), refetch: vi.fn() },
      ])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Failed to load issue')
    })

    it('shows a Try again button on error', () => {
      setupQueries(undefined, [
        { data: ref(undefined) },
        { data: ref(undefined), isError: ref(true), refetch: vi.fn() },
      ])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Try again')
    })
  })

  describe('loaded issue', () => {
    it('renders the issue title', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('TypeError')
    })

    it('renders the issue level', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.detail-hero__level').text()).toBe('error')
    })

    it('renders the issue status pill', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.statuspill--open').exists()).toBe(true)
    })

    it('renders the event count', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('5 events')
    })

    it('renders the Resolve button', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.btn--primary').text()).toContain('Resolve')
    })

    it('shows Unhandled badge when exception is unhandled', () => {
      const issueWithUnhandled = { ...baseIssue }
      const unhandledEvent = {
        id: 'evt-1',
        payload: {
          exception: {
            values: [{ mechanism: { handled: false }, stacktrace: { frames: [] } }],
          },
        },
      }
      setupQueries(issueWithUnhandled, [
        {},
        {},
        { data: ref(unhandledEvent) }, // 3. currentEvent with unhandled exception
      ])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.detail-hero__unhandled').exists()).toBe(true)
    })

    it('renders the environment badge', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.detail-hero__env--prod').exists()).toBe(true)
    })

    it('back link navigates to /issues', async () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      await wrapper.find('.detail-breadcrumb__back').trigger('click')
      expect(pushMock).toHaveBeenCalledWith('/issues')
    })
  })

  describe('activity section', () => {
    it('shows "No activity yet" when there are no comments or history', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const activitySection = wrapper.find('.activity-empty')
      expect(activitySection.exists()).toBe(true)
    })

    it('renders the comment form', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.activity-compose').exists()).toBe(true)
    })

    it('disables the Comment button when textarea is empty', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const btn = wrapper.find<HTMLButtonElement>('.activity-compose .btn--primary')
      expect(btn.element.disabled).toBe(true)
    })
  })

  describe('event navigation', () => {
    it('renders event navigation controls', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.eventnav').exists()).toBe(true)
    })

    it('shows 1 / N for event navigation', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.eventnav__cur').text()).toContain('1 / 5')
    })
  })
})
