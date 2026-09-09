import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { nextTick } from 'vue'
import {
  appUserIdentity,
  appUserInitial,
  appUserLabel,
  routeUserIdentity,
  useAppUserStore,
} from '../appUser'
import type { AppUser } from '@/api/types'

const alice: AppUser = {
  identity: 'u-1',
  user_id: 'u-1',
  username: 'alice',
  email: 'alice@example.com',
  name: 'Alice',
  last_seen: '',
  project_id: 'p',
}

describe('appUserIdentity', () => {
  it('prefers id over username and email', () => {
    expect(appUserIdentity({ id: 7, username: 'a', email: 'a@x.com' })).toBe('7')
  })
  it('falls back to username then email', () => {
    expect(appUserIdentity({ username: 'a', email: 'a@x.com' })).toBe('a')
    expect(appUserIdentity({ email: 'a@x.com' })).toBe('a@x.com')
  })
  it('trims whitespace and skips a blank id', () => {
    expect(appUserIdentity({ id: '  x  ', username: 'a' })).toBe('x')
    expect(appUserIdentity({ id: '   ', username: 'a' })).toBe('a')
  })
  it('returns empty when nothing is set', () => {
    expect(appUserIdentity({})).toBe('')
  })
})

describe('routeUserIdentity', () => {
  it('reads user from the query', () => {
    expect(routeUserIdentity({ user: 'u-1' })).toBe('u-1')
  })
  it('uses the first string in an array', () => {
    expect(routeUserIdentity({ user: ['u-1', 'u-2'] })).toBe('u-1')
  })
  it('returns empty for a non-string array entry', () => {
    expect(routeUserIdentity({ user: [1] })).toBe('')
  })
  it('returns empty when absent or not a string', () => {
    expect(routeUserIdentity({})).toBe('')
    expect(routeUserIdentity({ user: 12 })).toBe('')
  })
})

describe('appUserLabel and appUserInitial', () => {
  it('prefers name, then username, email, user_id, identity', () => {
    expect(appUserLabel(alice)).toBe('Alice')
    expect(appUserLabel({ ...alice, name: null })).toBe('alice')
    expect(appUserLabel({ ...alice, name: null, username: null })).toBe('alice@example.com')
    expect(appUserLabel({ ...alice, name: null, username: null, email: null })).toBe('u-1')
    expect(appUserLabel({
      identity: 'ident',
      user_id: '',
      username: null,
      email: null,
      name: null,
      last_seen: '',
      project_id: '',
    })).toBe('ident')
  })
  it('returns empty and ? when there is no user', () => {
    expect(appUserLabel(null)).toBe('')
    expect(appUserLabel(undefined)).toBe('')
    expect(appUserInitial(null)).toBe('?')
  })
  it('uppercases the first letter of the label', () => {
    expect(appUserInitial(alice)).toBe('A')
  })
})

describe('appUser store', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('selects and clears a user', () => {
    const store = useAppUserStore()
    store.select(alice)
    expect(store.identity).toBe('u-1')
    expect(store.label).toBe('Alice')
    expect(store.initial).toBe('A')
    store.clear()
    expect(store.identity).toBe('')
    expect(store.selected).toBeNull()
  })

  it('persists the selection to sessionStorage', async () => {
    const store = useAppUserStore()
    store.select(alice)
    await nextTick()
    expect(JSON.parse(sessionStorage.getItem('tindra:appUser') ?? '{}').identity).toBe('u-1')
    store.clear()
    await nextTick()
    expect(sessionStorage.getItem('tindra:appUser')).toBeNull()
  })

  it('hydrates from sessionStorage on init', () => {
    sessionStorage.setItem('tindra:appUser', JSON.stringify(alice))
    setActivePinia(createPinia())
    expect(useAppUserStore().identity).toBe('u-1')
    expect(useAppUserStore().label).toBe('Alice')
  })

  it('ignores invalid stored JSON', () => {
    sessionStorage.setItem('tindra:appUser', '{not json')
    setActivePinia(createPinia())
    expect(useAppUserStore().selected).toBeNull()
  })

  it('ignores stored objects without an identity', () => {
    sessionStorage.setItem('tindra:appUser', JSON.stringify({ username: 'alice' }))
    setActivePinia(createPinia())
    expect(useAppUserStore().selected).toBeNull()
  })

  it('ignores a blank stored identity', () => {
    sessionStorage.setItem('tindra:appUser', JSON.stringify({ identity: '' }))
    setActivePinia(createPinia())
    expect(useAppUserStore().selected).toBeNull()
  })

  it('swallows sessionStorage write errors', () => {
    vi.stubGlobal('sessionStorage', {
      getItem: () => null,
      setItem: () => { throw new Error('quota') },
      removeItem: () => { throw new Error('quota') },
      clear: () => {},
      get length() { return 0 },
      key: () => null,
    })
    setActivePinia(createPinia())
    const store = useAppUserStore()
    expect(() => store.select(alice)).not.toThrow()
    expect(() => store.clear()).not.toThrow()
  })
})

it('falls back past scrubbed identity fields', () => {
  expect(appUserIdentity({ id: '[Filtered]', username: ' alice ' })).toBe('alice')
  expect(appUserIdentity({ id: '[Filtered]', email: '[Filtered]' })).toBe('')
})
