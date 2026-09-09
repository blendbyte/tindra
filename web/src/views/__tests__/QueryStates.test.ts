import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { reactive, ref, nextTick } from 'vue'
import { createPinia } from 'pinia'
import { QueryClient, VueQueryPlugin, onlineManager } from '@tanstack/vue-query'
import type { IssueListPage, LogListPage, Project } from '@/api/types'

const route = reactive({ query: {} as Record<string, string> })
vi.mock('vue-router', () => ({
  useRoute: () => route,
  useRouter: () => ({ push: vi.fn(), replace: vi.fn().mockResolvedValue(undefined) }),
}))
vi.mock('@/api/client', () => ({ apiFetch: vi.fn() }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { timezone: 'UTC', permissions: {} } }) }))
vi.mock('@/stores/appUser', () => ({
  useAppUserStore: () => ({ identity: '', label: '', clear: vi.fn() }),
  routeUserIdentity: (q: Record<string, string>) => q.user ?? '',
}))
vi.mock('@/stores/issueNav', () => ({ useIssueNavStore: () => ({ set: vi.fn() }) }))
vi.mock('@/composables/useToast', () => ({ useToast: () => ({ show: vi.fn() }) }))
vi.mock('@tanstack/vue-virtual', () => ({
  useWindowVirtualizer: () => ref({ getVirtualItems: () => [], getTotalSize: () => 0, options: { scrollMargin: 0 } }),
}))

import { apiFetch } from '@/api/client'
import { useProjectsStore } from '@/stores/projects'
import LogsView from '../LogsView.vue'
import IssueListView from '../IssueListView.vue'

const project = { id: 'p1', name: 'App', slug: 'app' } as Project
const emptyLogs: LogListPage = { logs: [], has_more: false }
const emptyIssues: IssueListPage = { issues: [], total: 0, has_more: false }
const logPage = { logs: [{ id: 'l1', project_id: 'p1', level: 'info', body: 'Previously received log', timestamp: '2026-09-09T10:00:00Z', attributes: {} }], has_more: false } as LogListPage
const issuePage = {
  issues: [{ id: 'i1', project_id: 'p1', title: 'Previously received issue', status: 'open', level: 'error', last_seen: '2026-09-09T10:00:00Z' }],
  total: 2, has_more: true, next_cursor_time: '2026-09-09T10:00:00Z', next_cursor_id: 'i1',
} as IssueListPage

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

let client: QueryClient
let wrapper: VueWrapper | undefined
let readLogs: () => Promise<LogListPage>
let readIssues: (path: string) => Promise<IssueListPage>
let readProjects: () => Promise<Project[]>

beforeEach(() => {
  route.query = {}
  const stored = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => stored.get(key) ?? null,
    setItem: (key: string, value: string) => stored.set(key, value),
    clear: () => stored.clear(),
  })
  sessionStorage.clear()
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } })
  readLogs = async () => emptyLogs
  readIssues = async () => emptyIssues
  readProjects = async () => [project]
  vi.mocked(apiFetch).mockImplementation(async (path) => {
    if (path.startsWith('/api/logs?')) return readLogs()
    if (path.startsWith('/api/issues?')) return readIssues(path)
    if (path === '/api/projects/metadata') return readProjects()
    if (path === '/api/users') return []
    if (path === '/api/me') return { id: 'me' }
    throw new Error(`Unexpected request ${path}`)
  })
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  client?.clear()
  onlineManager.setOnline(true)
  vi.unstubAllGlobals()
})

function render(view: typeof LogsView | typeof IssueListView) {
  const pinia = createPinia()
  wrapper = mount(view, { global: {
    plugins: [pinia, [VueQueryPlugin, { queryClient: client }]],
    stubs: {
      RouterLink: { template: '<a><slot /></a>' },
      FilterChip: { name: 'FilterChip', props: ['label', 'value', 'options'], template: '<div />' },
      UserFilter: true, Sparkline: true, IgnoreButton: true,
    },
  } })
  return { wrapper, projects: useProjectsStore(pinia) }
}

for (const kind of ['logs', 'issues'] as const) {
  describe(`${kind} query states with a real query cache`, () => {
    const view = kind === 'logs' ? LogsView : IssueListView
    const empty = kind === 'logs' ? emptyLogs : emptyIssues
    const result = kind === 'logs' ? logPage : issuePage
    function respond(fn: () => Promise<LogListPage | IssueListPage>) {
      if (kind === 'logs') readLogs = fn as typeof readLogs
      else readIssues = fn as typeof readIssues
    }

    it('shows loading, then a recoverable error, then a verified empty result', async () => {
      const request = deferred<typeof empty>()
      respond(() => request.promise)
      const { wrapper } = render(view)
      expect(wrapper.find(`[aria-label="Loading ${kind}"]`).exists()).toBe(true)
      expect(wrapper.find('.empty-state').exists()).toBe(false)
      request.reject(new Error('database unavailable'))
      await flushPromises()
      expect(wrapper.text()).toContain(`We couldn't load ${kind}`)
      expect(wrapper.find('.empty-state').exists()).toBe(false)
      respond(async () => empty)
      await wrapper.find('.query-feedback button').trigger('click')
      await flushPromises()
      expect(wrapper.find('.query-feedback').exists()).toBe(false)
      expect(wrapper.find('.empty-state').exists()).toBe(true)
      expect(wrapper.text()).not.toMatch(/All clear|connected and listening|Everything's resolved/)
    })

    for (const previous of ['rows', 'empty'] as const) {
      it(`keeps the last ${previous} result and timestamp when a refresh fails`, async () => {
        respond(async () => previous === 'rows' ? result : empty)
        const { wrapper } = render(view)
        await flushPromises()
        const before = client.getQueriesData({ queryKey: [kind] })[0]![1]
        respond(async () => { throw new Error('offline') })
        await client.refetchQueries({ queryKey: [kind] })
        await flushPromises()
        expect(wrapper.text()).toContain(`Couldn't refresh ${kind}`)
        expect(wrapper.text()).toContain('Showing data last updated at')
        expect(client.getQueriesData({ queryKey: [kind] })[0]![1]).toEqual(before)
        if (previous === 'empty') expect(wrapper.find('.empty-state').exists()).toBe(true)
        else if (kind === 'logs') expect(wrapper.text()).toContain('Previously received log')
        else expect(wrapper.find('.list-footer__count').text()).toContain('1 of 2 loaded')
        respond(async () => empty)
        await wrapper.find('.query-feedback button').trigger('click')
        await flushPromises()
        expect(wrapper.find('.query-feedback').exists()).toBe(false)
      })
    }

    it('does not show results from the previous selection when the new query fails', async () => {
      respond(async () => result)
      const { wrapper } = render(view)
      await flushPromises()
      respond(async () => { throw new Error('new scope failed') })
      route.query = { user: 'different-user' }
      await nextTick()
      await flushPromises()
      expect(wrapper.text()).toContain(`We couldn't load ${kind}`)
      expect(wrapper.text()).not.toContain(`Couldn't refresh ${kind}`)
      expect(wrapper.text()).not.toContain('Previously received')
      expect(wrapper.find('.empty-state').exists()).toBe(false)
      expect(wrapper.find('.list-footer').exists()).toBe(false)
    })
  })
}

it('does not claim there are no projects while their request is pending or failed', async () => {
  const request = deferred<Project[]>()
  readProjects = () => request.promise
  sessionStorage.setItem('tindra:projectFilter', JSON.stringify(['p1']))
  const { wrapper, projects } = render(IssueListView)
  await flushPromises()
  expect(wrapper.text()).not.toContain('No projects yet')
  expect(projects.hasLoaded).toBe(false)
  expect(projects.selectedIds).toEqual(['p1'])
  request.reject(new Error('projects unavailable'))
  await flushPromises()
  expect(wrapper.text()).toContain("We couldn't load projects")
  expect(wrapper.text()).not.toContain('No projects yet')
  expect(projects.selectedIds).toEqual(['p1'])
  readProjects = async () => []
  await projects.refetch()
  await flushPromises()
  expect(projects.hasLoaded).toBe(true)
  expect(projects.selectedIds).toEqual(['p1'])
  expect(projects.invalidIds).toEqual(['p1'])
  expect(wrapper.text()).toContain('No projects yet')
})

it('reports pagination failures inline and retries without removing loaded issues', async () => {
  readIssues = async (path) => {
    if (path.includes('cursor_id')) throw new Error('next page unavailable')
    return issuePage
  }
  const { wrapper } = render(IssueListView)
  await flushPromises()
  await wrapper.find('.list-footer__more').trigger('click')
  await flushPromises()
  expect(wrapper.text()).toContain("Couldn't load more issues")
  expect(wrapper.find('.list-footer__count').text()).toContain('1 of 2 loaded')
  readIssues = async () => ({ ...issuePage, issues: [{ ...issuePage.issues[0]!, id: 'i2' }], has_more: false })
  await wrapper.find('[role="alert"] button').trigger('click')
  await flushPromises()
  expect(wrapper.text()).not.toContain("Couldn't load more issues")
  expect(wrapper.find('.list-footer__count').text()).toContain('2 of 2 loaded')
})

it('discards a late pagination response after the filter changes', async () => {
  const request = deferred<IssueListPage>()
  readIssues = async (path) => path.includes('cursor_id') ? request.promise : issuePage
  const { wrapper } = render(IssueListView)
  await flushPromises()
  await wrapper.find('.list-footer__more').trigger('click')
  readIssues = async () => emptyIssues
  route.query = { user: 'different-user' }
  await flushPromises()
  request.resolve({ ...issuePage, issues: [{ ...issuePage.issues[0]!, id: 'old-page-row' }], has_more: false })
  await flushPromises()
  expect(wrapper.find('.empty-filter').exists()).toBe(true)
  expect(wrapper.find('.list-footer').exists()).toBe(false)
  expect(wrapper.text()).not.toContain("Couldn't load more issues")
})

it('keeps loaded pages on unchanged polls but resets them when first-page contents change', async () => {
  readIssues = async (path) => path.includes('cursor_id')
    ? { ...issuePage, issues: [{ ...issuePage.issues[0]!, id: 'i2' }], has_more: false }
    : issuePage
  const { wrapper } = render(IssueListView)
  await flushPromises()
  await wrapper.find('.list-footer__more').trigger('click')
  await flushPromises()
  expect(wrapper.find('.list-footer__count').text()).toContain('2 of 2 loaded')
  const query = client.getQueryCache().find({ queryKey: ['issues'], exact: false })!
  const before = query.state.data
  const updatedAt = query.state.dataUpdatedAt
  await new Promise(resolve => setTimeout(resolve, 5))
  await client.refetchQueries({ queryKey: ['issues'] })
  await flushPromises()
  expect(query.state.dataUpdatedAt).toBeGreaterThan(updatedAt)
  expect(query.state.data).toBe(before)
  expect(wrapper.find('.list-footer__count').text()).toContain('2 of 2 loaded')
  readIssues = async () => ({ ...issuePage, total: 3 })
  await client.refetchQueries({ queryKey: ['issues'] })
  await flushPromises()
  expect(wrapper.find('.list-footer__count').text()).toContain('1 of 3 loaded')
})

it('keeps an in-flight next page when an unchanged first-page refresh completes', async () => {
  const request = deferred<IssueListPage>()
  readIssues = async (path) => path.includes('cursor_id') ? request.promise : issuePage
  const { wrapper } = render(IssueListView)
  await flushPromises()
  await wrapper.find('.list-footer__more').trigger('click')
  await client.refetchQueries({ queryKey: ['issues'] })
  await flushPromises()
  expect(wrapper.find('.list-footer__more').text()).toContain('Loading')
  request.resolve({ ...issuePage, issues: [{ ...issuePage.issues[0]!, id: 'i2' }], has_more: false })
  await flushPromises()
  expect(wrapper.find('.list-footer__count').text()).toContain('2 of 2 loaded')
})

for (const kind of ['logs', 'issues'] as const) {
  describe(`${kind} offline query states`, () => {
    const view = kind === 'logs' ? LogsView : IssueListView
    const empty = kind === 'logs' ? emptyLogs : emptyIssues

    it('shows a paused state instead of an endless loading skeleton, then resumes online', async () => {
      onlineManager.setOnline(false)
      const { wrapper } = render(view)
      await flushPromises()
      expect(wrapper.text()).toContain(`Loading ${kind} is paused`)
      expect(wrapper.text()).toContain('Check your connection')
      expect(wrapper.find(`[aria-label="Loading ${kind}"]`).exists()).toBe(false)
      expect(wrapper.find('.empty-state').exists()).toBe(false)
      expect(wrapper.find('.query-feedback button').exists()).toBe(false)
      onlineManager.setOnline(true)
      await flushPromises()
      expect(wrapper.find('.query-feedback').exists()).toBe(false)
      expect(wrapper.find('.empty-state').exists()).toBe(true)
    })

    for (const previous of ['rows', 'empty'] as const) {
      it(`marks cached ${previous} as stale during a paused refresh and recovers`, async () => {
        if (kind === 'logs') readLogs = async () => previous === 'rows' ? logPage : emptyLogs
        else readIssues = async () => previous === 'rows' ? issuePage : emptyIssues
        const { wrapper } = render(view)
        await flushPromises()
        const query = client.getQueryCache().find({ queryKey: [kind], exact: false })!
        const before = query.state.data
        const updatedAt = query.state.dataUpdatedAt
        onlineManager.setOnline(false)
        await client.refetchQueries({ queryKey: [kind] })
        await flushPromises()
        expect(query.state.fetchStatus).toBe('paused')
        expect(wrapper.text()).toContain(`Updates to ${kind} are paused`)
        expect(wrapper.text()).toContain('Showing data last updated at')
        expect(query.state.data).toBe(before)
        expect(query.state.dataUpdatedAt).toBe(updatedAt)
        if (previous === 'empty') expect(wrapper.find('.empty-state').exists()).toBe(true)
        else if (kind === 'logs') expect(wrapper.text()).toContain('Previously received log')
        else {
          expect(wrapper.find('.list-footer__count').text()).toContain('1 of 2 loaded')
          expect(wrapper.find('.list-footer__more').attributes('disabled')).toBeDefined()
        }
        if (kind === 'logs') readLogs = async () => empty as LogListPage
        else readIssues = async () => empty as IssueListPage
        onlineManager.setOnline(true)
        await flushPromises()
        expect(wrapper.find('.query-feedback').exists()).toBe(false)
      })
    }
  })
}
