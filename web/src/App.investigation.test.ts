import { describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import { defineComponent, h } from 'vue'
import App from './App.vue'
import PerformanceSubnav from '@/components/PerformanceSubnav.vue'
import { installInvestigationRouter } from '@/router/investigation'
import { useInvestigationStore } from '@/stores/investigation'

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ ready: true, user: { id: '1' } }) }))
vi.mock('@/stores/ui', () => ({ useUiStore: () => ({ resolvedTheme: 'light' }) }))

async function setup(path: string) {
  const pinia = createPinia()
  const requests = vi.fn()
  const state = useInvestigationStore(pinia)
  const view = defineComponent({
    setup() {
      requests(state.request('/api/spans/db?hours=24'))
      return () => h('div', [h(PerformanceSubnav), h('p', { class: 'loaded-view' }, 'No database spans in this window')])
    },
  })
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/issues', component: { template: '<div>Issues</div>' }, meta: { title: 'Issues' } },
    { path: '/performance/queries', component: view, meta: { title: 'Queries' } },
    { path: '/logs', component: view, meta: { title: 'Logs' } },
  ] })
  installInvestigationRouter(router, pinia)
  await router.push(path)
  const wrapper = mount(App, { global: { plugins: [pinia, router], stubs: {
    Navbar: true, QuotaBanner: true, InvestigationBar: true,
    CommandPalette: true, ShortcutsModal: true, ToastStack: true,
  } } })
  await flushPromises()
  return { wrapper, router, state, requests }
}

describe('investigation page recovery', () => {
  it('mounts the performance empty state and active tab with bounded data after All on Issues', async () => {
    const { wrapper, router, state, requests } = await setup('/issues?range=all')
    await router.push(state.link('/performance/queries'))
    await flushPromises()
    expect(wrapper.find('.loaded-view').exists()).toBe(true)
    expect(wrapper.find('.perf-subnav__link--active').text()).toBe('Queries')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(requests).toHaveBeenCalledOnce()
    const request = new URL(requests.mock.calls[0]![0], 'http://test')
    expect(Date.parse(request.searchParams.get('to')!) - Date.parse(request.searchParams.get('from')!)).toBe(90 * 86400000)
  })

  it('keeps the active tab visible for an invalid link and recovers without losing view parameters', async () => {
    const { wrapper, router, state, requests } = await setup('/performance/queries?range=custom&from=bad&to=bad&op=db.sql#results')
    expect(wrapper.find('.perf-subnav__link--active').text()).toBe('Queries')
    expect(wrapper.find('[role="alert"] h2').text()).toBe('Check the filters in this link')
    expect(wrapper.find('[role="alert"] p').text()).toContain('dates in this link')
    expect(requests).not.toHaveBeenCalled()
    await wrapper.find('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(state.routeError).toBe('')
    expect(router.currentRoute.value.query).toEqual({ range: '24h', project_id: 'all', environment: 'all', user: '', op: 'db.sql' })
    expect(router.currentRoute.value.hash).toBe('#results')
    expect(wrapper.find('.loaded-view').exists()).toBe(true)
    expect(requests).toHaveBeenCalledOnce()
  })

  it('shows the page title when invalid filters block a page outside Performance', async () => {
    const { wrapper, requests } = await setup('/logs?project_id=invalid')
    expect(wrapper.find('h1').text()).toBe('Logs')
    expect(wrapper.find('[role="alert"] p').text()).toContain('project selection in this link is invalid')
    expect(wrapper.find('[role="alert"] button').text()).toBe('Reset filters')
    expect(requests).not.toHaveBeenCalled()
  })
})
