import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, ref } from 'vue'
import { setActivePinia, createPinia } from 'pinia'

const replaceMock = vi.fn()
vi.mock('vue-router', () => ({
  useRoute: vi.fn(() => ({ query: {} })),
  useRouter: vi.fn(() => ({ replace: replaceMock })),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(() => ({ data: ref([]), isFetching: ref(false) })),
}))

vi.mock('@/stores/projects', () => ({
  useProjectsStore: vi.fn(() => ({ selectedIds: [] })),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn().mockResolvedValue([]),
}))

import UserFilter from '../UserFilter.vue'
import { useAppUserStore } from '@/stores/appUser'

beforeEach(() => {
  sessionStorage.clear()
  setActivePinia(createPinia())
  replaceMock.mockReset()
})

describe('UserFilter', () => {
  it('shows User: All when nothing is selected', () => {
    const wrapper = mount(UserFilter, { global: { stubs: { Icon: true } } })
    expect(wrapper.text()).toContain('User:')
    expect(wrapper.text()).toContain('All')
  })

  it('shows a chip after selecting a user from the store', async () => {
    const store = useAppUserStore()
    store.select({
      identity: 'u-1',
      user_id: 'u-1',
      username: 'alice',
      email: null,
      name: 'Alice',
      last_seen: '',
      project_id: 'p',
    })
    const wrapper = mount(UserFilter, { global: { stubs: { Icon: true } } })
    await nextTick()
    expect(wrapper.find('.user-chip').exists()).toBe(true)
    expect(wrapper.text()).toContain('Alice')
  })
})
