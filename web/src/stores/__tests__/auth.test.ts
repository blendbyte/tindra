import { describe, it, expect, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from '../auth'
import type { User } from '@/api/types'

const mockUser: User = {
  id: '1',
  email: 'test@example.com',
  name: 'Test User',
  role: 'member',
  created_at: '2025-01-01T00:00:00Z',
}

describe('auth store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('starts with null user', () => {
    const store = useAuthStore()
    expect(store.user).toBeNull()
  })

  it('sets a user', () => {
    const store = useAuthStore()
    store.setUser(mockUser)
    expect(store.user).toEqual(mockUser)
  })

  it('clears the user on logout', () => {
    const store = useAuthStore()
    store.setUser(mockUser)
    store.setUser(null)
    expect(store.user).toBeNull()
  })
})
