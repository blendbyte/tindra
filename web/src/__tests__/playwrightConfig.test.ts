// @vitest-environment node
import { afterEach, describe, expect, it, vi } from 'vitest'

afterEach(() => {
  vi.unstubAllEnvs()
  vi.resetModules()
})

describe('browser test server configuration', () => {
  it('starts an isolated production server in CI and rejects an existing server', async () => {
    vi.stubEnv('CI', 'true')
    const { default: config } = await import('../../playwright.config')
    expect(config.forbidOnly).toBe(true)
    expect(config.retries).toBe(2)
    expect(config.workers).toBe(1)
    expect(config.reporter).toBe('github')
    expect(config.use?.baseURL).toBe('http://127.0.0.1:18080')
    expect(config.webServer).toEqual({
      command: '../bin/tindra serve',
      url: `${config.use?.baseURL}/login`,
      reuseExistingServer: false,
      timeout: 60_000,
    })
  })

  it('keeps local browser tests on Vite with server reuse', async () => {
    vi.stubEnv('CI', undefined)
    const { default: config } = await import('../../playwright.config')
    expect(config.forbidOnly).toBe(false)
    expect(config.retries).toBe(0)
    expect(config.workers).toBeUndefined()
    expect(config.reporter).toBe('list')
    expect(config.use?.baseURL).toBe('http://localhost:5173')
    expect(config.webServer).toEqual({
      command: 'bun run dev',
      url: config.use?.baseURL,
      reuseExistingServer: true,
      timeout: 30_000,
    })
  })
})
