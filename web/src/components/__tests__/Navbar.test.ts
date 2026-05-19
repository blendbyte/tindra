import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import type { Project } from '@/api/types'

const pushMock = vi.fn()
const routePath = { path: '/issues' }

// Stub that passes attrs through so aria-current is testable
const globalStubsWithAttrs = {
  stubs: {
    RouterLink: { template: '<a v-bind="$attrs"><slot /></a>', inheritAttrs: false },
    BrandMark: { template: '<span />' },
    Icon: { template: '<span />' },
  },
}

vi.mock('vue-router', () => ({
  useRoute: vi.fn(() => routePath),
  useRouter: vi.fn(() => ({ push: pushMock })),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn().mockResolvedValue(undefined),
}))

vi.mock('@/stores/ui', () => ({
  useUiStore: vi.fn(),
}))

vi.mock('@/stores/projects', () => ({
  useProjectsStore: vi.fn(),
}))

import Navbar from '../Navbar.vue'
import { useUiStore } from '@/stores/ui'
import { useProjectsStore } from '@/stores/projects'
import { apiFetch } from '@/api/client'

function makeProject(id: string, name: string, slug = id): Project {
  return { id, name, public_key: id, slug, created_at: '', platform: 'javascript' } as Project
}

const globalStubs = {
  stubs: {
    RouterLink: { template: '<a><slot /></a>' },
    BrandMark: { template: '<span />' },
    Icon: { template: '<span />' },
  },
}

function makeWrapper(projects: Project[] = [], selectedIds: string[] = []) {
  const toggleProject = vi.fn()
  const setSelected = vi.fn()
  const toggleTheme = vi.fn()
  const openCmd = vi.fn()

  vi.mocked(useUiStore).mockReturnValue({
    cmdOpen: false,
    theme: null,
    resolvedTheme: 'light',
    toggleTheme,
    openCmd,
    closeCmd: vi.fn(),
  } as any)

  vi.mocked(useProjectsStore).mockReturnValue({
    projects,
    selectedIds,
    setSelected,
    toggleProject,
  } as any)

  return mount(Navbar, { global: globalStubs })
}

beforeEach(() => {
  routePath.path = '/issues'
  pushMock.mockReset()
  vi.mocked(useUiStore).mockReset()
  vi.mocked(useProjectsStore).mockReset()
  vi.mocked(apiFetch).mockResolvedValue(undefined)
})

describe('Navbar', () => {
  describe('filterLabel computed', () => {
    it('shows "No projects" when the project list is empty', () => {
      const wrapper = makeWrapper([], [])
      expect(wrapper.find('.nav__projects-trigger').text()).toContain('No projects')
    })

    it('shows "All projects" when no ids are selected', () => {
      const wrapper = makeWrapper([makeProject('1', 'Alpha')], [])
      expect(wrapper.find('.nav__projects-trigger').text()).toContain('All projects')
    })

    it('shows "All projects" when all project ids are selected', () => {
      const wrapper = makeWrapper(
        [makeProject('1', 'Alpha'), makeProject('2', 'Beta')],
        ['1', '2'],
      )
      expect(wrapper.find('.nav__projects-trigger').text()).toContain('All projects')
    })

    it('shows the project name when exactly one id is selected', () => {
      const wrapper = makeWrapper(
        [makeProject('1', 'Alpha'), makeProject('2', 'Beta')],
        ['2'],
      )
      expect(wrapper.find('.nav__projects-trigger').text()).toContain('Beta')
    })

    it('shows "N projects" when multiple but not all ids are selected', () => {
      const wrapper = makeWrapper(
        [makeProject('1', 'A'), makeProject('2', 'B'), makeProject('3', 'C')],
        ['1', '2'],
      )
      expect(wrapper.find('.nav__projects-trigger').text()).toContain('2 projects')
    })
  })

  describe('project filter popover', () => {
    it('opens the popover on trigger click', async () => {
      const wrapper = makeWrapper([makeProject('1', 'Alpha')])
      expect(wrapper.find('.popover').exists()).toBe(false)
      await wrapper.find('.nav__projects-trigger').trigger('click')
      expect(wrapper.find('.popover').exists()).toBe(true)
    })

    it('lists all projects in the popover', async () => {
      const wrapper = makeWrapper([makeProject('1', 'Alpha'), makeProject('2', 'Beta')])
      await wrapper.find('.nav__projects-trigger').trigger('click')
      const text = wrapper.find('.popover__list').text()
      expect(text).toContain('Alpha')
      expect(text).toContain('Beta')
    })

    it('shows the empty state when there are no projects', async () => {
      const wrapper = makeWrapper([])
      await wrapper.find('.nav__projects-trigger').trigger('click')
      expect(wrapper.find('.popover-empty').exists()).toBe(true)
    })

    it('filters projects by search input', async () => {
      const wrapper = makeWrapper([makeProject('1', 'Alpha'), makeProject('2', 'Beta')])
      await wrapper.find('.nav__projects-trigger').trigger('click')
      await wrapper.find('input[aria-label="Search projects"]').setValue('alpha')
      await nextTick()
      const items = wrapper.findAll('.popover__item')
      expect(items).toHaveLength(1)
      expect(items[0].text()).toContain('Alpha')
    })

    it('calls toggleProject when a project item is clicked', async () => {
      const wrapper = makeWrapper([makeProject('1', 'Alpha')])
      await wrapper.find('.nav__projects-trigger').trigger('click')
      await wrapper.find('.popover__item').trigger('click')

      const { toggleProject } = vi.mocked(useProjectsStore).mock.results[0].value
      expect(toggleProject).toHaveBeenCalledWith('1')
    })

    it('calls setSelected([]) when the "All projects" footer button is clicked', async () => {
      const wrapper = makeWrapper([makeProject('1', 'Alpha')], ['1'])
      await wrapper.find('.nav__projects-trigger').trigger('click')
      const allBtn = wrapper.findAll('.popover__footer button').find(
        (b) => b.text() === 'All projects',
      )!
      await allBtn.trigger('click')

      const { setSelected } = vi.mocked(useProjectsStore).mock.results[0].value
      expect(setSelected).toHaveBeenCalledWith([])
    })

    it('closes on mousedown outside', async () => {
      vi.mocked(useUiStore).mockReturnValue({
        cmdOpen: false, theme: null, resolvedTheme: 'light',
        toggleTheme: vi.fn(), openCmd: vi.fn(), closeCmd: vi.fn(),
      } as any)
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [makeProject('1', 'Alpha')], selectedIds: [],
        setSelected: vi.fn(), toggleProject: vi.fn(),
      } as any)
      const wrapper = mount(Navbar, { attachTo: document.body, global: globalStubs })

      await wrapper.find('.nav__projects-trigger').trigger('click')
      expect(wrapper.find('.popover').exists()).toBe(true)

      document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
      await nextTick()

      expect(wrapper.find('.popover').exists()).toBe(false)
      wrapper.unmount()
    })
  })

  describe('theme toggle', () => {
    it('calls toggleTheme when the toggle button is clicked', async () => {
      const wrapper = makeWrapper()
      await wrapper.find('[aria-label="Toggle theme"]').trigger('click')

      const { toggleTheme } = vi.mocked(useUiStore).mock.results[0].value
      expect(toggleTheme).toHaveBeenCalled()
    })
  })

  describe('command palette trigger', () => {
    it('calls openCmd when the search button is clicked', async () => {
      const wrapper = makeWrapper()
      await wrapper.find('.nav__btn').trigger('click')

      const { openCmd } = vi.mocked(useUiStore).mock.results[0].value
      expect(openCmd).toHaveBeenCalled()
    })
  })

  describe('settings navigation', () => {
    it('navigates to /settings when the settings button is clicked', async () => {
      const wrapper = makeWrapper()
      const settingsBtn = wrapper.findAll('.nav__icon-btn').find(
        (b) => b.attributes('title') === 'Settings',
      )!
      await settingsBtn.trigger('click')
      expect(pushMock).toHaveBeenCalledWith('/settings')
    })
  })

  describe('logout', () => {
    it('calls the logout endpoint when the logout button is clicked', async () => {
      Object.defineProperty(window, 'location', {
        writable: true,
        value: { href: '' },
      })

      const wrapper = makeWrapper()
      const logoutBtn = wrapper.findAll('.nav__icon-btn').find(
        (b) => b.attributes('title') === 'Log out',
      )!
      await logoutBtn.trigger('click')
      await nextTick()

      expect(apiFetch).toHaveBeenCalledWith('/api/auth/logout', { method: 'POST' })
    })
  })

  describe('dashboard nav link', () => {
    it('renders a Dashboard link in the nav', () => {
      const wrapper = makeWrapper()
      const links = wrapper.findAll('.nav__link')
      expect(links.some(l => l.text().includes('Dashboard'))).toBe(true)
    })

    it('sets aria-current="page" on the dashboard link when the route is /dashboard', () => {
      routePath.path = '/dashboard'
      vi.mocked(useUiStore).mockReturnValue({
        cmdOpen: false, theme: null, resolvedTheme: 'light',
        toggleTheme: vi.fn(), openCmd: vi.fn(), closeCmd: vi.fn(),
      } as any)
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [], selectedIds: [], setSelected: vi.fn(), toggleProject: vi.fn(),
      } as any)
      const wrapper = mount(Navbar, { global: globalStubsWithAttrs })
      const dashLink = wrapper.findAll('a').find(l => l.text().includes('Dashboard'))
      expect(dashLink?.attributes('aria-current')).toBe('page')
    })

    it('does not set aria-current on the dashboard link when on another route', () => {
      // routePath.path is '/issues' from beforeEach
      vi.mocked(useUiStore).mockReturnValue({
        cmdOpen: false, theme: null, resolvedTheme: 'light',
        toggleTheme: vi.fn(), openCmd: vi.fn(), closeCmd: vi.fn(),
      } as any)
      vi.mocked(useProjectsStore).mockReturnValue({
        projects: [], selectedIds: [], setSelected: vi.fn(), toggleProject: vi.fn(),
      } as any)
      const wrapper = mount(Navbar, { global: globalStubsWithAttrs })
      const dashLink = wrapper.findAll('a').find(l => l.text().includes('Dashboard'))
      expect(dashLink?.attributes('aria-current')).toBeUndefined()
    })
  })
})
