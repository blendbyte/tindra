import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref } from 'vue'
import { useDebounced } from '../useDebounced'

afterEach(() => vi.useRealTimers())

describe('useDebounced', () => {
  it('updates only after the source stops changing', async () => {
    vi.useFakeTimers()
    const scope = effectScope()
    const source = ref('')
    const debounced = scope.run(() => useDebounced(source, 300))!
    for (const value of ['a', 'ab', 'abc']) {
      source.value = value
      await nextTick()
      vi.advanceTimersByTime(100)
    }
    expect(debounced.value).toBe('')
    vi.advanceTimersByTime(300)
    expect(debounced.value).toBe('abc')
    scope.stop()
  })

  it('drops a pending update when its scope is disposed', async () => {
    vi.useFakeTimers()
    const scope = effectScope()
    const source = ref('start')
    const debounced = scope.run(() => useDebounced(source, 300))!
    source.value = 'changed'
    await nextTick()
    scope.stop()
    vi.advanceTimersByTime(1000)
    expect(debounced.value).toBe('start')
  })
})
