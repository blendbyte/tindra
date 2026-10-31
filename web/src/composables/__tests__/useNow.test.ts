import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope } from 'vue'
import { useNow } from '../useNow'

afterEach(() => vi.useRealTimers())

describe('useNow', () => {
  it('ticks while used and stops when the last user goes away', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-31T12:00:00Z'))
    const a = effectScope(), b = effectScope()
    const now = a.run(() => useNow(1000))!
    b.run(() => useNow(1000))
    expect(now.value).toBe(Date.parse('2026-10-31T12:00:00Z'))
    vi.advanceTimersByTime(3000)
    expect(now.value).toBe(Date.parse('2026-10-31T12:00:03Z'))
    a.stop()
    vi.advanceTimersByTime(1000)
    expect(now.value).toBe(Date.parse('2026-10-31T12:00:04Z'))
    b.stop()
    vi.advanceTimersByTime(5000)
    expect(now.value).toBe(Date.parse('2026-10-31T12:00:04Z'))
    expect(vi.getTimerCount()).toBe(0)
  })
})
