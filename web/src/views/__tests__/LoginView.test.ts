import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

const pushMock = vi.fn()
const resetQueriesMock = vi.fn()

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: pushMock })),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useQueryClient: vi.fn(() => ({ resetQueries: resetQueriesMock })),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

import LoginView from '../LoginView.vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'

function mountLogin(providers: string[] = []) {
  const providersData = ref<{ providers: string[] } | undefined>({ providers })
  vi.mocked(useQuery).mockReturnValue({ data: providersData } as any)
  return mount(LoginView, {
    global: { stubs: { Icon: true } },
  })
}

beforeEach(() => {
  pushMock.mockReset()
  resetQueriesMock.mockReset()
  vi.mocked(apiFetch).mockReset()
  vi.mocked(useQuery).mockReset()
})

describe('LoginView', () => {
  describe('password login form (no SSO providers)', () => {
    it('renders email and password fields', () => {
      const wrapper = mountLogin()
      expect(wrapper.find('#email').exists()).toBe(true)
      expect(wrapper.find('#password').exists()).toBe(true)
    })

    it('renders a sign-in button', () => {
      const wrapper = mountLogin()
      expect(wrapper.find('button[type="submit"]').text()).toContain('Sign in')
    })

    it('shows a required error when submitted with empty fields', async () => {
      const wrapper = mountLogin()
      await wrapper.find('form').trigger('submit')
      expect(wrapper.find('.login__error-title').text()).toBe('Email and password are required.')
    })

    it('does not call the API when fields are empty', async () => {
      const wrapper = mountLogin()
      await wrapper.find('form').trigger('submit')
      expect(apiFetch).not.toHaveBeenCalled()
    })

    it('calls the login API with email and password', async () => {
      vi.mocked(apiFetch).mockResolvedValue({})
      const wrapper = mountLogin()
      await wrapper.find('#email').setValue('user@example.com')
      await wrapper.find('#password').setValue('secret')
      await wrapper.find('form').trigger('submit')
      expect(apiFetch).toHaveBeenCalledWith('/api/auth/login', expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ email: 'user@example.com', password: 'secret' }),
      }))
    })

    it('redirects to /issues after a successful login', async () => {
      vi.mocked(apiFetch).mockResolvedValue({})
      const wrapper = mountLogin()
      await wrapper.find('#email').setValue('user@example.com')
      await wrapper.find('#password').setValue('secret')
      await wrapper.find('form').trigger('submit')
      await new Promise((r) => setTimeout(r, 0))
      expect(pushMock).toHaveBeenCalledWith('/dashboard')
    })

    it('resets query cache on successful login', async () => {
      vi.mocked(apiFetch).mockResolvedValue({})
      const wrapper = mountLogin()
      await wrapper.find('#email').setValue('user@example.com')
      await wrapper.find('#password').setValue('secret')
      await wrapper.find('form').trigger('submit')
      await new Promise((r) => setTimeout(r, 0))
      expect(resetQueriesMock).toHaveBeenCalled()
    })
  })

  describe('error messages', () => {
    async function submitAndGetError(errorMsg: string) {
      vi.mocked(apiFetch).mockRejectedValue(new Error(errorMsg))
      const wrapper = mountLogin()
      await wrapper.find('#email').setValue('a@b.com')
      await wrapper.find('#password').setValue('pw')
      await wrapper.find('form').trigger('submit')
      await new Promise((r) => setTimeout(r, 0))
      return wrapper.find('.login__error-title').text()
    }

    it('maps "invalid credentials" to a human-readable message', async () => {
      expect(await submitAndGetError('invalid credentials')).toBe('Incorrect email or password.')
    })

    it('maps "unauthorized" to the credentials error', async () => {
      expect(await submitAndGetError('unauthorized')).toBe('Incorrect email or password.')
    })

    it('maps "disabled" to the SSO nudge message', async () => {
      expect(await submitAndGetError('disabled')).toBe('Password login is disabled.')
    })

    it('maps "invalid code" to the MFA code error', async () => {
      expect(await submitAndGetError('invalid code')).toBe("That code didn't work.")
    })

    it('maps "expired" to the session expired message', async () => {
      expect(await submitAndGetError('expired')).toBe('Session expired.')
    })

    it('falls through to the raw message for unknown errors', async () => {
      expect(await submitAndGetError('something unexpected')).toBe('something unexpected')
    })
  })

  describe('MFA step', () => {
    async function triggerMfa() {
      vi.mocked(apiFetch).mockResolvedValueOnce({ mfa_required: true, mfa_token: 'tok-123' })
      const wrapper = mountLogin()
      await wrapper.find('#email').setValue('a@b.com')
      await wrapper.find('#password').setValue('pw')
      await wrapper.find('form').trigger('submit')
      await new Promise((r) => setTimeout(r, 0))
      return wrapper
    }

    it('shows the MFA step when the API returns mfa_required', async () => {
      const wrapper = await triggerMfa()
      expect(wrapper.find('.login__mfa').exists()).toBe(true)
      expect(wrapper.find('.login__mfa-title').text()).toBe('Two-factor authentication')
    })

    it('hides the password form when in MFA step', async () => {
      const wrapper = await triggerMfa()
      expect(wrapper.find('.login__form').exists()).toBe(false)
    })

    it('calls the MFA verify endpoint on Verify click', async () => {
      vi.mocked(apiFetch).mockResolvedValue({})
      const wrapper = await triggerMfa()
      await wrapper.find('.login__mfa-code').setValue('123456')
      await wrapper.find('.login__submit').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      expect(apiFetch).toHaveBeenCalledWith('/api/auth/mfa/verify', expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ mfa_token: 'tok-123', code: '123456' }),
      }))
    })

    it('redirects to /dashboard after successful MFA', async () => {
      vi.mocked(apiFetch).mockResolvedValue({})
      const wrapper = await triggerMfa()
      await wrapper.find('.login__mfa-code').setValue('123456')
      await wrapper.find('.login__submit').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      expect(pushMock).toHaveBeenCalledWith('/dashboard')
    })

    it('disables the Verify button when fewer than 6 digits are entered', async () => {
      const wrapper = await triggerMfa()
      await wrapper.find('.login__mfa-code').setValue('12345')
      expect(wrapper.find<HTMLButtonElement>('.login__submit').element.disabled).toBe(true)
    })

    it('auto-submits when 6 digits are typed', async () => {
      vi.mocked(apiFetch).mockResolvedValue({})
      const wrapper = await triggerMfa()
      await wrapper.find('.login__mfa-code').setValue('123456')
      await new Promise((r) => setTimeout(r, 0))
      expect(apiFetch).toHaveBeenCalledWith('/api/auth/mfa/verify', expect.anything())
    })

    it('returns to the password form when the back link is clicked', async () => {
      const wrapper = await triggerMfa()
      await wrapper.find('.login__back').trigger('click')
      expect(wrapper.find('.login__form').exists()).toBe(true)
      expect(wrapper.find('.login__mfa').exists()).toBe(false)
    })

    it('shows an error when the MFA code is wrong', async () => {
      vi.mocked(apiFetch).mockRejectedValue(new Error('invalid code'))
      const wrapper = await triggerMfa()
      await wrapper.find('.login__mfa-code').setValue('000000')
      await wrapper.find('.login__submit').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      expect(wrapper.find('.login__error-title').text()).toBe("That code didn't work.")
    })
  })

  describe('SSO mode', () => {
    it('shows SSO buttons instead of the password form when providers are present', () => {
      const wrapper = mountLogin(['google', 'github'])
      expect(wrapper.find('.login__form').exists()).toBe(false)
      expect(wrapper.findAll('.login__sso')).toHaveLength(2)
    })

    it('labels the Google provider correctly', () => {
      const wrapper = mountLogin(['google'])
      expect(wrapper.find('.login__sso').text()).toContain('Google Workspace')
    })

    it('labels the GitHub provider correctly', () => {
      const wrapper = mountLogin(['github'])
      expect(wrapper.find('.login__sso').text()).toContain('GitHub')
    })

    it('capitalizes unknown provider names', () => {
      const wrapper = mountLogin(['myidp'])
      expect(wrapper.find('.login__sso').text()).toContain('Myidp')
    })

    it('links each SSO button to the correct redirect path', () => {
      const wrapper = mountLogin(['google'])
      expect(wrapper.find('.login__sso').attributes('href')).toBe('/api/auth/google/redirect')
    })
  })
})
