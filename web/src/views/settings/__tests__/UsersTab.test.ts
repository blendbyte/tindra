import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'

const { showToast, confirm } = vi.hoisted(() => ({ showToast: vi.fn(), confirm: vi.fn() }))

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: vi.fn(), replace: vi.fn() })),
  useRoute: vi.fn(() => ({ params: { tab: 'users' }, query: {} })),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(),
  useQueryClient: vi.fn(),
}))

vi.mock('@/composables/useConfirm', () => ({
  useConfirm: vi.fn(() => ({ confirm })),
}))

vi.mock('@/composables/useToast', () => ({
  useToast: vi.fn(() => ({ show: showToast })),
}))

vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  apiFetch: vi.fn(),
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: vi.fn() }))
vi.mock('@/utils/formatters', () => ({ formatRel: vi.fn(() => '2m ago') }))

import UsersTab from '../UsersTab.vue'
import { ApiError, apiFetch } from '@/api/client'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { adminUser, isEnabled, mockQueries, queryOptions, recordMutations, stubs, type RecordedMutation } from './harness'

// Order of the useMutation calls inside UsersTab.
const M = {
  deleteUser: 0,
  savePermissions: 1,
  adminSetPassword: 2,
  adminRemoveMFA: 3,
  sendPasswordReset: 4,
  createInvite: 5,
  revokeInvite: 6,
  setAccess: 7,
} as const

let mutations: RecordedMutation[]
let qc: { invalidateQueries: ReturnType<typeof vi.fn>; setQueryData: ReturnType<typeof vi.fn> }

beforeEach(() => {
  showToast.mockReset()
  confirm.mockReset()
  qc = { invalidateQueries: vi.fn(), setQueryData: vi.fn() }
  vi.mocked(useQueryClient).mockReturnValue(qc as any)
  mutations = recordMutations()
  mockQueries()
})

function mountTab() {
  return mount(UsersTab, { global: { stubs } })
}

// Makes a mutation call its onSuccess (with result) or onError when mutate runs.
function succeed(name: keyof typeof M, result?: unknown) {
  const m = mutations[M[name]]
  m.mutate.mockImplementation((vars: unknown) => m.opts.onSuccess?.(result, vars))
}

function fail(name: keyof typeof M, error: Error) {
  const m = mutations[M[name]]
  m.mutate.mockImplementation((vars: unknown) => m.opts.onError?.(error, vars))
}

function mockClipboard() {
  const clipWrite = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { value: { writeText: clipWrite }, configurable: true })
  return clipWrite
}

async function openUserMenuItem(wrapper: VueWrapper, label: string) {
  await wrapper.find('.user-menu__trigger').trigger('click')
  const item = wrapper.findAll('.user-menu__item').find(b => b.text().includes(label))
  expect(item).toBeDefined()
  await item!.trigger('click')
}

async function openInviteForm(wrapper: VueWrapper) {
  await wrapper.findAll('.btn').find(b => b.text().includes('Invite user'))!.trigger('click')
}

async function submitInvite(wrapper: VueWrapper, email: string) {
  await openInviteForm(wrapper)
  await wrapper.find('#invite-email').setValue(email)
  await wrapper.find('form.proj-card--form').trigger('submit')
}

async function fillSetPassword(wrapper: VueWrapper, password: string, confirm: string) {
  const pwInputs = wrapper.findAll('input[type="password"]')
  expect(pwInputs).toHaveLength(2)
  await pwInputs[0].setValue(password)
  await pwInputs[1].setValue(confirm)
  await wrapper.findAll('.btn--primary').find(b => b.text().includes('Set password'))!.trigger('click')
}

const otherUser = {
  id: 'user-2', email: 'other@example.com', name: 'Other User',
  mfa_enabled: false, created_at: '2024-01-01T00:00:00Z',
  weekly_digest: false, timezone: 'UTC',
  permissions: { manage_projects: false, manage_users: false, manage_alerts: false, manage_issues: false },
}

describe('UsersTab', () => {
  describe('direct render', () => {
    it('renders users permission table on users tab', () => {
      const wrapper = mountTab()
      expect(wrapper.find('.team-perms-table').exists()).toBe(true)
    })

    it('shows 2FA column header in permission table', () => {
      const wrapper = mountTab()
      expect(wrapper.find('.perm-col[title="Two-factor authentication"]').exists()).toBe(true)
    })

    it('shows Invite user button when user can manage', () => {
      const wrapper = mountTab()
      expect(wrapper.findAll('.btn').some(b => b.text().includes('Invite user'))).toBe(true)
    })
  })

  describe('with users data', () => {
    const bob = {
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

    it('renders user rows when users exist', () => {
      mockQueries({ users: [adminUser, bob] })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('Bob')
    })

    it('shows permission checkboxes for each user', () => {
      mockQueries({ users: [adminUser, bob] })
      const wrapper = mountTab()
      expect(wrapper.findAll('.perm-check')).toHaveLength(8)
    })

    it('toggles invite form when Invite user button is clicked', async () => {
      const wrapper = mountTab()
      expect(wrapper.find('.proj-card--form').exists()).toBe(false)
      await openInviteForm(wrapper)
      expect(wrapper.find('.proj-card--form').exists()).toBe(true)
    })

    it('hides invite form when Cancel is clicked', async () => {
      const wrapper = mountTab()
      await openInviteForm(wrapper)
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('.proj-card--form').exists()).toBe(false)
    })
  })

  describe('menu and set password', () => {
    const bob = {
      id: 'user-2',
      name: 'Bob',
      email: 'bob@example.com',
      mfa_enabled: false,
      weekly_digest: false,
      timezone: 'UTC',
      created_at: '2024-01-01T00:00:00Z',
      permissions: { manage_projects: false, manage_users: false, manage_alerts: false, manage_issues: true },
    }

    beforeEach(() => {
      mockQueries({ users: [adminUser, bob] })
    })

    it('opens user action menu when trigger button is clicked', async () => {
      const wrapper = mountTab()
      // Only the other user's row has a menu, never your own.
      expect(wrapper.findAll('.user-menu__trigger')).toHaveLength(1)
      await wrapper.find('.user-menu__trigger').trigger('click')
      expect(wrapper.find('.user-menu__dropdown').exists()).toBe(true)
    })

    it('closes menu and opens set-password form when Set password is clicked', async () => {
      const wrapper = mountTab()
      await openUserMenuItem(wrapper, 'Set password')
      expect(wrapper.find('.user-menu__dropdown').exists()).toBe(false)
      expect(wrapper.find('.team-action-panel').exists()).toBe(true)
      expect(wrapper.find('.team-action-panel__title').text()).toBe('Set password for bob@example.com')
    })

    it('cancels set-password form when Cancel is clicked', async () => {
      const wrapper = mountTab()
      await openUserMenuItem(wrapper, 'Set password')
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('.team-action-panel').exists()).toBe(false)
    })

    it('calls togglePerm when permission checkbox is clicked', async () => {
      const wrapper = mountTab()
      const checkboxes = wrapper.findAll('.perm-check')
      // Clicking flips the box and fires change; setValue does both in happy-dom.
      await checkboxes[0].setValue(false)
      expect(wrapper.find('.team-perms-table').exists()).toBe(true)
      expect(mutations[M.savePermissions].mutate).toHaveBeenCalledWith({
        userID: 'user-1',
        perms: { ...adminUser.permissions, manage_projects: false },
      })
    })

    it('shows delete step 1 panel when Remove user is clicked', async () => {
      const wrapper = mountTab()
      await openUserMenuItem(wrapper, 'Remove user')
      expect(wrapper.find('.team-action-panel--danger').exists()).toBe(true)
      expect(wrapper.text()).toContain('Remove bob@example.com?')
    })

    it('advances to delete step 2 when Yes, continue is clicked', async () => {
      const wrapper = mountTab()
      await openUserMenuItem(wrapper, 'Remove user')
      await wrapper.findAll('.btn--ghost').find(b => b.text().includes('Yes, continue'))!.trigger('click')
      expect(wrapper.find('.team-action-panel--danger').exists()).toBe(true)
      expect(wrapper.text()).toContain('Final confirmation')
    })

    it('cancels delete when Cancel is clicked in delete step 1', async () => {
      const wrapper = mountTab()
      await openUserMenuItem(wrapper, 'Remove user')
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('.team-action-panel--danger').exists()).toBe(false)
      expect(mutations[M.deleteUser].mutate).not.toHaveBeenCalled()
    })
  })

  describe('invite form interactions', () => {
    it('enables Send invite button when email is typed', async () => {
      const wrapper = mountTab()
      await openInviteForm(wrapper)
      const submitBtn = wrapper.find('.proj-card--form .btn--primary')
      expect(submitBtn.exists()).toBe(true)
      expect(submitBtn.attributes('disabled')).toBeDefined()
      await wrapper.find('#invite-email').setValue('test@example.com')
      expect(submitBtn.attributes('disabled')).toBeUndefined()
    })

    it('submits invite form when email is filled and form submitted', async () => {
      const wrapper = mountTab()
      await openInviteForm(wrapper)
      await wrapper.find('#invite-email').setValue('test@example.com')
      await wrapper.find('#invite-name').setValue('Test User')
      await wrapper.find('.proj-card--form').trigger('submit')
      expect(mutations[M.createInvite].mutate).toHaveBeenCalledWith({ email: 'test@example.com', name: 'Test User' })
      // The mocked mutation never succeeds, so the form stays open.
      expect(wrapper.find('.proj-card--form').exists()).toBe(true)
    })
  })

  describe('permission toggles and admin actions', () => {
    beforeEach(() => {
      mockQueries({ users: [adminUser, otherUser] })
    })

    it('calls togglePerm via change event on checkbox', async () => {
      const wrapper = mountTab()
      await wrapper.findAll('input.perm-check')[0].trigger('change')
      expect(mutations[M.savePermissions].mutate).toHaveBeenCalledWith({
        userID: 'user-1',
        perms: { ...adminUser.permissions, manage_projects: false },
      })
    })

    it('savePermissions onSuccess calls setQueryData', async () => {
      const updated = { ...otherUser, permissions: { ...otherUser.permissions, manage_projects: true } }
      const wrapper = mountTab()
      succeed('savePermissions', updated)
      await wrapper.findAll('input.perm-check')[4].trigger('change')
      expect(qc.setQueryData).toHaveBeenCalledWith(['users'], expect.any(Function))
      expect(qc.setQueryData).toHaveBeenCalledWith(['me'], expect.any(Function))
      const usersUpdater = qc.setQueryData.mock.calls.find(([key]) => key[0] === 'users')![1]
      expect(usersUpdater([adminUser, otherUser])).toEqual([adminUser, updated])
      expect(showToast).toHaveBeenCalledWith('Permissions saved')
    })

    it('savePermissions onError shows toast', async () => {
      const wrapper = mountTab()
      fail('savePermissions', new Error('Permission denied'))
      await wrapper.findAll('input.perm-check')[0].trigger('change')
      expect(showToast).toHaveBeenCalledWith('Permissions not saved. Try again.')
    })

    it('savePermissions shows the server reason when the last user manager would be removed', async () => {
      const wrapper = mountTab()
      fail('savePermissions', new ApiError(409, 'At least one user must keep the Users permission.\n'))
      await wrapper.findAll('input.perm-check')[1].trigger('change')
      expect(showToast).toHaveBeenCalledWith('At least one user must keep the Users permission.', 'error')
    })

    it('keeps the checkbox on the saved value when a change is refused', async () => {
      const wrapper = mountTab()
      fail('savePermissions', new ApiError(409, 'At least one user must keep the Users permission.'))
      const box = wrapper.findAll('input.perm-check')[1]
      expect((box.element as HTMLInputElement).checked).toBe(true)
      await box.setValue(false)
      expect(mutations[M.savePermissions].mutate).toHaveBeenCalledWith({
        userID: 'user-1',
        perms: { ...adminUser.permissions, manage_users: false },
      })
      expect((box.element as HTMLInputElement).checked).toBe(true)
    })

    it('adminSetPassword onError sets setPwError', async () => {
      const wrapper = mountTab()
      fail('adminSetPassword', new Error('Could not set password'))
      await openUserMenuItem(wrapper, 'Set password')
      await fillSetPassword(wrapper, 'mynewpassword123', 'mynewpassword123')
      expect(mutations[M.adminSetPassword].mutate).toHaveBeenCalledWith({ userID: 'user-2', password: 'mynewpassword123' })
      expect(wrapper.find('.proj-form-error').text()).toBe('Could not set password')
    })

    it('adminRemoveMFA onSuccess updates user list', async () => {
      const mfaUser = { ...otherUser, id: 'user-3', mfa_enabled: true }
      mockQueries({ users: [adminUser, mfaUser] })
      const wrapper = mountTab()
      succeed('adminRemoveMFA')
      await openUserMenuItem(wrapper, 'Remove MFA')
      expect(mutations[M.adminRemoveMFA].mutate).toHaveBeenCalledWith('user-3')
      expect(qc.setQueryData).toHaveBeenCalledWith(['users'], expect.any(Function))
      const updater = qc.setQueryData.mock.calls[0][1]
      expect(updater([adminUser, mfaUser])).toEqual([adminUser, { ...mfaUser, mfa_enabled: false }])
      expect(showToast).toHaveBeenCalledWith('MFA removed')
    })

    it('adminRemoveMFA onError shows toast', async () => {
      const mfaUser = { ...otherUser, id: 'user-3', mfa_enabled: true }
      mockQueries({ users: [adminUser, mfaUser] })
      const wrapper = mountTab()
      fail('adminRemoveMFA', new Error('Cannot remove MFA'))
      await openUserMenuItem(wrapper, 'Remove MFA')
      expect(showToast).toHaveBeenCalledWith('Could not remove MFA. Try again.')
    })

    it('sendPasswordReset onError shows toast', async () => {
      const wrapper = mountTab()
      fail('sendPasswordReset', new Error('Email failed'))
      await openUserMenuItem(wrapper, 'Send password reset')
      expect(mutations[M.sendPasswordReset].mutate).toHaveBeenCalledWith('user-2')
      expect(showToast).toHaveBeenCalledWith('Could not send reset email. Try again.')
    })

    it('sendPasswordReset onSuccess shows reset link when email_sent false', async () => {
      const wrapper = mountTab()
      succeed('sendPasswordReset', { email_sent: false, reset_url: 'https://app.example.com/reset/abc', email_error: '' })
      await openUserMenuItem(wrapper, 'Send password reset')
      expect(wrapper.find('.invite-url-box').exists()).toBe(true)
      expect(wrapper.find('.invite-url-box__url').text()).toBe('https://app.example.com/reset/abc')
      expect(wrapper.find('.invite-url-box__label').text()).toContain('other@example.com')
    })
  })

  describe('invite callbacks', () => {
    it('createInvite onError shows invite error', async () => {
      const wrapper = mountTab()
      fail('createInvite', new Error('User already exists'))
      await submitInvite(wrapper, 'existing@example.com')
      expect(mutations[M.createInvite].mutate).toHaveBeenCalledWith({ email: 'existing@example.com', name: '' })
      expect(wrapper.find('.proj-form-error').text()).toBe('User already exists')
    })

    it('createInvite onSuccess shows invite URL box', async () => {
      const clipWrite = mockClipboard()
      const wrapper = mountTab()
      succeed('createInvite', { invite_url: 'https://app.example.com/invite/abc', email_sent: false, email_configured: false, email_error: '' })
      await submitInvite(wrapper, 'new@example.com')
      expect(wrapper.find('.invite-url-box').exists()).toBe(true)
      expect(qc.invalidateQueries).toHaveBeenCalledWith({ queryKey: ['invites'] })
      await wrapper.find('.invite-url-box__copy').trigger('click')
      expect(clipWrite).toHaveBeenCalledWith('https://app.example.com/invite/abc')
      await flushPromises()
      expect(showToast).toHaveBeenCalledWith('Invite link copied')
    })

    it('revokeInvite onSuccess invalidates invites query', async () => {
      const invItem = { id: 'inv-1', token: 'tok1', email: 'pending@example.com', name: 'Pending', created_at: '2024-01-01T00:00:00Z', expires_at: '2024-02-01T00:00:00Z' }
      mockQueries({ invites: [invItem] })
      const wrapper = mountTab()
      succeed('revokeInvite')
      await wrapper.find('[aria-label="Invite actions"]').trigger('click')
      await wrapper.findAll('.user-menu__item').find(b => b.text().includes('Revoke'))!.trigger('click')
      expect(mutations[M.revokeInvite].mutate).toHaveBeenCalledWith('inv-1')
      expect(qc.invalidateQueries).toHaveBeenCalledWith({ queryKey: ['invites'] })
      expect(showToast).toHaveBeenCalledWith('Invite revoked')
    })
  })

  describe('deleteUser flow', () => {
    const otherUser2 = {
      id: 'user-del', email: 'delete@example.com', name: 'Delete Me',
      mfa_enabled: false, created_at: '2024-01-01T00:00:00Z',
      weekly_digest: false, timezone: 'UTC',
      permissions: { manage_projects: false, manage_users: false, manage_alerts: false, manage_issues: false },
    }

    beforeEach(() => {
      mockQueries({ users: [adminUser, otherUser2] })
    })

    it('deleteUser onSuccess invalidates users query', async () => {
      const wrapper = mountTab()
      succeed('deleteUser')
      await openUserMenuItem(wrapper, 'Remove user')
      await wrapper.findAll('.btn--ghost').find(b => b.text().includes('Yes, continue'))!.trigger('click')
      const removeConfirmBtn = wrapper.findAll('.btn--ghost').find(b => b.text() === 'Remove')!
      expect(removeConfirmBtn.attributes('disabled')).toBeDefined()
      await wrapper.find('.team-action-panel input').setValue(otherUser2.email)
      await removeConfirmBtn.trigger('click')
      expect(mutations[M.deleteUser].mutate).toHaveBeenCalledWith('user-del')
      expect(qc.invalidateQueries).toHaveBeenCalledWith({ queryKey: ['users'] })
      expect(showToast).toHaveBeenCalledWith('User removed')
      expect(wrapper.find('.team-action-panel--danger').exists()).toBe(false)
    })

    it('cancelUserDelete resets delete state', async () => {
      const wrapper = mountTab()
      await openUserMenuItem(wrapper, 'Remove user')
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('.team-action-panel--danger').exists()).toBe(false)
    })
  })

  describe('adminSetPassword onSuccess', () => {
    const otherUser3 = {
      id: 'user-pw', email: 'pw@example.com', name: 'PW User',
      mfa_enabled: false, created_at: '2024-01-01T00:00:00Z',
      weekly_digest: false, timezone: 'UTC',
      permissions: { manage_projects: false, manage_users: false, manage_alerts: false, manage_issues: false },
    }

    beforeEach(() => {
      mockQueries({ users: [adminUser, otherUser3] })
    })

    it('adminSetPassword onSuccess closes set-password panel', async () => {
      const wrapper = mountTab()
      succeed('adminSetPassword')
      await openUserMenuItem(wrapper, 'Set password')
      await fillSetPassword(wrapper, 'mynewpassword123', 'mynewpassword123')
      expect(wrapper.find('.team-action-panel').exists()).toBe(false)
      expect(showToast).toHaveBeenCalledWith('Password updated')
    })

    it('submitSetPw shows error when passwords do not match', async () => {
      const wrapper = mountTab()
      await openUserMenuItem(wrapper, 'Set password')
      await fillSetPassword(wrapper, 'password12345', 'different1234')
      expect(wrapper.find('.proj-form-error').text()).toBe('Passwords do not match.')
      expect(mutations[M.adminSetPassword].mutate).not.toHaveBeenCalled()
    })

    it('submitSetPw shows error when password is too short', async () => {
      const wrapper = mountTab()
      await openUserMenuItem(wrapper, 'Set password')
      await fillSetPassword(wrapper, 'short', 'short')
      expect(wrapper.find('.proj-form-error').text()).toBe('Password must be at least 12 characters.')
      expect(mutations[M.adminSetPassword].mutate).not.toHaveBeenCalled()
    })
  })

  describe('reset result email_sent = true', () => {
    const baseUser = {
      id: 'u-target', email: 'target@example.com', name: 'Target',
      mfa_enabled: false, created_at: '2024-01-01T00:00:00Z',
      weekly_digest: false, timezone: 'UTC',
      permissions: { manage_projects: false, manage_users: false, manage_alerts: false, manage_issues: false },
    }

    beforeEach(() => {
      mockQueries({ users: [adminUser, baseUser] })
    })

    it('sendPasswordReset shows email-sent success when email_sent is true', async () => {
      const wrapper = mountTab()
      succeed('sendPasswordReset', { email_sent: true, reset_url: '', email_error: undefined })
      await openUserMenuItem(wrapper, 'Send password reset')
      expect(wrapper.find('.invite-success').exists()).toBe(true)
      expect(wrapper.find('.invite-success__title').text()).toBe('Reset email sent to target@example.com')
    })

    it('closeResetResult hides the reset result banner', async () => {
      const wrapper = mountTab()
      succeed('sendPasswordReset', { email_sent: false, reset_url: 'https://app.example.com/reset/tok', email_error: '' })
      await openUserMenuItem(wrapper, 'Send password reset')
      expect(wrapper.find('.invite-url-box').exists()).toBe(true)
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Dismiss')!.trigger('click')
      expect(wrapper.find('.invite-url-box').exists()).toBe(false)
    })

    it('copy reset link button calls clipboard', async () => {
      const clipWrite = mockClipboard()
      const wrapper = mountTab()
      succeed('sendPasswordReset', { email_sent: false, reset_url: 'https://app.example.com/reset/abc', email_error: '' })
      await openUserMenuItem(wrapper, 'Send password reset')
      await wrapper.find('.invite-url-box__copy').trigger('click')
      expect(clipWrite).toHaveBeenCalledWith('https://app.example.com/reset/abc')
      await flushPromises()
      expect(showToast).toHaveBeenCalledWith('Reset link copied')
    })
  })

  describe('invite list interactions', () => {
    const pendingInvite = {
      id: 'inv-1', token: 'tok-abc', email: 'pending@example.com', name: 'Pending User',
      created_at: '2024-01-01T00:00:00Z', expires_at: '2024-02-01T00:00:00Z',
    }

    // The invite list menu only offers Revoke: an invite link is shown once,
    // right after the invite is created, and is never exposed in the list.
    it('pending invite menu offers only revoke', async () => {
      const clipWrite = mockClipboard()
      mockQueries({ users: [adminUser], invites: [pendingInvite] })
      const wrapper = mountTab()
      const invTrigger = wrapper.findAll('.user-menu__trigger').slice(-1)[0]
      expect(invTrigger.attributes('aria-label')).toBe('Invite actions')
      await invTrigger.trigger('click')
      expect(wrapper.findAll('.user-menu__item').map(b => b.text())).toEqual(['Revoke'])
      expect(clipWrite).not.toHaveBeenCalled()
    })

    it('invite form done button closes invite form', async () => {
      mockQueries({ users: [adminUser] })
      const wrapper = mountTab()
      succeed('createInvite', { invite_url: 'https://app.example.com/invite/new', email_sent: false, email_configured: false, email_error: '' })
      await submitInvite(wrapper, 'new@example.com')
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Done')!.trigger('click')
      expect(wrapper.find('.proj-card--form').exists()).toBe(false)
    })

    it('send another button resets inviteResult', async () => {
      mockQueries({ users: [adminUser] })
      const wrapper = mountTab()
      succeed('createInvite', { invite_url: 'https://app.example.com/invite/x', email_sent: false, email_configured: false, email_error: '' })
      await submitInvite(wrapper, 'again@example.com')
      expect(wrapper.find('.invite-url-box').exists()).toBe(true)
      await wrapper.findAll('.btn--ghost').find(b => b.text().includes('Send another'))!.trigger('click')
      expect(wrapper.find('.invite-url-box').exists()).toBe(false)
      expect(wrapper.find('#invite-email').exists()).toBe(true)
    })
  })

  describe('MFA badge and user row rendering', () => {
    it('shows MFA badge for user with mfa_enabled', () => {
      const mfaUser = {
        id: 'user-mfa', email: 'mfa@example.com', name: 'MFA User',
        mfa_enabled: true, created_at: '2024-01-01T00:00:00Z',
        weekly_digest: false, timezone: 'UTC',
        permissions: { manage_projects: false, manage_users: false, manage_alerts: false, manage_issues: false },
      }
      mockQueries({ users: [adminUser, mfaUser] })
      const wrapper = mountTab()
      expect(wrapper.find('.team-badge--mfa').exists()).toBe(true)
      expect(wrapper.find('.team-badge--mfa').text()).toBe('2FA')
    })

    it('shows "you" badge for current user row', () => {
      mockQueries({ users: [adminUser] })
      const wrapper = mountTab()
      expect(wrapper.find('.team-badge').exists()).toBe(true)
      expect(wrapper.find('.team-badge').text()).toBe('you')
      expect(wrapper.find('tr.team-row--self').exists()).toBe(true)
    })
  })

  describe('invite list token privacy', () => {
    // The invite link is only shown once, right after creating the invite, so
    // the list must not expose the token or offer to copy it.
    it('does not expose or copy the invite token from the list', async () => {
      const clipWrite = mockClipboard()
      const invite = {
        id: 'inv-x', token: 'tok-xyz', email: 'invited@example.com', name: 'Invited',
        created_at: '2024-01-01T00:00:00Z', expires_at: '2024-02-01T00:00:00Z',
      }
      mockQueries({ users: [adminUser], invites: [invite] })
      const wrapper = mountTab()
      const triggers = wrapper.findAll('.user-menu__trigger')
      await triggers[triggers.length - 1].trigger('click')
      expect(wrapper.findAll('.user-menu__item').some(b => b.text().includes('Copy link'))).toBe(false)
      expect(wrapper.text()).not.toContain('tok-xyz')
      expect(clipWrite).not.toHaveBeenCalled()
    })
  })

  describe('permission checkboxes for manage_users, manage_alerts, manage_issues', () => {
    const userWithPerms = {
      id: 'user-perms', email: 'perms@example.com', name: 'Perms User',
      mfa_enabled: false, created_at: '2024-01-01T00:00:00Z',
      weekly_digest: false, timezone: 'UTC',
      permissions: { manage_projects: false, manage_users: false, manage_alerts: false, manage_issues: false },
    }

    beforeEach(() => {
      mockQueries({ users: [adminUser, userWithPerms] })
    })

    // Four checkboxes per row: the admin row first (0-3), then userWithPerms (4-7).
    it('toggles manage_users permission via second perm-check checkbox', async () => {
      const wrapper = mountTab()
      const checks = wrapper.findAll('input.perm-check')
      expect(checks).toHaveLength(8)
      await checks[5].trigger('change')
      expect(mutations[M.savePermissions].mutate).toHaveBeenCalledWith({
        userID: 'user-perms',
        perms: { ...userWithPerms.permissions, manage_users: true },
      })
    })

    it('toggles manage_alerts permission via third perm-check checkbox', async () => {
      const wrapper = mountTab()
      await wrapper.findAll('input.perm-check')[6].trigger('change')
      expect(mutations[M.savePermissions].mutate).toHaveBeenCalledWith({
        userID: 'user-perms',
        perms: { ...userWithPerms.permissions, manage_alerts: true },
      })
    })

    it('toggles manage_issues permission via fourth perm-check checkbox', async () => {
      const wrapper = mountTab()
      await wrapper.findAll('input.perm-check')[7].trigger('change')
      expect(mutations[M.savePermissions].mutate).toHaveBeenCalledWith({
        userID: 'user-perms',
        perms: { ...userWithPerms.permissions, manage_issues: true },
      })
    })
  })

  describe('invite menu revoke', () => {
    const inv = { id: 'inv-alice', token: 'tok-1', email: 'alice@example.com', name: 'Alice', expires_at: '2099-01-01T00:00:00Z' }

    beforeEach(() => {
      mockQueries({ invites: [inv] })
    })

    it('renders invite row email address', () => {
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('alice@example.com')
    })

    it('opens invite dropdown menu when trigger is clicked', async () => {
      const wrapper = mountTab()
      await wrapper.find('.user-menu__trigger').trigger('click')
      expect(wrapper.find('.user-menu__dropdown').exists()).toBe(true)
    })

    it('shows Revoke button in invite dropdown after menu opens', async () => {
      const wrapper = mountTab()
      await wrapper.find('.user-menu__trigger').trigger('click')
      expect(wrapper.find('.user-menu__dropdown').text()).toContain('Revoke')
    })

    it('clicking Revoke in invite dropdown calls revokeInvite', async () => {
      const wrapper = mountTab()
      await wrapper.find('.user-menu__trigger').trigger('click')
      await wrapper.findAll('.user-menu__item').find(b => b.text().includes('Revoke'))!.trigger('click')
      expect(mutations[M.revokeInvite].mutate).toHaveBeenCalledWith('inv-alice')
      expect(wrapper.find('.user-menu__dropdown').exists()).toBe(false)
    })
  })

  describe('permissions and limits', () => {
    const viewer = { ...adminUser, permissions: { ...adminUser.permissions, manage_users: false } }

    it('only fetches invites for users who can manage users', () => {
      mountTab()
      expect(isEnabled(queryOptions('invites'))).toBe(true)
      vi.mocked(useQuery).mockClear()
      mockQueries({ me: viewer })
      mountTab()
      expect(isEnabled(queryOptions('invites'))).toBe(false)
    })

    it('shows a read-only state without the table for users who cannot manage users', () => {
      mockQueries({ me: viewer, users: [adminUser, otherUser] })
      const wrapper = mountTab()
      expect(wrapper.find('.perm-readonly-badge').exists()).toBe(true)
      expect(wrapper.find('.team-perms-table').exists()).toBe(false)
      expect(wrapper.text()).toContain("You don't have permission to view or manage users.")
      expect(wrapper.findAll('.btn').some(b => b.text().includes('Invite user'))).toBe(false)
    })

    it('disables Invite user once the user limit is reached', async () => {
      mockQueries({ users: [adminUser, otherUser], settings: { user_limit: 2 } })
      const wrapper = mountTab()
      expect(wrapper.find('.pane-head__sub').text()).toBe('2 / 2 users')
      const inviteBtn = wrapper.findAll('.btn').find(b => b.text().includes('Invite user'))!
      expect(inviteBtn.attributes('disabled')).toBeDefined()
      expect(inviteBtn.attributes('title')).toBe('User limit of 2 reached')
      await inviteBtn.trigger('click')
      expect(wrapper.find('.proj-card--form').exists()).toBe(false)
    })

    it('closes the row menu on any document click', async () => {
      mockQueries({ users: [adminUser, otherUser] })
      const wrapper = mountTab()
      await wrapper.find('.user-menu__trigger').trigger('click')
      expect(wrapper.find('.user-menu__dropdown').exists()).toBe(true)
      document.body.click()
      await wrapper.vm.$nextTick()
      expect(wrapper.find('.user-menu__dropdown').exists()).toBe(false)
    })
  })
})

describe('set password panel', () => {
  it('cancelSetPw button closes set-password panel', async () => {
    const other = { id: 'u2', email: 'u2@example.com', name: 'U2', mfa_enabled: false, created_at: '2024-01-01T00:00:00Z', weekly_digest: false, timezone: 'UTC', permissions: { manage_projects: false, manage_users: false, manage_alerts: false, manage_issues: false } }
    mockQueries({ users: [adminUser, other] })
    const wrapper = mountTab()
    await wrapper.find('.user-menu__trigger').trigger('click')
    await wrapper.findAll('.user-menu__item').find(b => b.text().includes('Set password'))!.trigger('click')
    expect(wrapper.find('.team-action-panel').exists()).toBe(true)
    await wrapper.findAll('.team-action-panel .btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
    expect(wrapper.find('.team-action-panel').exists()).toBe(false)
  })

  describe('project access', () => {
    const restricted = {
      id: 'user-2', name: 'Bob', email: 'bob@example.com', project_access: 'teams',
      mfa_enabled: false, weekly_digest: false, timezone: 'UTC', created_at: '2024-01-01T00:00:00Z',
      permissions: { manage_projects: false, manage_users: false, manage_alerts: false, manage_issues: true },
    }
    const ops = { id: 'g-1', name: 'Ops', member_ids: ['user-2'], created_at: '2024-01-01T00:00:00Z' }
    const accessSelect = (wrapper: VueWrapper, email: string) =>
      wrapper.find(`select[aria-label="Project access of ${email}"]`)

    it('hides the setting while there is no team and nobody is restricted', () => {
      mockQueries({ users: [adminUser], teams: [] })
      const wrapper = mountTab()
      expect(wrapper.findAll('th').map(th => th.text())).not.toContain('Project access')
      expect(wrapper.find('select.perm-access').exists()).toBe(false)
    })

    it('shows a select per user once a team exists', () => {
      mockQueries({ users: [adminUser, restricted], teams: [ops] })
      const wrapper = mountTab()
      expect(wrapper.findAll('th').map(th => th.text())).toContain('Project access')
      expect((accessSelect(wrapper, 'admin@example.com').element as HTMLSelectElement).value).toBe('all')
      expect((accessSelect(wrapper, 'bob@example.com').element as HTMLSelectElement).value).toBe('teams')
      expect(wrapper.findAll('select.perm-access option').map(o => o.text())).toEqual([
        'All projects', "Their teams' projects", 'All projects', "Their teams' projects",
      ])
    })

    it('stays visible when someone is restricted although every team was deleted', () => {
      mockQueries({ users: [adminUser, restricted], teams: [] })
      expect(mountTab().find('select.perm-access').exists()).toBe(true)
    })

    it('greys out the administration permissions of a restricted user', () => {
      mockQueries({ users: [adminUser, restricted], teams: [ops] })
      const [projects, users, alerts, issues] = mountTab().findAll('tbody tr').at(1)!.findAll('.perm-check')
      expect(projects.attributes('disabled')).toBeDefined()
      expect(projects.attributes('title')).toBe('Needs access to all projects')
      expect(users.attributes('disabled')).toBeDefined()
      expect(alerts.attributes('disabled')).toBeUndefined()
      expect(issues.attributes('disabled')).toBeUndefined()
    })

    it('keeps every permission editable for a user with access to all projects', () => {
      mockQueries({ users: [adminUser, restricted], teams: [ops] })
      const boxes = mountTab().findAll('tbody tr').at(0)!.findAll('.perm-check')
      expect(boxes.every(b => b.attributes('disabled') === undefined)).toBe(true)
    })

    it('saves a new access and leaves the select on the saved one until then', async () => {
      mockQueries({ users: [adminUser, restricted], teams: [ops] })
      const wrapper = mountTab()
      const select = accessSelect(wrapper, 'bob@example.com')
      await select.setValue('all')
      expect(mutations[M.setAccess].mutate).toHaveBeenCalledWith({ userID: 'user-2', projectAccess: 'all' })
      expect((select.element as HTMLSelectElement).value).toBe('teams')
    })

    it('does nothing when the access is unchanged', async () => {
      mockQueries({ users: [adminUser, restricted], teams: [ops] })
      const wrapper = mountTab()
      await accessSelect(wrapper, 'bob@example.com').setValue('teams')
      expect(mutations[M.setAccess].mutate).not.toHaveBeenCalled()
    })

    describe('limiting a user to their teams', () => {
      const everything = { ...restricted, project_access: 'all' }
      const effect = {
        removed_recipients: [{ user_id: 'user-2', user_name: 'Bob', rule_id: 'r-1', rule_name: 'Prod alerts' }],
        paused_rules: [{ id: 'r-1', name: 'Prod alerts' }],
      }

      async function limit() {
        mockQueries({ users: [adminUser, everything], teams: [ops] })
        const wrapper = mountTab()
        await accessSelect(wrapper, 'bob@example.com').setValue('teams')
        await flushPromises()
      }

      it('previews the change and saves without asking when no alert rule is affected', async () => {
        vi.mocked(apiFetch).mockResolvedValue({ removed_recipients: [], paused_rules: [] })
        await limit()
        expect(apiFetch).toHaveBeenCalledWith('/api/users/user-2/access?dry_run=true', {
          method: 'PUT', body: JSON.stringify({ project_access: 'teams' }),
        })
        expect(confirm).not.toHaveBeenCalled()
        expect(mutations[M.setAccess].mutate).toHaveBeenCalledWith({ userID: 'user-2', projectAccess: 'teams' })
      })

      it('says what happens to the alert rules and saves once confirmed', async () => {
        vi.mocked(apiFetch).mockResolvedValue(effect)
        confirm.mockResolvedValue(true)
        await limit()
        expect(confirm).toHaveBeenCalledWith(expect.objectContaining({
          message: expect.stringContaining('This removes Bob from 1 alert rule. 1 push rule will be paused (Prod alerts)'),
        }))
        expect(mutations[M.setAccess].mutate).toHaveBeenCalledWith({ userID: 'user-2', projectAccess: 'teams' })
      })

      it('saves nothing when the admin cancels', async () => {
        vi.mocked(apiFetch).mockResolvedValue(effect)
        confirm.mockResolvedValue(false)
        await limit()
        expect(mutations[M.setAccess].mutate).not.toHaveBeenCalled()
      })

      it('does not preview when opening access up', async () => {
        mockQueries({ users: [adminUser, restricted], teams: [ops] })
        await accessSelect(mountTab(), 'bob@example.com').setValue('all')
        await flushPromises()
        expect(apiFetch).not.toHaveBeenCalled()
        expect(mutations[M.setAccess].mutate).toHaveBeenCalled()
      })
    })

    it('puts the access to /api/users/:id/access', async () => {
      mountTab()
      vi.mocked(apiFetch).mockResolvedValue(restricted)
      await mutations[M.setAccess].opts.mutationFn({ userID: 'user-2', projectAccess: 'all' })
      expect(apiFetch).toHaveBeenCalledWith('/api/users/user-2/access', { method: 'PUT', body: JSON.stringify({ project_access: 'all' }) })
    })

    it('refreshes the users, projects and current user, then says so', () => {
      mountTab()
      mutations[M.setAccess].opts.onSuccess({ ...restricted, project_access: 'all' })
      expect(qc.invalidateQueries.mock.calls.map(([a]) => a.queryKey[0]).sort()).toEqual(['me', 'projects', 'users'])
      expect(showToast).toHaveBeenCalledWith('bob@example.com now sees all projects')
    })

    it('shows the server message when the user administers users or projects', () => {
      mountTab()
      mutations[M.setAccess].opts.onError(new ApiError(409, 'a user who manages users or projects must have access to all projects\n'))
      expect(showToast).toHaveBeenCalledWith('a user who manages users or projects must have access to all projects', 'error')
    })

    it('shows a generic message for any other failure', () => {
      mountTab()
      mutations[M.setAccess].opts.onError(new ApiError(500, 'boom'))
      expect(showToast).toHaveBeenCalledWith('Project access not saved. Try again.')
    })
  })

  describe('inviting with project access', () => {
    const ops = { id: 'g-1', name: 'Ops', member_ids: [], created_at: '2024-01-01T00:00:00Z' }
    const dev = { id: 'g-2', name: 'Dev', member_ids: [], created_at: '2024-01-01T00:00:00Z' }
    const access = (wrapper: VueWrapper) => wrapper.find('#invite-access').element as HTMLSelectElement
    const teamBoxes = (wrapper: VueWrapper) => wrapper.findAll('.proj-card--form .rule__project-check input')

    async function openFilledForm(wrapper: VueWrapper) {
      await openInviteForm(wrapper)
      await wrapper.find('#invite-email').setValue('new@example.com')
    }

    it('is the plain invitation while there are no teams', async () => {
      mockQueries({ teams: [] })
      const wrapper = mountTab()
      await openFilledForm(wrapper)
      expect(wrapper.find('#invite-access').exists()).toBe(false)
      await wrapper.find('form.proj-card--form').trigger('submit')
      expect(mutations[M.createInvite].mutate).toHaveBeenCalledWith({ email: 'new@example.com', name: '', access: undefined })
    })

    it("defaults to the teams' projects and lists the teams as checkboxes", async () => {
      mockQueries({ teams: [ops, dev] })
      const wrapper = mountTab()
      await openInviteForm(wrapper)
      expect(wrapper.findAll('#invite-access option').map(o => o.text())).toEqual(["Only their teams' projects", 'All projects'])
      expect(access(wrapper).value).toBe('teams')
      expect(wrapper.findAll('.proj-card--form .rule__project-check').map(l => l.text())).toEqual(['Ops', 'Dev'])
      expect(wrapper.find('.proj-card--form').text()).toContain('first sign-in')
    })

    it('only fetches the teams for users who can manage users', () => {
      mockQueries({ teams: [ops] })
      mountTab()
      expect(isEnabled(queryOptions('teams'))).toBe(true)
    })

    it('needs at least one team before it can be sent', async () => {
      mockQueries({ teams: [ops, dev] })
      const wrapper = mountTab()
      await openFilledForm(wrapper)
      const send = wrapper.find('form.proj-card--form button[type="submit"]')
      expect(send.attributes('disabled')).toBeDefined()
      await wrapper.find('form.proj-card--form').trigger('submit')
      expect(mutations[M.createInvite].mutate).not.toHaveBeenCalled()
      await teamBoxes(wrapper)[0].setValue(true)
      expect(send.attributes('disabled')).toBeUndefined()
    })

    it('submits several teams with the invite', async () => {
      mockQueries({ teams: [ops, dev] })
      const wrapper = mountTab()
      await openFilledForm(wrapper)
      await teamBoxes(wrapper)[0].setValue(true)
      await teamBoxes(wrapper)[1].setValue(true)
      await wrapper.find('form.proj-card--form').trigger('submit')
      expect(mutations[M.createInvite].mutate).toHaveBeenCalledWith({
        email: 'new@example.com', name: '', access: { project_access: 'teams', team_ids: ['g-1', 'g-2'] },
      })
    })

    it('gives access to all projects only when chosen explicitly', async () => {
      mockQueries({ teams: [ops, dev] })
      const wrapper = mountTab()
      await openFilledForm(wrapper)
      await wrapper.find('#invite-access').setValue('all')
      expect(teamBoxes(wrapper)).toHaveLength(0)
      await wrapper.find('form.proj-card--form').trigger('submit')
      expect(mutations[M.createInvite].mutate).toHaveBeenCalledWith({
        email: 'new@example.com', name: '', access: { project_access: 'all', team_ids: [] },
      })
    })

    it('posts the access and the teams only when there are teams', async () => {
      mountTab()
      vi.mocked(apiFetch).mockResolvedValue(undefined)
      await mutations[M.createInvite].opts.mutationFn({
        email: 'a@example.com', name: 'A', access: { project_access: 'teams', team_ids: ['g-1'] },
      })
      expect(apiFetch).toHaveBeenLastCalledWith('/api/invites', {
        method: 'POST',
        body: JSON.stringify({ email: 'a@example.com', name: 'A', project_access: 'teams', team_ids: ['g-1'] }),
      })
      await mutations[M.createInvite].opts.mutationFn({ email: 'a@example.com', name: 'A' })
      expect(apiFetch).toHaveBeenLastCalledWith('/api/invites', {
        method: 'POST',
        body: JSON.stringify({ email: 'a@example.com', name: 'A' }),
      })
    })

    it('clears the chosen teams once the invitation is sent', async () => {
      mockQueries({ teams: [ops, dev] })
      const wrapper = mountTab()
      await openFilledForm(wrapper)
      await teamBoxes(wrapper)[0].setValue(true)
      mutations[M.createInvite].opts.onSuccess({ invite_url: 'https://x/invite/t', email_sent: false, email_configured: false, email_error: '' })
      await flushPromises()
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Send another')!.trigger('click')
      expect((teamBoxes(wrapper)[0].element as HTMLInputElement).checked).toBe(false)
    })

    it('shows the access of each pending invite', () => {
      const invite = { id: 'i-1', email: 'p1@example.com', name: '', expires_at: '2030-01-01T00:00:00Z' }
      mockQueries({
        teams: [ops, dev],
        invites: [
          { ...invite, project_access: 'teams', team_ids: ['g-1', 'g-2'] },
          { ...invite, id: 'i-2', email: 'p2@example.com', project_access: 'all', team_ids: [] },
          { ...invite, id: 'i-3', email: 'p3@example.com', project_access: 'teams', team_ids: ['g-9'] },
        ],
      })
      const wrapper = mountTab()
      const row = (email: string) => wrapper.findAll('tbody tr').find(r => r.text().includes(email))!
      expect(wrapper.findAll('th').map(th => th.text())).toContain('Project access')
      expect(row('p1@example.com').text()).toContain('Ops, Dev')
      expect(row('p2@example.com').text()).toContain('All projects')
      expect(row('p3@example.com').text()).toContain('Unknown team')
    })
  })
})
