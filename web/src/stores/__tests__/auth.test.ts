import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
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

  afterEach(() => {
    vi.restoreAllMocks()
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

  describe('init', () => {
    it('starts with ready=false', () => {
      const store = useAuthStore()
      expect(store.ready).toBe(false)
    })

    it('sets user and ready=true on successful fetch', async () => {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve(mockUser),
      }))
      const store = useAuthStore()
      await store.init()
      expect(store.user).toEqual(mockUser)
      expect(store.ready).toBe(true)
    })

    it('leaves user null and sets ready=true on non-ok response', async () => {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
        ok: false,
        json: () => Promise.resolve(null),
      }))
      const store = useAuthStore()
      await store.init()
      expect(store.user).toBeNull()
      expect(store.ready).toBe(true)
    })

    it('handles network errors gracefully, sets ready=true', async () => {
      vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('Network error')))
      const store = useAuthStore()
      await store.init()
      expect(store.user).toBeNull()
      expect(store.ready).toBe(true)
    })

    it('is idempotent — skips fetch on second call', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve(mockUser),
      })
      vi.stubGlobal('fetch', mockFetch)
      const store = useAuthStore()
      await store.init()
      await store.init()
      expect(mockFetch).toHaveBeenCalledOnce()
    })

    it('re-fetches /api/me when ready is reset to false after a prior init', async () => {
      const updated: User = { ...mockUser, name: 'Re-fetched User' }
      const mockFetch = vi.fn()
        .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve(mockUser) })
        .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve(updated) })
      vi.stubGlobal('fetch', mockFetch)

      const store = useAuthStore()
      await store.init()
      expect(store.user).toEqual(mockUser)
      expect(mockFetch).toHaveBeenCalledOnce()

      store.ready = false
      await store.init()

      expect(store.user).toEqual(updated)
      expect(mockFetch).toHaveBeenCalledTimes(2)
    })

    it('ready guard prevents re-fetch even after setUser', async () => {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve(mockUser),
      }))
      const store = useAuthStore()
      await store.init()
      store.setUser(null)
      const mockFetch2 = vi.fn()
      vi.stubGlobal('fetch', mockFetch2)
      await store.init()
      expect(mockFetch2).not.toHaveBeenCalled()
    })
  })
})
