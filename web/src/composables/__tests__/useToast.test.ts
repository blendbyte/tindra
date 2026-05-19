import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useToast } from '../useToast'

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.runAllTimers()
  vi.useRealTimers()
  // clear module-level state between tests
  const { toasts } = useToast()
  toasts.value = []
})

describe('detectType (via show)', () => {
  it.each([
    ['Upload failed', 'error'],
    ['An error occurred', 'error'],
    ['Please try again', 'error'],
    ['Could not connect', 'error'],
    ['Incorrect password', 'error'],
    ['Invalid token', 'error'],
    ['Copied to clipboard', 'success'],
    ['Changes saved', 'success'],
    ['Project created', 'success'],
    ['Invite sent', 'success'],
    ['Token removed', 'success'],
    ['Token revoked', 'success'],
    ['Password changed', 'success'],
    ['Settings updated', 'success'],
    ['Alerts enabled', 'success'],
    ['Alerts disabled', 'success'],
    ['Sourcemap uploaded', 'success'],
    ['DSN set', 'success'],
    ['Loading…', 'info'],
    ['Check your inbox', 'info'],
  ])('classifies "%s" as %s', (message, expectedType) => {
    const { toasts, show } = useToast()
    show(message)
    expect(toasts.value[0].type).toBe(expectedType)
  })
})

describe('show', () => {
  it('adds a toast with the given message', () => {
    const { toasts, show } = useToast()
    show('Hello world', 'info')
    expect(toasts.value).toHaveLength(1)
    expect(toasts.value[0].message).toBe('Hello world')
  })

  it('accepts an explicit type as second arg', () => {
    const { toasts, show } = useToast()
    show('Something happened', 'error')
    expect(toasts.value[0].type).toBe('error')
  })

  it('accepts an undo callback as second arg and auto-detects type', () => {
    const undo = vi.fn()
    const { toasts, show } = useToast()
    show('Project created', undo)
    expect(toasts.value[0].onUndo).toBe(undo)
    expect(toasts.value[0].type).toBe('success')
  })

  it('accepts type and undo callback', () => {
    const undo = vi.fn()
    const { toasts, show } = useToast()
    show('Something', 'info', undo)
    expect(toasts.value[0].type).toBe('info')
    expect(toasts.value[0].onUndo).toBe(undo)
  })

  it('auto-dismisses after 4 seconds', () => {
    const { toasts, show } = useToast()
    show('Temporary', 'info')
    expect(toasts.value).toHaveLength(1)
    vi.advanceTimersByTime(4000)
    // leaving flag set, then removed after 180ms
    vi.advanceTimersByTime(180)
    expect(toasts.value).toHaveLength(0)
  })
})

describe('dismiss', () => {
  it('sets the leaving flag immediately', () => {
    const { toasts, show, dismiss } = useToast()
    show('Hello', 'info')
    const id = toasts.value[0].id
    dismiss(id)
    expect(toasts.value[0].leaving).toBe(true)
  })

  it('removes the toast after 180ms', () => {
    const { toasts, show, dismiss } = useToast()
    show('Hello', 'info')
    const id = toasts.value[0].id
    dismiss(id)
    vi.advanceTimersByTime(180)
    expect(toasts.value).toHaveLength(0)
  })

  it('is a no-op for unknown ids', () => {
    const { toasts, show, dismiss } = useToast()
    show('Hello', 'info')
    dismiss(999)
    expect(toasts.value).toHaveLength(1)
  })
})
