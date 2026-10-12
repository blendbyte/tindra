import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: vi.fn(), replace: vi.fn() })),
  useRoute: vi.fn(() => ({ params: { tab: 'profile' }, query: {} })),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(),
  useQueryClient: vi.fn(),
}))

const showToast = vi.fn()
vi.mock('@/composables/useToast', () => ({
  useToast: vi.fn(() => ({ show: showToast })),
}))

vi.mock('@/api/client', () => ({ apiFetch: vi.fn() }))
vi.mock('@/stores/auth', () => ({ useAuthStore: vi.fn() }))
vi.mock('@/utils/formatters', () => ({ formatRel: vi.fn(() => '2m ago') }))

import ProfileTab from '../ProfileTab.vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { adminUser, mockQueries, recordMutations, runMutations, stubs } from './harness'

// useMutation call order inside ProfileTab.
const M = { updateProfile: 0, changePassword: 1, startMFASetup: 2, confirmMFASetup: 3, disableMFA: 4 }

const invalidateQueries = vi.fn()

beforeEach(() => {
  showToast.mockReset()
  invalidateQueries.mockReset()
  vi.mocked(apiFetch).mockReset()
  vi.mocked(useQueryClient).mockReturnValue({ invalidateQueries, setQueryData: vi.fn() } as any)
  vi.mocked(useMutation).mockImplementation(() => ({ mutate: vi.fn(), isPending: ref(false) } as any))
  mockQueries()
})

function mountTab(options: { attachTo?: HTMLElement } = {}) {
  return mount(ProfileTab, { ...options, global: { stubs } })
}

// Replaces the mutate of the mutations at the given indexes with an
// implementation built from that mutation's options; the rest are no-op spies.
function mockMutations(impls: Record<number, (opts: any) => (vars?: any) => void>) {
  let index = 0
  vi.mocked(useMutation).mockImplementation(((opts: any) => {
    const impl = impls[index++]
    return { mutate: impl ? impl(opts) : vi.fn(), isPending: ref(false) }
  }) as any)
}

const succeeds = (data?: unknown) => (opts: any) => () => opts.onSuccess?.(data)
const fails = (error: unknown) => (opts: any) => () => opts.onError?.(error)

const mfaSetup = { secret: 'SECRETKEY', uri: 'otpauth://...', qr: 'data:image/png;base64,test' }

function findButton(wrapper: ReturnType<typeof mountTab>, selector: string, text: string) {
  return wrapper.findAll(selector).find(b => b.text().includes(text))!
}

async function fillPasswords(wrapper: ReturnType<typeof mountTab>, current: string, next: string, confirm: string) {
  const inputs = wrapper.findAll('input[type="password"]')
  expect(inputs.length).toBe(3)
  await inputs[0].setValue(current)
  await inputs[1].setValue(next)
  await inputs[2].setValue(confirm)
}

function passwordSection(wrapper: ReturnType<typeof mountTab>) {
  return wrapper.findAll('.profile-section').find(s => s.find('.profile-section__title').text() === 'Password')!
}

async function startSetup(wrapper: ReturnType<typeof mountTab>) {
  await findButton(wrapper, '.btn--primary', 'Enable two-factor auth').trigger('click')
}

describe('ProfileTab', () => {
  describe('direct render', () => {
    it('renders Personal information section', () => {
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('Personal information')
    })

    it('renders Notifications section with weekly digest', () => {
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('Notifications')
      expect(wrapper.text()).toContain('Weekly digest')
    })

    it('renders Two-factor authentication section', () => {
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('Two-factor authentication')
    })

    it('shows Password section when user has a password', () => {
      mockQueries({ me: { ...adminUser, has_password: true } })
      const wrapper = mountTab()
      const sections = wrapper.findAll('.profile-section__title')
      expect(sections.some(s => s.text() === 'Password')).toBe(true)
    })

    it('hides Password section when user has no password (OAuth)', () => {
      mockQueries({ me: { ...adminUser, has_password: false } })
      const wrapper = mountTab()
      const sections = wrapper.findAll('.profile-section__title')
      expect(sections.some(s => s.text() === 'Password')).toBe(false)
    })
  })

  describe('form interactions', () => {
    it('shows password mismatch error when passwords differ', async () => {
      mockQueries({ me: { ...adminUser, has_password: true } })
      const wrapper = mountTab()
      await fillPasswords(wrapper, 'currentpassword', 'newpassword1234', 'differentpassword1234')
      await findButton(wrapper, '.btn--primary', 'Change password').trigger('click')
      expect(wrapper.find('.profile-error').text()).toBe('Passwords do not match')
    })

    it('initializes profile fields from me data on profile tab', () => {
      mockQueries({ me: { ...adminUser, name: 'Alice', email: 'alice@example.com' } })
      const wrapper = mountTab()
      expect(wrapper.find<HTMLInputElement>('input[type="email"]').element.value).toBe('alice@example.com')
    })

    it('shows "Save changes" button on profile tab', () => {
      const wrapper = mountTab()
      expect(wrapper.findAll('.btn--primary').some(b => b.text().includes('Save changes'))).toBe(true)
    })

    it('validates password mismatch on submit', async () => {
      const recorded = recordMutations()
      mockQueries({ me: { ...adminUser, has_password: true } })
      const wrapper = mountTab()
      await fillPasswords(wrapper, 'correctpassword', 'newpassword1234', 'differentpassword')
      const saveBtn = passwordSection(wrapper).find('.btn--primary')
      expect(saveBtn.attributes('disabled')).toBeUndefined()
      await saveBtn.trigger('click')
      expect(wrapper.text()).toContain('Passwords do not match')
      expect(recorded[M.changePassword].mutate).not.toHaveBeenCalled()
    })
  })

  describe('email change confirmation', () => {
    async function changeEmail(wrapper: ReturnType<typeof mountTab>, email: string) {
      await wrapper.find('input[type="email"]').setValue(email)
    }

    it('asks for the current password and sends it when the email changes', async () => {
      const recorded = recordMutations()
      mockQueries({ me: { ...adminUser, has_password: true } })
      const wrapper = mountTab()

      expect(wrapper.find('#profile-current-password').exists()).toBe(false)
      await changeEmail(wrapper, 'new@example.com')
      const pw = wrapper.find('#profile-current-password')
      expect(pw.exists()).toBe(true)
      await pw.setValue('my-current-password')

      await findButton(wrapper, '.btn--primary', 'Save changes').trigger('click')
      const profile = recorded[M.updateProfile]
      expect(profile.mutate).toHaveBeenCalledTimes(1)
      await profile.opts.mutationFn(profile.mutate.mock.calls[0][0])
      const [url, init] = vi.mocked(apiFetch).mock.calls[0] as [string, RequestInit]
      expect(url).toBe('/api/me')
      expect(JSON.parse(init.body as string)).toMatchObject({ email: 'new@example.com', current_password: 'my-current-password' })
    })

    it('does not send confirmation fields when the email is unchanged', async () => {
      const recorded = recordMutations()
      mockQueries({ me: { ...adminUser, has_password: true } })
      const wrapper = mountTab()

      await findButton(wrapper, '.btn--primary', 'Save changes').trigger('click')
      const profile = recorded[M.updateProfile]
      expect(profile.mutate).toHaveBeenCalledTimes(1)
      await profile.opts.mutationFn(profile.mutate.mock.calls[0][0])
      const body = JSON.parse((vi.mocked(apiFetch).mock.calls[0][1] as RequestInit).body as string)
      expect(body).not.toHaveProperty('current_password')
      expect(body).not.toHaveProperty('mfa_code')
    })

    it('asks SSO-only users with 2FA for a code', async () => {
      mockQueries({ me: { ...adminUser, has_password: false, mfa_enabled: true } })
      const wrapper = mountTab()
      await changeEmail(wrapper, 'new@example.com')
      expect(wrapper.find('#profile-mfa-code').exists()).toBe(true)
      expect(wrapper.find('#profile-current-password').exists()).toBe(false)
    })

    it('tells SSO-only users without 2FA to ask an admin', async () => {
      mockQueries({ me: { ...adminUser, has_password: false, mfa_enabled: false } })
      const wrapper = mountTab()
      await changeEmail(wrapper, 'new@example.com')
      expect(wrapper.text()).toContain('Ask an admin to update your email')
    })
  })

  describe('password validation', () => {
    it('shows "do not match" error when passwords differ on submit', async () => {
      mockQueries({ me: { ...adminUser, has_password: true } })
      const wrapper = mountTab()
      await fillPasswords(wrapper, 'correctcurrent', 'newpassword1234', 'differentpassword1234')
      await passwordSection(wrapper).find('.btn--primary').trigger('click')
      expect(wrapper.text()).toContain('do not match')
    })

    it('shows "12 characters" error when new password is too short', async () => {
      mockQueries({ me: { ...adminUser, has_password: true } })
      const wrapper = mountTab()
      await fillPasswords(wrapper, 'correctcurrent', 'short1234', 'short1234')
      await passwordSection(wrapper).find('.btn--primary').trigger('click')
      expect(wrapper.text()).toContain('12 characters')
    })
  })

  describe('password change callbacks', () => {
    it('shows error when passwords differ', async () => {
      mockQueries({ me: { ...adminUser, has_password: true } })
      const wrapper = mountTab()
      await fillPasswords(wrapper, 'oldpassword', 'newpassword123', 'different123456')
      await findButton(wrapper, '.btn--primary', 'Change password').trigger('click')
      expect(wrapper.find('.profile-error').exists()).toBe(true)
    })

    it('shows error when new password is too short', async () => {
      mockQueries({ me: { ...adminUser, has_password: true } })
      const wrapper = mountTab()
      await fillPasswords(wrapper, 'oldpassword', 'short', 'short')
      await findButton(wrapper, '.btn--primary', 'Change password').trigger('click')
      expect(wrapper.find('.profile-error').text()).toBe('Password must be at least 12 characters')
    })

    it('calls changePassword onSuccess clears fields', async () => {
      mockMutations({ [M.changePassword]: succeeds() })
      mockQueries({ me: { ...adminUser, has_password: true } })
      const wrapper = mountTab()
      await fillPasswords(wrapper, 'currentpassword', 'newpassword1234', 'newpassword1234')
      await findButton(wrapper, '.btn--primary', 'Change password').trigger('click')
      const values = wrapper.findAll<HTMLInputElement>('input[type="password"]').map(i => i.element.value)
      expect(values).toEqual(['', '', ''])
      expect(wrapper.find('.profile-error').exists()).toBe(false)
      expect(showToast).toHaveBeenCalledWith('Password changed')
    })

    it('calls changePassword onError sets pwError', async () => {
      mockMutations({ [M.changePassword]: fails(new Error('Wrong password')) })
      mockQueries({ me: { ...adminUser, has_password: true } })
      const wrapper = mountTab()
      await fillPasswords(wrapper, 'wrongpassword', 'newpassword1234', 'newpassword1234')
      await findButton(wrapper, '.btn--primary', 'Change password').trigger('click')
      expect(wrapper.find('.profile-error').text()).toBe('Wrong password')
    })
  })

  describe('MFA setup flow', () => {
    it('shows MFA setup card after startMFASetup onSuccess', async () => {
      mockMutations({ [M.startMFASetup]: succeeds(mfaSetup) })
      const wrapper = mountTab()
      await startSetup(wrapper)
      expect(wrapper.find('.mfa-setup-card').exists()).toBe(true)
      expect(wrapper.find('.mfa-qr__img').attributes('src')).toBe(mfaSetup.qr)
    })

    it('can toggle show secret in MFA setup card', async () => {
      const clipWrite = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', { value: { writeText: clipWrite }, configurable: true })
      mockMutations({ [M.startMFASetup]: succeeds({ ...mfaSetup, secret: 'MYSECRETKEY' }) })
      const wrapper = mountTab()
      await startSetup(wrapper)
      expect(wrapper.find('.mfa-setup-card__secret').exists()).toBe(false)
      await wrapper.find('.mfa-secret-toggle').trigger('click')
      expect(wrapper.find('.mfa-setup-card__secret').text()).toBe('MYSECRETKEY')
      await findButton(wrapper, '.btn--ghost', 'Copy').trigger('click')
      await flushPromises()
      expect(clipWrite).toHaveBeenCalledWith('MYSECRETKEY')
      expect(showToast).toHaveBeenCalledWith('Secret key copied')
    })

    it('shows MFA setup error after confirmMFASetup onError', async () => {
      mockMutations({
        [M.startMFASetup]: succeeds(mfaSetup),
        [M.confirmMFASetup]: fails(new Error('Invalid code')),
      })
      const wrapper = mountTab()
      await startSetup(wrapper)
      await wrapper.find('#settings-mfa-code').setValue('123456')
      await findButton(wrapper, '.btn--primary', 'Confirm').trigger('click')
      expect(wrapper.find('.mfa-setup-card').exists()).toBe(true)
      expect(wrapper.find('#settings-mfa-error').text()).toBe('Invalid code')
    })

    it('calls disableMFA onSuccess when confirm clicked', async () => {
      mockMutations({ [M.disableMFA]: succeeds() })
      mockQueries({ me: { ...adminUser, mfa_enabled: true } })
      const wrapper = mountTab()
      await findButton(wrapper, '.btn--ghost', 'Disable two-factor auth').trigger('click')
      await wrapper.find('input[autocomplete="current-password"]').setValue('mypassword123')
      await findButton(wrapper, '.btn--primary', 'Disable 2FA').trigger('click')
      expect(wrapper.find('input[autocomplete="current-password"]').exists()).toBe(false)
      expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['me'] })
      expect(showToast).toHaveBeenCalledWith('Two-factor authentication disabled')
    })

    it('calls disableMFA onError sets mfaDisableError', async () => {
      mockMutations({ [M.disableMFA]: fails(new Error('Bad password')) })
      mockQueries({ me: { ...adminUser, mfa_enabled: true } })
      const wrapper = mountTab()
      await findButton(wrapper, '.btn--ghost', 'Disable two-factor auth').trigger('click')
      await wrapper.find('input[autocomplete="current-password"]').setValue('bad')
      await findButton(wrapper, '.btn--primary', 'Disable 2FA').trigger('click')
      expect(wrapper.find('.profile-error').text()).toBe('Bad password')
    })
  })

  describe('updateProfile onSuccess and weekly digest toggle', () => {
    it('updateProfile onSuccess calls auth.setUser', async () => {
      const updated = { ...adminUser, name: 'Updated' }
      mockMutations({ [M.updateProfile]: succeeds(updated) })
      const wrapper = mountTab()
      const setUser = vi.mocked(useAuthStore)().setUser
      await findButton(wrapper, '.btn--primary', 'Save changes').trigger('click')
      expect(setUser).toHaveBeenCalledWith(updated)
      expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['me'] })
      expect(showToast).toHaveBeenCalledWith('Profile saved')
    })

    it('weekly digest toggle calls updateProfile', async () => {
      const recorded = recordMutations()
      mockQueries()
      const wrapper = mountTab()
      const toggle = wrapper.find('.toggle')
      expect(toggle.classes()).toContain('toggle--on')
      await toggle.trigger('click')
      expect(recorded[M.updateProfile].mutate).toHaveBeenCalledWith(expect.objectContaining({ weekly_digest: false }))
      expect(toggle.classes()).not.toContain('toggle--on')
    })

    it('initProfile cancel button resets profile form', async () => {
      const wrapper = mountTab()
      const nameInput = wrapper.find<HTMLInputElement>('input[placeholder="Admin"]')
      await nameInput.setValue('Changed Name')
      await findButton(wrapper, '.btn--ghost', 'Cancel').trigger('click')
      expect(nameInput.element.value).toBe('Admin')
    })
  })

  describe('confirmMFASetup onSuccess', () => {
    it('confirmMFASetup onSuccess clears mfaSetupData', async () => {
      mockMutations({
        [M.startMFASetup]: succeeds({ ...mfaSetup, secret: 'NEWSECRET' }),
        [M.confirmMFASetup]: succeeds(),
      })
      const wrapper = mountTab()
      await startSetup(wrapper)
      await wrapper.find('#settings-mfa-code').setValue('123456')
      await findButton(wrapper, '.btn--primary', 'Confirm').trigger('click')
      expect(wrapper.find('.mfa-setup-card').exists()).toBe(false)
      expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['me'] })
      expect(showToast).toHaveBeenCalledWith('Two-factor authentication enabled')
    })

    it('cancelMFASetup clears mfaSetupData', async () => {
      mockMutations({ [M.startMFASetup]: succeeds({ ...mfaSetup, secret: 'CANCELSECRET' }) })
      const wrapper = mountTab()
      await startSetup(wrapper)
      expect(wrapper.find('.mfa-setup-card').exists()).toBe(true)
      await wrapper.find('.mfa-setup-card').findAll('.btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('.mfa-setup-card').exists()).toBe(false)
    })
  })

  describe('v-model setters and interactions', () => {
    it('profile name and email inputs are interactive (covers profileName/profileEmail setters)', async () => {
      const recorded = recordMutations()
      mockQueries()
      const wrapper = mountTab()
      await wrapper.find('input[placeholder="Admin"]').setValue('New Name')
      await wrapper.find('input[type="email"].field__input').setValue('new@example.com')
      const tzSelect = wrapper.findAll('select.field__input').find(s => s.findAll('option').length > 10)!
      await tzSelect.setValue('America/New_York')
      await findButton(wrapper, '.btn--primary', 'Save changes').trigger('click')
      expect(recorded[M.updateProfile].mutate).toHaveBeenCalledWith({
        name: 'New Name', email: 'new@example.com', weekly_digest: true, timezone: 'America/New_York',
      })
    })
  })

  describe('MFA enabled user - disable flow', () => {
    beforeEach(() => {
      mockQueries({ me: { ...adminUser, mfa_enabled: true } })
    })

    it('shows MFA enabled badge when user has MFA enabled', () => {
      const wrapper = mountTab()
      expect(wrapper.find('.mfa-badge').exists()).toBe(true)
    })

    it('shows Disable two-factor auth button for MFA-enabled user', () => {
      const wrapper = mountTab()
      expect(wrapper.findAll('.btn--ghost').some(b => b.text().includes('Disable two-factor auth'))).toBe(true)
    })

    it('shows disable MFA confirm form after clicking disable button', async () => {
      const wrapper = mountTab()
      await findButton(wrapper, '.btn--ghost', 'Disable two-factor auth').trigger('click')
      expect(wrapper.find('input[autocomplete="current-password"]').exists()).toBe(true)
    })

    it('clicking Cancel in disable MFA form hides the confirm form', async () => {
      const wrapper = mountTab()
      await findButton(wrapper, '.btn--ghost', 'Disable two-factor auth').trigger('click')
      await wrapper.find('.btn--mfa-disable-cancel').trigger('click')
      expect(wrapper.find('input[autocomplete="current-password"]').exists()).toBe(false)
    })
  })

  describe('authenticator replacement', () => {
    it('focuses setup confirmation and links its label, instructions, and error', async () => {
      runMutations()
      vi.mocked(apiFetch).mockResolvedValueOnce({ secret: 'setup', uri: '', qr: '' })
      const wrapper = mountTab({ attachTo: document.body })
      await wrapper.findAll('button').find(b => b.text() === 'Enable two-factor auth')!.trigger('click')
      await new Promise(r => setTimeout(r, 0))
      const input = wrapper.get('#settings-mfa-code')
      expect(wrapper.get('label[for="settings-mfa-code"]').text()).toBe('Authenticator code')
      expect(document.activeElement).toBe(input.element)
      expect(input.attributes('aria-describedby')).toBe('settings-mfa-hint')
      vi.mocked(apiFetch).mockRejectedValueOnce(new Error('Invalid code'))
      await input.setValue('123456')
      await input.trigger('keydown', { key: 'Enter' })
      await new Promise(r => setTimeout(r, 0))
      expect(input.attributes('aria-invalid')).toBe('true')
      expect(input.attributes('aria-describedby')).toBe('settings-mfa-hint settings-mfa-error')
      expect(wrapper.get('#settings-mfa-error').attributes('role')).toBe('alert')
      await wrapper.get('.mfa-setup-card').findAll('button').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('#settings-mfa-code').exists()).toBe(false)
      expect(wrapper.find('#settings-mfa-error').exists()).toBe(false)
    })

    it('requires the current code before showing the replacement QR', async () => {
      mockQueries({ me: { ...adminUser, mfa_enabled: true } })
      runMutations()
      vi.mocked(apiFetch).mockResolvedValue({ secret: 'replacement', uri: 'otpauth://test', qr: 'data:image/png;base64,test' })
      const wrapper = mountTab()
      await wrapper.findAll('button').find(b => b.text() === 'Replace authenticator')!.trigger('click')
      const form = wrapper.find('#mfa-current-code').element.closest('form')!
      expect(form.querySelector<HTMLButtonElement>('button[type="submit"]')!.disabled).toBe(true)
      await wrapper.find('#mfa-current-code').setValue('123456')
      await wrapper.findAll('form').find(f => f.find('#mfa-current-code').exists())!.trigger('submit')
      await new Promise(r => setTimeout(r, 0))
      expect(apiFetch).toHaveBeenCalledWith('/api/auth/mfa/setup', {
        method: 'POST', body: JSON.stringify({ code: '123456' }),
      })
      expect(wrapper.find('.mfa-setup-card').exists()).toBe(true)
      expect(wrapper.find('#mfa-current-code').exists()).toBe(false)
    })
  })

  it.each([new Error('incorrect code'), 'unavailable'])('keeps the active authenticator when replacement setup fails: %s', async (failure) => {
    mockQueries({ me: { ...adminUser, mfa_enabled: true } })
    runMutations()
    vi.mocked(apiFetch).mockRejectedValue(failure)
    const wrapper = mountTab()
    await wrapper.findAll('button').find(b => b.text() === 'Replace authenticator')!.trigger('click')
    await wrapper.find('#mfa-current-code').setValue('123456')
    await wrapper.findAll('form').find(f => f.find('#mfa-current-code').exists())!.trigger('submit')
    await new Promise(r => setTimeout(r, 0))
    expect(wrapper.find('.profile-error').text()).toBe(failure instanceof Error ? failure.message : 'Failed to start authenticator setup')
    expect(wrapper.find('.mfa-badge').text()).toBe('Enabled')
    expect(wrapper.find('.mfa-setup-card').exists()).toBe(false)
    await wrapper.findAll('form').find(f => f.find('#mfa-current-code').exists())!.find('button[type="button"]').trigger('click')
    expect(wrapper.find('#mfa-current-code').exists()).toBe(false)
    expect(wrapper.find('.profile-error').exists()).toBe(false)
    await wrapper.findAll('button').find(b => b.text() === 'Replace authenticator')!.trigger('click')
    expect(wrapper.find<HTMLInputElement>('#mfa-current-code').element.value).toBe('')
  })

  describe('initProfile', () => {
    it('resets edited fields back to the me values', async () => {
      mockQueries({ me: { ...adminUser, name: 'Alice', email: 'alice@example.com', weekly_digest: false, timezone: 'Europe/Berlin' } })
      const wrapper = mountTab()
      const nameInput = wrapper.find<HTMLInputElement>('input[placeholder="Alice"]')
      const emailInput = wrapper.find<HTMLInputElement>('input[type="email"]')
      const tzSelect = wrapper.find<HTMLSelectElement>('select.field__input')
      await nameInput.setValue('Edited')
      await emailInput.setValue('edited@example.com')
      await tzSelect.setValue('America/New_York')
      await wrapper.find('.toggle').trigger('click')
      expect(wrapper.find('.toggle').classes()).toContain('toggle--on')

      wrapper.vm.initProfile()
      await wrapper.vm.$nextTick()

      expect(nameInput.element.value).toBe('Alice')
      expect(emailInput.element.value).toBe('alice@example.com')
      expect(tzSelect.element.value).toBe('Europe/Berlin')
      expect(wrapper.find('.toggle').classes()).not.toContain('toggle--on')
    })
  })
})
