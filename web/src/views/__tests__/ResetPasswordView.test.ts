import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'

const pushMock = vi.fn()
const tokenParam = { token: 'reset-token-xyz' }
const authStoreMock = { ready: true as boolean }

vi.mock('@/stores/auth', () => ({
  useAuthStore: vi.fn(() => authStoreMock),
}))

vi.mock('vue-router', () => ({
  useRoute: vi.fn(() => ({ params: tokenParam })),
  useRouter: vi.fn(() => ({ push: pushMock })),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

import ResetPasswordView from '../ResetPasswordView.vue'
import { apiFetch } from '@/api/client'

const stubs = { Icon: true }

beforeEach(() => {
  pushMock.mockReset()
  vi.mocked(apiFetch).mockReset()
})

async function mountReady(email = 'user@example.com') {
  vi.mocked(apiFetch).mockResolvedValueOnce({ email })
  const wrapper = mount(ResetPasswordView, { global: { stubs } })
  await new Promise((r) => setTimeout(r, 0))
  return wrapper
}

describe('ResetPasswordView', () => {
  describe('loading state', () => {
    it('shows loading skeleton while token is being validated', () => {
      vi.mocked(apiFetch).mockReturnValue(new Promise(() => {}))
      const wrapper = mount(ResetPasswordView, { global: { stubs } })
      expect(wrapper.find('.login__invite-loading').exists()).toBe(true)
    })
  })

  describe('invalid token', () => {
    it('shows expired link error when token is invalid', async () => {
      vi.mocked(apiFetch).mockRejectedValue(new Error('not found'))
      const wrapper = mount(ResetPasswordView, { global: { stubs } })
      await new Promise((r) => setTimeout(r, 0))
      expect(wrapper.find('.login__error-title').text()).toContain('expired or was already used')
    })
  })

  describe('ready form', () => {
    it('shows the email associated with the reset token', async () => {
      const wrapper = await mountReady('alice@example.com')
      expect(wrapper.find('.login__invite-email').text()).toBe('alice@example.com')
    })

    it('renders new password and confirm password fields', async () => {
      const wrapper = await mountReady()
      expect(wrapper.find('#reset-password').exists()).toBe(true)
      expect(wrapper.find('#reset-confirm').exists()).toBe(true)
    })

    it('disables submit when fields are empty', async () => {
      const wrapper = await mountReady()
      expect(wrapper.find<HTMLButtonElement>('button[type="submit"]').element.disabled).toBe(true)
    })

    it('shows an error when passwords do not match', async () => {
      const wrapper = await mountReady()
      await wrapper.find('#reset-password').setValue('newpassword1234')
      await wrapper.find('#reset-confirm').setValue('differentpass1234')
      await wrapper.find('form').trigger('submit')
      expect(wrapper.find('.login__error-title').text()).toBe('Passwords do not match.')
    })

    it('shows an error when password is shorter than 12 characters', async () => {
      const wrapper = await mountReady()
      await wrapper.find('#reset-password').setValue('short')
      await wrapper.find('#reset-confirm').setValue('short')
      await wrapper.find('form').trigger('submit')
      expect(wrapper.find('.login__error-title').text()).toBe('Password must be at least 12 characters.')
    })

    it('calls the reset endpoint on valid submission', async () => {
      vi.mocked(apiFetch).mockResolvedValue({})
      const wrapper = await mountReady()
      await wrapper.find('#reset-password').setValue('newpassword1234')
      await wrapper.find('#reset-confirm').setValue('newpassword1234')
      await wrapper.find('form').trigger('submit')
      await new Promise((r) => setTimeout(r, 0))
      expect(apiFetch).toHaveBeenCalledWith(
        `/api/auth/password-reset/${tokenParam.token}`,
        expect.objectContaining({ method: 'POST' }),
      )
    })

    it('redirects to /issues after successful reset', async () => {
      vi.mocked(apiFetch).mockResolvedValue({})
      const wrapper = await mountReady()
      await wrapper.find('#reset-password').setValue('newpassword1234')
      await wrapper.find('#reset-confirm').setValue('newpassword1234')
      await wrapper.find('form').trigger('submit')
      await new Promise((r) => setTimeout(r, 0))
      expect(pushMock).toHaveBeenCalledWith('/issues')
    })

    it('shows API error message on failure', async () => {
      vi.mocked(apiFetch).mockResolvedValueOnce({ email: 'u@example.com' })
      vi.mocked(apiFetch).mockRejectedValue(new Error('Token already used'))
      const wrapper = mountView()
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.find('#reset-password').setValue('newpassword1234')
      await wrapper.find('#reset-confirm').setValue('newpassword1234')
      await wrapper.find('form').trigger('submit')
      await new Promise((r) => setTimeout(r, 0))
      expect(wrapper.find('.login__error-title').text()).toBe('Token already used')
    })
  })

  describe('MFA step after reset', () => {
    async function triggerMfa() {
      vi.mocked(apiFetch).mockResolvedValueOnce({ email: 'u@example.com' })
      vi.mocked(apiFetch).mockResolvedValueOnce({ mfa_required: true, mfa_token: 'mfa-tok' })
      const wrapper = mount(ResetPasswordView, { global: { stubs } })
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.find('#reset-password').setValue('newpassword1234')
      await wrapper.find('#reset-confirm').setValue('newpassword1234')
      await wrapper.find('form').trigger('submit')
      await new Promise((r) => setTimeout(r, 0))
      return wrapper
    }

    it('shows MFA form when reset response requires MFA', async () => {
      const wrapper = await triggerMfa()
      expect(wrapper.find('.login__mfa').exists()).toBe(true)
      expect(wrapper.find('.login__mfa-title').text()).toBe('Two-factor authentication')
    })

    it('calls the MFA verify endpoint when 6 digits are entered', async () => {
      vi.mocked(apiFetch).mockResolvedValue({})
      const wrapper = await triggerMfa()
      await wrapper.find('.login__mfa-code').setValue('123456')
      await new Promise((r) => setTimeout(r, 0))
      expect(apiFetch).toHaveBeenCalledWith('/api/auth/mfa/verify', expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ mfa_token: 'mfa-tok', code: '123456' }),
      }))
    })

    it('redirects to /issues after successful MFA verification', async () => {
      vi.mocked(apiFetch).mockResolvedValue({})
      const wrapper = await triggerMfa()
      await wrapper.find('.login__mfa-code').setValue('123456')
      await new Promise((r) => setTimeout(r, 0))
      expect(pushMock).toHaveBeenCalledWith('/issues')
    })
  })
})

function mountView() {
  return mount(ResetPasswordView, { global: { stubs } })
}
