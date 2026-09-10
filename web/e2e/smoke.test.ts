/**
 * Smoke tests - require `make run` (Go backend) and the Vite dev server running.
 * Run with: bun run test:e2e
 */

import { test, expect } from '@playwright/test'

test.describe('login page', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/auth/providers', route => route.fulfill({ json: { providers: [] } }))
    await page.goto('/login')
  })

  test('has the correct page title', async ({ page }) => {
    await expect(page).toHaveTitle('Tindra')
  })

  test('shows the email and password fields', async ({ page }) => {
    await expect(page.getByLabel('Email')).toBeVisible()
    await expect(page.getByLabel('Password')).toBeVisible()
  })

  test('shows a submit button', async ({ page }) => {
    const btn = page.getByRole('button', { name: /sign in|log in/i })
    await expect(btn).toBeVisible()
  })

  test('shows a validation message on empty submit', async ({ page }) => {
    await page.getByRole('button', { name: /sign in|log in/i }).click()
    const emailField = page.getByLabel('Email')
    await expect(emailField).toBeFocused()
    await expect(emailField).toHaveAttribute('aria-invalid', 'true')
    await expect(emailField).toHaveAccessibleDescription('Email and password are required.')
    await expect(page.getByRole('alert')).toHaveText('Email and password are required.')
  })

  test('focuses the password when only the email is filled', async ({ page }) => {
    await page.getByLabel('Email').fill('user@example.com')
    await page.getByRole('button', { name: /sign in/i }).click()
    await expect(page.getByLabel('Password')).toBeFocused()
    await expect(page.getByLabel('Password')).toHaveAccessibleDescription('Email and password are required.')
    await expect(page.getByLabel('Email')).not.toHaveAttribute('aria-invalid', 'true')
  })

  test('keyboard submission focuses the email when only the password is filled', async ({ page }) => {
    await page.getByLabel('Password').fill('secret')
    await page.getByLabel('Password').press('Enter')
    await expect(page.getByLabel('Email')).toBeFocused()
    await expect(page.getByLabel('Password')).not.toHaveAttribute('aria-invalid', 'true')
  })
})

test.describe('unauthenticated redirect', () => {
  test('redirects /issues to /login when not signed in', async ({ page }) => {
    // The API returns 401 → apiFetch redirects to /login
    await page.goto('/issues')
    // Allow time for the client-side redirect
    await page.waitForURL('**/login', { timeout: 3000 }).catch(() => {
      // If the redirect doesn't happen (e.g., SSR handles it), just check the URL
    })
    // Either we're on /login or the page shows a login form
    const url = page.url()
    const hasLoginForm = await page.getByLabel('Email').isVisible().catch(() => false)
    expect(url.includes('/login') || hasLoginForm).toBe(true)
  })
})
