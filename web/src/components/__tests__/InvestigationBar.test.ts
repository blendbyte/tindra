import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import InvestigationBar from '../InvestigationBar.vue'
import { useInvestigationStore } from '@/stores/investigation'

const mocks = vi.hoisted(() => ({ api: vi.fn(), replace: vi.fn(), route: { path: '/logs' }, freshness: {} as any, projects: { metadataReady: false, invalidIds: [] as string[] } }))
vi.mock('vue-router', () => ({ useRoute: () => mocks.route, useRouter: () => ({ replace: mocks.replace }) }))
vi.mock('@/api/client', () => ({ apiFetch: (...args: any[]) => mocks.api(...args) }))
vi.mock('@/stores/projects', () => ({ useProjectsStore: () => mocks.projects }))
vi.mock('@/composables/useViewFreshness', () => ({ useViewFreshness: () => mocks.freshness }))
beforeEach(() => {
  mocks.route = reactive({ path: '/logs' })
  mocks.api.mockReset().mockResolvedValue(['preview', 'production', 'preview'])
  mocks.replace.mockClear()
  mocks.projects = reactive({ metadataReady: false, invalidIds: [] })
  mocks.freshness = {
    fetching: ref(false), failed: ref([]), updatedAt: ref(0), age: ref(0),
    online: ref(true), interval: ref(5000), automatic: ref(true), refresh: vi.fn(),
  }
})
function setup() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  return mount(InvestigationBar, { global: { stubs: { UserFilter: true }, plugins: [[VueQueryPlugin, { queryClient: client }]] } })
}
describe('investigation bar', () => {
  it('loads project-scoped environments, retains custom values and changes shared filters', async () => {
    const state = useInvestigationStore()
    state.projectIds = ['b', 'a']
    state.environment = 'regional'
    const wrapper = setup()
    await flushPromises()
    expect(mocks.api.mock.calls[0][0]).toBe('/api/environments?project_id=a&project_id=b')
    const chips = wrapper.findAllComponents({ name: 'FilterChip' })
    expect(chips[1]!.props('options')).toEqual(['All', 'preview', 'production', 'regional'])
    chips[0]!.vm.$emit('change', '7d')
    chips[1]!.vm.$emit('change', 'preview')
    expect(state.range).toBe('7d')
    expect(state.environment).toBe('preview')
    await wrapper.findAll('button').find(b => b.attributes('aria-label') === 'Pause')!.trigger('click')
    expect(state.paused).toBe(true)
    await wrapper.findAll('button').find(b => b.attributes('aria-label') === 'Resume')!.trigger('click')
    expect(state.paused).toBe(false)
    await wrapper.findAll('button').find(b => b.attributes('aria-label') === 'Refresh now')!.trigger('click')
    expect(mocks.freshness.refresh).toHaveBeenCalledOnce()
  })
  it('shows custom bounds, unavailable projects, and environment errors', async () => {
    const state = useInvestigationStore()
    state.absolute = { from: '2026-01-01T00:00:00Z', to: '2026-01-02T00:00:00Z' }
    mocks.projects.metadataReady = true; mocks.projects.invalidIds = ['missing']
    mocks.api.mockRejectedValue(new Error('unavailable'))
    const wrapper = setup()
    await flushPromises()
    expect(wrapper.text()).toContain("Couldn't load environment options.")
    expect(wrapper.text()).toContain(state.absolute.from)
    expect(wrapper.text()).toContain('Some selected projects are unavailable')
    expect(wrapper.text()).not.toContain('Resume')
  })
  it.each([
    ['/monitors/uptime', 'Current monitor status'],
    ['/releases/abc', 'All deployments'],
    ['/issues/abc', 'This record has its own project'],
  ])('explains the scope of %s without loading environments', async (path, note) => {
    mocks.route.path = path
    const wrapper = setup()
    await flushPromises()
    expect(wrapper.text()).toContain(note)
    expect(mocks.api).not.toHaveBeenCalled()
  })
  it('offers issue-only ranges and resets invalid links', async () => {
    mocks.route.path = '/issues'
    const state = useInvestigationStore()
    state.routeError = 'Invalid project'
    const wrapper = setup()
    expect(wrapper.findComponent({ name: 'FilterChip' }).props('options')).toContain('All')
    await wrapper.find('button').trigger('click')
    expect(mocks.replace).toHaveBeenCalledWith({ query: { project_id: 'all', environment: 'all', range: '24h' } })
    expect(mocks.api).not.toHaveBeenCalled()
  })
  it('reports loading, partial failures, age, offline and paused pagination accurately', async () => {
    const wrapper = setup()
    const f = mocks.freshness
    expect(wrapper.text()).toContain('Waiting for data')
    f.fetching.value = true
    await flushPromises()
    expect(wrapper.text()).toContain('Loading…')
    expect(wrapper.findAll('button').find(b => b.attributes('aria-label') === 'Refreshing…')!.attributes('disabled')).toBeDefined()
    f.fetching.value = false; f.failed.value = [{}]
    await flushPromises()
    expect(wrapper.text()).toContain("Couldn't refresh 1 panel.")
    expect(wrapper.text()).toContain('Data unavailable')
    f.updatedAt.value = 100; f.age.value = 125; f.failed.value = [{}, {}]
    await flushPromises()
    expect(wrapper.text()).toContain('Showing data from 2m ago')
    f.failed.value = []; f.age.value = 5; f.online.value = false; f.automatic.value = false
    await flushPromises()
    expect(wrapper.text()).toContain('Updated 5s ago')
    expect(wrapper.text()).toContain('Offline.')
    expect(wrapper.text()).toContain('Auto-refresh paused')
    useInvestigationStore().browsingHistory = true
    await flushPromises()
    expect(wrapper.text()).toContain('Paused while browsing older results')
    expect(wrapper.find('[aria-label="Refresh latest"]').exists()).toBe(true)
  })
})


it('keeps dashboard explanations behind the info control and dismisses them with Escape', async () => {
  mocks.route.path = '/dashboard'
  const wrapper = setup()
  const details = wrapper.find('details')
  expect(details.attributes('open')).toBeUndefined()
  expect(details.text()).toContain('Transaction metrics use the selected filters')
  expect(wrapper.find('[aria-label="Refresh now"]').text()).toBe('')
  ;(details.element as HTMLDetailsElement).open = true
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
  expect((details.element as HTMLDetailsElement).open).toBe(false)
})


it('shows compact elapsed time inside the refresh control', async () => {
  const wrapper = setup()
  const button = wrapper.find('[aria-label="Refresh now"]')
  expect(button.text()).toBe('')
  mocks.freshness.updatedAt.value = 100
  for (const [seconds, label] of [[13, '13s'], [125, '2m'], [7200, '2h']] as const) {
    mocks.freshness.age.value = seconds
    await flushPromises()
    expect(button.text()).toBe(label)
  }
  await button.trigger('click')
  expect(mocks.freshness.refresh).toHaveBeenCalledOnce()
})
