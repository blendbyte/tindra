import { beforeEach, describe, expect, it, vi } from 'vitest'
import { router } from '../index'

const auth = vi.hoisted(() => ({
  init: vi.fn().mockResolvedValue(undefined),
  user: { mfa_enabled: true } as { mfa_enabled: boolean } | null,
  requireMfa: false,
}))

vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('@/views/SettingsView.vue', () => ({ default: { template: '<div />' } }))
vi.mock('@/views/LoginView.vue', () => ({ default: { template: '<div />' } }))

beforeEach(() => {
  auth.user = { mfa_enabled: true }
})

describe('Alerts routing', () => {
  it('serves the dedicated page with its title and standalone presentation', async () => {
    await router.push('/alerts')
    expect(router.currentRoute.value.name).toBe('alerts')
    expect(router.currentRoute.value.matched[0]!.props.default).toEqual({ alertsPage: true })
    expect(document.title).toBe('Alerts - Tindra')
  })

  it('redirects old links while preserving repeated project filters and the fragment', async () => {
    await router.push('/settings/alerts?new=1&trigger=log_count&project_id=p1&project_id=p2&search=timeout#rule')
    expect(router.currentRoute.value.name).toBe('alerts')
    expect(router.currentRoute.value.query).toEqual({ new: '1', trigger: 'log_count', project_id: ['p1', 'p2'], search: 'timeout' })
    expect(router.currentRoute.value.hash).toBe('#rule')
  })

  it('keeps normal settings routes independent of the alerts presentation', async () => {
    await router.push('/settings/projects')
    expect(router.currentRoute.value.name).toBe('settings')
    expect(router.currentRoute.value.params.tab).toBe('projects')
    expect(router.currentRoute.value.matched[0]!.props.default).toBe(false)
    expect(document.title).toBe('Settings - Tindra')
  })

  it('requires authentication for legacy alerts links', async () => {
    auth.user = null
    await router.push('/settings/alerts')
    expect(router.currentRoute.value.name).toBe('login')
  })
})
