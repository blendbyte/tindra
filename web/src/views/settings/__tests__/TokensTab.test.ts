import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: vi.fn(), replace: vi.fn() })),
  useRoute: vi.fn(() => ({ params: { tab: 'tokens' }, query: {} })),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(),
  useQueryClient: vi.fn(() => ({ invalidateQueries: vi.fn(), setQueryData: vi.fn() })),
}))

vi.mock('@/composables/useToast', () => ({
  useToast: vi.fn(() => ({ show: vi.fn() })),
}))

vi.mock('@/api/client', () => ({ apiFetch: vi.fn() }))
vi.mock('@/stores/auth', () => ({ useAuthStore: vi.fn() }))
vi.mock('@/utils/formatters', () => ({ formatRel: vi.fn(() => '2m ago') }))

import TokensTab from '../TokensTab.vue'
import { useQueryClient } from '@tanstack/vue-query'
import { useToast } from '@/composables/useToast'
import { mockQueries, recordMutations, stubs, type RecordedMutation } from './harness'

// useMutation call order inside TokensTab.
const M = { createToken: 0, revokeToken: 1, updateToken: 2 }

let mutations: RecordedMutation[]
let showToast: ReturnType<typeof vi.fn>
let invalidate: ReturnType<typeof vi.fn>

beforeEach(() => {
  mutations = recordMutations()
  mockQueries()
  showToast = vi.fn()
  vi.mocked(useToast).mockReturnValue({ show: showToast } as any)
  invalidate = vi.fn()
  vi.mocked(useQueryClient).mockReturnValue({ invalidateQueries: invalidate, setQueryData: vi.fn() } as any)
})

function mountTab() {
  return mount(TokensTab, { global: { stubs } })
}

// Makes a recorded mutation call its onSuccess / onError when mutated.
function succeed(index: number, data?: unknown) {
  const m = mutations[index]!
  m.mutate.mockImplementation((vars: unknown) => m.opts.onSuccess?.(data, vars))
}

function fail(index: number, error: Error) {
  const m = mutations[index]!
  m.mutate.mockImplementation((vars: unknown) => m.opts.onError?.(error, vars))
}

const p1 = { id: 'p1', name: 'App', slug: 'app', public_key: 'k1', event_count: 0, event_limit: 0, passthrough_dsn: null }

async function openCreateForm(wrapper: ReturnType<typeof mountTab>) {
  await wrapper.findAll('.btn').find(b => b.text().includes('Create token'))!.trigger('click')
}

async function submitNewToken(wrapper: ReturnType<typeof mountTab>, name: string) {
  await openCreateForm(wrapper)
  await wrapper.find('.proj-card--form input.field__input').setValue(name)
  await wrapper.find('.proj-card--form').trigger('submit')
}

async function openTokenMenu(wrapper: ReturnType<typeof mountTab>) {
  await wrapper.find('.user-menu__trigger').trigger('click')
}

describe('TokensTab', () => {
  describe('direct render', () => {
    it('renders API tokens heading on tokens tab', () => {
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('API tokens')
    })

    it('shows token table on tokens tab', () => {
      const wrapper = mountTab()
      expect(wrapper.find('.token-table').exists()).toBe(true)
    })

    it('shows new token form when Create token button is clicked', async () => {
      const wrapper = mountTab()
      await openCreateForm(wrapper)
      expect(wrapper.find('.proj-card--form').exists()).toBe(true)
    })
  })

  describe('form interactions', () => {
    it('closes the new token form when Cancel is clicked', async () => {
      const wrapper = mountTab()
      await openCreateForm(wrapper)
      expect(wrapper.find('.proj-card--form').exists()).toBe(true)
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('.proj-card--form').exists()).toBe(false)
    })
  })

  describe('createToken callbacks and form flow', () => {
    it('createToken onSuccess sets flashToken and shows Copy & dismiss button', async () => {
      mockQueries({ projects: [p1] })
      const wrapper = mountTab()
      succeed(M.createToken, { token: 'tok_flash_123', meta: {} })
      await submitNewToken(wrapper, 'My Token')
      expect(mutations[M.createToken]!.mutate).toHaveBeenCalledWith({ name: 'My Token', project_id: 'p1', writable: false })
      expect(wrapper.find('.token-flash').exists()).toBe(true)
      expect(wrapper.find('.token-flash__val').text()).toBe('tok_flash_123')
      expect(wrapper.findAll('.btn').some(b => b.text().includes('Copy & dismiss'))).toBe(true)
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ['tokens'] })
    })

    it('createToken onError shows toast', async () => {
      mockQueries({ projects: [p1] })
      const wrapper = mountTab()
      fail(M.createToken, new Error('Token already exists'))
      await submitNewToken(wrapper, 'My Token')
      expect(showToast).toHaveBeenCalledWith('Token already exists', 'error')
    })

    it('copyFlash calls clipboard.writeText and closes form', async () => {
      mockQueries({ projects: [p1] })
      const clipWrite = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', { value: { writeText: clipWrite }, configurable: true })
      const wrapper = mountTab()
      succeed(M.createToken, { token: 'tok_abc_xyz', meta: {} })
      await submitNewToken(wrapper, 'Flash Token')
      await wrapper.findAll('.btn').find(b => b.text().includes('Copy'))!.trigger('click')
      await flushPromises()
      expect(clipWrite).toHaveBeenCalledWith('tok_abc_xyz')
      expect(showToast).toHaveBeenCalledWith('Token copied to clipboard')
      expect(wrapper.find('.proj-card--form').exists()).toBe(false)
    })

    it('revokeToken onSuccess invalidates queries', async () => {
      const token = { id: 'tok1', name: 'CI Token', project_id: 'p1', writable: false, created_at: '2024-01-01T00:00:00Z', last_used_at: null, expires_at: null }
      mockQueries({ tokens: [token] })
      const wrapper = mountTab()
      succeed(M.revokeToken)
      await openTokenMenu(wrapper)
      await wrapper.findAll('.user-menu__item').find(b => b.text().includes('Revoke'))!.trigger('click')
      expect(mutations[M.revokeToken]!.mutate).toHaveBeenCalledWith({ id: 'tok1', name: 'CI Token' })
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ['tokens'] })
      expect(showToast).toHaveBeenCalledWith('"CI Token" revoked')
    })

    it('revokeToken onError shows toast', async () => {
      const token = { id: 'tok1', name: 'CI Token', project_id: 'p1', writable: false, created_at: '2024-01-01T00:00:00Z', last_used_at: null, expires_at: null }
      mockQueries({ tokens: [token] })
      const wrapper = mountTab()
      fail(M.revokeToken, new Error('Cannot revoke'))
      await openTokenMenu(wrapper)
      await wrapper.findAll('.user-menu__item').find(b => b.text().includes('Revoke'))!.trigger('click')
      expect(showToast).toHaveBeenCalledWith('Cannot revoke', 'error')
    })

    it('token form submission with no name does not call createToken', async () => {
      const wrapper = mountTab()
      await openCreateForm(wrapper)
      await wrapper.find('.proj-card--form').trigger('submit')
      expect(mutations[M.createToken]!.mutate).not.toHaveBeenCalled()
    })
  })

  describe('writable token display', () => {
    it('shows token list with writable and last_used columns', () => {
      const tokens = [
        { id: 'tok1', name: 'Read Token', project_id: 'p1', writable: false, created_at: '2024-01-01T00:00:00Z', last_used_at: '2024-06-01T00:00:00Z', expires_at: null },
        { id: 'tok2', name: 'Write Token', project_id: 'p1', writable: true, created_at: '2024-01-01T00:00:00Z', last_used_at: null, expires_at: '2025-01-01T00:00:00Z' },
      ]
      mockQueries({ tokens, projects: [p1] })
      const wrapper = mountTab()
      expect(wrapper.find('.token-table').exists()).toBe(true)
      expect(wrapper.text()).toContain('Read Token')
      expect(wrapper.text()).toContain('Write Token')
    })
  })

  describe('updateToken mutation', () => {
    const tokenRow = {
      id: 'tok-1', name: 'My Token', project_id: 'proj-1', writable: false,
      created_at: '2024-01-01T00:00:00Z', last_used_at: null,
    }
    const proj = { id: 'proj-1', name: 'App', slug: 'app', public_key: 'k1', event_count: 0, event_limit: 0, passthrough_dsn: null, scrub_fields: [], scrub_patterns: [] }

    function mountWithToken() {
      mockQueries({ tokens: [tokenRow], projects: [proj] })
      return mountTab()
    }

    async function openEditPanel(wrapper: ReturnType<typeof mountTab>) {
      await openTokenMenu(wrapper)
      await wrapper.findAll('.user-menu__item').find(b => b.text().includes('Edit'))!.trigger('click')
    }

    it('updateToken onSuccess invalidates tokens query and shows toast', async () => {
      const wrapper = mountWithToken()
      succeed(M.updateToken)
      await openEditPanel(wrapper)
      await wrapper.find('.token-edit-panel input[type="text"]').setValue('Renamed')
      await wrapper.find('.token-edit-panel form').trigger('submit')
      expect(mutations[M.updateToken]!.mutate).toHaveBeenCalledWith({ id: 'tok-1', name: 'Renamed', project_id: 'proj-1', writable: false })
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ['tokens'] })
      expect(showToast).toHaveBeenCalledWith('Token updated')
      expect(wrapper.find('.token-edit-panel').exists()).toBe(false)
    })

    it('updateToken onError shows error toast', async () => {
      const wrapper = mountWithToken()
      fail(M.updateToken, new Error('Token update failed'))
      await openEditPanel(wrapper)
      await wrapper.find('.token-edit-panel form').trigger('submit')
      expect(showToast).toHaveBeenCalledWith('Token update failed', 'error')
      expect(wrapper.find('.token-edit-panel').exists()).toBe(true)
    })

    it('shows Edit button in token dropdown menu', async () => {
      const wrapper = mountWithToken()
      await openTokenMenu(wrapper)
      expect(wrapper.findAll('.user-menu__item').some(b => b.text().includes('Edit'))).toBe(true)
    })

    it('token edit panel appears after clicking Edit in dropdown', async () => {
      const wrapper = mountWithToken()
      await openEditPanel(wrapper)
      expect(wrapper.find('.token-edit-panel').exists()).toBe(true)
      expect((wrapper.find('.token-edit-panel input[type="text"]').element as HTMLInputElement).value).toBe('My Token')
      expect(wrapper.find('.user-menu__dropdown').exists()).toBe(false)
    })
  })
})

describe('token project scope', () => {
  const findBtn = (wrapper: ReturnType<typeof mountTab>, text: string) => wrapper.findAll('button').find(b => b.text().includes(text))

  it('token form shows project select when multiple projects exist', async () => {
    const p1 = { id: 'p1', name: 'App 1', slug: 'app-1', public_key: 'k1', event_count: 0, event_limit: 0, passthrough_dsn: null }
    const p2 = { id: 'p2', name: 'App 2', slug: 'app-2', public_key: 'k2', event_count: 0, event_limit: 0, passthrough_dsn: null }
    mockQueries({ projects: [p1, p2] })
    const wrapper = mountTab()
    const createTokenBtn = findBtn(wrapper, 'Create token')
    expect(createTokenBtn).toBeDefined()
    await createTokenBtn!.trigger('click')
    const projectSelect = wrapper.find('select.field__input')
    expect(projectSelect.exists()).toBe(true)
    await projectSelect.setValue('p2')
    expect((projectSelect.element as HTMLSelectElement).value).toBe('p2')
    const writableCheck = wrapper.find('input[type="checkbox"]')
    await writableCheck.setValue(true)
    expect((writableCheck.element as HTMLInputElement).checked).toBe(true)
    expect(wrapper.find('.proj-card--form').exists()).toBe(true)
  })
})
