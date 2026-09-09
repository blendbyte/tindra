import { test, expect } from '@playwright/test'

const project = { id: 'p1', name: 'Production checkout service with a very long project name', slug: 'checkout', public_key: 'public', created_at: '2026-01-01T00:00:00Z' }
const rule = { id: 'r1', name: 'Checkout errors', trigger: 'new_issue', channel: 'webhook', webhook_url: `https://example.com/${'long-path/'.repeat(15)}`, enabled: true, cooldown_mins: 60, project_ids: ['p1'] }

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => sessionStorage.setItem('tindra:projectFilter', '["p1"]'))
  await page.route('**/api/**', async route => {
    const path = new URL(route.request().url()).pathname
    if (!path.startsWith('/api/')) return route.fallback()
    let body: unknown = {}
    if (path === '/api/me') body = { id: 'u1', name: 'Alex', email: 'alex@example.com', mfa_enabled: true, permissions: { manage_projects: true, manage_alerts: true, manage_users: true } }
    else if (path === '/api/config') body = { require_mfa: false }
    else if (path === '/api/projects' || path === '/api/projects/metadata') body = [project, { ...project, id: 'p2', name: 'Staging', slug: 'staging' }]
    else if (path === '/api/alert-rules') body = { rules: [rule] }
    else if (path.endsWith('/firings')) body = { firings: [{ id: 'f1', fired_at: '2026-09-09T01:00:00Z', trigger: 'new_issue', status: 'failed', attempt: 3, status_code: 500, error: 'Delivery failed after retries', item_count: 12 }] }
    else if (['/api/tokens', '/api/users', '/api/invites'].includes(path)) body = []
    await route.fulfill({ json: body })
  })
})

for (const width of [320, 375, 479, 480, 639, 640, 768, 1024, 1199, 1200, 1399, 1400, 1920]) {
  test(`navigation and alert forms fit at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 800 })
    await page.goto('/alerts')
    await expect(page.getByRole('heading', { name: 'Alerts', exact: true })).toBeVisible()
    await expect(page.locator('.rule')).toBeVisible()
    await page.evaluate(() => document.fonts.ready)
    if (width === 375 || width === 1200) {
      await page.screenshot({ path: testInfo.outputPath('alerts.png') })
    }
    const navBounds = await page.locator('.nav').evaluate(nav => Array.from(nav.querySelectorAll<HTMLElement>(':scope > *, .nav__right > *')).filter(el => el.getBoundingClientRect().width > 0).map(el => ({ left: el.getBoundingClientRect().left, right: el.getBoundingClientRect().right })))
    for (const rect of navBounds) {
      expect(rect.left).toBeGreaterThanOrEqual(0)
      expect(rect.right).toBeLessThanOrEqual(width)
    }
    if (width < 1200) {
      await expect(page.locator('.nav__links')).toBeHidden()
      await page.getByRole('button', { name: 'Toggle navigation' }).click()
      await expect(page.locator('.nav__mobile-link[aria-current="page"]')).toHaveText('Alerts')
      await page.keyboard.press('Escape')
      await expect(page.locator('.nav__mobile-drawer')).toHaveCount(0)
      await expect(page.getByRole('button', { name: 'Toggle navigation' })).toBeFocused()
    } else {
      await expect(page.locator('.nav__links').getByRole('link', { name: 'Alerts', exact: true })).toBeVisible()
      const gap = await page.evaluate(() => document.querySelector('.nav__right')!.getBoundingClientRect().left - document.querySelector('.nav__links')!.getBoundingClientRect().right)
      expect(gap).toBeGreaterThanOrEqual(0)
    }
    await page.locator('.rule__head').click()
    await expect(page.locator('.rule__history-row')).toBeVisible()
    await page.getByRole('button', { name: 'Edit', exact: true }).click()
    expect(await page.locator('.page').evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
    await page.getByRole('button', { name: 'Cancel', exact: true }).click()
    await page.getByRole('button', { name: 'New rule', exact: true }).click()
    expect(await page.locator('.page').evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
    await page.getByRole('button', { name: /Filter projects:/ }).click()
    const popover = await page.locator('.nav__projects .popover').boundingBox()
    expect(popover!.x).toBeGreaterThanOrEqual(0)
    expect(popover!.x + popover!.width).toBeLessThanOrEqual(width)
  })
}

test('old alert links preserve log rule prefill', async ({ page }) => {
  await page.goto('/settings/alerts?new=1&trigger=log_count&level=error&environment=production&search=timeout&project_id=p1')
  await expect(page).toHaveURL(/\/alerts$/)
  await expect(page.locator('.alert-create')).toBeVisible()
  await expect(page.locator('.alert-create input').first()).toBeVisible()
  await expect(page.locator('.alert-create input[placeholder="Same as the Logs search box"]')).toHaveValue('timeout')
})

test('phone menu scrolls in landscape and closes on desktop resize', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 320 })
  await page.goto('/alerts')
  await page.getByRole('button', { name: 'Toggle navigation' }).click()
  await page.locator('.nav__mobile-drawer').getByRole('button', { name: 'Log out' }).scrollIntoViewIfNeeded()
  const bounds = await page.locator('.nav__mobile-drawer').boundingBox()
  expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(320)
  await page.setViewportSize({ width: 1440, height: 900 })
  await expect(page.locator('.nav__mobile-drawer')).toHaveCount(0)
})
