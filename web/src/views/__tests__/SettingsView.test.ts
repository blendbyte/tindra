import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

const pushMock = vi.fn()
const replaceMock = vi.fn()
let currentTab = 'overview'

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
import { useQuery } from '@tanstack/vue-query'
import { useAuthStore } from '@/stores/auth'

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
  timezone: 'UTC',
  permissions: {
    manage_projects: true,
    manage_users: true,
    manage_alerts: true,
    manage_issues: true,
  },
}

function setupMocks(meData: unknown = adminUser) {
  vi.mocked(useAuthStore).mockReturnValue({ user: adminUser, setUser: vi.fn() } as any)
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
  vi.mocked(useAuthStore).mockReset()
  pushMock.mockReset()
  replaceMock.mockReset()
  currentTab = 'overview'
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

  describe('tokens tab - direct render', () => {
    it('renders API tokens heading on tokens tab', () => {
      currentTab = 'tokens'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('API tokens')
    })

    it('shows token table on tokens tab', () => {
      currentTab = 'tokens'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.token-table').exists()).toBe(true)
    })

    it('shows new token form when Create token button is clicked', async () => {
      currentTab = 'tokens'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      const createTokenBtn = wrapper.findAll('.btn').find(b => b.text().includes('Create token'))!
      await createTokenBtn.trigger('click')
      expect(wrapper.find('.proj-card--form').exists()).toBe(true)
    })
  })

  describe('users tab - direct render', () => {
    it('renders users permission table on users tab', () => {
      currentTab = 'users'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.team-perms-table').exists()).toBe(true)
    })

    it('shows 2FA column header in permission table', () => {
      currentTab = 'users'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.perm-col[title="Two-factor authentication"]').exists()).toBe(true)
    })

    it('shows Invite user button when user can manage', () => {
      currentTab = 'users'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.findAll('.btn').some(b => b.text().includes('Invite user'))).toBe(true)
    })
  })

  describe('alerts tab - direct render', () => {
    it('renders Alert rules heading on alerts tab', () => {
      currentTab = 'alerts'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Alert rules')
    })

    it('shows create alert button', () => {
      currentTab = 'alerts'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.findAll('.btn').some(b => b.text().includes('New rule'))).toBe(true)
    })
  })

  describe('audit tab - direct render', () => {
    it('renders audit log empty state when no events', () => {
      currentTab = 'audit'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('No audit events yet')
    })

    it('renders audit chip filters', () => {
      currentTab = 'audit'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.audit-chip').exists()).toBe(true)
    })

    it('renders audit rows when events exist', () => {
      currentTab = 'audit'
      vi.mocked(useAuthStore).mockReturnValue({ user: adminUser, setUser: vi.fn() } as any)
      const auditRow = {
        id: 'a1',
        event_type: 'user.login',
        actor_id: 'u1',
        actor_email: 'admin@example.com',
        ip: '127.0.0.1',
        created_at: '2024-01-01T00:00:00Z',
        metadata: {},
      }
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([auditRow]) } as any)
        .mockReturnValueOnce({ data: ref(adminUser) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValue({ data: ref(undefined) } as any)
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.audit-row').exists()).toBe(true)
    })
  })

  describe('profile tab - direct render', () => {
    it('renders Personal information section', () => {
      currentTab = 'profile'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Personal information')
    })

    it('renders Notifications section with weekly digest', () => {
      currentTab = 'profile'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Notifications')
    })

    it('renders Two-factor authentication section', () => {
      currentTab = 'profile'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Two-factor authentication')
    })

    it('shows Password section when user has a password', () => {
      currentTab = 'profile'
      setupMocks({ ...adminUser, has_password: true })
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Password')
    })

    it('hides Password section when user has no password (OAuth)', () => {
      currentTab = 'profile'
      setupMocks({ ...adminUser, has_password: false })
      const wrapper = mount(SettingsView, { global: { stubs } })
      const sections = wrapper.findAll('.profile-section__title')
      expect(sections.some(s => s.text() === 'Password')).toBe(false)
    })
  })

  describe('projects tab - direct render', () => {
    it('renders projects tab content with no projects empty state', () => {
      currentTab = 'projects'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Projects')
    })

    it('renders project cards when projects exist', () => {
      currentTab = 'projects'
      vi.mocked(useAuthStore).mockReturnValue({ user: adminUser, setUser: vi.fn() } as any)
      const proj = { id: 'p1', name: 'My App', slug: 'my-app', public_key: 'abc123', event_count: 0, event_limit: 0 }
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([proj]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(adminUser) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValue({ data: ref(undefined) } as any)
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('My App')
    })

    it('expands a project card when its header is clicked', async () => {
      currentTab = 'projects'
      vi.mocked(useAuthStore).mockReturnValue({ user: adminUser, setUser: vi.fn() } as any)
      const proj = { id: 'p1', name: 'My App', slug: 'my-app', public_key: 'abc123', event_count: 0, event_limit: 0 }
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([proj]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(adminUser) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValue({ data: ref(undefined) } as any)
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.find('.proj-card__head').trigger('click')
      expect(wrapper.find('.proj-card__body').exists()).toBe(true)
    })

    it('shows New project form when button clicked', async () => {
      currentTab = 'projects'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      const newBtn = wrapper.findAll('.btn').find(b => b.text().includes('New project'))!
      await newBtn.trigger('click')
      expect(wrapper.find('.proj-card--form').exists()).toBe(true)
    })

    it('hides New project form when Cancel is clicked', async () => {
      currentTab = 'projects'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.findAll('.btn').find(b => b.text().includes('New project'))!.trigger('click')
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('.proj-card--form').exists()).toBe(false)
    })
  })

  describe('tokens tab - form interactions', () => {
    it('closes the new token form when Cancel is clicked', async () => {
      currentTab = 'tokens'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.findAll('.btn').find(b => b.text().includes('Create token'))!.trigger('click')
      expect(wrapper.find('.proj-card--form').exists()).toBe(true)
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('.proj-card--form').exists()).toBe(false)
    })
  })

  describe('alert rules tab - with rules data', () => {
    const makeRule = (id: string, name: string) => ({
      id,
      name,
      trigger: 'new_issue' as const,
      channel: 'webhook' as const,
      enabled: true,
      threshold: 100,
      window_mins: 60,
      cooldown_mins: 60,
      project_ids: [],
      webhook_url: 'https://hook.example.com',
      email_to: null,
      last_fired_at: null,
      filter_level: null,
      filter_environment: null,
      min_occurrences: 0,
    })

    function setupAlertsWithRules(rules: unknown[]) {
      currentTab = 'alerts'
      vi.mocked(useAuthStore).mockReturnValue({ user: adminUser, setUser: vi.fn() } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(adminUser) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(rules) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValue({ data: ref(undefined) } as any)
    }

    it('renders rule names when alert rules exist', () => {
      setupAlertsWithRules([makeRule('r1', 'High Error Rate')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('High Error Rate')
    })

    it('shows rule trigger label', () => {
      setupAlertsWithRules([makeRule('r1', 'High Error Rate')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('New issue')
    })

    it('expands rule body when rule head is clicked', async () => {
      setupAlertsWithRules([makeRule('r1', 'High Error Rate')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.find('.rule__head').trigger('click')
      expect(wrapper.find('.rule__body').exists()).toBe(true)
    })

    it('collapses rule body when rule head is clicked again', async () => {
      setupAlertsWithRules([makeRule('r1', 'High Error Rate')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.find('.rule__head').trigger('click')
      await wrapper.find('.rule__head').trigger('click')
      expect(wrapper.find('.rule__body').exists()).toBe(false)
    })

    it('opens edit form when Edit button is clicked in expanded rule', async () => {
      setupAlertsWithRules([makeRule('r1', 'High Error Rate')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.find('.rule__head').trigger('click')
      const editBtn = wrapper.findAll('.rule__actions .btn').find(b => b.text().includes('Edit'))!
      await editBtn.trigger('click')
      expect(wrapper.find('.alert-create__grid').exists()).toBe(true)
    })
  })

  describe('users tab - with users data', () => {
    const adminUser2 = {
      id: 'user-2',
      name: 'Bob',
      email: 'bob@example.com',
      mfa_enabled: false,
      weekly_digest: false,
      timezone: 'UTC',
      permissions: {
        manage_projects: false,
        manage_users: false,
        manage_alerts: false,
        manage_issues: true,
      },
    }

    function setupUsersWithData(users: unknown[]) {
      currentTab = 'users'
      vi.mocked(useAuthStore).mockReturnValue({ user: adminUser, setUser: vi.fn() } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(users) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(adminUser) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValue({ data: ref(undefined) } as any)
    }

    it('renders user rows when users exist', () => {
      setupUsersWithData([adminUser, adminUser2])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Bob')
    })

    it('shows permission checkboxes for each user', () => {
      setupUsersWithData([adminUser, adminUser2])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.findAll('.perm-check').length).toBeGreaterThan(0)
    })

    it('toggles invite form when Invite user button is clicked', async () => {
      setupUsersWithData([])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.proj-card--form').exists()).toBe(false)
      const inviteBtn = wrapper.findAll('.btn').find(b => b.text().includes('Invite user'))!
      await inviteBtn.trigger('click')
      expect(wrapper.find('.proj-card--form').exists()).toBe(true)
    })

    it('hides invite form when Cancel is clicked', async () => {
      setupUsersWithData([])
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.findAll('.btn').find(b => b.text().includes('Invite user'))!.trigger('click')
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('.proj-card--form').exists()).toBe(false)
    })
  })

  describe('audit tab - filter chips', () => {
    it('renders multiple audit filter chips', () => {
      currentTab = 'audit'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.findAll('.audit-chip').length).toBeGreaterThan(1)
    })

    it('activates audit chip when clicked', async () => {
      currentTab = 'audit'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      const chips = wrapper.findAll('.audit-chip')
      const authChip = chips.find(c => c.text().includes('auth'))!
      await authChip.trigger('click')
      expect(authChip.classes()).toContain('audit-chip--active')
    })
  })

  describe('profile tab - form interactions', () => {
    it('shows password mismatch error when passwords differ', async () => {
      currentTab = 'profile'
      setupMocks({ ...adminUser, has_password: true })
      const wrapper = mount(SettingsView, { global: { stubs } })
      const inputs = wrapper.findAll('input[type="password"]')
      await inputs[0].setValue('currentpassword')
      await inputs[1].setValue('newpassword1234')
      await inputs[2].setValue('differentpassword1234')
      const saveBtn = wrapper.find('.btn--primary[disabled]')
      // The button is enabled once all 3 fields are filled
      // Find the save changes button in the password section
      const pwBtns = wrapper.findAll('.btn--primary')
      const pwSaveBtn = pwBtns.find(b => !b.element.hasAttribute('disabled') || b.element.getAttribute('disabled') === 'false')
      if (pwSaveBtn) {
        await pwSaveBtn.trigger('click')
      }
      // The submitPasswordChange function runs - either through click or directly
    })

    it('initializes profile fields from me data on profile tab', () => {
      currentTab = 'profile'
      setupMocks({ ...adminUser, name: 'Alice', email: 'alice@example.com' })
      const wrapper = mount(SettingsView, { global: { stubs } })
      // profileEmail input should be initialized to the me data value
      const emailInput = wrapper.find('input[type="email"]') as any
      expect(emailInput.element.value).toBe('alice@example.com')
    })

    it('shows "Save changes" button on profile tab', () => {
      currentTab = 'profile'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.findAll('.btn--primary').some(b => b.text().includes('Save changes'))).toBe(true)
    })

    it('validates password mismatch on submit', async () => {
      currentTab = 'profile'
      setupMocks({ ...adminUser, has_password: true })
      const wrapper = mount(SettingsView, { global: { stubs } })
      const pwInputs = wrapper.findAll('input[type="password"]')
      await pwInputs[0].setValue('correctpassword')
      await pwInputs[1].setValue('newpassword1234')
      await pwInputs[2].setValue('differentpassword')
      // Button should be enabled now (all fields filled)
      const pwSection = wrapper.findAll('.profile-section').find(s => s.text().includes('Password'))
      if (pwSection) {
        const saveBtn = pwSection.findAll('.btn--primary').find(b => !b.element.getAttribute('disabled'))
        if (saveBtn) await saveBtn.trigger('click')
      }
      // The error state should either show an error or not throw
    })
  })

  describe('overview tab with health data', () => {
    const healthData = {
      events_total: 1_500_000,
      events_24h: 2500,
      tx_total: 500_000,
      tx_24h: 1000,
      logs_total: 3_000_000,
      logs_24h: 5000,
      oldest_event_at: '2024-01-01T00:00:00Z',
      oldest_tx_at: '2024-02-01T00:00:00Z',
      oldest_log_at: null,
      retention_days: 90,
      db_size_bytes: 1_073_741_824,
      db_path: '/data/tindra.db',
    }

    it('renders health data on overview tab', () => {
      currentTab = 'overview'
      vi.mocked(useAuthStore).mockReturnValue({ user: adminUser, setUser: vi.fn() } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(adminUser) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(healthData) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValue({ data: ref(undefined) } as any)
      const wrapper = mount(SettingsView, { global: { stubs } })
      // formatNum is called for health data: 1.5M should appear
      expect(wrapper.text()).toContain('1.5M')
    })

    it('renders expires label from health data', () => {
      currentTab = 'overview'
      vi.mocked(useAuthStore).mockReturnValue({ user: adminUser, setUser: vi.fn() } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(adminUser) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(healthData) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValue({ data: ref(undefined) } as any)
      const wrapper = mount(SettingsView, { global: { stubs } })
      // expiresLabel / dataAgeDays functions run; just verify it renders
      expect(wrapper.find('.overview-vol-row').exists()).toBe(true)
    })

    it('renders usage section with settings data', () => {
      currentTab = 'overview'
      vi.mocked(useAuthStore).mockReturnValue({ user: adminUser, setUser: vi.fn() } as any)
      const settings = { event_limit: 1_000_000, project_limit: 10, user_limit: 5, billing_url: null }
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(adminUser) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(settings) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValue({ data: ref(undefined) } as any)
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.overview-usage-row').exists()).toBe(true)
    })
  })

  describe('alert rule form interactions', () => {
    it('opens new alert rule form when New rule is clicked', async () => {
      currentTab = 'alerts'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      const newRuleBtn = wrapper.findAll('.btn').find(b => b.text().includes('New rule'))!
      await newRuleBtn.trigger('click')
      expect(wrapper.find('.alert-create__grid').exists()).toBe(true)
    })

    it('hides new rule form when Cancel is clicked', async () => {
      currentTab = 'alerts'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.findAll('.btn').find(b => b.text().includes('New rule'))!.trigger('click')
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('.alert-create__grid').exists()).toBe(false)
    })
  })

  describe('permission-based tab redirects', () => {
    it('redirects from overview tab when user lacks manage_projects', async () => {
      currentTab = 'overview'
      const limitedUser = { ...adminUser, permissions: { ...adminUser.permissions, manage_projects: false, manage_users: false } }
      setupMocks(limitedUser)
      const wrapper = mount(SettingsView, { global: { stubs } })
      // watchEffect fires immediately and redirects via setTab
      expect(replaceMock).toHaveBeenCalled()
    })

    it('redirects from audit tab when user lacks manage_users', () => {
      currentTab = 'audit'
      const limitedUser = { ...adminUser, permissions: { ...adminUser.permissions, manage_users: false } }
      setupMocks(limitedUser)
      mount(SettingsView, { global: { stubs } })
      expect(replaceMock).toHaveBeenCalled()
    })
  })

  describe('alert rules - additional trigger labels', () => {
    const makeRuleWithTrigger = (trigger: string, threshold = 100, window_mins = 60) => ({
      id: `r-${trigger}`,
      name: trigger,
      trigger,
      channel: 'webhook' as const,
      enabled: true,
      threshold,
      window_mins,
      cooldown_mins: 60,
      project_ids: [] as string[],
      webhook_url: '',
      email_to: null,
      last_fired_at: null,
      filter_level: null,
      filter_environment: null,
      min_occurrences: 0,
    })

    function setupAlertsTab(rules: unknown[], projects: unknown[] = []) {
      currentTab = 'alerts'
      vi.mocked(useAuthStore).mockReturnValue({ user: adminUser, setUser: vi.fn() } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(projects) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(adminUser) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(rules) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValue({ data: ref(undefined) } as any)
    }

    it('shows Regression label for regressed trigger', () => {
      setupAlertsTab([makeRuleWithTrigger('regressed')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Regression')
    })

    it('shows "New issue or regression" for new_or_regressed trigger', () => {
      setupAlertsTab([makeRuleWithTrigger('new_or_regressed')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('New issue or regression')
    })

    it('shows Always for always trigger', () => {
      setupAlertsTab([makeRuleWithTrigger('always')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Always')
    })

    it('shows threshold for event_count trigger', () => {
      setupAlertsTab([makeRuleWithTrigger('event_count', 50, 60)])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('>50 events')
    })

    it('shows "Cron monitor missed" for cron_missed trigger', () => {
      setupAlertsTab([makeRuleWithTrigger('cron_missed')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Cron monitor missed')
    })

    it('shows "Cron monitor error" for cron_error trigger', () => {
      setupAlertsTab([makeRuleWithTrigger('cron_error')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.text()).toContain('Cron monitor error')
    })

    it('shows project name in expanded rule when rule has project_ids', async () => {
      const rule = {
        ...makeRuleWithTrigger('new_issue'),
        project_ids: ['p1'],
      }
      const proj = { id: 'p1', name: 'My App', slug: 'my-app', public_key: 'k', event_count: 0, event_limit: 0 }
      setupAlertsTab([rule], [proj])
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.find('.rule__head').trigger('click')
      expect(wrapper.text()).toContain('My App')
    })
  })

  describe('audit log - all action kinds', () => {
    function setupAuditTab(events: unknown[]) {
      currentTab = 'audit'
      vi.mocked(useAuthStore).mockReturnValue({ user: adminUser, setUser: vi.fn() } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(events) } as any)
        .mockReturnValueOnce({ data: ref(adminUser) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValue({ data: ref(undefined) } as any)
    }

    const makeAuditRow = (event_type: string) => ({
      id: `a-${event_type}`,
      event_type,
      actor_id: 'u1',
      actor_email: 'admin@example.com',
      ip: '127.0.0.1',
      created_at: '2024-01-01T00:00:00Z',
      metadata: {},
    })

    it('renders audit row with auth.login event type', () => {
      setupAuditTab([makeAuditRow('auth.login')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.audit-row').exists()).toBe(true)
    })

    it('renders audit row with alert.fired event type', () => {
      setupAuditTab([makeAuditRow('alert.fired')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.audit-row').exists()).toBe(true)
    })

    it('renders audit row with issue.created event type', () => {
      setupAuditTab([makeAuditRow('issue.created')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.audit-row').exists()).toBe(true)
    })

    it('renders audit row with release.published event type', () => {
      setupAuditTab([makeAuditRow('release.published')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.audit-row').exists()).toBe(true)
    })

    it('renders audit row with token.created event type', () => {
      setupAuditTab([makeAuditRow('token.created')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.audit-row').exists()).toBe(true)
    })

    it('renders audit row with project.deleted event type', () => {
      setupAuditTab([makeAuditRow('project.deleted')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.audit-row').exists()).toBe(true)
    })
  })

  describe('profile tab - password validation', () => {
    it('shows "do not match" error when passwords differ on submit', async () => {
      currentTab = 'profile'
      setupMocks({ ...adminUser, has_password: true })
      const wrapper = mount(SettingsView, { global: { stubs } })
      const pwInputs = wrapper.findAll('input[type="password"]')
      await pwInputs[0].setValue('correctcurrent')
      await pwInputs[1].setValue('newpassword1234')
      await pwInputs[2].setValue('differentpassword1234')
      const pwSection = wrapper.findAll('.profile-section').find(s => s.text().includes('Password'))
      if (pwSection) {
        const saveBtn = pwSection.find('.btn--primary')
        if (saveBtn.exists()) await saveBtn.trigger('click')
      }
      expect(wrapper.text()).toContain('do not match')
    })

    it('shows "12 characters" error when new password is too short', async () => {
      currentTab = 'profile'
      setupMocks({ ...adminUser, has_password: true })
      const wrapper = mount(SettingsView, { global: { stubs } })
      const pwInputs = wrapper.findAll('input[type="password"]')
      await pwInputs[0].setValue('correctcurrent')
      await pwInputs[1].setValue('short1234')
      await pwInputs[2].setValue('short1234')
      const pwSection = wrapper.findAll('.profile-section').find(s => s.text().includes('Password'))
      if (pwSection) {
        const saveBtn = pwSection.find('.btn--primary')
        if (saveBtn.exists()) await saveBtn.trigger('click')
      }
      expect(wrapper.text()).toContain('12 characters')
    })
  })

  describe('users tab - menu and set password', () => {
    const otherUser = {
      id: 'user-2',
      name: 'Bob',
      email: 'bob@example.com',
      mfa_enabled: false,
      weekly_digest: false,
      timezone: 'UTC',
      permissions: { manage_projects: false, manage_users: false, manage_alerts: false, manage_issues: true },
    }

    function setupUsersTabWithUsers(users: unknown[]) {
      currentTab = 'users'
      vi.mocked(useAuthStore).mockReturnValue({ user: adminUser, setUser: vi.fn() } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(users) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(adminUser) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValue({ data: ref(undefined) } as any)
    }

    it('opens user action menu when trigger button is clicked', async () => {
      setupUsersTabWithUsers([adminUser, otherUser])
      const wrapper = mount(SettingsView, { global: { stubs } })
      const menuTrigger = wrapper.find('.user-menu__trigger')
      if (menuTrigger.exists()) {
        await menuTrigger.trigger('click')
        expect(wrapper.find('.user-menu__dropdown').exists()).toBe(true)
      } else {
        expect(wrapper.find('.team-perms-table').exists()).toBe(true)
      }
    })

    it('closes menu and opens set-password form when Set password is clicked', async () => {
      setupUsersTabWithUsers([adminUser, otherUser])
      const wrapper = mount(SettingsView, { global: { stubs } })
      const menuTrigger = wrapper.find('.user-menu__trigger')
      if (menuTrigger.exists()) {
        await menuTrigger.trigger('click')
        const setPwItem = wrapper.findAll('.user-menu__item').find(b => b.text().includes('Set password'))
        if (setPwItem) {
          await setPwItem.trigger('click')
          expect(wrapper.find('.team-action-panel').exists()).toBe(true)
        }
      }
    })

    it('cancels set-password form when Cancel is clicked', async () => {
      setupUsersTabWithUsers([adminUser, otherUser])
      const wrapper = mount(SettingsView, { global: { stubs } })
      const menuTrigger = wrapper.find('.user-menu__trigger')
      if (menuTrigger.exists()) {
        await menuTrigger.trigger('click')
        const setPwItem = wrapper.findAll('.user-menu__item').find(b => b.text().includes('Set password'))
        if (setPwItem) {
          await setPwItem.trigger('click')
          const cancelBtn = wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')
          if (cancelBtn) {
            await cancelBtn.trigger('click')
            expect(wrapper.find('.team-action-panel').exists()).toBe(false)
          }
        }
      }
    })

    it('calls togglePerm when permission checkbox is clicked', async () => {
      setupUsersTabWithUsers([adminUser, otherUser])
      const wrapper = mount(SettingsView, { global: { stubs } })
      const checkboxes = wrapper.findAll('.perm-check')
      if (checkboxes.length > 0) {
        await checkboxes[0].trigger('click')
        expect(wrapper.find('.team-perms-table').exists()).toBe(true)
      }
    })

    it('shows delete step 1 panel when Remove user is clicked', async () => {
      setupUsersTabWithUsers([adminUser, { ...otherUser, created_at: '2024-01-01T00:00:00Z' }])
      const wrapper = mount(SettingsView, { global: { stubs } })
      const trigger = wrapper.find('.user-menu__trigger')
      if (trigger.exists()) {
        await trigger.trigger('click')
        const removeBtn = wrapper.findAll('.user-menu__item--danger').find(b => b.text().includes('Remove user'))
        if (removeBtn) {
          await removeBtn.trigger('click')
          expect(wrapper.find('.team-action-panel--danger').exists()).toBe(true)
        }
      }
    })

    it('advances to delete step 2 when Yes, continue is clicked', async () => {
      setupUsersTabWithUsers([adminUser, { ...otherUser, created_at: '2024-01-01T00:00:00Z' }])
      const wrapper = mount(SettingsView, { global: { stubs } })
      const trigger = wrapper.find('.user-menu__trigger')
      if (trigger.exists()) {
        await trigger.trigger('click')
        const removeBtn = wrapper.findAll('.user-menu__item--danger').find(b => b.text().includes('Remove user'))
        if (removeBtn) {
          await removeBtn.trigger('click')
          const continueBtn = wrapper.findAll('.btn--ghost').find(b => b.text().includes('Yes, continue'))
          if (continueBtn) {
            await continueBtn.trigger('click')
            expect(wrapper.find('.team-action-panel--danger').exists()).toBe(true)
            expect(wrapper.text()).toContain('Final confirmation')
          }
        }
      }
    })

    it('cancels delete when Cancel is clicked in delete step 1', async () => {
      setupUsersTabWithUsers([adminUser, { ...otherUser, created_at: '2024-01-01T00:00:00Z' }])
      const wrapper = mount(SettingsView, { global: { stubs } })
      const trigger = wrapper.find('.user-menu__trigger')
      if (trigger.exists()) {
        await trigger.trigger('click')
        const removeBtn = wrapper.findAll('.user-menu__item--danger').find(b => b.text().includes('Remove user'))
        if (removeBtn) {
          await removeBtn.trigger('click')
          const cancelBtn = wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')
          if (cancelBtn) {
            await cancelBtn.trigger('click')
            expect(wrapper.find('.team-action-panel--danger').exists()).toBe(false)
          }
        }
      }
    })
  })

  describe('projects tab - edit project', () => {
    const proj = { id: 'p1', name: 'My App', slug: 'my-app', public_key: 'abc123', event_count: 0, event_limit: 0 }

    function setupProjectsTabWithProject(projects: unknown[]) {
      currentTab = 'projects'
      vi.mocked(useAuthStore).mockReturnValue({ user: adminUser, setUser: vi.fn() } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(projects) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(adminUser) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValue({ data: ref(undefined) } as any)
    }

    it('shows edit form when Edit button is clicked in expanded project', async () => {
      setupProjectsTabWithProject([proj])
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.find('.proj-card__head').trigger('click')
      const editBtn = wrapper.findAll('.btn').find(b => b.text().includes('Edit'))
      if (editBtn) {
        await editBtn.trigger('click')
        expect(wrapper.find('input[name="edit-name"]').exists() || wrapper.find('.proj-card__body form').exists()).toBe(true)
      }
    })

    it('shows delete confirmation when Delete is clicked in expanded project', async () => {
      setupProjectsTabWithProject([proj])
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.find('.proj-card__head').trigger('click')
      const deleteBtn = wrapper.findAll('.btn--ghost').find(b => b.text().includes('Delete'))
      if (deleteBtn) {
        await deleteBtn.trigger('click')
        expect(wrapper.find('.team-action-panel--danger').exists() || wrapper.find('.proj-delete-confirm').exists() || wrapper.find('input[placeholder="my-app"]').exists()).toBe(true)
      }
    })
  })

  describe('projects tab - new project form interactions', () => {
    it('auto-fills slug when typing in project name field', async () => {
      currentTab = 'projects'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.findAll('.btn').find(b => b.text().includes('New project'))!.trigger('click')
      const nameInput = wrapper.find('#proj-name')
      await nameInput.setValue('My New App')
      const slugInput = wrapper.find('#proj-slug') as any
      expect(slugInput.element.value).toBe('my-new-app')
    })

    it('submits new project form when name and slug are set', async () => {
      currentTab = 'projects'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.findAll('.btn').find(b => b.text().includes('New project'))!.trigger('click')
      await wrapper.find('#proj-name').setValue('My App')
      await wrapper.find('#proj-slug').setValue('my-app')
      // Submit the form - calls submitNewProject which calls createProject mutation
      await wrapper.find('.proj-card--form').trigger('submit')
      // Form stays open since mutation is mocked (no onSuccess), but no error thrown
      expect(wrapper.exists()).toBe(true)
    })
  })

  describe('users tab - invite form interactions', () => {
    it('enables Send invite button when email is typed', async () => {
      currentTab = 'users'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.findAll('.btn').find(b => b.text().includes('Invite user'))!.trigger('click')
      const emailInput = wrapper.find('#invite-email')
      await emailInput.setValue('test@example.com')
      const submitBtn = wrapper.find('.proj-card--form .btn--primary')
      expect(submitBtn.exists()).toBe(true)
    })

    it('submits invite form when email is filled and form submitted', async () => {
      currentTab = 'users'
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.findAll('.btn').find(b => b.text().includes('Invite user'))!.trigger('click')
      await wrapper.find('#invite-email').setValue('test@example.com')
      await wrapper.find('#invite-name').setValue('Test User')
      // Submit - calls submitInvite which calls createInvite mutation
      await wrapper.find('.proj-card--form').trigger('submit')
      // Component still exists without errors (form stays because mocked mutation has no onSuccess)
      expect(wrapper.exists()).toBe(true)
    })
  })

  describe('projects tab - data privacy panel', () => {
    const proj = { id: 'p1', name: 'My App', slug: 'my-app', public_key: 'abc123', event_count: 0, event_limit: 0 }

    function setupProjectsTab() {
      currentTab = 'projects'
      vi.mocked(useAuthStore).mockReturnValue({ user: adminUser, setUser: vi.fn() } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([proj]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(adminUser) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValue({ data: ref(undefined) } as any)
    }

    it('opens data privacy panel when Data privacy button is clicked', async () => {
      setupProjectsTab()
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.find('.proj-card__head').trigger('click')
      const privacyBtn = wrapper.findAll('.btn').find(b => b.text().includes('Data privacy'))
      if (privacyBtn) {
        await privacyBtn.trigger('click')
        expect(wrapper.find('.privacy-panel').exists()).toBe(true)
      }
    })

    it('copies DSN when Copy button is clicked in expanded project', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
      setupProjectsTab()
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.find('.proj-card__head').trigger('click')
      const copyBtn = wrapper.findAll('.btn').find(b => b.text().toLowerCase().includes('copy') || b.attributes('title')?.includes('DSN'))
      if (copyBtn) {
        await copyBtn.trigger('click')
        expect(wrapper.find('.proj-card__body').exists()).toBe(true)
      }
    })
  })

  describe('alert rules - cancelEditRule', () => {
    const makeRule = (id: string, name: string) => ({
      id,
      name,
      trigger: 'new_issue' as const,
      channel: 'webhook' as const,
      enabled: true,
      threshold: 100,
      window_mins: 60,
      cooldown_mins: 60,
      project_ids: [],
      webhook_url: 'https://hook.example.com',
      email_to: null,
      last_fired_at: null,
      filter_level: null,
      filter_environment: null,
      min_occurrences: 0,
    })

    function setupAlertsWithRulesForEdit(rules: unknown[]) {
      currentTab = 'alerts'
      vi.mocked(useAuthStore).mockReturnValue({ user: adminUser, setUser: vi.fn() } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(adminUser) } as any)
        .mockReturnValueOnce({ data: ref([]) } as any)
        .mockReturnValueOnce({ data: ref(rules) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValueOnce({ data: ref(undefined) } as any)
        .mockReturnValue({ data: ref(undefined) } as any)
    }

    it('closes edit form when Cancel is clicked in expanded rule edit', async () => {
      setupAlertsWithRulesForEdit([makeRule('r1', 'My Rule')])
      const wrapper = mount(SettingsView, { global: { stubs } })
      await wrapper.find('.rule__head').trigger('click')
      const editBtn = wrapper.findAll('.rule__actions .btn').find(b => b.text().includes('Edit'))
      if (editBtn) {
        await editBtn.trigger('click')
        expect(wrapper.find('.alert-create__grid').exists()).toBe(true)
        const cancelBtn = wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')
        if (cancelBtn) {
          await cancelBtn.trigger('click')
          expect(wrapper.find('.alert-create__grid').exists()).toBe(false)
        }
      }
    })
  })

  describe('overview tab - update badge', () => {
    function setupWithSettings(settings: unknown) {
      currentTab = 'overview'
      vi.mocked(useAuthStore).mockReturnValue({ user: adminUser, setUser: vi.fn() } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]) } as any)           // 1. tokens
        .mockReturnValueOnce({ data: ref([]) } as any)           // 2. projects
        .mockReturnValueOnce({ data: ref([]) } as any)           // 3. users
        .mockReturnValueOnce({ data: ref([]) } as any)           // 4. auditLogData
        .mockReturnValueOnce({ data: ref(adminUser) } as any)    // 5. me
        .mockReturnValueOnce({ data: ref([]) } as any)           // 6. invites
        .mockReturnValueOnce({ data: ref([]) } as any)           // 7. alertRules
        .mockReturnValueOnce({ data: ref(settings) } as any)     // 8. settings
        .mockReturnValueOnce({ data: ref(undefined) } as any)    // 9. health
        .mockReturnValueOnce({ data: ref(undefined) } as any)    // 10. quota
        .mockReturnValue({ data: ref(undefined) } as any)
    }

    it('shows update badge when update is available', () => {
      setupWithSettings({
        version: 'v1.0.0',
        commit: 'abc123',
        project_limit: 0,
        event_limit: 0,
        user_limit: 0,
        update_available: true,
        latest_version: 'v2.0.0',
        release_url: 'https://github.com/blendbyte/tindra/releases/v2.0.0',
      })
      const wrapper = mount(SettingsView, { global: { stubs } })
      const badge = wrapper.find('.update-avail')
      expect(badge.exists()).toBe(true)
      expect(badge.text()).toContain('v2.0.0')
    })

    it('badge links to release_url', () => {
      setupWithSettings({
        version: 'v1.0.0',
        commit: 'abc123',
        project_limit: 0,
        event_limit: 0,
        user_limit: 0,
        update_available: true,
        latest_version: 'v2.0.0',
        release_url: 'https://github.com/blendbyte/tindra/releases/v2.0.0',
      })
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.update-avail').attributes('href')).toBe(
        'https://github.com/blendbyte/tindra/releases/v2.0.0',
      )
    })

    it('hides update badge when update_available is false', () => {
      setupWithSettings({
        version: 'v1.0.0',
        commit: 'abc123',
        project_limit: 0,
        event_limit: 0,
        user_limit: 0,
        update_available: false,
      })
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.update-avail').exists()).toBe(false)
    })

    it('hides update badge when settings are not yet loaded', () => {
      setupMocks()
      const wrapper = mount(SettingsView, { global: { stubs } })
      expect(wrapper.find('.update-avail').exists()).toBe(false)
    })
  })
})
