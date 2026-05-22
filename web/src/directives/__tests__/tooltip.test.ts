import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { vTooltip } from '../tooltip'

function mount(text: string | undefined): HTMLButtonElement {
  const el = document.createElement('button')
  document.body.appendChild(el)
  vTooltip.mounted!(el, { value: text } as any, {} as any, {} as any)
  return el
}

function unmount(el: HTMLElement) {
  vTooltip.unmounted!(el, {} as any, {} as any, {} as any)
  el.remove()
}

function tooltip(): Element | null {
  return document.querySelector('.v-tooltip')
}

beforeEach(() => {
  // Reset visibility before each test
  tooltip()?.classList.remove('v-tooltip--visible')
})

afterEach(() => {
  tooltip()?.classList.remove('v-tooltip--visible')
})

describe('vTooltip directive', () => {
  it('mounts without throwing', () => {
    const el = document.createElement('button')
    document.body.appendChild(el)
    expect(() =>
      vTooltip.mounted!(el, { value: 'hello' } as any, {} as any, {} as any)
    ).not.toThrow()
    unmount(el)
  })

  it('creates the singleton tooltip element on first use', () => {
    const el = mount('test')
    expect(document.querySelector('.v-tooltip')).not.toBeNull()
    unmount(el)
  })

  it('shows tooltip with correct text on mouseenter', () => {
    const el = mount('Hello world')
    el.dispatchEvent(new MouseEvent('mouseenter'))
    expect(tooltip()?.classList.contains('v-tooltip--visible')).toBe(true)
    expect(tooltip()?.textContent).toBe('Hello world')
    unmount(el)
  })

  it('hides tooltip on mouseleave', () => {
    const el = mount('Hover me')
    el.dispatchEvent(new MouseEvent('mouseenter'))
    el.dispatchEvent(new MouseEvent('mouseleave'))
    expect(tooltip()?.classList.contains('v-tooltip--visible')).toBe(false)
    unmount(el)
  })

  it('shows tooltip on focus', () => {
    const el = mount('Focus me')
    el.dispatchEvent(new FocusEvent('focus'))
    expect(tooltip()?.classList.contains('v-tooltip--visible')).toBe(true)
    unmount(el)
  })

  it('hides tooltip on blur', () => {
    const el = mount('Focus me')
    el.dispatchEvent(new FocusEvent('focus'))
    el.dispatchEvent(new FocusEvent('blur'))
    expect(tooltip()?.classList.contains('v-tooltip--visible')).toBe(false)
    unmount(el)
  })

  it('does not show tooltip when value is empty string', () => {
    const el = mount('')
    el.dispatchEvent(new MouseEvent('mouseenter'))
    expect(tooltip()?.classList.contains('v-tooltip--visible')).toBe(false)
    unmount(el)
  })

  it('does not show tooltip when value is undefined', () => {
    const el = mount(undefined)
    el.dispatchEvent(new MouseEvent('mouseenter'))
    expect(tooltip()?.classList.contains('v-tooltip--visible')).toBe(false)
    unmount(el)
  })

  it('removes event listeners on unmount so tooltip no longer shows', () => {
    const el = mount('gone')
    unmount(el)
    tooltip()?.classList.remove('v-tooltip--visible')
    el.dispatchEvent(new MouseEvent('mouseenter'))
    expect(tooltip()?.classList.contains('v-tooltip--visible')).toBe(false)
  })

  it('updates tooltip text when binding value changes', () => {
    const el = mount('first')
    vTooltip.updated!(el, { value: 'updated' } as any, {} as any, {} as any)
    el.dispatchEvent(new MouseEvent('mouseenter'))
    expect(tooltip()?.textContent).toBe('updated')
    unmount(el)
  })

  it('hides tooltip on document scroll', () => {
    const el = mount('scroll test')
    el.dispatchEvent(new MouseEvent('mouseenter'))
    expect(tooltip()?.classList.contains('v-tooltip--visible')).toBe(true)
    document.dispatchEvent(new Event('scroll'))
    expect(tooltip()?.classList.contains('v-tooltip--visible')).toBe(false)
    unmount(el)
  })
})
