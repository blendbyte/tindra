import { test, expect } from '@playwright/test'

for (const flow of ['password', 'SSO', 'required setup', 'settings setup', 'replacement', 'disable']) {
  test(`MFA fields expose names and feedback: ${flow}`, async ({ page }) => {
    const login = flow === 'password' || flow === 'SSO'
    const enabled = flow === 'replacement' || flow === 'disable'
    await page.route('**/api/**', async route => {
      const path = new URL(route.request().url()).pathname
      if (!path.startsWith('/api/')) return route.fallback()
      if (path === '/api/me' && login) return route.fulfill({ status: 401, body: 'unauthorized' })
      if (path === '/api/auth/mfa/verify' || path === '/api/auth/mfa/confirm' || path === '/api/auth/mfa' || (path === '/api/auth/mfa/setup' && enabled)) {
        return route.fulfill({ status: 400, body: 'Invalid code or password' })
      }
      let json: unknown = []
      if (path === '/api/me') json = { id: 'u1', email: 'dev@example.com', mfa_enabled: enabled, permissions: {} }
      if (path === '/api/config') json = { require_mfa: flow === 'required setup' }
      if (path === '/api/auth/providers') json = { providers: [] }
      if (path === '/api/settings') json = {}
      if (path === '/api/auth/login') json = { mfa_required: true, mfa_token: 'challenge' }
      if (path === '/api/auth/mfa/setup') json = { secret: 'EXAMPLE', qr: '', uri: '' }
      await route.fulfill({ json })
    })
    await page.goto(login ? (flow === 'SSO' ? '/login?mfa=1' : '/login') : flow === 'required setup' ? '/setup-mfa' : '/settings/profile')
    if (flow === 'password') {
      await page.getByLabel('Email', { exact: true }).fill('dev@example.com')
      await page.getByLabel('Password', { exact: true }).fill('example-password')
      await page.getByRole('button', { name: /Sign in/ }).click()
    }
    if (flow === 'settings setup') await page.getByRole('button', { name: 'Enable two-factor auth', exact: true }).click()
    if (flow === 'replacement') await page.getByRole('button', { name: 'Replace authenticator', exact: true }).click()
    if (flow === 'disable') await page.getByRole('button', { name: 'Disable two-factor auth', exact: true }).click()
    const name = flow === 'replacement' ? 'Code from your current authenticator' : flow === 'disable' ? 'Confirm your password to disable 2FA' : 'Authenticator code'
    const input = page.getByLabel(name, { exact: true })
    await expect(input).toBeVisible()
    if (!enabled) {
      await expect(input).toBeFocused()
      await expect(input).toHaveAccessibleDescription(/6-digit code/)
    }
    await input.fill(flow === 'disable' ? 'wrong-password' : '123456')
    if (flow === 'settings setup') await page.getByRole('button', { name: 'Confirm', exact: true }).click()
    if (flow === 'replacement') await page.getByRole('button', { name: 'Continue', exact: true }).click()
    if (flow === 'disable') await page.getByRole('button', { name: 'Disable 2FA', exact: true }).click()
    await expect(input).toHaveAttribute('aria-invalid', 'true')
    await expect(page.getByRole('alert')).toBeVisible()
    await expect(input).toHaveAccessibleDescription(login ? /That code didn't work/ : /Invalid code or password/)
  })
}
