import type { RouteLocationNormalized } from 'vue-router'

// safeRedirect returns a same-origin path to continue to after login. Anything
// else, such as "//evil.example" or "https://...", falls back to the dashboard.
export function safeRedirect(value: unknown): string {
  if (typeof value !== 'string' || !value.startsWith('/')) return '/dashboard'
  if (value.startsWith('//') || value.startsWith('/\\')) return '/dashboard'
  if (value === '/login' || value.startsWith('/login?')) return '/dashboard'
  return value
}

// shortIdGuard turns /issues/BACKEND-42 into the issue's own URL, so links
// built from a short ID open the issue and every request uses its UUID.
export async function shortIdGuard(to: RouteLocationNormalized) {
  if (to.name !== 'issue-detail') return
  const ref = String(to.params.id ?? '')
  const { isShortId, findIssueByShortId } = await import('@/utils/shortId')
  if (!isShortId(ref)) return
  try {
    const issue = await findIssueByShortId(ref)
    if (issue) return { name: 'issue-detail', params: { id: issue.id }, query: to.query, hash: to.hash, replace: true }
  } catch {
    // The detail view reports the failure when it loads the issue.
  }
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
