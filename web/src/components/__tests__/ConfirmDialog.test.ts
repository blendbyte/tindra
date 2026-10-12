import { describe, it, expect, afterEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ConfirmDialog from '../ConfirmDialog.vue'
import { useConfirm } from '@/composables/useConfirm'

afterEach(() => {
  useConfirm().answer(false)
  document.body.innerHTML = ''
})

function mountDialog() {
  return mount(ConfirmDialog, { attachTo: document.body })
}

function button(label: string) {
  return [...document.querySelectorAll<HTMLButtonElement>('.confirm-dialog button')].find(b => b.textContent?.trim() === label)!
}

describe('ConfirmDialog', () => {
  it('stays hidden until something asks a question', () => {
    mountDialog()
    expect(document.querySelector('.confirm-dialog')).toBeNull()
  })

  it('shows the question and resolves true when confirmed', async () => {
    mountDialog()
    const answer = useConfirm().confirm({ title: 'Delete monitor "Nightly"?', message: 'This cannot be undone.', confirmLabel: 'Delete monitor', danger: true })
    await flushPromises()
    expect(document.querySelector('#confirm-dialog-title')?.textContent).toBe('Delete monitor "Nightly"?')
    expect(document.querySelector('.confirm-dialog__message')?.textContent).toBe('This cannot be undone.')
    expect(button('Delete monitor').classList).toContain('btn--danger')
    button('Delete monitor').click()
    expect(await answer).toBe(true)
    await flushPromises()
    expect(document.querySelector('.confirm-dialog')).toBeNull()
  })

  it('resolves false on cancel and on Escape', async () => {
    mountDialog()
    const cancelled = useConfirm().confirm({ title: 'Delete this comment?' })
    await flushPromises()
    expect(button('Confirm').classList).toContain('btn--primary')
    button('Cancel').click()
    expect(await cancelled).toBe(false)

    const escaped = useConfirm().confirm({ title: 'Delete this comment?' })
    await flushPromises()
    document.querySelector('dialog')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(await escaped).toBe(false)
  })

  it('treats a replaced question as cancelled', async () => {
    const { confirm, answer } = useConfirm()
    const first = confirm({ title: 'First?' })
    const second = confirm({ title: 'Second?' })
    expect(await first).toBe(false)
    answer(true)
    expect(await second).toBe(true)
  })
})
