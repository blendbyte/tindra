import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRouter, createWebHistory } from 'vue-router'
import { defineComponent } from 'vue'
import { setActivePinia, createPinia } from 'pinia'
import type { User } from '@/api/types'
import { authGuard, safeRedirect, shortIdGuard } from '../guard'
import { findIssueByShortId } from '@/utils/shortId'

vi.mock('@/utils/shortId', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/utils/shortId')>()),
  findIssueByShortId: vi.fn(),
}))

const mockUser: User = {
  id: '1',
  email: 'test@example.com',
  name: 'Test User',
  has_password: true,
  mfa_enabled: true,
  weekly_digest: false,
  timezone: 'UTC',
  permissions: { manage_projects: false, manage_users: false, manage_alerts: false, manage_issues: false },
  created_at: '2025-01-01T00:00:00Z',
}

const mockUserNoMFA: User = {
  ...mockUser,
  mfa_enabled: false,
}

function buildGuardRouter() {
  const r = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/login', component: defineComponent({ template: '<div />' }) },
      { path: '/public', component: defineComponent({ template: '<div />' }) },
      { path: '/setup-mfa', name: 'setup-mfa', component: defineComponent({ template: '<div />' }), meta: { requiresAuth: true } },
      {
        path: '/dashboard',
        component: defineComponent({ template: '<div />' }),
        meta: { requiresAuth: true },
      },
      { path: '/issues/:id', component: defineComponent({ template: '<div />' }), meta: { requiresAuth: true } },
    ],
  })
  r.beforeEach(authGuard)
  return r
}

describe('router auth guard', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('allows navigation to public routes without authentication', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }))
    const router = buildGuardRouter()
    await router.push('/public')
    expect(router.currentRoute.value.path).toBe('/public')
  })

  it('allows navigation to /login without authentication', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }))
    const router = buildGuardRouter()
    await router.push('/login')
    expect(router.currentRoute.value.path).toBe('/login')
  })

  it('redirects to /login when navigating to a protected route with no session', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }))
    const router = buildGuardRouter()
    await router.push('/dashboard')
    expect(router.currentRoute.value.path).toBe('/login')
  })

  it('remembers the requested page so login can return to it', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }))
    const router = buildGuardRouter()
    await router.push('/issues/42?event_id=abc')
    expect(router.currentRoute.value.path).toBe('/login')
    expect(router.currentRoute.value.query.redirect).toBe('/issues/42?event_id=abc')
  })

  it('allows navigation to a protected route when /api/me returns a user', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve(mockUser),
    }))
    const router = buildGuardRouter()
    await router.push('/dashboard')
    expect(router.currentRoute.value.path).toBe('/dashboard')
  })

  it('calls /api/me on first protected navigation', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: false })
    vi.stubGlobal('fetch', mockFetch)
    const router = buildGuardRouter()
    await router.push('/dashboard')
    expect(mockFetch).toHaveBeenCalledWith('/api/me', expect.anything())
  })

  describe('double-login bug: ready-gate prevents re-fetch after app init', () => {
    it('redirects to /login when ready=true but user is null: init() skips the fetch', async () => {
      // Simulate app boot: init() ran, no session, sets ready=true, user stays null.
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }))
      const { useAuthStore } = await import('@/stores/auth')
      const store = useAuthStore()
      await store.init()
      expect(store.ready).toBe(true)
      expect(store.user).toBeNull()

      // After login the cookie exists, but ready is still true: the bug scenario.
      // Swap fetch for one that would succeed to prove it isn't called.
      const fetchAfterLogin = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve(mockUser),
      })
      vi.stubGlobal('fetch', fetchAfterLogin)

      const router = buildGuardRouter()
      await router.push('/dashboard')

      // init() was skipped entirely: no fetch, user still null, guard redirected.
      expect(fetchAfterLogin).not.toHaveBeenCalled()
      expect(router.currentRoute.value.path).toBe('/login')
    })

    it('allows navigation when ready is reset to false: init() re-fetches and gets the user', async () => {
      // Simulate app boot with no session.
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }))
      const { useAuthStore } = await import('@/stores/auth')
      const store = useAuthStore()
      await store.init()

      // User logs in → cookie is now valid. LoginView sets auth.ready = false.
      store.ready = false
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve(mockUser),
      }))

      const router = buildGuardRouter()
      await router.push('/dashboard')

      expect(store.user).toEqual(mockUser)
      expect(router.currentRoute.value.path).toBe('/dashboard')
    })
  })

  describe('MFA enforcement', () => {
    it('redirects to /setup-mfa when user has no MFA and navigates to a protected route', async () => {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve(mockUserNoMFA),
      }))
      const router = buildGuardRouter()
      await router.push('/dashboard')
      expect(router.currentRoute.value.path).toBe('/setup-mfa')
    })

    it('allows navigation to /setup-mfa when user has no MFA', async () => {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve(mockUserNoMFA),
      }))
      const router = buildGuardRouter()
      await router.push('/setup-mfa')
      expect(router.currentRoute.value.path).toBe('/setup-mfa')
    })

    it('redirects away from /setup-mfa to /dashboard when MFA is already enabled', async () => {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve(mockUser),
      }))
      const router = buildGuardRouter()
      await router.push('/setup-mfa')
      expect(router.currentRoute.value.path).toBe('/dashboard')
    })

    it('allows navigation to protected routes when MFA is enabled', async () => {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve(mockUser),
      }))
      const router = buildGuardRouter()
      await router.push('/dashboard')
      expect(router.currentRoute.value.path).toBe('/dashboard')
    })
  })
})

describe('safeRedirect', () => {
  it('keeps same-origin paths', () => {
    expect(safeRedirect('/issues/42?event_id=abc')).toBe('/issues/42?event_id=abc')
  })

  it('falls back to the dashboard for anything that could leave the site', () => {
    for (const value of [undefined, '', 'https://evil.example', '//evil.example', '/\\evil.example', 'issues', ['/issues'], '/login', '/login?redirect=/x']) {
      expect(safeRedirect(value)).toBe('/dashboard')
    }
  })
})

describe('short ID guard', () => {
  const uuid = '550e8400-e29b-41d4-a716-446655440000'

  function buildShortIdRouter() {
    const r = createRouter({
      history: createWebHistory(),
      routes: [
        { path: '/', component: defineComponent({ template: '<div />' }) },
        { path: '/issues/:id', name: 'issue-detail', component: defineComponent({ template: '<div />' }) },
      ],
    })
    r.beforeEach(shortIdGuard)
    return r
  }

  beforeEach(() => { vi.mocked(findIssueByShortId).mockReset() })

  it('opens the issue a short ID names under its own URL, keeping query and hash', async () => {
    vi.mocked(findIssueByShortId).mockResolvedValue({ id: uuid } as any)
    const router = buildShortIdRouter()
    await router.push('/issues/BACKEND-42?event_id=e1#stack')
    expect(findIssueByShortId).toHaveBeenCalledWith('BACKEND-42')
    expect(router.currentRoute.value.fullPath).toBe(`/issues/${uuid}?event_id=e1#stack`)
  })

  it('leaves UUIDs, unknown short IDs and failed lookups alone', async () => {
    const router = buildShortIdRouter()
    await router.push(`/issues/${uuid}`)
    expect(findIssueByShortId).not.toHaveBeenCalled()
    expect(router.currentRoute.value.path).toBe(`/issues/${uuid}`)

    vi.mocked(findIssueByShortId).mockResolvedValue(null)
    await router.push('/issues/BACKEND-999')
    expect(router.currentRoute.value.path).toBe('/issues/BACKEND-999')

    vi.mocked(findIssueByShortId).mockRejectedValue(new Error('offline'))
    await router.push('/issues/BACKEND-7')
    expect(router.currentRoute.value.path).toBe('/issues/BACKEND-7')
  })
})
