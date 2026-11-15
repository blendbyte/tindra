import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import RecipientPicker from '../RecipientPicker.vue'

const users = [
  { id: 'u1', name: 'Mara', email: 'mara@example.com' },
  { id: 'u2', name: '', email: 'ops@example.com' },
] as any[]

describe('RecipientPicker', () => {
  it('lists users and updates the selection', async () => {
    const w = mount(RecipientPicker, { props: { users, modelValue: ['u1'], 'onUpdate:modelValue': (v: string[]) => w.setProps({ modelValue: v }) } })
    const boxes = w.findAll<HTMLInputElement>('input[type="checkbox"]')
    expect(w.findAll('label.rule__project-check').map(l => l.text())).toEqual(['Mara', 'ops@example.com'])
    expect(boxes.map(b => b.element.checked)).toEqual([true, false])
    await boxes[1]!.setValue(true)
    expect(w.props('modelValue')).toEqual(['u1', 'u2'])
  })

  it('shows selected people the user list does not have, so they can be cleared', async () => {
    const w = mount(RecipientPicker, { props: { users: [], modelValue: ['gone'], 'onUpdate:modelValue': (v: string[]) => w.setProps({ modelValue: v }) } })
    const label = w.find('label.rule__project-check')
    expect(label.text()).toBe('Unknown user')
    await label.find('input').setValue(false)
    expect(w.props('modelValue')).toEqual([])
  })
})
