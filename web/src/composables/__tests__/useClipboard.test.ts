import { describe, it, expect, vi, afterEach } from 'vitest'

const show = vi.fn()
vi.mock('@/composables/useToast', () => ({ useToast: () => ({ show }) }))

import { copyText, useClipboard } from '../useClipboard'

const originalClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard')

function setClipboard(value: unknown) {
  Object.defineProperty(navigator, 'clipboard', { value, configurable: true })
}

afterEach(() => {
  show.mockReset()
  if (originalClipboard) Object.defineProperty(navigator, 'clipboard', originalClipboard)
  else setClipboard(undefined)
})

describe('copyText', () => {
  it('uses the async clipboard API when available', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    setClipboard({ writeText })
    expect(await copyText('abc')).toBe(true)
    expect(writeText).toHaveBeenCalledWith('abc')
  })

  it('falls back to execCommand outside secure contexts', async () => {
    setClipboard(undefined)
    const exec = vi.fn().mockReturnValue(true)
    Object.defineProperty(document, 'execCommand', { value: exec, configurable: true })
    expect(await copyText('plain http')).toBe(true)
    expect(exec).toHaveBeenCalledWith('copy')
    expect(document.querySelector('textarea')).toBeNull()
  })

  it('reports failure when neither method works', async () => {
    setClipboard({ writeText: vi.fn().mockRejectedValue(new Error('denied')) })
    Object.defineProperty(document, 'execCommand', { value: vi.fn().mockReturnValue(false), configurable: true })
    expect(await copyText('x')).toBe(false)
  })
})

describe('useClipboard', () => {
  it('toasts success only when a message is given, and always toasts failure', async () => {
    const { copy } = useClipboard()
    setClipboard({ writeText: vi.fn().mockResolvedValue(undefined) })
    await copy('a', 'DSN copied')
    expect(show).toHaveBeenLastCalledWith('DSN copied')
    show.mockReset()
    await copy('a')
    expect(show).not.toHaveBeenCalled()

    setClipboard(undefined)
    Object.defineProperty(document, 'execCommand', { value: vi.fn().mockReturnValue(false), configurable: true })
    expect(await copy('a', 'DSN copied')).toBe(false)
    expect(show).toHaveBeenLastCalledWith(expect.stringContaining('Could not copy'), 'error')
  })
})
