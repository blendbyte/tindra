import { describe, it, expect, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useTimezone } from '../useTimezone'
import { useAuthStore } from '@/stores/auth'
import type { User } from '@/api/types'

describe('useTimezone', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('returns UTC when no user is logged in', () => {
    const tz = useTimezone()
    expect(tz.value).toBe('UTC')
  })

  it('returns the timezone from the logged-in user', () => {
    const auth = useAuthStore()
    auth.user = { timezone: 'America/New_York' } as User
    const tz = useTimezone()
    expect(tz.value).toBe('America/New_York')
  })

  it('returns UTC when user has no timezone property', () => {
    const auth = useAuthStore()
    auth.user = {} as User
    const tz = useTimezone()
    expect(tz.value).toBe('UTC')
  })

  it('is reactive to user timezone changes', () => {
    const auth = useAuthStore()
    auth.user = { timezone: 'Europe/Paris' } as User
    const tz = useTimezone()
    expect(tz.value).toBe('Europe/Paris')
    auth.user = { timezone: 'Asia/Tokyo' } as User
    expect(tz.value).toBe('Asia/Tokyo')
  })
})
