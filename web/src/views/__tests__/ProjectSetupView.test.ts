import { afterEach, describe, expect, it, vi } from 'vitest'
import { shallowMount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createPinia, disposePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import ProjectSetupView from '../ProjectSetupView.vue'
import { router as appRouter } from '@/router'

let wrapper: VueWrapper | undefined
let pinia: ReturnType<typeof createPinia>
let client: QueryClient
const projects = [{ id: 'one', slug: 'first', name: 'First', public_key: 'key' }, { id: 'two', slug: 'second', name: 'Second', public_key: 'key2' }]
afterEach(() => { wrapper?.unmount(); disposePinia(pinia); client.clear() })
async function open(path: string) {
 pinia = createPinia()
 client = new QueryClient({ defaultOptions: { queries: { enabled: false, retry: false } } })
 client.setQueryData(['projects', 'metadata'], projects)
 const router = createRouter({ history: createMemoryHistory(), routes: appRouter.options.routes })
 await router.push(path)
 await router.isReady()
 wrapper = shallowMount(ProjectSetupView, { global: { plugins: [pinia, router, [VueQueryPlugin, { queryClient: client }]] } })
 await flushPromises()
 return router
}
describe('project setup routes', () => {
 it('selects a project by slug, forwards the diagnostic focus, and continues to dashboard', async () => {
  const router = await open('/projects/first/setup?check=profiles')
  expect(router.currentRoute.value.meta.requiresAuth).toBe(true)
  const panel = wrapper!.findComponent({ name: 'ProjectSetup' })
  expect(panel.props('project').id).toBe('one')
  expect(panel.props('focus')).toBe('profiles')
  panel.vm.$emit('continue')
  await vi.waitFor(() => expect(router.currentRoute.value.path).toBe('/dashboard'))
 })
 it('selects by project ID and switches to the chosen project route', async () => {
  const router = await open('/setup?project_id=two')
  expect(wrapper!.findComponent({ name: 'ProjectSetup' }).props('project').id).toBe('two')
  await wrapper!.find('select').setValue('first')
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/projects/first/setup')
 })
 it('asks for a project when the linked project is unavailable', async () => {
  await open('/setup?project_id=missing')
  expect(useProjectsStore(pinia).projects).toHaveLength(2)
  expect(wrapper!.text()).toContain('Choose an available project')
  expect(wrapper!.findComponent({ name: 'ProjectSetup' }).exists()).toBe(false)
 })
})
