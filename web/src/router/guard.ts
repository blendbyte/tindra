import type { RouteLocationNormalized } from 'vue-router'

// safeRedirect returns a same-origin path to continue to after login. Anything
// else, such as "//evil.example" or "https://...", falls back to the dashboard.
export function safeRedirect(value: unknown): string {
  if (typeof value !== 'string' || !value.startsWith('/')) return '/dashboard'
  if (value.startsWith('//') || value.startsWith('/\\')) return '/dashboard'
  if (value === '/login' || value.startsWith('/login?')) return '/dashboard'
  return value
}

// authGuard sends signed-out users to the login page, remembering where they
// were going, and users who still need MFA to its setup page.
export async function authGuard(to: RouteLocationNormalized) {
  const { useAuthStore } = await import('@/stores/auth')
  const auth = useAuthStore()
  await auth.init()
  if (to.meta.requiresAuth && !auth.user) return { path: '/login', query: { redirect: to.fullPath } }
  if (to.meta.requiresAuth && auth.user && auth.requireMfa && !auth.user.mfa_enabled && to.name !== 'setup-mfa') return '/setup-mfa'
  if (to.name === 'setup-mfa' && auth.user?.mfa_enabled) return '/dashboard'
}
