import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { nextTick } from 'vue'

// Same Node 22+ localStorage workaround as ui.test.ts.
const storage: Record<string, string> = {}
const mockLocalStorage: Storage = {
  getItem: (key) => storage[key] ?? null,
  setItem: (key, val) => { storage[key] = val },
  removeItem: (key) => { delete storage[key] },
  clear: () => { Object.keys(storage).forEach((k) => delete storage[k]) },
  get length() { return Object.keys(storage).length },
  key: (i) => Object.keys(storage)[i] ?? null,
}

describe('performance store', () => {
  beforeEach(() => {
    mockLocalStorage.clear()
    vi.stubGlobal('localStorage', mockLocalStorage)
    setActivePinia(createPinia())
    vi.resetModules()
  })

  it('defaults to 24h window and All env when localStorage is empty', async () => {
    const { usePerformanceStore } = await import('../performance')
    const store = usePerformanceStore()
    expect(store.windowHrs).toBe('24h')
    expect(store.envFilter).toBe('All')
  })

  describe('windowHrs hydration', () => {
    it.each(['1h', '24h', '7d', '30d'])('accepts valid stored window "%s"', async (val) => {
      mockLocalStorage.setItem('tindra:perf:window', val)
      const { usePerformanceStore } = await import('../performance')
      const store = usePerformanceStore()
      expect(store.windowHrs).toBe(val)
    })

    it('falls back to 24h for an invalid stored window', async () => {
      mockLocalStorage.setItem('tindra:perf:window', 'bad')
      const { usePerformanceStore } = await import('../performance')
      const store = usePerformanceStore()
      expect(store.windowHrs).toBe('24h')
    })
  })

  describe('envFilter hydration', () => {
    it.each(['All', 'production', 'staging', 'development'])('accepts valid stored env "%s"', async (val) => {
      mockLocalStorage.setItem('tindra:perf:env', val)
      const { usePerformanceStore } = await import('../performance')
      const store = usePerformanceStore()
      expect(store.envFilter).toBe(val)
    })

    it('falls back to All for an invalid stored env', async () => {
      mockLocalStorage.setItem('tindra:perf:env', 'prod')
      const { usePerformanceStore } = await import('../performance')
      const store = usePerformanceStore()
      expect(store.envFilter).toBe('All')
    })
  })

  describe('windowHrs persistence', () => {
    it('writes to localStorage when changed to a non-default value', async () => {
      const { usePerformanceStore } = await import('../performance')
      const store = usePerformanceStore()
      store.windowHrs = '7d'
      await nextTick()
      expect(mockLocalStorage.getItem('tindra:perf:window')).toBe('7d')
    })

    it('removes the key from localStorage when reset to the default (24h)', async () => {
      mockLocalStorage.setItem('tindra:perf:window', '7d')
      const { usePerformanceStore } = await import('../performance')
      const store = usePerformanceStore()
      store.windowHrs = '24h'
      await nextTick()
      expect(mockLocalStorage.getItem('tindra:perf:window')).toBeNull()
    })
  })

  describe('envFilter persistence', () => {
    it('writes to localStorage when changed to a non-default value', async () => {
      const { usePerformanceStore } = await import('../performance')
      const store = usePerformanceStore()
      store.envFilter = 'staging'
      await nextTick()
      expect(mockLocalStorage.getItem('tindra:perf:env')).toBe('staging')
    })

    it('removes the key from localStorage when reset to the default (All)', async () => {
      mockLocalStorage.setItem('tindra:perf:env', 'staging')
      const { usePerformanceStore } = await import('../performance')
      const store = usePerformanceStore()
      store.envFilter = 'All'
      await nextTick()
      expect(mockLocalStorage.getItem('tindra:perf:env')).toBeNull()
    })
  })
})
