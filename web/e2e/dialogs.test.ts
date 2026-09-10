import { test, expect } from '@playwright/test'

test.beforeEach(async ({ page }) => {
  await page.route('**/api/**', async route => {
    const path = new URL(route.request().url()).pathname
    if (!path.startsWith('/api/')) return route.fallback()
    let json: unknown = []
    if (path === '/api/me') json = { id: 'u1', email: 'dev@example.com', mfa_enabled: true, permissions: {} }
    if (path === '/api/config') json = { require_mfa: false }
    if (path === '/api/settings') json = {}
    await route.fulfill({ json })
  })
})

for (const width of [375, 1280]) {
  test(`dialogs contain and restore focus at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 })
    await page.goto('/settings')
    const trigger = page.getByRole('button', { name: /^Shortcuts/ })
    await trigger.focus()
    await page.keyboard.press('Control+k')
    const palette = page.getByRole('dialog', { name: 'Command palette' })
    const search = palette.getByRole('combobox', { name: 'Search' })
    await expect(search).toBeFocused()
    await expect(palette).toHaveAttribute('aria-modal', 'true')
    for (const key of ['Tab', 'Shift+Tab', 'Tab']) {
      await page.keyboard.press(key)
      await expect(search).toBeFocused()
    }
    // Native modal isolation prevents even programmatic background focus.
    await page.locator('button.settings__about-link').evaluate(el => (el as HTMLElement).focus())
    await expect(search).toBeFocused()
    await page.keyboard.press('ArrowDown')
    await expect(search).toHaveAttribute('aria-activedescendant', 'command-option-1')
    await expect(palette.getByRole('option', { selected: true })).toHaveCount(1)
    await page.keyboard.press('Escape')
    await expect(palette).toHaveCount(0)
    await expect(trigger).toBeFocused()

    await trigger.click()
    const shortcuts = page.getByRole('dialog', { name: 'Keyboard shortcuts' })
    const close = shortcuts.getByRole('button', { name: 'Close keyboard shortcuts' })
    await expect(close).toBeFocused()
    for (const key of ['Tab', 'Shift+Tab']) {
      await page.keyboard.press(key)
      await expect(close).toBeFocused()
    }
    await page.locator('button.settings__about-link').evaluate(el => (el as HTMLElement).focus())
    await expect(close).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(shortcuts).toHaveCount(0)
    await expect(trigger).toBeFocused()

    await trigger.click()
    await close.click()
    await expect(trigger).toBeFocused()
    await trigger.click()
    await page.locator('.shortcuts-overlay').click({ position: { x: 2, y: 2 } })
    await expect(shortcuts).toHaveCount(0)
    await expect(trigger).toBeFocused()

    // One question-mark press opens help and cannot close it again in the
    // same event dispatch. Subsequent held-key events must leave it open.
    await page.keyboard.press('?')
    await expect(shortcuts).toBeVisible()
    await expect(close).toBeFocused()
    await page.keyboard.down('?')
    await page.keyboard.down('?')
    await page.keyboard.up('?')
    await expect(shortcuts).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(shortcuts).toHaveCount(0)
    await expect(trigger).toBeFocused()
  })
}
