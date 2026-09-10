import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import ModalDialog from '../ModalDialog.vue'

afterEach(() => vi.restoreAllMocks())

describe('ModalDialog', () => {
  it('contains Tab in both directions and restores the invoking control', async () => {
    const trigger = document.createElement('button')
    document.body.append(trigger)
    trigger.focus()
    const show = vi.spyOn(HTMLDialogElement.prototype, 'showModal')
    vi.spyOn(HTMLElement.prototype, 'getClientRects').mockReturnValue([{}] as any)
    const wrapper = mount(ModalDialog, {
      attachTo: document.body,
      attrs: { 'aria-label': 'Test dialog' },
      slots: { default: '<button id="first">First</button><button disabled tabindex="0">Disabled</button><button hidden>Hidden</button><button style="visibility:hidden">Invisible</button><button tabindex="-1">Skipped</button><button id="last">Last</button>' },
    })
    expect(show).toHaveBeenCalledOnce()
    expect(wrapper.attributes('aria-modal')).toBe('true')
    const first = wrapper.get('#first').element as HTMLElement
    const last = wrapper.get('#last').element as HTMLElement
    first.focus()
    await wrapper.trigger('keydown', { key: 'Tab', shiftKey: true })
    expect(document.activeElement).toBe(last)
    await wrapper.trigger('keydown', { key: 'Tab' })
    expect(document.activeElement).toBe(first)
    await wrapper.trigger('keydown', { key: 'Tab' })
    expect(document.activeElement).toBe(last)
    await wrapper.trigger('keydown', { key: 'Tab', shiftKey: true })
    expect(document.activeElement).toBe(first)
    wrapper.unmount()
    expect(document.activeElement).toBe(trigger)
    trigger.remove()
  })

  it('focuses itself when there are no available controls and handles dismissal', async () => {
    const wrapper = mount(ModalDialog, { attachTo: document.body })
    await wrapper.trigger('keydown', { key: 'Tab' })
    expect(document.activeElement).toBe(wrapper.element)
    const background = vi.fn()
    document.addEventListener('keydown', background)
    await wrapper.trigger('keydown', { key: 'Escape' })
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(background).not.toHaveBeenCalled()
    await wrapper.trigger('cancel')
    expect(wrapper.emitted('close')).toHaveLength(2)
    await wrapper.trigger('keydown', { key: 'ArrowDown' })
    expect(wrapper.emitted('keydown')).toHaveLength(1)
    document.removeEventListener('keydown', background)
    wrapper.unmount()
  })

  it('does not restore focus to an opener removed while the dialog is open', () => {
    const trigger = document.createElement('button')
    document.body.append(trigger)
    trigger.focus()
    const wrapper = mount(ModalDialog, { attachTo: document.body })
    trigger.remove()
    const focus = vi.spyOn(trigger, 'focus')
    wrapper.unmount()
    expect(focus).not.toHaveBeenCalled()
  })
})
