import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref, nextTick } from 'vue'

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

vi.mock('vue-router', () => ({
  useRoute: vi.fn(),
  useRouter: vi.fn(),
}))

import { useQuery } from '@tanstack/vue-query'
import { useRoute, useRouter } from 'vue-router'
import type { Project, ServerSettings } from '@/api/types'
import QuotaBanner from '../QuotaBanner.vue'

function makeProject(id: string, eventCount: number): Project {
  return {
    id, name: `Project ${id}`, public_key: id, slug: id,
    passthrough_dsn: null, created_at: '2025-01-01T00:00:00Z',
    event_count: eventCount,
  }
}

function makeSettings(eventLimit: number): ServerSettings {
  return { event_limit: eventLimit, project_limit: 0, user_limit: 0, version: '0', commit: '' }
}

describe('QuotaBanner', () => {
  let settingsData: ReturnType<typeof ref<ServerSettings | undefined>>
  let projectsData: ReturnType<typeof ref<Project[] | undefined>>
  let pushMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    settingsData = ref(undefined)
    projectsData = ref(undefined)
    pushMock = vi.fn()

    vi.mocked(useQuery).mockImplementation(({ queryKey }: any) => {
      if (queryKey[0] === 'settings') return { data: settingsData } as any
      if (queryKey[0] === 'projects') return { data: projectsData } as any
      return { data: ref(undefined) } as any
    })

    vi.mocked(useRoute).mockReturnValue({ name: 'issues', path: '/issues' } as any)
    vi.mocked(useRouter).mockReturnValue({ push: pushMock } as any)
  })

  function mountBanner(routeName = 'issues') {
    if (routeName !== 'issues') {
      vi.mocked(useRoute).mockReturnValue({ name: routeName, path: `/${routeName}` } as any)
    }
    return mount(QuotaBanner, { global: { stubs: { Icon: true } } })
  }

  it('is hidden when settings data has not loaded', () => {
    projectsData.value = []
    const wrapper = mountBanner()
    expect(wrapper.find('.quota-banner').exists()).toBe(false)
  })

  it('is hidden when event_limit is 0 (unlimited)', () => {
    settingsData.value = makeSettings(0)
    projectsData.value = [makeProject('a', 9000)]
    const wrapper = mountBanner()
    expect(wrapper.find('.quota-banner').exists()).toBe(false)
  })

  it('is hidden when usage is below 80%', () => {
    settingsData.value = makeSettings(1000)
    projectsData.value = [makeProject('a', 799)]
    const wrapper = mountBanner()
    expect(wrapper.find('.quota-banner').exists()).toBe(false)
  })

  it('shows a warning at exactly 80% usage', () => {
    settingsData.value = makeSettings(1000)
    projectsData.value = [makeProject('a', 800)]
    const wrapper = mountBanner()
    expect(wrapper.find('.quota-banner').exists()).toBe(true)
    expect(wrapper.find('.quota-banner').classes()).toContain('quota-banner--warn')
  })

  it('shows the usage percentage in warning state', () => {
    settingsData.value = makeSettings(1000)
    projectsData.value = [makeProject('a', 900)]
    const wrapper = mountBanner()
    expect(wrapper.find('.quota-banner__msg').text()).toContain('90%')
  })

  it('shows an over banner at 100% usage', () => {
    settingsData.value = makeSettings(1000)
    projectsData.value = [makeProject('a', 1000)]
    const wrapper = mountBanner()
    expect(wrapper.find('.quota-banner').classes()).toContain('quota-banner--over')
  })

  it('shows the limit-reached message when over', () => {
    settingsData.value = makeSettings(1000)
    projectsData.value = [makeProject('a', 1001)]
    const wrapper = mountBanner()
    expect(wrapper.find('.quota-banner__msg').text()).toContain('Monthly event limit reached')
  })

  it('sums event_count across multiple projects', () => {
    settingsData.value = makeSettings(1000)
    // 600 + 600 = 1200 / 1000 = 120%
    projectsData.value = [makeProject('a', 600), makeProject('b', 600)]
    const wrapper = mountBanner()
    expect(wrapper.find('.quota-banner').classes()).toContain('quota-banner--over')
  })

  it('is hidden on the settings route', () => {
    settingsData.value = makeSettings(1000)
    projectsData.value = [makeProject('a', 900)]
    const wrapper = mountBanner('settings')
    expect(wrapper.find('.quota-banner').exists()).toBe(false)
  })

  it('dismisses the warning banner', async () => {
    settingsData.value = makeSettings(1000)
    projectsData.value = [makeProject('a', 900)]
    const wrapper = mountBanner()
    await wrapper.find('.quota-banner__dismiss').trigger('click')
    expect(wrapper.find('.quota-banner').exists()).toBe(false)
  })

  it('dismisses the over banner', async () => {
    settingsData.value = makeSettings(1000)
    projectsData.value = [makeProject('a', 1000)]
    const wrapper = mountBanner()
    await wrapper.find('.quota-banner__dismiss').trigger('click')
    expect(wrapper.find('.quota-banner').exists()).toBe(false)
  })

  it('re-shows when crossing from warning to over after dismissing the warning', async () => {
    settingsData.value = makeSettings(1000)
    projectsData.value = [makeProject('a', 850)]
    const wrapper = mountBanner()
    expect(wrapper.find('.quota-banner').classes()).toContain('quota-banner--warn')

    await wrapper.find('.quota-banner__dismiss').trigger('click')
    expect(wrapper.find('.quota-banner').exists()).toBe(false)

    // Cross into over territory
    projectsData.value = [makeProject('a', 1000)]
    await nextTick()
    expect(wrapper.find('.quota-banner').exists()).toBe(true)
    expect(wrapper.find('.quota-banner').classes()).toContain('quota-banner--over')
  })

  it('"View usage" navigates to the projects settings tab', async () => {
    settingsData.value = makeSettings(1000)
    projectsData.value = [makeProject('a', 900)]
    const wrapper = mountBanner()
    await wrapper.find('.quota-banner__cta').trigger('click')
    expect(pushMock).toHaveBeenCalledWith({ name: 'settings', params: { tab: 'projects' } })
  })
})
