import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

// Provide a localStorage shim for happy-dom (the projects tab reads its sort from it)
let lsStore: Record<string, string> = {}
const localStorageMock = {
  getItem: (key: string) => lsStore[key] ?? null,
  setItem: (key: string, val: string) => { lsStore[key] = val },
  removeItem: (key: string) => { delete lsStore[key] },
  clear: () => { lsStore = {} },
  get length() { return Object.keys(lsStore).length },
  key: (i: number) => Object.keys(lsStore)[i] ?? null,
}
Object.defineProperty(window, 'localStorage', { value: localStorageMock, writable: true })

const pushMock = vi.fn()
const replaceMock = vi.fn()
let currentTab: string | undefined = 'overview'

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: pushMock, replace: replaceMock })),
  useRoute: vi.fn(() => ({ params: { tab: currentTab }, query: {} })),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(() => ({ mutate: vi.fn(), isPending: ref(false) })),
  useQueryClient: vi.fn(() => ({
    invalidateQueries: vi.fn(),
    setQueryData: vi.fn(),
  })),
}))

vi.mock('@/stores/ui', () => ({
  useUiStore: vi.fn(() => ({
    theme: null,
    resolvedTheme: 'light',
    toggleTheme: vi.fn(),
  })),
}))

vi.mock('@/composables/useToast', () => ({
  useToast: vi.fn(() => ({ show: vi.fn() })),
}))

vi.mock('@/composables/useConfig', () => ({
  useConfig: vi.fn(() => ({ dsnFor: vi.fn(() => 'https://key@host/1') })),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: vi.fn(),
}))

vi.mock('@/utils/formatters', () => ({
  formatRel: vi.fn(() => '2m ago'),
}))

import SettingsView from '../SettingsView.vue'
import OverviewTab from '../settings/OverviewTab.vue'
import TokensTab from '../settings/TokensTab.vue'
import ProjectsTab from '../settings/ProjectsTab.vue'
import UsersTab from '../settings/UsersTab.vue'
import AlertsTab from '../settings/AlertsTab.vue'
import AuditTab from '../settings/AuditTab.vue'
import ProfileTab from '../settings/ProfileTab.vue'
import { useQuery, useMutation } from '@tanstack/vue-query'
import { adminUser, mockQueries, queryName, stubs } from '../settings/__tests__/harness'

function setupMocks(meData: unknown = adminUser) {
  mockQueries({ me: meData })
}

function mountView(props: Record<string, unknown> = {}) {
  return mount(SettingsView, { props, global: { stubs } })
}

function navButton(wrapper: ReturnType<typeof mountView>, label: string) {
  return wrapper.findAll('.settings__nav button').find(t => t.text() === label)!
}

beforeEach(async () => {
  vi.mocked(useQuery).mockReset()
  vi.mocked(useMutation).mockReset()
  vi.mocked(useMutation).mockImplementation(() => ({ mutate: vi.fn(), isPending: ref(false) } as any))
  currentTab = 'overview'
  const { useRoute } = await import('vue-router')
  vi.mocked(useRoute).mockImplementation(() => ({ params: { tab: currentTab }, query: {} } as any))
  pushMock.mockReset()
  replaceMock.mockReset()
  localStorage.clear()
})

afterEach(() => {
  localStorage.clear()
})

describe('SettingsView', () => {
  describe('tab navigation', () => {
    it('renders the settings nav', () => {
      setupMocks()
      const wrapper = mountView()
      expect(wrapper.find('.settings__nav').exists()).toBe(true)
    })

    it('renders tab buttons', () => {
      setupMocks()
      const wrapper = mountView()
      const navBtns = wrapper.findAll('.settings__nav button')
      expect(navBtns.length).toBeGreaterThan(0)
    })

    it('shows the Profile tab button', () => {
      setupMocks()
      const wrapper = mountView()
      const navBtns = wrapper.findAll('.settings__nav button')
      expect(navBtns.some(b => b.text() === 'Profile')).toBe(true)
    })

    it('shows Overview tab when user has manage_projects', () => {
      setupMocks()
      const wrapper = mountView()
      const navBtns = wrapper.findAll('.settings__nav button')
      expect(navBtns.some(b => b.text() === 'Overview')).toBe(true)
    })

    it('shows Projects tab', () => {
      setupMocks()
      const wrapper = mountView()
      const navBtns = wrapper.findAll('.settings__nav button')
      expect(navBtns.some(b => b.text() === 'Projects')).toBe(true)
    })

    it('activates a tab when clicked', async () => {
      setupMocks()
      const wrapper = mountView()
      await navButton(wrapper, 'Profile').trigger('click')
      expect(replaceMock).toHaveBeenCalledWith(expect.objectContaining({ name: 'settings', params: { tab: 'profile' } }))
    })

    it('lists the admin tabs in order and marks the active one', () => {
      setupMocks()
      const wrapper = mountView()
      const navBtns = wrapper.findAll('.settings__nav button')
      expect(navBtns.map(b => b.text())).toEqual(['Overview', 'Projects', 'Users', 'Audit', 'Tokens', 'Profile'])
      expect(navBtns[0].attributes('aria-current')).toBe('page')
      expect(navBtns[1].attributes('aria-current')).toBeUndefined()
    })

    it('hides gated tabs from a user without manage permissions', () => {
      setupMocks({ ...adminUser, permissions: { manage_projects: false, manage_users: false, manage_alerts: false, manage_issues: false } })
      const wrapper = mountView()
      expect(wrapper.findAll('.settings__nav button').map(b => b.text())).toEqual(['Projects', 'Users', 'Profile'])
    })
  })

  describe('tab resolution', () => {
    it.each([
      ['overview', OverviewTab],
      ['tokens', TokensTab],
      ['projects', ProjectsTab],
      ['users', UsersTab],
      ['alerts', AlertsTab],
      ['audit', AuditTab],
      ['profile', ProfileTab],
    ] as const)('renders only the %s tab component for its route param', (tab, component) => {
      currentTab = tab
      setupMocks()
      const wrapper = mountView()
      const all = [OverviewTab, TokensTab, ProjectsTab, UsersTab, AlertsTab, AuditTab, ProfileTab]
      for (const c of all) expect(wrapper.findComponent(c).exists()).toBe(c === component)
    })

    it('maps the old team URL to the users tab', () => {
      currentTab = 'team'
      setupMocks()
      const wrapper = mountView()
      expect(wrapper.findComponent(UsersTab).exists()).toBe(true)
    })

    it('falls back to the overview tab for a missing or unknown tab', () => {
      for (const tab of [undefined, 'nope']) {
        currentTab = tab
        setupMocks()
        const wrapper = mountView()
        expect(wrapper.findComponent(OverviewTab).exists()).toBe(true)
        wrapper.unmount()
      }
    })

    it('only runs the queries of the active tab', () => {
      currentTab = 'audit'
      setupMocks()
      mountView()
      const names = vi.mocked(useQuery).mock.calls.map(([options]) => queryName(options))
      expect(new Set(names)).toEqual(new Set(['me', 'audit']))
    })
  })

  describe('overview tab content', () => {
    it('renders the overview pane when on overview tab', () => {
      setupMocks()
      const wrapper = mountView()
      expect(wrapper.find('.pane-head').exists()).toBe(true)
      expect(wrapper.text()).toContain('Instance')
    })
  })

  describe('profile tab content', () => {
    it('renders profile section when profile tab is active', async () => {
      setupMocks()
      const wrapper = mountView()
      await navButton(wrapper, 'Profile').trigger('click')
      expect(wrapper.findComponent(ProfileTab).exists()).toBe(true)
      expect(wrapper.text()).toContain('Personal information')
    })

    it('resets unsaved profile edits when the Profile tab is clicked again', async () => {
      currentTab = 'profile'
      setupMocks()
      const wrapper = mountView()
      const name = wrapper.find('input[placeholder="Admin"]')
      await name.setValue('Edited')
      await navButton(wrapper, 'Profile').trigger('click')
      expect((name.element as HTMLInputElement).value).toBe('Admin')
    })
  })

  describe('projects tab content', () => {
    it('renders projects heading when projects tab is active', async () => {
      setupMocks()
      const wrapper = mountView()
      await navButton(wrapper, 'Projects').trigger('click')
      expect(wrapper.findComponent(ProjectsTab).exists()).toBe(true)
      expect(wrapper.text()).toContain('Projects')
    })
  })

  describe('tokens tab content', () => {
    it('renders tokens section when tokens tab is active', async () => {
      setupMocks()
      const wrapper = mountView()
      await navButton(wrapper, 'Tokens').trigger('click')
      expect(wrapper.findComponent(TokensTab).exists()).toBe(true)
      expect(wrapper.text()).toContain('API tokens')
    })
  })

  describe('users tab content', () => {
    it('renders users section when users tab is active', async () => {
      setupMocks()
      const wrapper = mountView()
      await navButton(wrapper, 'Users').trigger('click')
      expect(wrapper.findComponent(UsersTab).exists()).toBe(true)
      expect(wrapper.text()).toContain('Users')
    })
  })

  describe('dedicated alerts page', () => {
    it('renders rules without the settings navigation', () => {
      setupMocks()
      const wrapper = mountView({ alertsPage: true })
      expect(wrapper.find('h1').text()).toBe('Alerts')
      expect(wrapper.text()).toContain('Alert rules')
      expect(wrapper.find('.settings__nav').exists()).toBe(false)
      expect(wrapper.find('.settings__about').exists()).toBe(false)
      expect(wrapper.find('.settings').classes()).toContain('alerts-page')
    })

    it('removes Alerts from the settings tabs', () => {
      setupMocks()
      const wrapper = mountView()
      expect(wrapper.findAll('.settings__nav button').map(t => t.text())).not.toContain('Alerts')
    })
  })

  describe('permission-based tab redirects', () => {
    it('redirects from overview tab when user lacks manage_projects', async () => {
      currentTab = 'overview'
      const limitedUser = { ...adminUser, permissions: { ...adminUser.permissions, manage_projects: false, manage_users: false } }
      setupMocks(limitedUser)
      mountView()
      // watchEffect fires immediately and redirects via setTab
      expect(replaceMock).toHaveBeenCalledWith({ name: 'settings', params: { tab: 'projects' } })
    })

    it('redirects from audit tab when user lacks manage_users', () => {
      currentTab = 'audit'
      const limitedUser = { ...adminUser, permissions: { ...adminUser.permissions, manage_users: false } }
      setupMocks(limitedUser)
      mountView()
      expect(replaceMock).toHaveBeenCalledWith({ name: 'settings', params: { tab: 'projects' } })
    })

    it('does not redirect an admin', () => {
      for (const tab of ['overview', 'tokens', 'audit', 'alerts']) {
        currentTab = tab
        setupMocks()
        mountView().unmount()
      }
      expect(replaceMock).not.toHaveBeenCalled()
    })
  })

  describe('tab navigation - non-admin redirects via watchEffect', () => {
    it('redirects tokens tab to projects when user lacks manage_projects', () => {
      currentTab = 'tokens'
      const limitedUser = { ...adminUser, permissions: { manage_projects: false, manage_users: true, manage_alerts: true, manage_issues: true } }
      setupMocks(limitedUser)
      const wrapper = mountView()
      expect(replaceMock).toHaveBeenCalledWith({ name: 'settings', params: { tab: 'projects' } })
      expect(wrapper.findComponent(ProjectsTab).exists()).toBe(true)
    })

    it('redirects audit tab to projects when user lacks manage_users', () => {
      currentTab = 'audit'
      const limitedUser = { ...adminUser, permissions: { manage_projects: true, manage_users: false, manage_alerts: true, manage_issues: true } }
      setupMocks(limitedUser)
      const wrapper = mountView()
      expect(replaceMock).toHaveBeenCalledWith({ name: 'settings', params: { tab: 'projects' } })
      expect(wrapper.findComponent(ProjectsTab).exists()).toBe(true)
    })

    it('redirects alerts tab to projects when user lacks manage_alerts', () => {
      currentTab = 'alerts'
      const limitedUser = { ...adminUser, permissions: { manage_projects: true, manage_users: true, manage_alerts: false, manage_issues: true } }
      setupMocks(limitedUser)
      const wrapper = mountView()
      expect(replaceMock).toHaveBeenCalledWith({ name: 'settings', params: { tab: 'projects' } })
      expect(wrapper.findComponent(ProjectsTab).exists()).toBe(true)
    })
  })

  describe('settings shortcuts button and UI store interaction', () => {
    it('clicking shortcuts button sets ui.shortcutsOpen', async () => {
      setupMocks()
      const { useUiStore } = await import('@/stores/ui')
      const uiMock = { theme: null, resolvedTheme: 'light', toggleTheme: vi.fn(), shortcutsOpen: false }
      vi.mocked(useUiStore).mockReturnValue(uiMock as any)
      const wrapper = mountView()
      const shortcutsBtn = wrapper.findAll('.settings__about-link').find(b => b.text().includes('Shortcuts'))!
      await shortcutsBtn.trigger('click')
      expect(uiMock.shortcutsOpen).toBe(true)
    })
  })
})

describe('switching between Settings and the dedicated Alerts page', () => {
  it('updates the page and the rendered tab when the route props change', async () => {
    setupMocks()
    const wrapper = mountView()
    const queried = () => vi.mocked(useQuery).mock.calls.map(([options]) => queryName(options))
    expect(wrapper.find('h1').text()).toBe('Settings')
    expect(wrapper.findComponent(OverviewTab).exists()).toBe(true)
    for (const key of ['users', 'settings']) expect(queried()).toContain(key)
    expect(queried()).not.toContain('alert-rules')

    vi.mocked(useQuery).mockClear()
    await wrapper.setProps({ alertsPage: true })
    expect(wrapper.find('h1').text()).toBe('Alerts')
    expect(wrapper.find('.settings__nav').exists()).toBe(false)
    expect(wrapper.findComponent(OverviewTab).exists()).toBe(false)
    expect(wrapper.findComponent(AlertsTab).exists()).toBe(true)
    // The alerts page only runs the alert queries, never the settings-only ones.
    expect(queried()).toContain('alert-rules')
    for (const key of ['tokens', 'users', 'settings', 'invites']) expect(queried()).not.toContain(key)

    vi.mocked(useQuery).mockClear()
    await wrapper.setProps({ alertsPage: false })
    expect(wrapper.find('h1').text()).toBe('Settings')
    expect(wrapper.find('.settings__nav').exists()).toBe(true)
    expect(wrapper.findComponent(AlertsTab).exists()).toBe(false)
    expect(wrapper.findComponent(OverviewTab).exists()).toBe(true)
    for (const key of ['users', 'settings']) expect(queried()).toContain(key)
    expect(queried()).not.toContain('alert-rules')
    wrapper.unmount()
  })
})
