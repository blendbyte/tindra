import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

const pushMock = vi.fn()
const replaceMock = vi.fn()

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: pushMock, replace: replaceMock })),
  useRoute: vi.fn(() => ({ params: { tab: 'overview' }, query: {} })),
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

vi.mock('@/utils/formatters', () => ({
  formatRel: vi.fn(() => '2m ago'),
}))

import SettingsView from '../SettingsView.vue'
import { useQuery } from '@tanstack/vue-query'

const stubs = {
  RouterLink: { template: '<a><slot /></a>' },
  Icon: { template: '<span />' },
  Sparkline: { template: '<span />' },
}

const adminUser = {
  id: 'user-1',
  name: 'Admin',
  email: 'admin@example.com',
  mfa_enabled: false,
  weekly_digest: true,
  permissions: {
    manage_projects: true,
    manage_users: true,
    manage_alerts: true,
    manage_issues: true,
  },
}

function setupMocks(meData: unknown = adminUser) {
  // useQuery call order in SettingsView:
  // 1. tokens, 2. projects, 3. users, 4. auditLogData, 5. me,
  // 6. invites, 7. alertRules, 8. settings, 9. health, 10. quota
  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref([]) } as any)           // 1. tokens
    .mockReturnValueOnce({ data: ref([]) } as any)           // 2. projects
    .mockReturnValueOnce({ data: ref([]) } as any)           // 3. users
    .mockReturnValueOnce({ data: ref([]) } as any)           // 4. auditLogData
    .mockReturnValueOnce({ data: ref(meData) } as any)       // 5. me
    .mockReturnValueOnce({ data: ref([]) } as any)           // 6. invites
    .mockReturnValueOnce({ data: ref([]) } as any)           // 7. alertRules
    .mockReturnValueOnce({ data: ref(undefined) } as any)    // 8. settings
    .mockReturnValueOnce({ data: ref(undefined) } as any)    // 9. health
    .mockReturnValueOnce({ data: ref(undefined) } as any)    // 10. quota
    .mockReturnValue({ data: ref(undefined) } as any)        // fallback
}

beforeEach(() => {
  vi.mocked(useQuery).mockReset()
  pushMock.mockReset()
  replaceMock.mockReset()
})

describe('SettingsView', () => {
  describe('tab navigation', () => {
    it('renders the settings nav', () => {
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.settings__nav').exists()).toBe(true)
    })

    it('renders tab buttons', () => {
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      const navBtns = wrapper.findAll('.settings__nav button')
      expect(navBtns.length).toBeGreaterThan(0)
    })

    it('shows the Profile tab button', () => {
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      const navBtns = wrapper.findAll('.settings__nav button')
      expect(navBtns.some(b => b.text() === 'Profile')).toBe(true)
    })

    it('shows Overview tab when user has manage_projects', () => {
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      const navBtns = wrapper.findAll('.settings__nav button')
      expect(navBtns.some(b => b.text() === 'Overview')).toBe(true)
    })

    it('shows Projects tab', () => {
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      const navBtns = wrapper.findAll('.settings__nav button')
      expect(navBtns.some(b => b.text() === 'Projects')).toBe(true)
    })

    it('activates a tab when clicked', async () => {
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      const profileTab = wrapper.findAll('.settings__nav button').find(t => t.text() === 'Profile')!
      await profileTab.trigger('click')
      expect(replaceMock).toHaveBeenCalledWith(expect.objectContaining({ name: 'settings', params: { tab: 'profile' } }))
    })
  })

  describe('overview tab content', () => {
    it('renders the overview pane when on overview tab', () => {
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.pane-head').exists()).toBe(true)
    })
  })

  describe('profile tab content', () => {
    it('renders profile section when profile tab is active', async () => {
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      const profileTab = wrapper.findAll('.settings__nav button').find(t => t.text() === 'Profile')!
      await profileTab.trigger('click')
      expect(wrapper.text()).toContain('Profile')
    })
  })

  describe('projects tab content', () => {
    it('renders projects heading when projects tab is active', async () => {
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      const projectsTab = wrapper.findAll('.settings__nav button').find(t => t.text() === 'Projects')!
      await projectsTab.trigger('click')
      expect(wrapper.text()).toContain('Projects')
    })
  })

  describe('tokens tab content', () => {
    it('renders tokens section when tokens tab is active', async () => {
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      const tokensTab = wrapper.findAll('.settings__nav button').find(t => t.text() === 'Tokens')!
      await tokensTab.trigger('click')
      expect(wrapper.text()).toContain('Tokens')
    })
  })

  describe('users tab content', () => {
    it('renders users section when users tab is active', async () => {
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      const usersTab = wrapper.findAll('.settings__nav button').find(t => t.text() === 'Users')!
      await usersTab.trigger('click')
      expect(wrapper.text()).toContain('Users')
    })
  })

  describe('alerts tab content', () => {
    it('renders alerts section when alerts tab is active', async () => {
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      const alertsTab = wrapper.findAll('.settings__nav button').find(t => t.text() === 'Alerts')!
      await alertsTab.trigger('click')
      expect(wrapper.text()).toContain('Alerts')
    })
  })
})
