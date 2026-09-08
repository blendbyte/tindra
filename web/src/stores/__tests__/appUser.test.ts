import { describe, it, expect, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { appUserIdentity, appUserLabel, routeUserIdentity, useAppUserStore } from '../appUser'

describe('appUserIdentity', () => {
  it('prefers id over username and email', () => {
    expect(appUserIdentity({ id: 7, username: 'a', email: 'a@x.com' })).toBe('7')
  })
  it('falls back to username then email', () => {
    expect(appUserIdentity({ username: 'a', email: 'a@x.com' })).toBe('a')
    expect(appUserIdentity({ email: 'a@x.com' })).toBe('a@x.com')
  })
  it('returns empty when nothing is set', () => {
    expect(appUserIdentity({})).toBe('')
  })
})

describe('routeUserIdentity', () => {
  it('reads user from the query', () => {
    expect(routeUserIdentity({ user: 'u-1' })).toBe('u-1')
  })
  it('returns empty when absent', () => {
    expect(routeUserIdentity({})).toBe('')
  })
})

describe('appUser store', () => {
  beforeEach(() => {
    sessionStorage.clear()
    setActivePinia(createPinia())
  })

  it('selects and clears a user', () => {
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
    expect(store.identity).toBe('u-1')
    expect(appUserLabel(store.selected)).toBe('Alice')
    store.clear()
    expect(store.identity).toBe('')
  })
})
