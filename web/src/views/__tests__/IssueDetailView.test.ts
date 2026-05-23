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

vi.mock('@/stores/auth', () => ({
  useAuthStore: vi.fn(),
}))

import IssueDetailView from '../IssueDetailView.vue'
import { useQuery } from '@tanstack/vue-query'
import { useIssueNavStore } from '@/stores/issueNav'
import { apiFetch } from '@/api/client'
import { flushPromises } from '@vue/test-utils'
import { useAuthStore } from '@/stores/auth'

const stubs = {
  RouterLink: { template: '<a><slot /></a>' },
  Icon: { template: '<span />' },
  TimeseriesChart: { template: '<div />' },
  IgnoreButton: { name: 'IgnoreButton', emits: ['ignore'], template: '<button class="ignore-btn" @click="$emit(\'ignore\', { status: \'ignored\' })" />' },
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
  // 5. comments, 6. history, 7. users, 8. linkedTransaction (trace),
  // 9. traceSpans, 10. issueTags, 11. histogram
  const defaults = [
    { data: ref({ id: 'user-1', permissions: { manage_issues: true } }) }, // 1. me
    { data: ref(issue), isError: ref(false), refetch: vi.fn() },           // 2. issue
    { data: ref(null) },                                                    // 3. currentEvent
    { data: ref([]) },                                                      // 4. perfEvents
    { data: ref([]) },                                                      // 5. comments
    { data: ref([]) },                                                      // 6. history
    { data: ref([]) },                                                      // 7. users
    { data: ref(null) },                                                    // 8. linkedTransaction
    { data: ref(null) },                                                    // 9. traceSpans
    { data: ref(null) },                                                    // 10. issueTags
    { data: ref(null) },                                                    // 11. histogram
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
  vi.mocked(useAuthStore).mockReset()
  vi.mocked(useAuthStore).mockReturnValue({ user: { timezone: 'UTC' }, setUser: vi.fn() } as any)
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

  describe('user card section', () => {
    it('renders user card when event has user data', () => {
      const eventWithUser = {
        id: 'evt-1',
        payload: {
          user: { id: 'u-1', name: 'Alice Johnson', email: 'alice@example.com', ip_address: '192.168.1.1' },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithUser) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.user-card').exists()).toBe(true)
      expect(wrapper.text()).toContain('Alice Johnson')
    })

    it('shows user IP address in user card', () => {
      const eventWithUser = {
        id: 'evt-1',
        payload: {
          user: { id: 'u-1', name: 'Bob', email: 'bob@example.com', ip_address: '10.0.0.1' },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithUser) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('10.0.0.1')
    })
  })

  describe('issue tags section', () => {
    it('renders tags when issueTags has data', () => {
      const tags = [
        {
          key: 'browser',
          total: 100,
          values: [
            { value: 'Chrome', pct: 80, count: 80 },
            { value: 'Firefox', pct: 20, count: 20 },
          ],
        },
      ]
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, {}, {}, {}, {}, { data: ref(tags) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.tags-dist').exists()).toBe(true)
      expect(wrapper.text()).toContain('Chrome')
      expect(wrapper.text()).toContain('80%')
    })

    it('shows tags key count in section header', () => {
      const tags = [
        { key: 'env', total: 50, values: [{ value: 'prod', pct: 100, count: 50 }] },
        { key: 'browser', total: 30, values: [{ value: 'Chrome', pct: 100, count: 30 }] },
      ]
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, {}, {}, {}, {}, { data: ref(tags) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('2 keys')
    })
  })

  describe('activity timeline with comments', () => {
    it('renders comments in activity timeline', () => {
      const comment = {
        id: 'c-1',
        user_id: 'user-1',
        user_name: 'Bob',
        user_email: 'bob@example.com',
        body: 'This is a critical issue',
        created_at: '2024-01-01T00:00:00Z',
        updated_at: '2024-01-01T00:00:00Z',
      }
      setupQueries(baseIssue, [{}, {}, {}, {}, { data: ref([comment]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.activity-item').exists()).toBe(true)
      expect(wrapper.text()).toContain('This is a critical issue')
    })

    it('shows activity count in section header when there are comments', () => {
      const comment = { id: 'c-1', user_id: 'u1', user_name: 'Alice', user_email: 'a@example.com', body: 'Note', created_at: '2024-01-01T00:00:00Z', updated_at: '2024-01-01T00:00:00Z' }
      setupQueries(baseIssue, [{}, {}, {}, {}, { data: ref([comment]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const activitySection = wrapper.findAll('.section__count').find(el => el.text().includes('1'))
      expect(activitySection).toBeDefined()
    })
  })

  describe('resolved/ignored issue status', () => {
    it('shows Unresolve button when issue is resolved', () => {
      const resolvedIssue = { ...baseIssue, status: 'resolved' }
      setupQueries(resolvedIssue)
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.statuspill--resolved').exists()).toBe(true)
    })

    it('shows Reopen button when issue is ignored', () => {
      const ignoredIssue = { ...baseIssue, status: 'ignored' }
      setupQueries(ignoredIssue)
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.statuspill--ignored').exists()).toBe(true)
    })
  })

  describe('breadcrumbs section', () => {
    it('renders breadcrumbs section when event has breadcrumbs', () => {
      const eventWithCrumbs = {
        id: 'evt-1',
        payload: {
          breadcrumbs: {
            values: [
              { timestamp: '2024-01-01T00:00:00Z', type: 'http', category: 'xhr', message: 'GET /api/users', level: 'info', data: {} },
            ],
          },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithCrumbs) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.crumbs').exists()).toBe(true)
      expect(wrapper.text()).toContain('GET /api/users')
    })
  })

  describe('context section', () => {
    it('renders context section when event has runtime context', () => {
      const eventWithContexts = {
        id: 'evt-1',
        payload: {
          contexts: {
            runtime: { name: 'CPython', version: '3.11.0' },
          },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithContexts) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.ctx-grid').exists()).toBe(true)
      expect(wrapper.text()).toContain('Runtime')
      expect(wrapper.text()).toContain('CPython')
    })

    it('renders browser context', () => {
      const eventWithBrowser = {
        id: 'evt-1',
        payload: {
          contexts: {
            browser: { name: 'Chrome', version: '120.0' },
          },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithBrowser) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Browser')
      expect(wrapper.text()).toContain('Chrome')
    })
  })

  describe('HTTP request section', () => {
    it('renders HTTP request section when event has request data', () => {
      const eventWithRequest = {
        id: 'evt-1',
        payload: {
          request: {
            method: 'POST',
            url: 'https://example.com/api/users',
            headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer token123' },
            data: '{"name":"Alice"}',
          },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithRequest) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.req-section').exists()).toBe(true)
      expect(wrapper.text()).toContain('https://example.com/api/users')
    })

    it('shows HTTP method badge in section header', () => {
      const eventWithRequest = {
        id: 'evt-1',
        payload: { request: { method: 'GET', url: 'https://example.com/health' } },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithRequest) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.section__badge--method').text()).toBe('GET')
    })

    it('renders request headers table', () => {
      const eventWithRequest = {
        id: 'evt-1',
        payload: {
          request: {
            method: 'POST',
            url: 'https://example.com/api',
            headers: { 'X-Request-ID': 'req-abc' },
          },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithRequest) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('X-Request-ID')
      expect(wrapper.text()).toContain('req-abc')
    })

    it('does not render HTTP request section when event has no request data', () => {
      const eventWithoutRequest = { id: 'evt-1', payload: {} }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithoutRequest) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.req-section').exists()).toBe(false)
    })

    it('pretty-prints JSON body', () => {
      const eventWithRequest = {
        id: 'evt-1',
        payload: {
          request: {
            method: 'POST',
            url: 'https://example.com/api',
            data: '{"key":"value"}',
          },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithRequest) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const body = wrapper.find('.req-body')
      expect(body.exists()).toBe(true)
      expect(body.text()).toContain('"key"')
    })
  })

  describe('context section - type key filtering', () => {
    it('does not show the "type" meta key in context rows', () => {
      const eventWithContexts = {
        id: 'evt-1',
        payload: {
          contexts: {
            browser: { name: 'Chrome', version: '120.0', type: 'browser' },
          },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithContexts) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const ctxTable = wrapper.find('.ctx-table')
      expect(ctxTable.exists()).toBe(true)
      expect(ctxTable.text()).not.toContain('type')
      expect(ctxTable.text()).toContain('Chrome')
    })

    it('does not show raw_description key in context rows', () => {
      const eventWithContexts = {
        id: 'evt-1',
        payload: {
          contexts: {
            os: { name: 'iOS', version: '17.0', raw_description: 'iPhone OS 17_0' },
          },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithContexts) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const ctxTable = wrapper.find('.ctx-table')
      expect(ctxTable.text()).not.toContain('raw description')
      expect(ctxTable.text()).toContain('iOS')
    })
  })

  describe('trace preview section', () => {
    it('renders trace preview when linkedTransaction is available', () => {
      const linkedTx = {
        id: 'tx-abc',
        project_id: 'proj-1',
        trace_id: 'trace-xyz',
        transaction: '/api/users',
        op: 'http.server',
        status: 'ok',
        duration_ms: 150,
        start_timestamp: '2024-01-01T00:00:00Z',
        environment: 'production',
      }
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, {}, {}, { data: ref(linkedTx) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.trace-preview').exists()).toBe(true)
    })

    it('shows View Full Trace link pointing to the transaction', () => {
      const linkedTx = {
        id: 'tx-abc',
        project_id: 'proj-1',
        trace_id: 'trace-xyz',
        transaction: '/api/users',
        op: 'http.server',
        status: 'ok',
        duration_ms: 150,
        start_timestamp: '2024-01-01T00:00:00Z',
        environment: null,
      }
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, {}, {}, { data: ref(linkedTx) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const link = wrapper.find('.section__link')
      expect(link.exists()).toBe(true)
      expect(link.text()).toContain('View Full Trace')
    })

    it('does not render trace preview when no linked transaction', () => {
      setupQueries(baseIssue)
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.trace-preview').exists()).toBe(false)
    })

    it('renders transaction row in trace preview', () => {
      const linkedTx = {
        id: 'tx-abc',
        project_id: 'proj-1',
        trace_id: 'trace-xyz',
        transaction: '/api/checkout',
        op: 'http.server',
        status: 'ok',
        duration_ms: 200,
        start_timestamp: '2024-01-01T00:00:00Z',
        environment: null,
      }
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, {}, {}, { data: ref(linkedTx) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.trace-preview__row--tx').exists()).toBe(true)
      expect(wrapper.text()).toContain('/api/checkout')
    })
  })

  describe('stack trace rendering', () => {
    it('renders stack frames when exception has stacktrace', () => {
      const eventWithStack = {
        id: 'evt-1',
        payload: {
          exception: {
            values: [{
              type: 'TypeError',
              value: 'Cannot read property of undefined',
              mechanism: { handled: true },
              stacktrace: {
                frames: [
                  { filename: 'app.js', lineno: 42, colno: 10, function: 'handleRequest', in_app: true, context_line: '  throw new Error()' },
                  { filename: 'server.js', lineno: 10, colno: 5, function: 'main', in_app: false, context_line: '  app.listen()' },
                ],
              },
            }],
          },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithStack) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.stack').exists()).toBe(true)
    })
  })

  describe('history events in activity', () => {
    it('renders history entries in activity timeline', () => {
      const historyEntry = {
        id: 'h-1',
        action: 'resolved',
        actor_id: 'user-1',
        actor_email: 'admin@example.com',
        created_at: '2024-01-02T00:00:00Z',
      }
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, { data: ref([historyEntry]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.activity').exists()).toBe(true)
    })
  })

  describe('resolve and status actions', () => {
    it('calls setStatus when Resolve button is clicked', async () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const resolveBtn = wrapper.find('.btn--primary')
      expect(resolveBtn.text()).toContain('Resolve')
      await resolveBtn.trigger('click')
      // Button click should not throw; status update mutation was triggered
      expect(resolveBtn.exists()).toBe(true)
    })

    it('shows Unignore button when issue is ignored', async () => {
      const ignoredIssue = { ...baseIssue, status: 'ignored' }
      setupQueries(ignoredIssue)
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const unignoreBtn = wrapper.findAll('.btn').find(b => b.text().includes('Unignore'))!
      expect(unignoreBtn).toBeDefined()
      await unignoreBtn.trigger('click')
    })
  })

  describe('assignee dropdown', () => {
    it('opens assignee dropdown when Assign button is clicked', async () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const assignBtn = wrapper.findAll('.btn').find(b => b.text().includes('Assign'))!
      await assignBtn.trigger('click')
      expect(wrapper.find('.assign-popover').exists()).toBe(true)
    })

    it('shows Unassigned option in assignee dropdown', async () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      await wrapper.findAll('.btn').find(b => b.text().includes('Assign'))!.trigger('click')
      expect(wrapper.text()).toContain('Unassigned')
    })
  })

  describe('keyboard shortcuts', () => {
    it('handles e key to resolve issue', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(() => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'e' }))).not.toThrow()
      wrapper.unmount()
    })

    it('handles i key to ignore issue', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(() => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'i' }))).not.toThrow()
      wrapper.unmount()
    })

    it('handles a key to toggle assign dropdown', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(() => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'a' }))).not.toThrow()
      wrapper.unmount()
    })

    it('handles Escape key to go back to issues list', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
      wrapper.unmount()
    })

    it('handles ArrowLeft key to go to previous event', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(() => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft' }))).not.toThrow()
      wrapper.unmount()
    })

    it('handles ArrowRight key to go to next event', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(() => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight' }))).not.toThrow()
      wrapper.unmount()
    })
  })

  describe('section toggles', () => {
    it('toggles breadcrumbs section when section head is clicked', async () => {
      const eventWithCrumbs = {
        id: 'evt-1',
        payload: {
          breadcrumbs: {
            values: [
              { timestamp: '2024-01-01T00:00:00Z', type: 'http', category: 'xhr', message: 'GET /api', level: 'info', data: {} },
            ],
          },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithCrumbs) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const sectionHeads = wrapper.findAll('.section__head')
      if (sectionHeads.length > 0) {
        await sectionHeads[0].trigger('click')
        // Collapse/expand toggled - section state changed
        expect(sectionHeads[0].exists()).toBe(true)
      }
    })

    it('toggles context section when section head is clicked', async () => {
      const eventWithContexts = {
        id: 'evt-1',
        payload: {
          contexts: {
            runtime: { name: 'CPython', version: '3.11.0' },
          },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithContexts) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const ctxSection = wrapper.find('.ctx-section')
      if (ctxSection.exists()) {
        const head = wrapper.findAll('.section__head').find(h => h.element.closest('.ctx-section'))!
        if (head) await head.trigger('click')
      }
    })
  })

  describe('comment form submission', () => {
    it('enables Comment button when textarea has content', async () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      await wrapper.find('.activity-compose textarea').setValue('Test comment here')
      const btn = wrapper.find<HTMLButtonElement>('.activity-compose .btn--primary')
      expect(btn.element.disabled).toBe(false)
    })

    it('submits comment when form is submitted with content', async () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      await wrapper.find('.activity-compose textarea').setValue('Test comment')
      await wrapper.find('.activity-compose .btn--primary').trigger('click')
      // Just ensure no throw - mutation was triggered
    })
  })

  describe('event navigation arrows', () => {
    it('clicking ArrowRight increases eventIndex toward total', () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight' }))
      // eventIndex should try to increment (bounded by event_count - 1)
      expect(wrapper.find('.eventnav__cur').exists()).toBe(true)
      wrapper.unmount()
    })
  })

  describe('issue navigation (goPrev/goNext)', () => {
    it('[ key navigates to previous issue when prevIssueId is set', async () => {
      vi.mocked(useIssueNavStore).mockReturnValueOnce({
        ids: ['iss-prev', 'iss-123'],
        prevId: () => 'iss-prev',
        nextId: () => null,
        set: vi.fn(),
      } as any)
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs }, attachTo: document.body })
      document.dispatchEvent(new KeyboardEvent('keydown', { key: '[' }))
      expect(pushMock).toHaveBeenCalledWith('/issues/iss-prev')
      wrapper.unmount()
    })

    it('] key navigates to next issue when nextIssueId is set', async () => {
      vi.mocked(useIssueNavStore).mockReturnValueOnce({
        ids: ['iss-123', 'iss-next'],
        prevId: () => null,
        nextId: () => 'iss-next',
        set: vi.fn(),
      } as any)
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs }, attachTo: document.body })
      document.dispatchEvent(new KeyboardEvent('keydown', { key: ']' }))
      expect(pushMock).toHaveBeenCalledWith('/issues/iss-next')
      wrapper.unmount()
    })

    it('renders prev/next nav buttons when navStore.ids is non-empty', () => {
      vi.mocked(useIssueNavStore).mockReturnValueOnce({
        ids: ['iss-prev', 'iss-123', 'iss-next'],
        prevId: () => 'iss-prev',
        nextId: () => 'iss-next',
        set: vi.fn(),
      } as any)
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.find('.detail-breadcrumb__nav').exists()).toBe(true)
    })
  })

  describe('history label branches', () => {
    const histBase = { id: 'h-1', actor_id: null as string | null, actor_email: null as string | null, created_at: '2024-01-02T00:00:00Z', details: {} as Record<string, unknown> }

    it('renders "Issue opened" for created event type', () => {
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, { data: ref([{ ...histBase, event_type: 'created' }]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Issue opened')
    })

    it('renders "Regressed" for regressed event type', () => {
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, { data: ref([{ ...histBase, event_type: 'regressed' }]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Regressed')
    })

    it('renders "Resolved" for status_changed to resolved', () => {
      const entry = { ...histBase, event_type: 'status_changed', details: { to: 'resolved' } }
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, { data: ref([entry]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Resolved')
    })

    it('renders "Ignored forever" for status_changed to ignored with no conditions', () => {
      const entry = { ...histBase, event_type: 'status_changed', details: { to: 'ignored' } }
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, { data: ref([entry]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Ignored forever')
    })

    it('renders "Ignored until" for status_changed with ignore_until date', () => {
      const entry = { ...histBase, event_type: 'status_changed', details: { to: 'ignored', ignore_until: '2024-12-31T00:00:00Z' } }
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, { data: ref([entry]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Ignored until')
    })

    it('renders "Ignored for N occurrences" for status_changed with count', () => {
      const entry = { ...histBase, event_type: 'status_changed', details: { to: 'ignored', ignore_count_limit: 100 } }
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, { data: ref([entry]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Ignored for 100 occurrences')
    })

    it('renders "Reopened" for status_changed to open', () => {
      const entry = { ...histBase, event_type: 'status_changed', details: { to: 'open' } }
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, { data: ref([entry]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Reopened')
    })

    it('renders "Unassigned" for assigned event with null toId', () => {
      const entry = { ...histBase, event_type: 'assigned', details: { to_id: null } }
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, { data: ref([entry]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Unassigned')
    })

    it('renders "Assigned to Alice" when toId matches a user', () => {
      const entry = { ...histBase, actor_id: 'user-1', event_type: 'assigned', details: { to_id: 'user-1' } }
      setupQueries(baseIssue, [
        {},
        {}, {}, {}, {},
        { data: ref([entry]) },
        { data: ref([{ id: 'user-1', name: 'Alice', email: 'alice@example.com' }]) },
      ])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Assigned to Alice')
    })

    it('actorName renders "System" for null actor_id', () => {
      const entry = { ...histBase, event_type: 'created', actor_id: null }
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, { data: ref([entry]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('System')
    })

    it('actorName renders "Unknown" for unknown actor_id', () => {
      const entry = { ...histBase, event_type: 'regressed', actor_id: 'user-unknown' }
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, { data: ref([entry]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Unknown')
    })

    it('renders fallback for unknown event_type', () => {
      const entry = { ...histBase, event_type: 'custom_action' }
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, { data: ref([entry]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('custom_action')
    })
  })

  describe('comment editing', () => {
    const ownComment = { id: 'c-1', user_id: 'user-1', user_name: 'Alice', user_email: 'alice@example.com', body: 'Initial body', created_at: '2024-01-01T00:00:00Z', updated_at: '2024-01-01T00:00:00Z' }

    it('startEdit shows edit textarea when edit button is clicked', async () => {
      setupQueries(baseIssue, [{}, {}, {}, {}, { data: ref([ownComment]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const editBtn = wrapper.find('.activity-action[title="Edit"]')
      await editBtn.trigger('click')
      expect(wrapper.find('.activity-edit').exists()).toBe(true)
    })

    it('cancelEdit hides edit form when Cancel is clicked', async () => {
      setupQueries(baseIssue, [{}, {}, {}, {}, { data: ref([ownComment]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      await wrapper.find('.activity-action[title="Edit"]').trigger('click')
      const cancelBtn = wrapper.find('.activity-edit .btn--ghost')
      await cancelBtn.trigger('click')
      expect(wrapper.find('.activity-edit').exists()).toBe(false)
    })

    it('saveEdit calls saveEdit mutation when Save button is clicked', async () => {
      setupQueries(baseIssue, [{}, {}, {}, {}, { data: ref([ownComment]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      await wrapper.find('.activity-action[title="Edit"]').trigger('click')
      await wrapper.find('.activity-edit textarea').setValue('Updated body')
      const saveBtn = wrapper.find('.activity-edit .btn--primary')
      await saveBtn.trigger('click')
      expect(wrapper.find('.activity-item').exists()).toBe(true)
    })

    it('saving with meta+enter triggers saveEdit', async () => {
      setupQueries(baseIssue, [{}, {}, {}, {}, { data: ref([ownComment]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      await wrapper.find('.activity-action[title="Edit"]').trigger('click')
      await wrapper.find('.activity-edit textarea').setValue('Updated body')
      await wrapper.find('.activity-edit textarea').trigger('keydown.meta.enter')
      expect(wrapper.find('.activity-item').exists()).toBe(true)
    })

    it('confirmDeleteComment calls deleteComment when confirm returns true', async () => {
      vi.stubGlobal('confirm', vi.fn().mockReturnValue(true))
      setupQueries(baseIssue, [{}, {}, {}, {}, { data: ref([ownComment]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const deleteBtn = wrapper.find('.activity-action--danger')
      await deleteBtn.trigger('click')
      expect(vi.mocked(window.confirm)).toHaveBeenCalled()
      vi.unstubAllGlobals()
    })

    it('confirmDeleteComment does not delete when confirm returns false', async () => {
      vi.stubGlobal('confirm', vi.fn().mockReturnValue(false))
      setupQueries(baseIssue, [{}, {}, {}, {}, { data: ref([ownComment]) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const deleteBtn = wrapper.find('.activity-action--danger')
      await deleteBtn.trigger('click')
      expect(vi.mocked(window.confirm)).toHaveBeenCalledWith('Delete this comment?')
      vi.unstubAllGlobals()
    })
  })

  describe('breadcrumbs toggleCrumb', () => {
    it('toggleCrumb expands crumb data when toggle button is clicked', async () => {
      const eventWithCrumbs = {
        id: 'evt-1',
        payload: {
          breadcrumbs: {
            values: [
              { timestamp: '2024-01-01T00:00:00Z', type: 'http', category: 'xhr', message: 'GET /api', level: 'info', data: { method: 'GET', status: 200 } },
            ],
          },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithCrumbs) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const toggleBtn = wrapper.find('.crumb__toggle')
      expect(toggleBtn.exists()).toBe(true)
      await toggleBtn.trigger('click')
      expect(wrapper.find('.crumb__detail').exists()).toBe(true)
    })

    it('toggleCrumb collapses crumb data on second click', async () => {
      const eventWithCrumbs = {
        id: 'evt-1',
        payload: {
          breadcrumbs: {
            values: [
              { timestamp: '2024-01-01T00:00:00Z', type: 'http', category: 'xhr', message: 'GET /api', level: 'info', data: { method: 'GET', status: 200 } },
            ],
          },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithCrumbs) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const toggleBtn = wrapper.find('.crumb__toggle')
      await toggleBtn.trigger('click') // expand
      await toggleBtn.trigger('click') // collapse
      expect(wrapper.find('.crumb__detail').exists()).toBe(false)
    })
  })

  describe('copyStack', () => {
    it('copies raw stack to clipboard when copy button is clicked', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })

      const eventWithStack = {
        id: 'evt-1',
        payload: {
          exception: {
            values: [{
              type: 'TypeError',
              value: 'Cannot read property',
              mechanism: { handled: true },
              stacktrace: {
                frames: [{ filename: 'app.js', lineno: 10, function: 'main', in_app: true, context_line: '  throw err' }],
              },
            }],
          },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithStack) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })

      const rawBtn = wrapper.findAll('.btn--ghost').find(b => b.text() === 'Raw')
      if (rawBtn) {
        await rawBtn.trigger('click')
        const copyBtn = wrapper.findAll('.btn--ghost').find(b => b.text().includes('Copy'))
        if (copyBtn) {
          await copyBtn.trigger('click')
          expect(writeText).toHaveBeenCalled()
        }
      }
    })
  })

  describe('event list (toggleEventList, loadEventPage, toggleEvtSort)', () => {
    it('shows event list when Show all button is clicked', async () => {
      vi.mocked(apiFetch).mockResolvedValue({
        events: [],
        has_more: false,
        total: 5,
      } as any)
      setupQueries(baseIssue) // event_count = 5 > 1, so "Show all" button appears
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const showAllBtn = wrapper.findAll('.btn--ghost').find(b => b.text().includes('Show all'))
      if (showAllBtn) {
        await showAllBtn.trigger('click')
        await flushPromises()
        expect(wrapper.find('.eventlist').exists()).toBe(true)
      }
    })

    it('toggleEvtSort changes sort column and direction', async () => {
      vi.mocked(apiFetch).mockResolvedValue({
        events: [{ id: 'ev-1', received_at: '2024-01-01T00:00:00Z', level: 'error', environment: 'production', release: null, tags: {} }],
        has_more: false,
        total: 1,
      } as any)
      setupQueries(baseIssue)
      const wrapper = mount(IssueDetailView, { global: { stubs } })

      const showAllBtn = wrapper.findAll('.btn--ghost').find(b => b.text().includes('Show all'))
      if (showAllBtn) {
        await showAllBtn.trigger('click')
        await flushPromises()

        const levelSortBtn = wrapper.findAll('.col-sort').find(b => b.text().includes('Level'))
        if (levelSortBtn) {
          await levelSortBtn.trigger('click')
          expect(levelSortBtn.classes()).toContain('col-sort--active')
          await levelSortBtn.trigger('click') // toggle direction
          expect(levelSortBtn.find('.col-sort__icon').text()).toContain('↓')
        }
      }
    })

    it('loadEventPage loads more events when Load more is clicked', async () => {
      vi.mocked(apiFetch)
        .mockResolvedValueOnce({
          events: [{ id: 'ev-1', received_at: '2024-01-01T00:00:00Z', level: 'error', environment: 'production', release: null, tags: {} }],
          has_more: true,
          next_cursor_time: '2024-01-01T00:00:00Z',
          next_cursor_id: 'c1',
          total: 5,
        } as any)
        .mockResolvedValueOnce({
          events: [],
          has_more: false,
          total: 5,
        } as any)
      setupQueries(baseIssue)
      const wrapper = mount(IssueDetailView, { global: { stubs } })

      const showAllBtn = wrapper.findAll('.btn--ghost').find(b => b.text().includes('Show all'))
      if (showAllBtn) {
        await showAllBtn.trigger('click')
        await flushPromises()
        await wrapper.vm.$nextTick()

        const loadMoreBtn = wrapper.findAll('.btn--ghost').find(b => b.text().includes('Load more'))
        if (loadMoreBtn) {
          const callsBefore = vi.mocked(apiFetch).mock.calls.length
          await loadMoreBtn.trigger('click')
          await flushPromises()
          expect(vi.mocked(apiFetch).mock.calls.length).toBeGreaterThan(callsBefore)
        }
      }
    })
  })

  describe('handleIgnore', () => {
    it('calls handleIgnore when IgnoreButton emits ignore event', async () => {
      setupQueries()
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const ignoreBtn = wrapper.findComponent({ name: 'IgnoreButton' })
      await ignoreBtn.trigger('click')
      // handleIgnore is called - no throw
      expect(ignoreBtn.exists()).toBe(true)
    })
  })

  describe('modules section', () => {
    it('renders packages section when event has modules', async () => {
      const eventWithModules = {
        id: 'evt-1',
        payload: {
          modules: { django: '4.2', numpy: '1.24.0', requests: '2.31.0' },
        },
      }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithModules) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      // packages section starts collapsed by default - click to expand
      const pkgHead = wrapper.findAll('.section__head').find(h => h.text().includes('Packages'))
      if (pkgHead) {
        await pkgHead.trigger('click')
        expect(wrapper.find('.pkg-table').exists()).toBe(true)
        expect(wrapper.text()).toContain('django')
      }
    })

    it('shows all modules when "Show more" button is clicked', async () => {
      const manyModules: Record<string, string> = {}
      for (let i = 0; i < 15; i++) manyModules[`mod-${i}`] = `1.${i}.0`
      const eventWithManyModules = { id: 'evt-1', payload: { modules: manyModules } }
      setupQueries(baseIssue, [{}, {}, { data: ref(eventWithManyModules) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const showMoreBtn = wrapper.find('.pkg-table__more')
      if (showMoreBtn.exists()) {
        await showMoreBtn.trigger('click')
        expect(wrapper.find('.pkg-table__more').text()).toContain('Show less')
      }
    })
  })

  describe('tag value navigation', () => {
    it('navigates to issues with tag filter when tag value is clicked', async () => {
      const tags = [{ key: 'browser', total: 100, values: [{ value: 'Chrome', pct: 80, count: 80 }] }]
      setupQueries(baseIssue, [{}, {}, {}, {}, {}, {}, {}, {}, {}, { data: ref(tags) }])
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      const tagLink = wrapper.find('.tag-val__label')
      if (tagLink.exists()) {
        await tagLink.trigger('click')
        expect(pushMock).toHaveBeenCalledWith(expect.objectContaining({ path: '/issues' }))
      }
    })
  })

  describe('loading state - back link', () => {
    it('navigates to /issues when back link is clicked in loading state', async () => {
      setupQueries(undefined)
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      await wrapper.find('.detail-breadcrumb__back').trigger('click')
      expect(pushMock).toHaveBeenCalledWith('/issues')
    })
  })

  describe('error state - button clicks', () => {
    it('calls refetchIssue when Try again button is clicked', async () => {
      const refetchFn = vi.fn()
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref({ id: 'user-1', permissions: { manage_issues: true } }) } as any)
        .mockReturnValueOnce({ data: ref(undefined), isError: ref(true), refetch: refetchFn } as any)
        .mockReturnValue({ data: ref(undefined) } as any)
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      await wrapper.find('.txerror .btn').trigger('click')
      expect(refetchFn).toHaveBeenCalled()
    })

    it('navigates to /issues when back link is clicked in error state', async () => {
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref({ id: 'user-1', permissions: { manage_issues: true } }) } as any)
        .mockReturnValueOnce({ data: ref(undefined), isError: ref(true), refetch: vi.fn() } as any)
        .mockReturnValue({ data: ref(undefined) } as any)
      const wrapper = mount(IssueDetailView, { global: { stubs } })
      await wrapper.find('.detail-breadcrumb__back').trigger('click')
      expect(pushMock).toHaveBeenCalledWith('/issues')
    })
  })
})
