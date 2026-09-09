import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { nextTick, reactive } from 'vue'
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

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn().mockResolvedValue({ issues: [] }),
}))

vi.mock('@/stores/appUser', () => ({
  useAppUserStore: vi.fn(() => ({
    identity: '',
    select: vi.fn(),
    clear: vi.fn(),
  })),
  appUserLabel: (u: { name?: string | null; username?: string | null; identity?: string }) =>
    u.name || u.username || u.identity || '',
}))

import CommandPalette from '../CommandPalette.vue'
import { useUiStore } from '@/stores/ui'
import { useProjectsStore } from '@/stores/projects'
import { useAuthStore } from '@/stores/auth'
import { useAppUserStore } from '@/stores/appUser'
import { apiFetch } from '@/api/client'

function makeProject(id: string, name: string, slug = id): Project {
  return { id, name, public_key: id, slug, created_at: '', platform: 'javascript' } as Project
}

function makeWrapper(cmdOpen = true, projects: Project[] = []) {
  const closeCmd = vi.fn()
  const openCmd = vi.fn()

  vi.mocked(useUiStore).mockReturnValue(reactive({
    cmdOpen,
    closeCmd,
    openCmd,
    toggleTheme: vi.fn(),
    resolvedTheme: 'light',
    theme: null,
  }) as any)

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
  vi.mocked(apiFetch).mockReset()
  vi.mocked(apiFetch).mockResolvedValue({ issues: [] })
  vi.mocked(useAppUserStore).mockReturnValue({
    identity: '',
    select: vi.fn(),
    clear: vi.fn(),
  } as any)
})

afterEach(() => {
  vi.useRealTimers()
})

describe('CommandPalette', () => {
  it('cancels replaced searches, ignores late results, and cancels on close', async () => {
    vi.useFakeTimers()
    const pending: { signal: AbortSignal; resolve: (data: any) => void }[] = []
    vi.mocked(apiFetch).mockImplementation((_path, init) => new Promise(resolve => {
      pending.push({ signal: init!.signal as AbortSignal, resolve })
    }))
    const wrapper = makeWrapper()
    const input = wrapper.find('input[aria-label="Search"]')
    await input.setValue('first')
    await vi.advanceTimersByTimeAsync(200)
    expect(pending).toHaveLength(2)
    await input.setValue('second')
    expect(pending[0]!.signal.aborted).toBe(true)
    expect(pending[1]!.signal.aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(200)
    pending[2]!.resolve({ issues: [{ id: 'new', title: 'second result' }] })
    pending[3]!.resolve([])
    await flushPromises()
    pending[0]!.resolve({ issues: [{ id: 'old', title: 'obsolete result' }] })
    pending[1]!.resolve([])
    await flushPromises()
    expect(wrapper.text()).toContain('second result')
    expect(wrapper.text()).not.toContain('obsolete result')
    await input.setValue('third')
    await vi.advanceTimersByTimeAsync(200)
    useUiStore().cmdOpen = false
    await nextTick()
    expect(pending[4]!.signal.aborted).toBe(true)
    expect(pending[5]!.signal.aborted).toBe(true)
    wrapper.unmount()
  })

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
      expect(pushMock).toHaveBeenCalledWith({ path: '/issues', query: {} })
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
      expect(pushMock).toHaveBeenCalledWith({ path: '/performance', query: {} })
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
      expect(pushMock).toHaveBeenCalledWith({ path: '/issues', query: {} })
    })

    it('navigates to /performance on ⌘2', async () => {
      mountWithStore(false)
      document.dispatchEvent(new KeyboardEvent('keydown', { key: '2', metaKey: true }))
      await nextTick()
      expect(pushMock).toHaveBeenCalledWith({ path: '/performance', query: {} })
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

    it('keeps the selected user on ⌘1 and ⌘2', async () => {
      vi.mocked(useAppUserStore).mockReturnValue({
        identity: 'u-1',
        select: vi.fn(),
        clear: vi.fn(),
      } as any)
      mountWithStore(false)
      document.dispatchEvent(new KeyboardEvent('keydown', { key: '1', metaKey: true }))
      document.dispatchEvent(new KeyboardEvent('keydown', { key: '2', metaKey: true }))
      await nextTick()
      expect(pushMock).toHaveBeenCalledWith({ path: '/issues', query: { user: 'u-1' } })
      expect(pushMock).toHaveBeenCalledWith({ path: '/performance', query: { user: 'u-1' } })
    })
  })

  describe('people search', () => {
    const alice = {
      identity: 'u-1',
      user_id: 'u-1',
      username: 'alice',
      email: 'alice@example.com',
      name: 'Alice',
      last_seen: '2024-01-01T00:00:00Z',
      project_id: 'p1',
    }

    it('lists matching people and selects one', async () => {
      vi.useFakeTimers()
      const select = vi.fn()
      vi.mocked(useAppUserStore).mockReturnValue({
        identity: '',
        select,
        clear: vi.fn(),
      } as any)
      vi.mocked(apiFetch).mockImplementation(async (url: string) => {
        if (String(url).includes('/api/app-users')) return [alice]
        return { issues: [] }
      })
      const wrapper = makeWrapper(true)
      await wrapper.find('input[aria-label="Search"]').setValue('ali')
      await vi.advanceTimersByTimeAsync(200)
      await flushPromises()
      expect(wrapper.text()).toContain('People')
      expect(wrapper.text()).toContain('Alice')
      const person = wrapper.findAll('.cmdk__item').find((i) => i.find('.cmdk__item-text').text() === 'Alice')!
      await person.trigger('click')
      expect(select).toHaveBeenCalledWith(alice)
      expect(pushMock).toHaveBeenCalledWith({ path: '/issues', query: { user: 'u-1' } })
    })

    it('clears people results when the search is too short', async () => {
      vi.useFakeTimers()
      vi.mocked(apiFetch).mockImplementation(async (url: string) => {
        if (String(url).includes('/api/app-users')) return [alice]
        return { issues: [] }
      })
      const wrapper = makeWrapper(true)
      await wrapper.find('input[aria-label="Search"]').setValue('ali')
      await vi.advanceTimersByTimeAsync(200)
      await flushPromises()
      expect(wrapper.text()).toContain('Alice')
      await wrapper.find('input[aria-label="Search"]').setValue('a')
      await nextTick()
      expect(wrapper.text()).not.toContain('Alice')
    })

    it('swallows search errors', async () => {
      vi.useFakeTimers()
      vi.mocked(apiFetch).mockRejectedValue(new Error('nope'))
      const wrapper = makeWrapper(true)
      await wrapper.find('input[aria-label="Search"]').setValue('alice')
      await vi.advanceTimersByTimeAsync(200)
      await flushPromises()
      expect(wrapper.text()).toContain('No results')
    })

    it('keeps the selected user when jumping to Issues from the palette', async () => {
      vi.mocked(useAppUserStore).mockReturnValue({
        identity: 'u-1',
        select: vi.fn(),
        clear: vi.fn(),
      } as any)
      const wrapper = makeWrapper(true)
      const issues = wrapper.findAll('.cmdk__item').find(
        (i) => i.find('.cmdk__item-text').text() === 'Issues',
      )!
      await issues.trigger('click')
      expect(pushMock).toHaveBeenCalledWith({ path: '/issues', query: { user: 'u-1' } })
    })

    it('clears a pending people search on unmount', async () => {
      vi.useFakeTimers()
      const wrapper = makeWrapper(true)
      await wrapper.find('input[aria-label="Search"]').setValue('alice')
      wrapper.unmount()
      await vi.advanceTimersByTimeAsync(200)
      expect(apiFetch).not.toHaveBeenCalled()
    })
  })
})

describe('search context', () => {
  it('scopes both issue and people requests to the selected projects', async () => {
    vi.useFakeTimers()
    const wrapper = makeWrapper(true)
    useProjectsStore().selectedIds = ['project-a', 'project-b']
    await wrapper.find('input[aria-label="Search"]').setValue('alice')
    await vi.advanceTimersByTimeAsync(200)
    await flushPromises()
    const urls = vi.mocked(apiFetch).mock.calls.map(([path]) => new URL(path, 'http://localhost'))
    expect(urls.map(u => u.pathname)).toEqual(['/api/issues', '/api/app-users'])
    for (const url of urls) {
      expect(url.searchParams.getAll('project_id')).toEqual(['project-a', 'project-b'])
      expect(url.searchParams.get('q')).toBe('alice')
    }
    wrapper.unmount()
  })

  it('starts a fresh search when the palette reopens', async () => {
    vi.useFakeTimers()
    vi.mocked(apiFetch).mockImplementation(async (path: string) =>
      path.includes('/api/app-users') ? [{ identity: 'alice', name: 'Alice Picker' }] : { issues: [] },
    )
    const wrapper = makeWrapper(true)
    await wrapper.find('input[aria-label="Search"]').setValue('alice')
    await vi.advanceTimersByTimeAsync(200)
    await flushPromises()
    expect(wrapper.text()).toContain('Alice Picker')
    useUiStore().cmdOpen = false
    await nextTick()
    useUiStore().cmdOpen = true
    await flushPromises()
    expect((wrapper.find('input[aria-label="Search"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.text()).not.toContain('Alice Picker')
    expect(wrapper.text()).toContain('Performance')
    wrapper.unmount()
  })
})
