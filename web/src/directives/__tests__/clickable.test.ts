import { describe, it, expect, vi } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { vClickable } from '../clickable'

function mountWith(template: string, onClick = vi.fn()) {
  const wrapper = mount(defineComponent({ directives: { clickable: vClickable }, setup: () => ({ onClick }), template }), { attachTo: document.body })
  return { wrapper, onClick }
}

describe('v-clickable', () => {
  it('makes an element a focusable button', () => {
    const { wrapper } = mountWith('<div v-clickable @click="onClick">Open</div>')
    expect(wrapper.attributes('role')).toBe('button')
    expect(wrapper.attributes('tabindex')).toBe('0')
  })

  it('clicks on Enter and Space but not on other keys', async () => {
    const { wrapper, onClick } = mountWith('<div v-clickable @click="onClick">Open</div>')
    await wrapper.trigger('keydown', { key: 'Enter' })
    await wrapper.trigger('keydown', { key: ' ' })
    await wrapper.trigger('keydown', { key: 'a' })
    expect(onClick).toHaveBeenCalledTimes(2)
  })

  it('leaves keys typed into inner controls alone', async () => {
    const { wrapper, onClick } = mountWith('<div v-clickable @click="onClick"><input /></div>')
    await wrapper.find('input').trigger('keydown', { key: 'Enter' })
    expect(onClick).not.toHaveBeenCalled()
  })

  it('keeps table row semantics and an explicit role', () => {
    const row = mountWith('<table><tbody><tr v-clickable @click="onClick"><td>x</td></tr></tbody></table>')
    expect(row.wrapper.find('tr').attributes('role')).toBeUndefined()
    expect(row.wrapper.find('tr').attributes('tabindex')).toBe('0')
    const option = mountWith('<div v-clickable role="option" @click="onClick">x</div>')
    expect(option.wrapper.attributes('role')).toBe('option')
  })
})
