import { describe, expect, it, vi } from 'vitest'
import { router } from '../index'

const auth = vi.hoisted(() => ({
  init: vi.fn().mockResolvedValue(undefined),
  user: { mfa_enabled: true },
  requireMfa: false,
}))

vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('@/views/monitors/CronMonitorsView.vue', () => ({ default: { name: 'CronMonitorsView', template: '<div />' } }))
vi.mock('@/views/monitors/UptimeMonitorsView.vue', () => ({ default: { name: 'UptimeMonitorsView', template: '<div />' } }))

function loadedComponentName() {
  const component = router.currentRoute.value.matched[0]!.components!.default as { name?: string }
  return component.name
}

describe('Monitors routing', () => {
  it('redirects /monitors to the cron page', async () => {
    await router.push('/monitors')
    expect(router.currentRoute.value.name).toBe('monitors-cron')
    expect(router.currentRoute.value.path).toBe('/monitors/cron')
  })

  it.each([
    ['/monitors/cron', 'monitors-cron', 'CronMonitorsView', 'Cron Monitors - Tindra'],
    ['/monitors/uptime', 'monitors-uptime', 'UptimeMonitorsView', 'Uptime Monitors - Tindra'],
  ])('serves %s from its own view', async (path, name, component, title) => {
    await router.push(path)
    expect(router.currentRoute.value.name).toBe(name)
    expect(router.currentRoute.value.meta.requiresAuth).toBe(true)
    expect(loadedComponentName()).toBe(component)
    expect(document.title).toBe(title)
  })
})
