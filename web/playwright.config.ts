import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: process.env.CI ? 'http://127.0.0.1:18080' : 'http://localhost:5173',
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  // CI tests the compiled app and its embedded production frontend.
  // Locally, Vite proxies /api to the backend started with `make run`.
  webServer: process.env.CI
    ? {
        command: '../bin/tindra serve',
        url: 'http://127.0.0.1:18080/login',
        reuseExistingServer: false,
        timeout: 60_000,
      }
    : {
        command: 'bun run dev',
        url: 'http://localhost:5173',
        reuseExistingServer: true,
        timeout: 30_000,
      },
})
