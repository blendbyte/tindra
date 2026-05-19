import { createRouter, createWebHistory } from 'vue-router'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('@/views/LoginView.vue'),
      meta: { title: 'Sign in' },
    },
    {
      path: '/',
      redirect: '/dashboard',
    },
    {
      path: '/dashboard',
      name: 'dashboard',
      component: () => import('@/views/DashboardView.vue'),
      meta: { requiresAuth: true, title: 'Dashboard' },
    },
    {
      path: '/issues',
      name: 'issues',
      component: () => import('@/views/IssueListView.vue'),
      meta: { requiresAuth: true, title: 'Issues' },
    },
    {
      path: '/issues/:id',
      name: 'issue-detail',
      component: () => import('@/views/IssueDetailView.vue'),
      meta: { requiresAuth: true, title: 'Issue' },
    },
    {
      path: '/performance',
      redirect: '/performance/transactions',
    },
    {
      path: '/performance/transactions',
      name: 'transactions',
      component: () => import('@/views/TransactionListView.vue'),
      meta: { requiresAuth: true, title: 'Transactions' },
    },
    {
      path: '/performance/queries',
      name: 'perf-queries',
      component: () => import('@/views/DBQueriesView.vue'),
      meta: { requiresAuth: true, title: 'Queries' },
    },
    {
      path: '/performance/caches',
      name: 'perf-caches',
      component: () => import('@/views/CachesView.vue'),
      meta: { requiresAuth: true, title: 'Caches' },
    },
    {
      path: '/performance/jobs',
      name: 'perf-jobs',
      component: () => import('@/views/JobsView.vue'),
      meta: { requiresAuth: true, title: 'Jobs' },
    },
    {
      path: '/performance/browser',
      name: 'perf-browser',
      component: () => import('@/views/BrowserView.vue'),
      meta: { requiresAuth: true, title: 'Browser' },
    },
    {
      path: '/transactions/profile',
      name: 'transaction-profile',
      component: () => import('@/views/TransactionProfileView.vue'),
      meta: { requiresAuth: true, title: 'Transaction' },
    },
    {
      path: '/transactions/:id',
      name: 'transaction-detail',
      component: () => import('@/views/TransactionDetailView.vue'),
      meta: { requiresAuth: true, title: 'Transaction' },
    },
    {
      path: '/logs',
      name: 'logs',
      component: () => import('@/views/LogsView.vue'),
      meta: { requiresAuth: true, title: 'Logs' },
    },
    {
      path: '/monitors',
      name: 'monitors',
      component: () => import('@/views/MonitorsView.vue'),
      meta: { requiresAuth: true, title: 'Monitors' },
    },
    {
      path: '/releases',
      name: 'releases',
      component: () => import('@/views/ReleasesView.vue'),
      meta: { requiresAuth: true, title: 'Releases' },
    },
    {
      path: '/releases/:id',
      name: 'release-detail',
      component: () => import('@/views/ReleaseDetailView.vue'),
      meta: { requiresAuth: true, title: 'Release' },
    },
    {
      path: '/settings/:tab?',
      name: 'settings',
      component: () => import('@/views/SettingsView.vue'),
      meta: { requiresAuth: true, title: 'Settings' },
    },
    {
      path: '/invite/:token',
      name: 'accept-invite',
      component: () => import('@/views/AcceptInviteView.vue'),
      meta: { title: 'Accept invitation' },
    },
    {
      path: '/reset-password/:token',
      name: 'reset-password',
      component: () => import('@/views/ResetPasswordView.vue'),
      meta: { title: 'Reset password' },
    },
  ],
  scrollBehavior() {
    return { top: 0 }
  },
})

router.afterEach((to) => {
  const base = 'Tindra'
  const title = to.meta.title as string | undefined
  document.title = title ? `${title} - ${base}` : base
})

router.beforeEach(async (to) => {
  const { useAuthStore } = await import('@/stores/auth')
  const auth = useAuthStore()
  await auth.init()
  if (to.meta.requiresAuth && !auth.user) return '/login'
})
