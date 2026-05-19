import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'

vi.mock('@/stores/ui', () => ({
  useUiStore: vi.fn(),
}))

import ShortcutsModal from '../ShortcutsModal.vue'
import { useUiStore } from '@/stores/ui'

function makeWrapper(shortcutsOpen: boolean) {
  const ui = { shortcutsOpen }
  vi.mocked(useUiStore).mockReturnValue(ui as any)
  return mount(ShortcutsModal, {
    global: {
      stubs: { Teleport: { template: '<div><slot /></div>' } },
    },
    attachTo: document.body,
  })
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.mocked(useUiStore).mockReset()
})

describe('ShortcutsModal', () => {
  describe('visibility', () => {
    it('renders nothing when shortcutsOpen is false', () => {
      const wrapper = makeWrapper(false)
      expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    })

    it('renders the modal when shortcutsOpen is true', () => {
      const wrapper = makeWrapper(true)
      expect(wrapper.find('[role="dialog"]').exists()).toBe(true)
    })
  })

  describe('close button', () => {
    it('sets shortcutsOpen to false when the close button is clicked', async () => {
      const ui = { shortcutsOpen: true }
      vi.mocked(useUiStore).mockReturnValue(ui as any)
      const wrapper = mount(ShortcutsModal, {
        global: { stubs: { Teleport: { template: '<div><slot /></div>' } } },
      })
      await wrapper.find('.shortcuts-modal__close').trigger('click')
      expect(ui.shortcutsOpen).toBe(false)
    })
  })

  describe('keyboard dismissal', () => {
    it('closes on Escape key', async () => {
      const ui = { shortcutsOpen: true }
      vi.mocked(useUiStore).mockReturnValue(ui as any)
      mount(ShortcutsModal, {
        global: { stubs: { Teleport: { template: '<div><slot /></div>' } } },
        attachTo: document.body,
      })
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
      expect(ui.shortcutsOpen).toBe(false)
    })

    it('closes on ? key', async () => {
      const ui = { shortcutsOpen: true }
      vi.mocked(useUiStore).mockReturnValue(ui as any)
      mount(ShortcutsModal, {
        global: { stubs: { Teleport: { template: '<div><slot /></div>' } } },
        attachTo: document.body,
      })
      document.dispatchEvent(new KeyboardEvent('keydown', { key: '?' }))
      expect(ui.shortcutsOpen).toBe(false)
    })

    it('does not close for other keys', () => {
      const ui = { shortcutsOpen: true }
      vi.mocked(useUiStore).mockReturnValue(ui as any)
      mount(ShortcutsModal, {
        global: { stubs: { Teleport: { template: '<div><slot /></div>' } } },
        attachTo: document.body,
      })
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }))
      expect(ui.shortcutsOpen).toBe(true)
    })

    it('does not fire when shortcutsOpen is false', () => {
      const ui = { shortcutsOpen: false }
      vi.mocked(useUiStore).mockReturnValue(ui as any)
      mount(ShortcutsModal, {
        global: { stubs: { Teleport: { template: '<div><slot /></div>' } } },
        attachTo: document.body,
      })
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
      expect(ui.shortcutsOpen).toBe(false)
    })
  })

  describe('overlay click', () => {
    it('closes when clicking the overlay backdrop', async () => {
      const ui = { shortcutsOpen: true }
      vi.mocked(useUiStore).mockReturnValue(ui as any)
      const wrapper = mount(ShortcutsModal, {
        global: { stubs: { Teleport: { template: '<div><slot /></div>' } } },
        attachTo: document.body,
      })
      await wrapper.find('.shortcuts-overlay').trigger('mousedown')
      expect(ui.shortcutsOpen).toBe(false)
    })
  })

  describe('content', () => {
    it('renders all five shortcut groups', () => {
      const wrapper = makeWrapper(true)
      const groups = wrapper.findAll('.shortcuts-group__title').map((g) => g.text())
      expect(groups).toEqual([
        'Global',
        'Issue list',
        'Issue detail',
        'Transaction detail',
        'Transaction samples',
      ])
    })

    it('renders the command palette shortcut', () => {
      const wrapper = makeWrapper(true)
      const labels = wrapper.findAll('.shortcuts-row__label').map((l) => l.text())
      expect(labels).toContain('Command palette')
    })

    it('renders keyboard hint keys', () => {
      const wrapper = makeWrapper(true)
      const keys = wrapper.findAll('.shortcut-kbd').map((k) => k.text())
      expect(keys).toContain('⌘K')
      expect(keys).toContain('J')
      expect(keys).toContain('K')
    })

    it('has an accessible dialog label', () => {
      const wrapper = makeWrapper(true)
      expect(wrapper.find('[role="dialog"]').attributes('aria-label')).toBe('Keyboard shortcuts')
    })
  })
})
