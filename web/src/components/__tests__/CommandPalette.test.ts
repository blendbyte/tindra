import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import type { Project } from '@/api/types'

const pushMock = vi.fn()

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: pushMock })),
}))

vi.mock('@/stores/ui', () => ({
  useUiStore: vi.fn(),
}))

vi.mock('@/stores/projects', () => ({
  useProjectsStore: vi.fn(),
}))

vi.mock('@/stores/issueNav', () => ({
  useIssueNavStore: vi.fn(() => ({ set: vi.fn() })),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: vi.fn(),
}))

import CommandPalette from '../CommandPalette.vue'
import { useUiStore } from '@/stores/ui'
import { useProjectsStore } from '@/stores/projects'
import { useAuthStore } from '@/stores/auth'

function makeProject(id: string, name: string, slug = id): Project {
  return { id, name, public_key: id, slug, created_at: '', platform: 'javascript' } as Project
}

function makeWrapper(cmdOpen = true, projects: Project[] = []) {
  const closeCmd = vi.fn()
  const openCmd = vi.fn()

  vi.mocked(useUiStore).mockReturnValue({
    cmdOpen,
    closeCmd,
    openCmd,
    toggleTheme: vi.fn(),
    resolvedTheme: 'light',
    theme: null,
  } as any)

  vi.mocked(useProjectsStore).mockReturnValue({
    projects,
    selectedIds: [],
    setSelected: vi.fn(),
    toggleProject: vi.fn(),
  } as any)

  return mount(CommandPalette, {
    global: {
      stubs: {
        Teleport: { template: '<div><slot /></div>' },
        Icon: { template: '<span />' },
      },
    },
  })
}

beforeEach(() => {
  pushMock.mockReset()
  vi.mocked(useUiStore).mockReset()
  vi.mocked(useProjectsStore).mockReset()
  vi.mocked(useAuthStore).mockReset()
  vi.mocked(useAuthStore).mockReturnValue({ user: { timezone: 'UTC' }, setUser: vi.fn() } as any)
})

describe('CommandPalette', () => {
  describe('visibility', () => {
    it('is not rendered when cmdOpen is false', () => {
      const wrapper = makeWrapper(false)
      expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    })

    it('is rendered when cmdOpen is true', () => {
      const wrapper = makeWrapper(true)
      expect(wrapper.find('[role="dialog"]').exists()).toBe(true)
    })

    it('has an accessible dialog label', () => {
      const wrapper = makeWrapper(true)
      expect(wrapper.find('[role="dialog"]').attributes('aria-label')).toBe('Command palette')
    })
  })

  describe('navigation items', () => {
    it('shows all four navigation items by default', () => {
      const wrapper = makeWrapper(true)
      const labels = wrapper.findAll('.cmdk__item').map((i) => i.find('.cmdk__item-text').text())
      expect(labels).toContain('Issues')
      expect(labels).toContain('Performance')
      expect(labels).toContain('Releases')
      expect(labels).toContain('Settings')
    })

    it('shows a "Show all projects" item', () => {
      const wrapper = makeWrapper(true)
      const labels = wrapper.findAll('.cmdk__item').map((i) => i.find('.cmdk__item-text').text())
      expect(labels).toContain('Show all projects')
    })

    it('shows filter items for each project', () => {
      const wrapper = makeWrapper(true, [makeProject('1', 'Alpha'), makeProject('2', 'Beta')])
      const labels = wrapper.findAll('.cmdk__item').map((i) => i.find('.cmdk__item-text').text())
      expect(labels).toContain('Alpha')
      expect(labels).toContain('Beta')
    })
  })

  describe('search filtering', () => {
    it('filters items to matching results', async () => {
      const wrapper = makeWrapper(true)
      await wrapper.find('input[aria-label="Search"]').setValue('Issues')
      await nextTick()
      const items = wrapper.findAll('.cmdk__item')
      expect(items).toHaveLength(1)
      expect(items[0].find('.cmdk__item-text').text()).toBe('Issues')
    })

    it('shows a no-results message when nothing matches', async () => {
      const wrapper = makeWrapper(true)
      // Single character to avoid triggering async issue search (isSearching stays false)
      await wrapper.find('input[aria-label="Search"]').setValue('z')
      await nextTick()
      expect(wrapper.find('.cmdk__item').exists()).toBe(false)
      expect(wrapper.find('.cmdk__empty').text()).toContain('No results')
    })

    it('restores all items when the search is cleared', async () => {
      const wrapper = makeWrapper(true)
      const input = wrapper.find('input[aria-label="Search"]')
      await input.setValue('Issues')
      await input.setValue('')
      await nextTick()
      expect(wrapper.findAll('.cmdk__item').length).toBeGreaterThan(1)
    })
  })

  describe('keyboard navigation', () => {
    it('highlights the first item by default', () => {
      const wrapper = makeWrapper(true)
      expect(wrapper.findAll('.cmdk__item')[0].classes()).toContain('cmdk__item--active')
    })

    it('moves active index down on ArrowDown', async () => {
      const wrapper = makeWrapper(true)
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown' }))
      await nextTick()
      expect(wrapper.findAll('.cmdk__item')[0].classes()).not.toContain('cmdk__item--active')
      expect(wrapper.findAll('.cmdk__item')[1].classes()).toContain('cmdk__item--active')
    })

    it('moves active index up on ArrowUp after moving down twice', async () => {
      const wrapper = makeWrapper(true)
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown' }))
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown' }))
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp' }))
      await nextTick()
      expect(wrapper.findAll('.cmdk__item')[1].classes()).toContain('cmdk__item--active')
    })

    it('does not go below index 0 with ArrowUp at the top', async () => {
      const wrapper = makeWrapper(true)
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp' }))
      await nextTick()
      expect(wrapper.findAll('.cmdk__item')[0].classes()).toContain('cmdk__item--active')
    })

    it('executes the active item on Enter (first item navigates to /issues)', async () => {
      makeWrapper(true)
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }))
      await nextTick()
      expect(pushMock).toHaveBeenCalledWith('/issues')
    })

    it('calls closeCmd when Escape is pressed', async () => {
      const closeCmd = vi.fn()
      vi.mocked(useUiStore).mockReturnValue({
        cmdOpen: true,
        closeCmd,
        openCmd: vi.fn(),
        toggleTheme: vi.fn(),
        resolvedTheme: 'light',
        theme: null,
      } as any)
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [],
        selectedIds: [],
        setSelected: vi.fn(),
        toggleProject: vi.fn(),
      } as any)
      mount(CommandPalette, {
        global: {
          stubs: { Teleport: { template: '<div><slot /></div>' }, Icon: { template: '<span />' } },
        },
      })
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
      await nextTick()
      expect(closeCmd).toHaveBeenCalled()
    })
  })

  describe('mouse interaction', () => {
    it('updates active index on mouseenter', async () => {
      const wrapper = makeWrapper(true)
      await wrapper.findAll('.cmdk__item')[2].trigger('mouseenter')
      await nextTick()
      expect(wrapper.findAll('.cmdk__item')[2].classes()).toContain('cmdk__item--active')
    })

    it('navigates to /performance when that item is clicked', async () => {
      const wrapper = makeWrapper(true)
      const perfItem = wrapper.findAll('.cmdk__item').find(
        (i) => i.find('.cmdk__item-text').text() === 'Performance',
      )!
      await perfItem.trigger('click')
      expect(pushMock).toHaveBeenCalledWith('/performance')
    })

    it('calls closeCmd when the overlay backdrop is clicked', async () => {
      const closeCmd = vi.fn()
      vi.mocked(useUiStore).mockReturnValue({
        cmdOpen: true,
        closeCmd,
        openCmd: vi.fn(),
        toggleTheme: vi.fn(),
        resolvedTheme: 'light',
        theme: null,
      } as any)
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [],
        selectedIds: [],
        setSelected: vi.fn(),
        toggleProject: vi.fn(),
      } as any)
      const wrapper = mount(CommandPalette, {
        global: {
          stubs: { Teleport: { template: '<div><slot /></div>' }, Icon: { template: '<span />' } },
        },
      })
      await wrapper.find('.cmdk-overlay').trigger('mousedown')
      expect(closeCmd).toHaveBeenCalled()
    })
  })

  describe('global keyboard shortcuts', () => {
    function mountWithStore(cmdOpen: boolean) {
      const openCmd = vi.fn()
      const closeCmd = vi.fn()
      vi.mocked(useUiStore).mockReturnValue({
        cmdOpen,
        openCmd,
        closeCmd,
        toggleTheme: vi.fn(),
        resolvedTheme: 'light',
        theme: null,
      } as any)
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [],
        selectedIds: [],
        setSelected: vi.fn(),
        toggleProject: vi.fn(),
      } as any)
      mount(CommandPalette, {
        global: {
          stubs: { Teleport: { template: '<div><slot /></div>' }, Icon: { template: '<span />' } },
        },
      })
      return { openCmd, closeCmd }
    }

    it('calls openCmd on ⌘K when palette is closed', async () => {
      const { openCmd } = mountWithStore(false)
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true }))
      await nextTick()
      expect(openCmd).toHaveBeenCalled()
    })

    it('calls closeCmd on ⌘K when palette is open', async () => {
      const { closeCmd } = mountWithStore(true)
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true }))
      await nextTick()
      expect(closeCmd).toHaveBeenCalled()
    })

    it('navigates to /issues on ⌘1', async () => {
      mountWithStore(false)
      document.dispatchEvent(new KeyboardEvent('keydown', { key: '1', metaKey: true }))
      await nextTick()
      expect(pushMock).toHaveBeenCalledWith('/issues')
    })

    it('navigates to /performance on ⌘2', async () => {
      mountWithStore(false)
      document.dispatchEvent(new KeyboardEvent('keydown', { key: '2', metaKey: true }))
      await nextTick()
      expect(pushMock).toHaveBeenCalledWith('/performance')
    })

    it('navigates to /releases on ⌘3', async () => {
      mountWithStore(false)
      document.dispatchEvent(new KeyboardEvent('keydown', { key: '3', metaKey: true }))
      await nextTick()
      expect(pushMock).toHaveBeenCalledWith('/releases')
    })

    it('navigates to /settings on ⌘,', async () => {
      mountWithStore(false)
      document.dispatchEvent(new KeyboardEvent('keydown', { key: ',', metaKey: true }))
      await nextTick()
      expect(pushMock).toHaveBeenCalledWith('/settings')
    })
  })
})
