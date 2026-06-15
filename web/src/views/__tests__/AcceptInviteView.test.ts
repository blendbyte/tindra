import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'

const pushMock = vi.fn()
const tokenParam = { token: 'test-token-123' }
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

import AcceptInviteView from '../AcceptInviteView.vue'
import { apiFetch } from '@/api/client'

const stubs = { Icon: true }

function mountView() {
  return mount(AcceptInviteView, { global: { stubs } })
}

beforeEach(() => {
  pushMock.mockReset()
  vi.mocked(apiFetch).mockReset()
})

describe('AcceptInviteView', () => {
  describe('loading state', () => {
    it('shows loading skeleton while invite is being fetched', () => {
      vi.mocked(apiFetch).mockReturnValue(new Promise(() => {}))
      const wrapper = mountView()
      expect(wrapper.find('.login__invite-loading').exists()).toBe(true)
    })
  })

  describe('invalid state', () => {
    it('shows expired error when the invite token is invalid', async () => {
      vi.mocked(apiFetch).mockRejectedValue(new Error('not found'))
      const wrapper = mountView()
      await new Promise((r) => setTimeout(r, 0))
      expect(wrapper.find('.login__error-title').text()).toContain('expired or was already used')
    })

    it('shows a link to go to sign in when token is invalid', async () => {
      vi.mocked(apiFetch).mockRejectedValue(new Error('not found'))
      const wrapper = mountView()
      await new Promise((r) => setTimeout(r, 0))
      const link = wrapper.find('a.login__submit')
      expect(link.exists()).toBe(true)
      expect(link.text()).toContain('Go to sign in')
    })
  })

  describe('ready state', () => {
    async function mountReady(email = 'user@example.com', name = '') {
      vi.mocked(apiFetch).mockResolvedValueOnce({ email, name })
      const wrapper = mountView()
      await new Promise((r) => setTimeout(r, 0))
      return wrapper
    }

    it('shows the invite email when token is valid', async () => {
      const wrapper = await mountReady('alice@example.com')
      expect(wrapper.find('.login__invite-email').text()).toBe('alice@example.com')
    })

    it('renders name and password fields', async () => {
      const wrapper = await mountReady()
      expect(wrapper.find('#invite-name').exists()).toBe(true)
      expect(wrapper.find('#invite-password').exists()).toBe(true)
    })

    it('pre-fills the name field with the invite name', async () => {
      const wrapper = await mountReady('user@example.com', 'Alice')
      expect((wrapper.find<HTMLInputElement>('#invite-name').element).value).toBe('Alice')
    })

    it('disables the submit button when password is empty', async () => {
      const wrapper = await mountReady()
      expect(wrapper.find<HTMLButtonElement>('button[type="submit"]').element.disabled).toBe(true)
    })

    it('enables the submit button once a password is entered', async () => {
      const wrapper = await mountReady()
      await wrapper.find('#invite-password').setValue('supersecurepassword')
      expect(wrapper.find<HTMLButtonElement>('button[type="submit"]').element.disabled).toBe(false)
    })

    it('calls the accept endpoint on submit', async () => {
      vi.mocked(apiFetch).mockResolvedValue({})
      const wrapper = await mountReady('user@example.com', 'Alice')
      await wrapper.find('#invite-password').setValue('mysecretpassword!')
      await wrapper.find('form').trigger('submit')
      await new Promise((r) => setTimeout(r, 0))
      expect(apiFetch).toHaveBeenCalledWith(
        `/api/auth/invite/${tokenParam.token}/accept`,
        expect.objectContaining({ method: 'POST' }),
      )
    })

    it('redirects to /issues after successful account creation', async () => {
      vi.mocked(apiFetch).mockResolvedValue({})
      const wrapper = await mountReady()
      await wrapper.find('#invite-password').setValue('mysecretpassword!')
      await wrapper.find('form').trigger('submit')
      await new Promise((r) => setTimeout(r, 0))
      expect(pushMock).toHaveBeenCalledWith('/issues')
    })

    it('shows an error message when the API call fails', async () => {
      vi.mocked(apiFetch).mockResolvedValueOnce({ email: 'u@example.com', name: '' })
      vi.mocked(apiFetch).mockRejectedValue(new Error('Token expired'))
      const wrapper = mountView()
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.find('#invite-password').setValue('mysecretpassword!')
      await wrapper.find('form').trigger('submit')
      await new Promise((r) => setTimeout(r, 0))
      expect(wrapper.find('.login__error-title').text()).toBe('Token expired')
    })

    it('shows "Creating account…" while submitting', async () => {
      vi.mocked(apiFetch).mockResolvedValueOnce({ email: 'u@example.com', name: '' })
      vi.mocked(apiFetch).mockReturnValue(new Promise(() => {}))
      const wrapper = mountView()
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.find('#invite-password').setValue('mysecretpassword!')
      await wrapper.find('form').trigger('submit')
      expect(wrapper.find('button[type="submit"]').text()).toContain('Creating account')
    })
  })
})
