import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import type { EventSummary } from '@/api/types'

vi.mock('@/api/client', () => ({ apiFetch: vi.fn() }))

const { showMock } = vi.hoisted(() => ({ showMock: vi.fn() }))
vi.mock('@/composables/useToast', () => ({ useToast: () => ({ show: showMock }) }))

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { timezone: 'UTC' } }) }))

import IssueEventList from '../IssueEventList.vue'
import { apiFetch } from '@/api/client'

function summary(id: string, extra: Partial<EventSummary> = {}): EventSummary {
  return { id, timestamp: '2024-01-01T00:00:00Z', received_at: '2024-01-01T00:00:00Z', level: 'error', environment: null, release: null, tags: {}, ...extra }
}

function page(events: EventSummary[], next?: { time: string; id: string }) {
  return { events, has_more: !!next, next_cursor_time: next?.time, next_cursor_id: next?.id }
}

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

function mountList(props: Partial<{ issueId: string; open: boolean; currentEventId: string }> = {}) {
  return mount(IssueEventList, { props: { issueId: 'iss-1', open: false, ...props } })
}

beforeEach(() => {
  vi.mocked(apiFetch).mockReset()
  showMock.mockReset()
})

describe('IssueEventList', () => {
  it('renders nothing and fetches nothing while closed', () => {
    const wrapper = mountList()
    expect(wrapper.find('.eventlist').exists()).toBe(false)
    expect(apiFetch).not.toHaveBeenCalled()
  })

  it('loads the first page when opened', async () => {
    vi.mocked(apiFetch).mockResolvedValue(page([summary('ev-1')]))
    const wrapper = mountList()
    await wrapper.setProps({ open: true })
    expect(wrapper.find('.eventlist__empty').text()).toBe('Loading…')
    await flushPromises()
    expect(apiFetch).toHaveBeenCalledWith('/api/issues/iss-1/events?')
    expect(wrapper.findAll('.evttable__row')).toHaveLength(1)
  })

  it('does not reload when reopened with events already loaded', async () => {
    vi.mocked(apiFetch).mockResolvedValue(page([summary('ev-1')]))
    const wrapper = mountList({ open: true })
    await flushPromises()
    await wrapper.setProps({ open: false })
    expect(wrapper.find('.eventlist').exists()).toBe(false)
    await wrapper.setProps({ open: true })
    await flushPromises()
    expect(apiFetch).toHaveBeenCalledTimes(1)
    expect(wrapper.findAll('.evttable__row')).toHaveLength(1)
  })

  it('shows an empty state when the issue has no events', async () => {
    vi.mocked(apiFetch).mockResolvedValue(page([]))
    const wrapper = mountList({ open: true })
    await flushPromises()
    expect(wrapper.find('.eventlist__empty').text()).toBe('No events found.')
    expect(wrapper.find('.eventlist__footer').exists()).toBe(false)
  })

  it('pages on with the cursor of the previous page', async () => {
    vi.mocked(apiFetch)
      .mockResolvedValueOnce(page([summary('ev-1')], { time: '2024-01-01T00:00:00Z', id: 'ev-1' }))
      .mockResolvedValueOnce(page([summary('ev-2', { received_at: '2023-12-31T00:00:00Z' })]))
    const wrapper = mountList({ open: true })
    await flushPromises()
    await wrapper.find('.eventlist__footer button').trigger('click')
    await flushPromises()
    expect(apiFetch).toHaveBeenLastCalledWith('/api/issues/iss-1/events?cursor_time=2024-01-01T00%3A00%3A00Z&cursor_id=ev-1')
    expect(wrapper.findAll('.evttable__row')).toHaveLength(2)
    expect(wrapper.find('.eventlist__footer').exists()).toBe(false)
  })

  it('emits select with the picked event and marks the current one', async () => {
    vi.mocked(apiFetch).mockResolvedValue(page([summary('ev-1'), summary('ev-2', { received_at: '2023-01-01T00:00:00Z' })]))
    const wrapper = mountList({ open: true, currentEventId: 'ev-2' })
    await flushPromises()
    const rows = wrapper.findAll('.evttable__row')
    expect(rows[1].classes()).toContain('evttable__row--active')
    expect(rows[1].attributes('aria-current')).toBe('true')
    expect(rows[0].attributes('aria-current')).toBeUndefined()
    await rows[0].trigger('click')
    expect(wrapper.emitted('select')![0][0]).toMatchObject({ id: 'ev-1' })
  })

  it('sorts by a column and flips the direction on a second click', async () => {
    vi.mocked(apiFetch).mockResolvedValue(page([
      summary('ev-1', { level: 'warning' }),
      summary('ev-2', { level: 'error' }),
    ]))
    const wrapper = mountList({ open: true })
    await flushPromises()
    const level = wrapper.findAll('.col-sort').find(b => b.text().includes('Level'))!
    await level.trigger('click')
    expect(level.find('.col-sort__icon').text()).toBe('↑')
    expect(wrapper.findAll('.evttable__row').map(r => r.text())).toEqual([expect.stringContaining('error'), expect.stringContaining('warning')])
    await level.trigger('click')
    expect(level.find('.col-sort__icon').text()).toBe('↓')
    expect(wrapper.findAll('.evttable__row')[0].text()).toContain('warning')
  })

  it('shows priority tags first, at most three', async () => {
    vi.mocked(apiFetch).mockResolvedValue(page([summary('ev-1', { tags: { custom: 'x', zone: 'y', url: '/a', browser: 'Chrome' } })]))
    const wrapper = mountList({ open: true })
    await flushPromises()
    expect(wrapper.findAll('.eventlist__tag-k').map(t => t.text())).toEqual(['browser', 'url', 'custom'])
  })

  it('toasts when a page fails to load', async () => {
    vi.mocked(apiFetch).mockRejectedValue(new Error('boom'))
    const wrapper = mountList({ open: true })
    await flushPromises()
    expect(showMock).toHaveBeenCalledWith('Could not load events. Try again.', 'error')
    expect(wrapper.find('.eventlist__empty').text()).toBe('No events found.')
  })

  it('drops a page that arrives after moving to another issue', async () => {
    const pending = deferred<unknown>()
    vi.mocked(apiFetch).mockReturnValueOnce(pending.promise as never)
    const wrapper = mountList({ open: true })
    await flushPromises()
    await wrapper.setProps({ issueId: 'iss-2', open: false })
    pending.resolve(page([summary('old-issue-event', { level: 'fatal' })]))
    await flushPromises()

    vi.mocked(apiFetch).mockResolvedValueOnce(page([summary('new-issue-event')]))
    await wrapper.setProps({ open: true })
    await flushPromises()
    expect(apiFetch).toHaveBeenLastCalledWith('/api/issues/iss-2/events?')
    expect(wrapper.findAll('.evttable__row')).toHaveLength(1)
    expect(wrapper.find('.eventlist').text()).not.toContain('fatal')
  })

  it('does not toast a failure that arrives after moving to another issue', async () => {
    const pending = deferred<unknown>()
    vi.mocked(apiFetch).mockReturnValueOnce(pending.promise as never)
    const wrapper = mountList({ open: true })
    await flushPromises()
    await wrapper.setProps({ issueId: 'iss-2' })
    pending.reject(new Error('aborted'))
    await flushPromises()
    expect(showMock).not.toHaveBeenCalled()
  })

  it('ignores responses that arrive after it was unmounted', async () => {
    const pending = deferred<unknown>()
    vi.mocked(apiFetch).mockReturnValueOnce(pending.promise as never)
    const wrapper = mountList({ open: true })
    await flushPromises()
    wrapper.unmount()
    pending.reject(new Error('gone'))
    await flushPromises()
    expect(showMock).not.toHaveBeenCalled()
  })
})
