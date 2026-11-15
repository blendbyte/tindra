/**
 * PWA basics: the app is installable and registers its push service worker.
 * localhost counts as a secure context, so the worker registers here too.
 */

import { test, expect } from '@playwright/test'

test.beforeEach(async ({ page }) => {
  await page.route('**/api/auth/providers', route => route.fulfill({ json: { providers: [] } }))
})

test('links a valid web app manifest', async ({ page, request }) => {
  await page.goto('/login')
  const href = await page.locator('link[rel="manifest"]').getAttribute('href')
  expect(href).toBe('/manifest.webmanifest')
  const res = await request.get(href!)
  expect(res.ok()).toBe(true)
  const manifest = await res.json()
  expect(manifest).toMatchObject({ name: 'Tindra', start_url: '/', display: 'standalone' })
  for (const icon of manifest.icons as { src: string }[]) {
    expect((await request.get(icon.src)).headers()['content-type']).toBe('image/png')
  }
})

test('registers the service worker at the root scope', async ({ page }) => {
  await page.goto('/login')
  const scope = await page.evaluate(async () => {
    const reg = await navigator.serviceWorker.ready
    return new URL(reg.scope).pathname
  })
  expect(scope).toBe('/')
})
