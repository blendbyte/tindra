import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { nextTick, reactive, ref, unref } from 'vue'
import { setActivePinia, createPinia } from 'pinia'
import type { AppUser } from '@/api/types'

const replaceMock = vi.fn()
const routeState = reactive({ query: {} as Record<string, unknown> })

vi.mock('vue-router', () => ({
  useRoute: vi.fn(() => routeState),
  useRouter: vi.fn(() => ({
    replace: (arg: { query?: Record<string, unknown> }) => {
      replaceMock(arg)
      if (arg?.query) routeState.query = { ...arg.query }
    },
  })),
}))

const usersRef = ref<AppUser[]>([])
const isFetchingRef = ref(false)
vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(() => ({ data: usersRef, isFetching: isFetchingRef })),
}))

vi.mock('@/stores/projects', () => ({
  useProjectsStore: vi.fn(() => ({ selectedIds: ['proj-1'] })),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn().mockResolvedValue([]),
}))

import UserFilter from '../UserFilter.vue'
import { useAppUserStore } from '@/stores/appUser'
import { apiFetch } from '@/api/client'
import { useProjectsStore } from '@/stores/projects'
import { useQuery } from '@tanstack/vue-query'

const alice: AppUser = {
  identity: 'u-1',
  user_id: 'u-1',
  username: 'alice',
  email: 'alice@example.com',
  name: 'Alice',
  last_seen: '2024-01-01T00:00:00Z',
  project_id: 'proj-1',
}

let wrapper: ReturnType<typeof mount> | null = null

function mountFilter() {
  wrapper?.unmount()
  wrapper = mount(UserFilter, { global: { stubs: { Icon: true } } })
  return wrapper
}

beforeEach(() => {
  sessionStorage.clear()
  setActivePinia(createPinia())
  replaceMock.mockReset()
  routeState.query = {}
  usersRef.value = []
  isFetchingRef.value = false
  vi.mocked(apiFetch).mockReset()
  vi.mocked(apiFetch).mockResolvedValue([])
  vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: ['proj-1'] } as any)
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  vi.useRealTimers()
})

describe('UserFilter', () => {
  it('shows User: All when nothing is selected', () => {
    const wrapper = mountFilter()
    expect(wrapper.text()).toContain('User:')
    expect(wrapper.text()).toContain('All')
  })

  it('shows a chip after selecting a user from the store', async () => {
    const store = useAppUserStore()
    store.select(alice)
    const wrapper = mountFilter()
    await nextTick()
    expect(wrapper.find('.user-chip').exists()).toBe(true)
    expect(wrapper.text()).toContain('Alice')
  })

  it('opens the popover and lists people', async () => {
    usersRef.value = [alice]
    const wrapper = mountFilter()
    await wrapper.find('[aria-label="Filter by user"]').trigger('click')
    await nextTick()
    expect(wrapper.find('.popover').exists()).toBe(true)
    expect(wrapper.text()).toContain('Alice')
    expect(wrapper.text()).toContain('alice@example.com')
  })

  it('picks a person from the list', async () => {
    usersRef.value = [alice]
    const wrapper = mountFilter()
    await wrapper.find('[aria-label="Filter by user"]').trigger('click')
    await nextTick()
    const items = wrapper.findAll('.popover__item')
    await items[1].trigger('click')
    expect(useAppUserStore().identity).toBe('u-1')
    expect(wrapper.find('.popover').exists()).toBe(false)
  })

  it('clears the selected user from the chip', async () => {
    const store = useAppUserStore()
    store.select(alice)
    const wrapper = mountFilter()
    await nextTick()
    await flushPromises()
    expect(routeState.query.user).toBe('u-1')
    await wrapper.find('[aria-label="Clear user filter"]').trigger('click')
    expect(store.identity).toBe('')
    expect(wrapper.text()).toContain('All')
    expect(routeState.query.user).toBeUndefined()
  })

  it('clears from the All users row', async () => {
    const store = useAppUserStore()
    store.select(alice)
    const wrapper = mountFilter()
    await wrapper.find('[aria-label="Change user filter"]').trigger('click')
    await nextTick()
    await wrapper.findAll('.popover__item')[0].trigger('click')
    expect(store.identity).toBe('')
  })

  it('closes the popover on outside click', async () => {
    const wrapper = mountFilter()
    await wrapper.find('[aria-label="Filter by user"]').trigger('click')
    await nextTick()
    expect(wrapper.find('.popover').exists()).toBe(true)
    document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    await nextTick()
    expect(wrapper.find('.popover').exists()).toBe(false)
  })

  it('hydrates the store from ?user= when the API returns a match', async () => {
    routeState.query = { user: 'u-1' }
    vi.mocked(apiFetch).mockResolvedValue([alice])
    mountFilter()
    await flushPromises()
    expect(useAppUserStore().identity).toBe('u-1')
    expect(apiFetch).toHaveBeenCalled()
  })

  it('hydrates a stub when the API returns no match', async () => {
    routeState.query = { user: 'missing' }
    vi.mocked(apiFetch).mockResolvedValue([])
    mountFilter()
    await flushPromises()
    expect(useAppUserStore().identity).toBe('missing')
  })

  it('hydrates a stub when the API fails', async () => {
    routeState.query = { user: 'u-err' }
    vi.mocked(apiFetch).mockRejectedValue(new Error('nope'))
    mountFilter()
    await flushPromises()
    expect(useAppUserStore().identity).toBe('u-err')
  })

  it('skips hydrate when the store already has that identity', async () => {
    const store = useAppUserStore()
    store.select(alice)
    routeState.query = { user: 'u-1' }
    mountFilter()
    await flushPromises()
    expect(apiFetch).not.toHaveBeenCalled()
  })

  it('writes ?user= when the store has a selection and the URL does not', async () => {
    const store = useAppUserStore()
    store.select(alice)
    routeState.query = {}
    mountFilter()
    await flushPromises()
    expect(replaceMock).toHaveBeenCalled()
    const arg = replaceMock.mock.calls.at(-1)?.[0]
    expect(arg.query.user).toBe('u-1')
  })

  it('restores ?user= if the URL drops it while a user is selected', async () => {
    const store = useAppUserStore()
    store.select(alice)
    routeState.query = { user: 'u-1' }
    mountFilter()
    await flushPromises()
    replaceMock.mockClear()
    routeState.query = {}
    await nextTick()
    await flushPromises()
    expect(replaceMock).toHaveBeenCalled()
  })

  it('uses project_id from the route when it is a string', async () => {
    routeState.query = { user: 'u-1', project_id: 'p-route' }
    vi.mocked(apiFetch).mockResolvedValue([alice])
    mountFilter()
    await flushPromises()
    const url = String(vi.mocked(apiFetch).mock.calls[0][0])
    expect(url).toContain('project_id=p-route')
  })

  it('uses project_id from the route when it is an array', async () => {
    routeState.query = { user: 'u-1', project_id: ['p-a', 'p-b'] }
    vi.mocked(apiFetch).mockResolvedValue([alice])
    mountFilter()
    await flushPromises()
    const url = String(vi.mocked(apiFetch).mock.calls[0][0])
    expect(url).toContain('project_id=p-a')
    expect(url).toContain('project_id=p-b')
  })

  it('shows an empty hint when no people exist', async () => {
    const wrapper = mountFilter()
    await wrapper.find('[aria-label="Filter by user"]').trigger('click')
    await nextTick()
    expect(wrapper.text()).toContain('No people yet')
  })

  it('runs the app-users queryFn', async () => {
    const w = mountFilter()
    await w.find('[aria-label="Filter by user"]').trigger('click')
    const opts = vi.mocked(useQuery).mock.calls.at(-1)?.[0] as {
      queryFn: () => unknown
      queryKey: unknown
      enabled: unknown
    }
    opts.queryFn()
    expect(unref(opts.enabled)).toBe(true)
    expect(unref(opts.queryKey)?.[0]).toBe('app-users')
    expect(apiFetch).toHaveBeenCalledWith(expect.stringContaining('/api/app-users?'))
  })

  it('debounces search before putting q on the query key', async () => {
    vi.useFakeTimers()
    const wrapper = mountFilter()
    await wrapper.find('[aria-label="Filter by user"]').trigger('click')
    const opts = vi.mocked(useQuery).mock.calls.at(-1)?.[0] as { queryKey: unknown }
    const input = wrapper.find('input[aria-label="Search people"]')
    await input.setValue('ali')
    vi.advanceTimersByTime(199)
    await nextTick()
    expect(String(unref(opts.queryKey)?.[1] ?? '')).not.toContain('q=')
    vi.advanceTimersByTime(1)
    await nextTick()
    expect(String(unref(opts.queryKey)?.[1] ?? '')).toContain('q=ali')
    await input.setValue('alice')
    vi.advanceTimersByTime(200)
    await nextTick()
    expect(String(unref(opts.queryKey)?.[1] ?? '')).toContain('q=alice')
  })

  it('clears the debounced query immediately when search is emptied', async () => {
    vi.useFakeTimers()
    const wrapper = mountFilter()
    await wrapper.find('[aria-label="Filter by user"]').trigger('click')
    const opts = vi.mocked(useQuery).mock.calls.at(-1)?.[0] as { queryKey: unknown }
    const input = wrapper.find('input[aria-label="Search people"]')
    await input.setValue('alice')
    vi.advanceTimersByTime(200)
    await nextTick()
    expect(String(unref(opts.queryKey)?.[1] ?? '')).toContain('q=alice')
    await input.setValue('   ')
    await nextTick()
    expect(String(unref(opts.queryKey)?.[1] ?? '')).not.toContain('q=')
  })

  it('shows No matching people when a search has no hits', async () => {
    const wrapper = mountFilter()
    await wrapper.find('[aria-label="Filter by user"]').trigger('click')
    await wrapper.find('input[aria-label="Search people"]').setValue('zzz')
    await nextTick()
    expect(wrapper.text()).toContain('No matching people')
  })

  it('hides the empty hint while the list is fetching', async () => {
    isFetchingRef.value = true
    const wrapper = mountFilter()
    await wrapper.find('[aria-label="Filter by user"]').trigger('click')
    await nextTick()
    expect(wrapper.text()).not.toContain('No people yet')
  })

  it('shows username and email as secondary text', async () => {
    usersRef.value = [alice]
    const wrapper = mountFilter()
    await wrapper.find('[aria-label="Filter by user"]').trigger('click')
    await nextTick()
    expect(wrapper.find('.user-filter__sub').text()).toContain('alice')
    expect(wrapper.find('.user-filter__sub').text()).toContain('alice@example.com')
  })

  it('closes the popover when the chip is clicked again', async () => {
    const wrapper = mountFilter()
    await wrapper.find('[aria-label="Filter by user"]').trigger('click')
    await nextTick()
    expect(wrapper.find('.popover').exists()).toBe(true)
    await wrapper.find('[aria-label="Filter by user"]').trigger('click')
    await nextTick()
    expect(wrapper.find('.popover').exists()).toBe(false)
  })

  it('keeps the popover open for clicks inside the filter', async () => {
    const wrapper = mountFilter()
    await wrapper.find('[aria-label="Filter by user"]').trigger('click')
    await nextTick()
    wrapper.find('.popover').element.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    await nextTick()
    expect(wrapper.find('.popover').exists()).toBe(true)
  })

  it('hydrates the first API result when no identity matches', async () => {
    routeState.query = { user: 'missing' }
    vi.mocked(apiFetch).mockResolvedValue([{ ...alice, identity: 'other' }])
    mountFilter()
    await flushPromises()
    expect(useAppUserStore().identity).toBe('other')
  })

  it('re-hydrates when the URL user changes', async () => {
    routeState.query = { user: 'u-1' }
    vi.mocked(apiFetch).mockResolvedValue([alice])
    mountFilter()
    await flushPromises()
    const bob: AppUser = { ...alice, identity: 'u-2', name: 'Bob', username: 'bob' }
    vi.mocked(apiFetch).mockResolvedValue([bob])
    routeState.query = { user: 'u-2' }
    await nextTick()
    await flushPromises()
    expect(useAppUserStore().identity).toBe('u-2')
  })

  it('falls back to selected project ids when project_id is empty', async () => {
    routeState.query = { user: 'u-1', project_id: '' }
    vi.mocked(apiFetch).mockResolvedValue([alice])
    mountFilter()
    await flushPromises()
    const url = String(vi.mocked(apiFetch).mock.calls[0][0])
    expect(url).toContain('project_id=proj-1')
  })

  it('ignores a non-string ?user= value', async () => {
    routeState.query = { user: ['u-1'] }
    mountFilter()
    await flushPromises()
    expect(apiFetch).not.toHaveBeenCalled()
  })

  it('removes listeners on unmount', async () => {
    const wrapper = mountFilter()
    await wrapper.find('[aria-label="Filter by user"]').trigger('click')
    wrapper.unmount()
    document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
  })
})
