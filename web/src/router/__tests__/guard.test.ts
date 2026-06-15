import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRouter, createWebHistory } from 'vue-router'
import { defineComponent } from 'vue'
import { setActivePinia, createPinia } from 'pinia'
import type { User } from '@/api/types'

const mockUser: User = {
  id: '1',
  email: 'test@example.com',
  name: 'Test User',
  role: 'member',
  mfa_enabled: true,
  created_at: '2025-01-01T00:00:00Z',
}

const mockUserNoMFA: User = {
  ...mockUser,
  mfa_enabled: false,
}

// Mirrors the beforeEach guard from router/index.ts — update both together if the logic changes.
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
    ],
  })

  r.beforeEach(async (to) => {
    const { useAuthStore } = await import('@/stores/auth')
    const auth = useAuthStore()
    await auth.init()
    if (to.meta.requiresAuth && !auth.user) return '/login'
    if (to.meta.requiresAuth && auth.user && !auth.user.mfa_enabled && to.name !== 'setup-mfa') return '/setup-mfa'
    if (to.name === 'setup-mfa' && auth.user?.mfa_enabled) return '/dashboard'
  })

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
    it('redirects to /login when ready=true but user is null — init() skips the fetch', async () => {
      // Simulate app boot: init() ran, no session, sets ready=true, user stays null.
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }))
      const { useAuthStore } = await import('@/stores/auth')
      const store = useAuthStore()
      await store.init()
      expect(store.ready).toBe(true)
      expect(store.user).toBeNull()

      // After login the cookie exists, but ready is still true — the bug scenario.
      // Swap fetch for one that would succeed to prove it isn't called.
      const fetchAfterLogin = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve(mockUser),
      })
      vi.stubGlobal('fetch', fetchAfterLogin)

      const router = buildGuardRouter()
      await router.push('/dashboard')

      // init() was skipped entirely — no fetch, user still null, guard redirected.
      expect(fetchAfterLogin).not.toHaveBeenCalled()
      expect(router.currentRoute.value.path).toBe('/login')
    })

    it('allows navigation when ready is reset to false — init() re-fetches and gets the user', async () => {
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
