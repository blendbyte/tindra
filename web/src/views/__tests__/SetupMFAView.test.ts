import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'

const pushMock = vi.fn()
const resetQueriesMock = vi.fn()
const authStoreMock = { ready: true as boolean }

vi.mock('@/stores/auth', () => ({
  useAuthStore: vi.fn(() => authStoreMock),
}))

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: pushMock })),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQueryClient: vi.fn(() => ({ resetQueries: resetQueriesMock })),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

import SetupMFAView from '../SetupMFAView.vue'
import { apiFetch } from '@/api/client'

const stubs = { Icon: true }

const mockSetupData = {
  secret: 'JBSWY3DPEHPK3PXP',
  uri: 'otpauth://totp/Tindra:user@example.com?secret=JBSWY3DPEHPK3PXP',
  qr: 'data:image/png;base64,abc123',
}

beforeEach(() => {
  pushMock.mockReset()
  resetQueriesMock.mockReset()
  authStoreMock.ready = true
  vi.mocked(apiFetch).mockReset()
})

async function mountReady() {
  vi.mocked(apiFetch).mockResolvedValueOnce(mockSetupData)
  const wrapper = mount(SetupMFAView, { global: { stubs } })
  await new Promise((r) => setTimeout(r, 0))
  return wrapper
}

describe('SetupMFAView', () => {
  describe('loading state', () => {
    it('shows loading skeleton while setup data is being fetched', () => {
      vi.mocked(apiFetch).mockReturnValue(new Promise(() => {}))
      const wrapper = mount(SetupMFAView, { global: { stubs } })
      expect(wrapper.find('.mfa-gate__loading').exists()).toBe(true)
    })

    it('does not show setup form while loading', () => {
      vi.mocked(apiFetch).mockReturnValue(new Promise(() => {}))
      const wrapper = mount(SetupMFAView, { global: { stubs } })
      expect(wrapper.find('.mfa-gate').exists()).toBe(false)
    })
  })

  describe('load error state', () => {
    it('shows error box when setup API call fails', async () => {
      vi.mocked(apiFetch).mockRejectedValue(new Error('Unauthorized'))
      const wrapper = mount(SetupMFAView, { global: { stubs } })
      await new Promise((r) => setTimeout(r, 0))
      expect(wrapper.find('.login__error-title').text()).toBe('Could not start setup')
    })

    it('shows the error message from the exception', async () => {
      vi.mocked(apiFetch).mockRejectedValue(new Error('Session expired'))
      const wrapper = mount(SetupMFAView, { global: { stubs } })
      await new Promise((r) => setTimeout(r, 0))
      expect(wrapper.find('.login__error-hint').text()).toBe('Session expired')
    })

    it('shows generic message when error is not an Error instance', async () => {
      vi.mocked(apiFetch).mockRejectedValue('oops')
      const wrapper = mount(SetupMFAView, { global: { stubs } })
      await new Promise((r) => setTimeout(r, 0))
      expect(wrapper.find('.login__error-hint').text()).toBe('Failed to load setup')
    })
  })

  describe('setup flow', () => {
    it('renders the QR code image after loading', async () => {
      const wrapper = await mountReady()
      const img = wrapper.find('.mfa-qr__img')
      expect(img.exists()).toBe(true)
      expect(img.attributes('src')).toBe(mockSetupData.qr)
    })

    it('does not show the secret by default', async () => {
      const wrapper = await mountReady()
      expect(wrapper.find('.mfa-gate__secret-row').exists()).toBe(false)
    })

    it('reveals the secret when the toggle is clicked', async () => {
      const wrapper = await mountReady()
      await wrapper.find('.mfa-secret-toggle').trigger('click')
      expect(wrapper.find('.mfa-setup-card__secret').text()).toBe(mockSetupData.secret)
    })

    it('hides the secret again on second toggle click', async () => {
      const wrapper = await mountReady()
      await wrapper.find('.mfa-secret-toggle').trigger('click')
      await wrapper.find('.mfa-secret-toggle').trigger('click')
      expect(wrapper.find('.mfa-gate__secret-row').exists()).toBe(false)
    })

    it('copies the secret to the clipboard when the Copy button is clicked', async () => {
      const writeMock = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', {
        value: { writeText: writeMock },
        writable: true,
        configurable: true,
      })
      const wrapper = await mountReady()
      await wrapper.find('.mfa-secret-toggle').trigger('click')
      await wrapper.find('button.btn--ghost').trigger('click')
      expect(writeMock).toHaveBeenCalledWith(mockSetupData.secret)
    })

    it('renders the code input field', async () => {
      const wrapper = await mountReady()
      expect(wrapper.find('input[maxlength="6"]').exists()).toBe(true)
    })

    it('disables the confirm button when code is shorter than 6 digits', async () => {
      const wrapper = await mountReady()
      await wrapper.find('input[maxlength="6"]').setValue('123')
      const btn = wrapper.find<HTMLButtonElement>('.btn--primary')
      expect(btn.element.disabled).toBe(true)
    })

    it('disables the confirm button with a 5-digit code', async () => {
      const wrapper = await mountReady()
      await wrapper.find('input[maxlength="6"]').setValue('12345')
      const btn = wrapper.find<HTMLButtonElement>('.btn--primary')
      expect(btn.element.disabled).toBe(true)
    })

    it('auto-submits and calls the confirm API when the 6th digit is entered', async () => {
      vi.mocked(apiFetch).mockResolvedValue({})
      const wrapper = await mountReady()
      await wrapper.find('input[maxlength="6"]').setValue('999999')
      await new Promise((r) => setTimeout(r, 0))
      expect(apiFetch).toHaveBeenCalledWith('/api/auth/mfa/confirm', expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ code: '999999' }),
      }))
    })

    it('does not call confirm API when code is fewer than 6 digits', async () => {
      const wrapper = await mountReady()
      await wrapper.find('input[maxlength="6"]').setValue('12345')
      await new Promise((r) => setTimeout(r, 0))
      expect(apiFetch).toHaveBeenCalledTimes(1) // only the setup call
    })

    it('resets auth.ready and invalidates queries on success', async () => {
      vi.mocked(apiFetch).mockResolvedValue({})
      const wrapper = await mountReady()
      await wrapper.find('input[maxlength="6"]').setValue('123456')
      await new Promise((r) => setTimeout(r, 0))
      expect(authStoreMock.ready).toBe(false)
      expect(resetQueriesMock).toHaveBeenCalled()
    })

    it('shows confirm error message when the code is wrong', async () => {
      vi.mocked(apiFetch).mockResolvedValueOnce(mockSetupData)
      vi.mocked(apiFetch).mockRejectedValue(new Error('Invalid code'))
      const wrapper = mount(SetupMFAView, { global: { stubs } })
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.find('input[maxlength="6"]').setValue('000000')
      await new Promise((r) => setTimeout(r, 0))
      expect(wrapper.find('.login__error-title').text()).toBe('Invalid code')
    })

    it('shows generic error when confirm error is not an Error instance', async () => {
      vi.mocked(apiFetch).mockResolvedValueOnce(mockSetupData)
      vi.mocked(apiFetch).mockRejectedValue('bad')
      const wrapper = mount(SetupMFAView, { global: { stubs } })
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.find('input[maxlength="6"]').setValue('000000')
      await new Promise((r) => setTimeout(r, 0))
      expect(wrapper.find('.login__error-title').text()).toContain("didn't work")
    })

    it('clears the code input after a failed confirm', async () => {
      vi.mocked(apiFetch).mockResolvedValueOnce(mockSetupData)
      vi.mocked(apiFetch).mockRejectedValue(new Error('Nope'))
      const wrapper = mount(SetupMFAView, { global: { stubs } })
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.find('input[maxlength="6"]').setValue('000000')
      await new Promise((r) => setTimeout(r, 0))
      expect((wrapper.find<HTMLInputElement>('input[maxlength="6"]').element).value).toBe('')
    })
  })

  describe('success state', () => {
    async function confirmSuccess() {
      vi.mocked(apiFetch).mockResolvedValue({})
      const wrapper = await mountReady()
      // watch fires submit automatically when 6 digits are entered
      await wrapper.find('input[maxlength="6"]').setValue('123456')
      await new Promise((r) => setTimeout(r, 0))
      return wrapper
    }

    it('shows success panel after successful confirmation', async () => {
      const wrapper = await confirmSuccess()
      expect(wrapper.find('.mfa-gate__success').exists()).toBe(true)
    })

    it('hides the setup form after successful confirmation', async () => {
      const wrapper = await confirmSuccess()
      expect(wrapper.find('.mfa-gate').exists()).toBe(false)
    })

    it('shows a "protected" success title', async () => {
      const wrapper = await confirmSuccess()
      expect(wrapper.find('.mfa-gate__success-title').text()).toBe("You're protected")
    })
  })
})
