import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { ref, toValue, type Ref } from 'vue'
import type { Comment, IssueHistoryEntry, User } from '@/api/types'

const { invalidateMock, confirmMock } = vi.hoisted(() => ({ invalidateMock: vi.fn(), confirmMock: vi.fn() }))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(),
  useQueryClient: () => ({ invalidateQueries: invalidateMock }),
}))
vi.mock('@/api/client', () => ({ apiFetch: vi.fn() }))
vi.mock('@/composables/useConfirm', () => ({ useConfirm: () => ({ confirm: confirmMock }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { timezone: 'UTC' } }) }))

import IssueActivity from '../IssueActivity.vue'
import { useQuery, useMutation } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'

const stubs = { Icon: { template: '<span />' } }

type MutationMock = { mutate: ReturnType<typeof vi.fn>; isPending: Ref<boolean>; options: { mutationFn: (v: never) => unknown; onSuccess?: () => void } }
let mutations: MutationMock[] = []
let queryKeys: unknown[][] = []

const me = { id: 'user-1', permissions: { manage_issues: false } } as User
const users = [{ id: 'user-2', name: 'Alice', email: 'alice@example.com' }] as User[]

function comment(extra: Partial<Comment> = {}): Comment {
  return { id: 'c-1', issue_id: 'iss-1', user_id: 'user-1', user_email: 'me@example.com', user_name: 'Me', body: 'Hello', created_at: '2024-01-01T00:00:00Z', updated_at: '2024-01-01T00:00:00Z', ...extra }
}

function setup(comments: Comment[] = [], history: IssueHistoryEntry[] = []) {
  mutations = []
  queryKeys = []
  vi.mocked(useQuery).mockImplementation(((options: { queryKey: unknown }) => {
    const key = toValue(options.queryKey) as unknown[]
    queryKeys.push(key)
    return { data: ref(key[2] === 'comments' ? comments : history) }
  }) as never)
  vi.mocked(useMutation).mockImplementation(((options: MutationMock['options']) => {
    const m = { mutate: vi.fn(), isPending: ref(false), options }
    mutations.push(m)
    return m
  }) as never)
}

function mountActivity(props: Partial<{ issueId: string; me: User | null; users: User[] }> = {}) {
  return mount(IssueActivity, { props: { issueId: 'iss-1', me, users, ...props }, global: { stubs } })
}

// The section creates its mutations in this order.
const postComment = () => mutations[0]
const saveEdit = () => mutations[1]
const deleteComment = () => mutations[2]

beforeEach(() => {
  localStorage.removeItem('tindra:issue:collapsed')
  invalidateMock.mockReset()
  confirmMock.mockReset()
  vi.mocked(apiFetch).mockReset()
})

describe('IssueActivity', () => {
  it('reads comments and history under the issue query keys', () => {
    setup()
    mountActivity()
    expect(queryKeys).toEqual([['issues', 'iss-1', 'comments'], ['issues', 'iss-1', 'history']])
  })

  it('merges comments and history into one timeline ordered by time', () => {
    setup(
      [comment({ created_at: '2024-01-03T00:00:00Z' })],
      [{ id: 'h-1', issue_id: 'iss-1', actor_id: 'user-2', event_type: 'assigned', details: { to_id: 'user-2' }, created_at: '2024-01-02T00:00:00Z' }],
    )
    const wrapper = mountActivity()
    expect(wrapper.find('.section__count').text()).toBe('2 events')
    const items = wrapper.findAll('.activity-event, .activity-item')
    expect(items[0].classes()).toContain('activity-event')
    expect(items[0].text()).toContain('Assigned to Alice')
    expect(items[0].find('.activity-event__actor').text()).toBe('Alice')
    expect(items[1].classes()).toContain('activity-item')
  })

  it('marks edited comments', () => {
    setup([comment({ updated_at: '2024-01-02T00:00:00Z' })])
    expect(mountActivity().find('.activity-edited').exists()).toBe(true)
  })

  it('posts the trimmed comment and clears the draft once saved', async () => {
    setup()
    const wrapper = mountActivity()
    await wrapper.find('.activity-compose textarea').setValue('  Looks like a race  ')
    await wrapper.find('form.activity-compose').trigger('submit')
    expect(postComment().mutate).toHaveBeenCalledWith('Looks like a race')

    await postComment().options.mutationFn('Looks like a race' as never)
    expect(apiFetch).toHaveBeenCalledWith('/api/issues/iss-1/comments', { method: 'POST', body: JSON.stringify({ body: 'Looks like a race' }) })
    postComment().options.onSuccess!()
    await wrapper.vm.$nextTick()
    expect((wrapper.find('.activity-compose textarea').element as HTMLTextAreaElement).value).toBe('')
    expect(invalidateMock).toHaveBeenCalledWith({ queryKey: ['issues', 'iss-1', 'comments'] })
  })

  it('does not post a blank comment', async () => {
    setup()
    const wrapper = mountActivity()
    await wrapper.find('.activity-compose textarea').setValue('   ')
    await wrapper.find('form.activity-compose').trigger('submit')
    expect(postComment().mutate).not.toHaveBeenCalled()
  })

  it('edits an own comment and closes the editor once saved', async () => {
    setup([comment()])
    const wrapper = mountActivity()
    await wrapper.find('.activity-action[title="Edit"]').trigger('click')
    expect((wrapper.find('.activity-edit textarea').element as HTMLTextAreaElement).value).toBe('Hello')
    await wrapper.find('.activity-edit textarea').setValue(' Hello again ')
    await wrapper.find('.activity-edit .btn--primary').trigger('click')
    expect(saveEdit().mutate).toHaveBeenCalledWith({ id: 'c-1', body: 'Hello again' })

    await saveEdit().options.mutationFn({ id: 'c-1', body: 'Hello again' } as never)
    expect(apiFetch).toHaveBeenCalledWith('/api/comments/c-1', { method: 'PUT', body: JSON.stringify({ body: 'Hello again' }) })
    saveEdit().options.onSuccess!()
    await wrapper.vm.$nextTick()
    expect(wrapper.find('.activity-edit').exists()).toBe(false)
  })

  it('cancels an edit with Escape', async () => {
    setup([comment()])
    const wrapper = mountActivity()
    await wrapper.find('.activity-action[title="Edit"]').trigger('click')
    await wrapper.find('.activity-edit textarea').trigger('keydown', { key: 'Escape' })
    expect(wrapper.find('.activity-edit').exists()).toBe(false)
  })

  it('deletes a comment only after confirmation', async () => {
    setup([comment()])
    const wrapper = mountActivity()
    confirmMock.mockResolvedValueOnce(false)
    await wrapper.find('.activity-action--danger').trigger('click')
    await flushPromises()
    expect(confirmMock).toHaveBeenCalledWith({ title: 'Delete this comment?', confirmLabel: 'Delete comment', danger: true })
    expect(deleteComment().mutate).not.toHaveBeenCalled()

    confirmMock.mockResolvedValueOnce(true)
    await wrapper.find('.activity-action--danger').trigger('click')
    await flushPromises()
    expect(deleteComment().mutate).toHaveBeenCalledWith('c-1')
    await deleteComment().options.mutationFn('c-1' as never)
    expect(apiFetch).toHaveBeenCalledWith('/api/comments/c-1', { method: 'DELETE' })
  })

  it('lets managers delete but not edit comments by others', () => {
    setup([comment({ user_id: 'user-9' })])
    const wrapper = mountActivity({ me: { id: 'user-1', permissions: { manage_issues: true } } as User })
    expect(wrapper.find('.activity-action--danger').exists()).toBe(true)
    expect(wrapper.find('.activity-action[title="Edit"]').exists()).toBe(false)
  })

  it('offers no comment actions before the signed-in user is known', () => {
    setup([comment()])
    const wrapper = mountActivity({ me: null })
    expect(wrapper.find('.activity-action').exists()).toBe(false)
  })

  it('drops the draft and an open edit when moving to another issue', async () => {
    setup([comment()])
    const wrapper = mountActivity()
    await wrapper.find('.activity-compose textarea').setValue('half-written')
    await wrapper.find('.activity-action[title="Edit"]').trigger('click')
    await wrapper.setProps({ issueId: 'iss-2' })
    expect((wrapper.find('.activity-compose textarea').element as HTMLTextAreaElement).value).toBe('')
    expect(wrapper.find('.activity-edit').exists()).toBe(false)
  })

  it('keeps the draft while the section is collapsed', async () => {
    setup()
    const wrapper = mountActivity()
    await wrapper.find('.activity-compose textarea').setValue('keep me')
    await wrapper.find('.section__head').trigger('click')
    expect(wrapper.find('.activity').exists()).toBe(false)
    await wrapper.find('.section__head').trigger('click')
    expect((wrapper.find('.activity-compose textarea').element as HTMLTextAreaElement).value).toBe('keep me')
  })
})
