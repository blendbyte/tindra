import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

const routeState = { name: 'issues' }

vi.mock('vue-router', () => ({
  useRoute: vi.fn(() => routeState),
  RouterView: { template: '<div class="router-view" />' },
}))

vi.mock('@/stores/ui', () => ({
  useUiStore: vi.fn(),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: vi.fn(),
}))

import App from './App.vue'
import { useUiStore } from '@/stores/ui'
import { useAuthStore } from '@/stores/auth'

const globalStubs = {
  stubs: {
    Navbar: { template: '<nav class="navbar-stub" />' },
    QuotaBanner: { template: '<div class="quota-banner-stub" />' },
    CommandPalette: { template: '<div class="command-palette-stub" />' },
    ShortcutsModal: { template: '<div class="shortcuts-modal-stub" />' },
    ToastStack: { template: '<div class="toast-stack-stub" />' },
    RouterView: { template: '<div class="router-view-stub" />' },
  },
}

function setupMocks(opts: {
  ready?: boolean
  user?: object | null
  resolvedTheme?: string
} = {}) {
  const { ready = true, user = { id: '1', email: 'test@test.com' }, resolvedTheme = 'light' } = opts

  vi.mocked(useUiStore).mockReturnValue({
    resolvedTheme,
    toggleTheme: vi.fn(),
    openCmd: vi.fn(),
    closeCmd: vi.fn(),
    cmdOpen: false,
    theme: null,
  } as any)

  vi.mocked(useAuthStore).mockReturnValue({
    ready,
    user,
    setUser: vi.fn(),
    init: vi.fn(),
  } as any)
}

describe('App', () => {
  it('renders nothing when auth is not ready', () => {
    routeState.name = 'issues'
    setupMocks({ ready: false, user: null })
    const wrapper = mount(App, { global: globalStubs })
    expect(wrapper.find('.app').exists()).toBe(false)
  })

  it('renders nothing when auth is ready but user is null and not on login route', () => {
    routeState.name = 'issues'
    setupMocks({ ready: true, user: null })
    const wrapper = mount(App, { global: globalStubs })
    expect(wrapper.find('.app').exists()).toBe(false)
  })

  it('renders the app shell when auth is ready and user is set', () => {
    routeState.name = 'issues'
    setupMocks({ ready: true, user: { id: '1' } })
    const wrapper = mount(App, { global: globalStubs })
    expect(wrapper.find('.app').exists()).toBe(true)
  })

  it('renders Navbar for non-login routes', () => {
    routeState.name = 'issues'
    setupMocks()
    const wrapper = mount(App, { global: globalStubs })
    expect(wrapper.find('.navbar-stub').exists()).toBe(true)
  })

  it('renders ToastStack for all authenticated routes', () => {
    routeState.name = 'issues'
    setupMocks()
    const wrapper = mount(App, { global: globalStubs })
    expect(wrapper.find('.toast-stack-stub').exists()).toBe(true)
  })

  it('renders RouterView when authenticated', () => {
    routeState.name = 'issues'
    setupMocks()
    const wrapper = mount(App, { global: globalStubs })
    expect(wrapper.find('.router-view-stub').exists()).toBe(true)
  })

  it('renders CommandPalette when not on login route', () => {
    routeState.name = 'issues'
    setupMocks()
    const wrapper = mount(App, { global: globalStubs })
    expect(wrapper.find('.command-palette-stub').exists()).toBe(true)
  })

  it('renders on login route with ready=true and null user', () => {
    routeState.name = 'login'
    setupMocks({ ready: true, user: null })
    const wrapper = mount(App, { global: globalStubs })
    expect(wrapper.find('.app').exists()).toBe(true)
  })

  it('hides Navbar on login route', () => {
    routeState.name = 'login'
    setupMocks({ ready: true, user: null })
    const wrapper = mount(App, { global: globalStubs })
    expect(wrapper.find('.navbar-stub').exists()).toBe(false)
  })

  it('hides CommandPalette on login route', () => {
    routeState.name = 'login'
    setupMocks({ ready: true, user: null })
    const wrapper = mount(App, { global: globalStubs })
    expect(wrapper.find('.command-palette-stub').exists()).toBe(false)
  })

  it('shows ToastStack even on login route', () => {
    routeState.name = 'login'
    setupMocks({ ready: true, user: null })
    const wrapper = mount(App, { global: globalStubs })
    expect(wrapper.find('.toast-stack-stub').exists()).toBe(true)
  })

  it('renders on accept-invite route with null user', () => {
    routeState.name = 'accept-invite'
    setupMocks({ ready: true, user: null })
    const wrapper = mount(App, { global: globalStubs })
    expect(wrapper.find('.app').exists()).toBe(true)
  })

  it('renders on reset-password route with null user', () => {
    routeState.name = 'reset-password'
    setupMocks({ ready: true, user: null })
    const wrapper = mount(App, { global: globalStubs })
    expect(wrapper.find('.app').exists()).toBe(true)
  })
})
