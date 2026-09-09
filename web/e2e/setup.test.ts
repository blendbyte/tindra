import { expect, test } from '@playwright/test'

const project = { id: '11111111-1111-4111-8111-111111111111', slug: 'storefront', name: 'Storefront', public_key: 'demo-public-key', event_count: 0 }
const other = { ...project, id: '22222222-2222-4222-8222-222222222222', slug: 'worker', name: 'Worker', public_key: 'worker-key' }

test('setup confirms its test and works across projects, themes, and mobile', async ({ page }, testInfo) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  let received = false
  let started = false
  const check = { id: '33333333-3333-4333-8333-333333333333', created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 86_400_000).toISOString(), received_at: null as string | null, event_id: null as string | null }
  await page.route(url => url.pathname.startsWith('/api/'), async route => {
    const url = new URL(route.request().url())
    const path = url.pathname
    let body: unknown = {}
    if (path === '/api/me') body = { id: 'user', email: 'developer@example.com', name: 'Developer', timezone: 'UTC', mfa_enabled: true, permissions: { manage_projects: true } }
    else if (path === '/api/config') body = { public_url: 'https://tindra.example.com', require_mfa: false }
    else if (path === '/api/projects/metadata' || path === '/api/projects') body = [project, other]
    else if (path.endsWith('/setup-checks')) { started = true; body = check }
    else if (path.endsWith('/setup-status')) body = {
      checked_at: new Date().toISOString(), profiling_enabled: true, observations: [],
      receipts: received && path.includes('storefront') ? [{ kind: 'events', first_received_at: check.created_at, last_received_at: check.created_at, latest_id: 'event' }] : [],
      check: started && path.includes('storefront') ? { ...check, received_at: received ? check.created_at : null, event_id: received ? 'event' : null } : null,
      examples: received ? { test: { id: 'event', issue_id: 'issue', environment: 'staging', sdk: 'sentry.javascript.node 10', received_at: check.created_at, release: '2026.09.09' } } : {},
      profile_transaction_id: null, sourcemap_count: 1, sourcemaps_available: true,
    }
    await route.fulfill({ json: body })
  })
  await page.setViewportSize({ width: 1280, height: 1000 })
  await page.goto('/projects/storefront/setup')
  await expect(page.getByRole('heading', { name: 'Waiting for your first event' })).toBeVisible()
  await page.getByRole('button', { name: 'Start setup check' }).click()
  await expect(page.locator('pre')).toContainText(check.id)
  await page.screenshot({ path: testInfo.outputPath('setup-desktop.png'), fullPage: true })
  received = true
  // Confirmation must arrive through polling without a manual refresh.
  await expect(page.getByRole('heading', { name: 'Test event received' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'View event' }).first()).toHaveAttribute('href', '/issues/issue?event_id=event')
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'))
  await page.screenshot({ path: testInfo.outputPath('setup-dark.png'), fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.screenshot({ path: testInfo.outputPath('setup-mobile.png'), fullPage: true })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  expect(await page.locator('.setup').evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
  await page.getByLabel('Project', { exact: true }).selectOption('worker')
  await expect(page.getByRole('heading', { name: 'Waiting for your first event' })).toBeVisible()
  await expect(page.getByLabel('Project DSN')).toHaveValue(`https://worker-key@tindra.example.com/${other.id}`)
  await expect(page.locator('pre')).toHaveCount(0)
  expect(errors).toEqual([])
})
