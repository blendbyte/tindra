import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'

vi.mock('@/api/client', () => ({ apiFetch: vi.fn() }))

import { apiFetch } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { useMe } from '../useMe'
import { useConfig } from '../useConfig'
import type { User } from '@/api/types'

const baseUser = {
  id: 'u1', name: 'Ada', email: 'ada@example.com', has_password: true, mfa_enabled: false,
  weekly_digest: true, timezone: 'UTC', created_at: '2026-01-01T00:00:00Z',
  permissions: { manage_projects: false, manage_users: false, manage_alerts: false, manage_issues: false },
} as User

let cleanup = () => {}
afterEach(() => { cleanup(); vi.mocked(apiFetch).mockReset() })

function mountWith<T>(use: () => T) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  let result!: T
  const wrapper = mount(defineComponent({ setup() { result = use(); return () => h('div') } }), {
    global: { plugins: [pinia, [VueQueryPlugin, { queryClient: client }]] },
  })
  cleanup = () => { wrapper.unmount(); client.clear() }
  return { result: () => result, client }
}

describe('useMe', () => {
  it('mirrors a refetched user into the auth store', async () => {
    const updated = { ...baseUser, permissions: { ...baseUser.permissions, manage_issues: true } }
    vi.mocked(apiFetch).mockResolvedValue(updated)
    const { result } = mountWith(() => {
      const store = useAuthStore()
      store.setUser(baseUser)
      return { me: useMe(), store }
    })
    await flushPromises()
    expect(result().me.data.value?.permissions.manage_issues).toBe(true)
    expect(result().store.user?.permissions.manage_issues).toBe(true)
  })
})

describe('useConfig', () => {
  it('reuses the config the auth store loaded instead of fetching again', async () => {
    const { result } = mountWith(() => {
      const store = useAuthStore()
      store.config = { public_url: 'https://tindra.example.com', require_mfa: true }
      return useConfig()
    })
    await flushPromises()
    expect(result().baseUrl.value).toBe('https://tindra.example.com')
    expect(apiFetch).not.toHaveBeenCalledWith('/api/config', expect.anything())
  })
})
