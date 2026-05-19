import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'

// Node 22+ exposes an experimental (broken) localStorage that shadows happy-dom's.
// Stub it explicitly so tests have a working, isolated implementation.
const storage: Record<string, string> = {}
const mockLocalStorage: Storage = {
  getItem: (key) => storage[key] ?? null,
  setItem: (key, val) => { storage[key] = val },
  removeItem: (key) => { delete storage[key] },
  clear: () => { Object.keys(storage).forEach((k) => delete storage[k]) },
  get length() { return Object.keys(storage).length },
  key: (i) => Object.keys(storage)[i] ?? null,
}

function mockMatchMedia(prefersDark: boolean) {
  const listeners: Array<(e: MediaQueryListEvent) => void> = []
  const mql = {
    matches: prefersDark,
    media: '(prefers-color-scheme: dark)',
    addEventListener: vi.fn((_: string, cb: (e: MediaQueryListEvent) => void) => {
      listeners.push(cb)
    }),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }
  Object.defineProperty(window, 'matchMedia', { writable: true, value: vi.fn(() => mql) })
  return mql
}

describe('ui store', () => {
  beforeEach(() => {
    mockLocalStorage.clear()
    vi.stubGlobal('localStorage', mockLocalStorage)
    mockMatchMedia(false)
    setActivePinia(createPinia())
    vi.resetModules()
  })

  it('resolves to light when system prefers light and no stored preference', async () => {
    mockMatchMedia(false)
    const { useUiStore } = await import('../ui')
    const store = useUiStore()
    expect(store.theme).toBeNull()
    expect(store.resolvedTheme).toBe('light')
  })

  it('resolves to dark when system prefers dark and no stored preference', async () => {
    mockMatchMedia(true)
    const { useUiStore } = await import('../ui')
    const store = useUiStore()
    expect(store.theme).toBeNull()
    expect(store.resolvedTheme).toBe('dark')
  })

  it('respects stored theme over system preference', async () => {
    mockLocalStorage.setItem('tindra:theme', 'dark')
    mockMatchMedia(false)
    const { useUiStore } = await import('../ui')
    const store = useUiStore()
    expect(store.resolvedTheme).toBe('dark')
  })

  it('toggleTheme switches dark → light and persists', async () => {
    mockMatchMedia(true)
    const { useUiStore } = await import('../ui')
    const store = useUiStore()
    store.toggleTheme()
    expect(store.resolvedTheme).toBe('light')
    expect(mockLocalStorage.getItem('tindra:theme')).toBe('light')
  })

  it('toggleTheme switches light → dark and persists', async () => {
    mockMatchMedia(false)
    const { useUiStore } = await import('../ui')
    const store = useUiStore()
    store.toggleTheme()
    expect(store.resolvedTheme).toBe('dark')
    expect(mockLocalStorage.getItem('tindra:theme')).toBe('dark')
  })

  it('openCmd and closeCmd toggle the command palette', async () => {
    const { useUiStore } = await import('../ui')
    const store = useUiStore()
    expect(store.cmdOpen).toBe(false)
    store.openCmd()
    expect(store.cmdOpen).toBe(true)
    store.closeCmd()
    expect(store.cmdOpen).toBe(false)
  })
})
