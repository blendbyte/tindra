import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref, toValue } from 'vue'
import type { Event as TindraEvent, Issue } from '@/api/types'

const { pushMock, selectMock } = vi.hoisted(() => ({ pushMock: vi.fn(), selectMock: vi.fn() }))

vi.mock('vue-router', () => ({ useRouter: () => ({ push: pushMock }) }))
vi.mock('@tanstack/vue-query', () => ({ useQuery: vi.fn() }))
vi.mock('@/api/client', () => ({ apiFetch: vi.fn() }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { timezone: 'UTC' } }) }))
vi.mock('@/stores/appUser', () => ({
  useAppUserStore: () => ({ select: selectMock }),
  appUserIdentity: (u: { id?: string | null; username?: string | null; email?: string | null }) => (u.id || u.username || u.email || '').toString(),
}))

import IssueTracePreview from '../IssueTracePreview.vue'
import IssueTags from '../IssueTags.vue'
import IssuePerfTransactions from '../IssuePerfTransactions.vue'
import IssueUserCard from '../IssueUserCard.vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'

const stubs = { Icon: { template: '<span />' }, RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } }

type QueryOptions = { queryKey: unknown; queryFn: (ctx: { signal?: AbortSignal }) => unknown; enabled?: unknown }
let queries: QueryOptions[] = []

// Every query gets the next queued result, in creation order.
function setupQueries(...results: Record<string, unknown>[]) {
  queries = []
  vi.mocked(useQuery).mockImplementation(((options: QueryOptions) => {
    queries.push(options)
    return { data: ref(undefined), isLoading: ref(false), ...results[queries.length - 1] }
  }) as never)
}

const issue = { id: 'iss-1', project_id: 'proj-1', kind: 'n1_query', event_count: 1234 } as Issue

beforeEach(() => {
  localStorage.removeItem('tindra:issue:collapsed')
  pushMock.mockReset()
  selectMock.mockReset()
  vi.mocked(apiFetch).mockReset()
})

describe('IssueTracePreview', () => {
  const tx = { id: 'tx-1', transaction: '/checkout', op: 'http.server', duration_ms: 200 }
  const spans = Array.from({ length: 12 }, (_, i) => ({ op: 'db', description: `q${i}`, start_offset_ms: i * 10, duration_ms: 5 }))
  const props = { issueId: 'iss-1', eventIndex: 2, pinnedEvent: '', eventParams: 'offset=2' }

  it('keys the trace by the selected event and fetches it with the same selection', () => {
    setupQueries()
    mount(IssueTracePreview, { props: { ...props, event: { id: 'e', trace_id: 'trace-1', payload: {} } as unknown as TindraEvent }, global: { stubs } })
    expect(toValue(queries[0].queryKey)).toEqual(['issues', 'iss-1', 'trace', 2, ''])
    expect(toValue(queries[0].enabled)).toBe(true)
    queries[0].queryFn({})
    expect(apiFetch).toHaveBeenCalledWith('/api/issues/iss-1/trace?offset=2', { signal: undefined })
  })

  it('reads the trace id from the trace context and skips events without one', () => {
    setupQueries()
    mount(IssueTracePreview, { props: { ...props, event: { id: 'e', payload: { contexts: { trace: { trace_id: 't' } } } } as unknown as TindraEvent }, global: { stubs } })
    expect(toValue(queries[0].enabled)).toBe(true)
    setupQueries()
    mount(IssueTracePreview, { props: { ...props, event: { id: 'e', payload: {} } as unknown as TindraEvent }, global: { stubs } })
    expect(toValue(queries[0].enabled)).toBe(false)
  })

  it('previews the transaction and its first nine spans', async () => {
    setupQueries({ data: ref(tx) }, { data: ref(spans) })
    const wrapper = mount(IssueTracePreview, { props: { ...props, event: null }, global: { stubs } })
    expect(toValue(queries[1].queryKey)).toEqual(['transactions', 'tx-1', 'spans'])
    expect(wrapper.findAll('.trace-preview__row')).toHaveLength(10)
    expect(wrapper.find('.trace-preview__more').text()).toBe('+3 more spans')
    await wrapper.find('.section__link').trigger('click')
    expect(pushMock).toHaveBeenCalledWith('/transactions/tx-1')
    expect(wrapper.find('.trace-preview').exists()).toBe(true)
  })

  it('renders nothing without a linked transaction', () => {
    setupQueries()
    expect(mount(IssueTracePreview, { props: { ...props, event: null }, global: { stubs } }).find('.section').exists()).toBe(false)
  })
})

describe('IssueTags', () => {
  const tags = [{ key: 'browser', total: 1500, values: [{ value: 'Chrome', count: 1200, pct: 80 }] }]

  it('reads the tags of the issue and links values to the filtered list', async () => {
    setupQueries({ data: ref(tags) })
    const wrapper = mount(IssueTags, { props: { issueId: 'iss-1' }, global: { stubs } })
    expect(toValue(queries[0].queryKey)).toEqual(['issues', 'iss-1', 'tags'])
    expect(wrapper.find('.section__count').text()).toBe('1 keys')
    expect(wrapper.find('.tag-val__label').attributes('href')).toBe('/issues?tag_key=browser&tag_value=Chrome')
    await wrapper.find('.tag-val__label').trigger('click')
    expect(pushMock).toHaveBeenCalledWith({ path: '/issues', query: { tag_key: 'browser', tag_value: 'Chrome' } })
  })

  it('marks a value every event shares so the sidebar can drop its bar', () => {
    setupQueries({ data: ref([
      ...tags,
      { key: 'level', total: 3, values: [{ value: 'error', count: 3, pct: 100 }] },
    ]) })
    const wrapper = mount(IssueTags, { props: { issueId: 'iss-1' }, global: { stubs } })
    const rows = wrapper.findAll('.tag-val')
    expect(rows[0].classes()).not.toContain('tag-val--only')
    expect(rows[1].classes()).toContain('tag-val--only')
  })

  it('renders nothing without tags', () => {
    setupQueries({ data: ref([]) })
    expect(mount(IssueTags, { props: { issueId: 'iss-1' }, global: { stubs } }).find('.section').exists()).toBe(false)
  })
})

describe('IssuePerfTransactions', () => {
  it('lists the affected transactions', () => {
    setupQueries({ data: ref([{ id: 'pe-1', transaction_id: 'tx-9', transaction: '/items', span_count: 7, total_ms: 42, created_at: '2024-01-01T00:00:00Z' }]) })
    const wrapper = mount(IssuePerfTransactions, { props: { issueId: 'iss-1', issue }, global: { stubs } })
    expect(toValue(queries[0].queryKey)).toEqual(['issues', 'iss-1', 'perf-events'])
    expect(toValue(queries[0].enabled)).toBe(true)
    expect(wrapper.find('.section__count').text()).toBe(`${(1234).toLocaleString()} detections`)
    expect(wrapper.find('.perf-table a').attributes('href')).toBe('/transactions/tx-9')
    expect(wrapper.text()).toContain('7×')
  })

  it('shows loading and empty states', () => {
    setupQueries({ isLoading: ref(true) })
    expect(mount(IssuePerfTransactions, { props: { issueId: 'iss-1', issue }, global: { stubs } }).text()).toContain('Loading...')
    setupQueries({ data: ref([]) })
    expect(mount(IssuePerfTransactions, { props: { issueId: 'iss-1', issue }, global: { stubs } }).text()).toContain('No transactions recorded yet.')
  })
})

describe('IssueUserCard', () => {
  function userEvent(user: Record<string, unknown>) {
    return { id: 'e', payload: { user } } as unknown as TindraEvent
  }

  it('renders nothing for events without a user', () => {
    expect(mount(IssueUserCard, { props: { issue, event: null }, global: { stubs } }).find('.user-card').exists()).toBe(false)
  })

  it('lenses into the user with the project of the issue', async () => {
    const wrapper = mount(IssueUserCard, { props: { issue, event: userEvent({ id: 7, username: 'ann', email: 'ann@example.com' }) }, global: { stubs } })
    expect(wrapper.find('.user-card__avatar').text()).toBe('A')
    expect(wrapper.find('.user-card__name').text()).toBe('ann')
    expect(wrapper.find('.user-card__sub').text()).toBe('ann@example.com')
    await wrapper.findAll('.user-card__link')[1].trigger('click')
    expect(selectMock).toHaveBeenCalledWith(expect.objectContaining({ identity: '7', user_id: '7', project_id: 'proj-1' }))
    expect(pushMock).toHaveBeenCalledWith({ path: '/performance/transactions', query: { user: '7' } })
  })

  it('keeps a user without an identity as a static card', () => {
    const wrapper = mount(IssueUserCard, { props: { issue, event: userEvent({ ip_address: '10.0.0.1' }) }, global: { stubs } })
    expect(wrapper.find('.user-card__body--btn').exists()).toBe(false)
    expect(wrapper.find('.user-card__avatar').text()).toBe('?')
    expect(wrapper.text()).toContain('10.0.0.1')
  })
})
