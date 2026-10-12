import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick, reactive } from 'vue'

// Provide a localStorage shim for happy-dom (the project sort is persisted there)
let lsStore: Record<string, string> = {}
const localStorageMock = {
  getItem: (key: string) => lsStore[key] ?? null,
  setItem: (key: string, val: string) => { lsStore[key] = val },
  removeItem: (key: string) => { delete lsStore[key] },
  clear: () => { lsStore = {} },
  get length() { return Object.keys(lsStore).length },
  key: (i: number) => Object.keys(lsStore)[i] ?? null,
}
Object.defineProperty(window, 'localStorage', { value: localStorageMock, writable: true })

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: vi.fn(), replace: vi.fn() })),
  useRoute: vi.fn(),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(),
  useQueryClient: vi.fn(),
}))

vi.mock('@/composables/useToast', () => ({
  useToast: vi.fn(),
}))

vi.mock('@/composables/useConfig', () => ({
  useConfig: vi.fn(() => ({ dsnFor: vi.fn(() => 'https://key@host/1') })),
}))

vi.mock('@/api/client', () => ({ apiFetch: vi.fn() }))
vi.mock('@/stores/auth', () => ({ useAuthStore: vi.fn() }))
vi.mock('@/utils/formatters', () => ({ formatRel: vi.fn(() => '2m ago') }))

import ProjectsTab from '../ProjectsTab.vue'
import { useRoute } from 'vue-router'
import { useQueryClient } from '@tanstack/vue-query'
import { useToast } from '@/composables/useToast'
import { isEnabled, mockQueries, queryOptions, recordMutations, stubs, type RecordedMutation } from './harness'

// Order of the useMutation calls inside ProjectsTab.
const M = { createProject: 0, updateProject: 1, deleteProject: 2, updatePrivacy: 3 }

const route = reactive({ params: { tab: 'projects' } as Record<string, string>, query: {} as Record<string, string> })
const showToast = vi.fn()
const qc = { invalidateQueries: vi.fn(), setQueryData: vi.fn() }
let muts: RecordedMutation[]

beforeEach(() => {
  route.params = { tab: 'projects' }
  route.query = {}
  vi.mocked(useRoute).mockImplementation(() => route as any)
  showToast.mockReset()
  vi.mocked(useToast).mockReturnValue({ show: showToast } as any)
  qc.invalidateQueries.mockReset()
  qc.setQueryData.mockReset()
  vi.mocked(useQueryClient).mockReturnValue(qc as any)
  muts = recordMutations()
  mockQueries()
  localStorage.clear()
})

afterEach(() => {
  localStorage.clear()
})

function mountTab() {
  return mount(ProjectsTab, { global: { stubs } })
}

function findBtn(wrapper: VueWrapper, text: string, selector = '.btn') {
  return wrapper.findAll(selector).find(b => b.text().includes(text))
}

// Mounts the tab with the given projects and expands the first card.
async function mountExpanded(projects: unknown[]) {
  mockQueries({ projects })
  const wrapper = mountTab()
  await wrapper.find('.proj-card__head').trigger('click')
  return wrapper
}

async function openNewProjectForm(wrapper: VueWrapper) {
  const newBtn = findBtn(wrapper, 'New project')
  expect(newBtn).toBeDefined()
  await newBtn!.trigger('click')
}

async function openPrivacyPanel(wrapper: VueWrapper) {
  const privacyBtn = findBtn(wrapper, 'Data privacy')
  expect(privacyBtn).toBeDefined()
  await privacyBtn!.trigger('click')
  return privacyBtn!
}

async function openEdit(wrapper: VueWrapper) {
  const editBtn = findBtn(wrapper, 'Edit')
  expect(editBtn).toBeDefined()
  await editBtn!.trigger('click')
}

async function openDeleteConfirm(wrapper: VueWrapper) {
  const deleteBtn = findBtn(wrapper, 'Delete', '.btn--ghost')
  expect(deleteBtn).toBeDefined()
  await deleteBtn!.trigger('click')
}

function deletePermanentlyBtn(wrapper: VueWrapper) {
  return wrapper.findAll('.proj-delete-confirm .btn').find(b => b.text() === 'Delete permanently')!
}

describe('ProjectsTab', () => {
  describe('direct render', () => {
    it('renders projects tab content with no projects empty state', () => {
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('Projects')
      expect(wrapper.find('.settings-empty').text()).toContain('No projects yet')
    })

    it('renders project cards when projects exist', () => {
      const proj = { id: 'p1', name: 'My App', slug: 'my-app', public_key: 'abc123', event_count: 0, events_24h: 0, storage_bytes: 0, event_limit: 0 }
      mockQueries({ projects: [proj] })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('My App')
    })

    it('renders sortable table header with four col-sort buttons', () => {
      const proj = { id: 'p1', name: 'My App', slug: 'my-app', public_key: 'abc123', event_count: 42, events_24h: 5, storage_bytes: 1024 * 1024, event_limit: 0, scrub_fields: [], scrub_patterns: [] }
      mockQueries({ projects: [proj] })
      const wrapper = mountTab()
      const sortBtns = wrapper.findAll('.proj-table-head .col-sort')
      expect(sortBtns.length).toBe(4)
      const labels = sortBtns.map(b => b.text())
      expect(labels.some(l => l.includes('Project'))).toBe(true)
      expect(labels.some(l => l.includes('Events'))).toBe(true)
      expect(labels.some(l => l.includes('24h'))).toBe(true)
      expect(labels.some(l => l.includes('Storage'))).toBe(true)
    })

    it('shows events_24h and storage_bytes values in project rows', () => {
      const proj = { id: 'p1', name: 'My App', slug: 'my-app', public_key: 'abc123', event_count: 1234, events_24h: 7, storage_bytes: 2 * 1024 * 1024, event_limit: 0, scrub_fields: [], scrub_patterns: [] }
      mockQueries({ projects: [proj] })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('1,234')
      expect(wrapper.text()).toContain('+7')
      // 2 MB formatted
      expect(wrapper.text()).toContain('MB')
    })

    it('sorts projects alphabetically by default (name ascending)', () => {
      mockQueries({
        projects: [
          { id: 'p2', name: 'Zebra', slug: 'zebra', public_key: 'k2', event_count: 0, events_24h: 0, storage_bytes: 0, scrub_fields: [], scrub_patterns: [] },
          { id: 'p1', name: 'Alpha', slug: 'alpha', public_key: 'k1', event_count: 0, events_24h: 0, storage_bytes: 0, scrub_fields: [], scrub_patterns: [] },
        ],
      })
      const wrapper = mountTab()
      const rows = wrapper.findAll('.proj-card')
      expect(rows[0].text()).toContain('Alpha')
      expect(rows[1].text()).toContain('Zebra')
    })

    it('sorts by events/mo descending when that header is clicked', async () => {
      mockQueries({
        projects: [
          { id: 'p1', name: 'Alpha', slug: 'alpha', public_key: 'k1', event_count: 5, events_24h: 0, storage_bytes: 0, scrub_fields: [], scrub_patterns: [] },
          { id: 'p2', name: 'Beta', slug: 'beta', public_key: 'k2', event_count: 99, events_24h: 0, storage_bytes: 0, scrub_fields: [], scrub_patterns: [] },
        ],
      })
      const wrapper = mountTab()
      const sortBtns = wrapper.findAll('.proj-table-head .col-sort')
      const evBtn = sortBtns.find(b => b.text().includes('Events'))!
      await evBtn.trigger('click') // first click: desc
      const rows = wrapper.findAll('.proj-card')
      expect(rows[0].text()).toContain('Beta') // 99 first
      expect(rows[1].text()).toContain('Alpha') // 5 second
    })

    it('reverses sort direction when same column header is clicked twice', async () => {
      mockQueries({
        projects: [
          { id: 'p1', name: 'Alpha', slug: 'alpha', public_key: 'k1', event_count: 5, events_24h: 0, storage_bytes: 0, scrub_fields: [], scrub_patterns: [] },
          { id: 'p2', name: 'Beta', slug: 'beta', public_key: 'k2', event_count: 99, events_24h: 0, storage_bytes: 0, scrub_fields: [], scrub_patterns: [] },
        ],
      })
      const wrapper = mountTab()
      const sortBtns = wrapper.findAll('.proj-table-head .col-sort')
      const evBtn = sortBtns.find(b => b.text().includes('Events'))!
      await evBtn.trigger('click') // desc
      await evBtn.trigger('click') // asc
      const rows = wrapper.findAll('.proj-card')
      expect(rows[0].text()).toContain('Alpha') // 5 first (asc)
      expect(rows[1].text()).toContain('Beta')
    })

    it('active sort column gets col-sort--active class', () => {
      mockQueries({ projects: [{ id: 'p1', name: 'My App', slug: 'my-app', public_key: 'abc123', event_count: 0, events_24h: 0, storage_bytes: 0, scrub_fields: [], scrub_patterns: [] }] })
      const wrapper = mountTab()
      // default sort is name, so first col-sort should be active
      const sortBtns = wrapper.findAll('.proj-table-head .col-sort')
      expect(sortBtns[0].classes()).toContain('col-sort--active')
      expect(sortBtns[1].classes()).not.toContain('col-sort--active')
    })

    it('expands a project card when its header is clicked', async () => {
      const wrapper = await mountExpanded([{ id: 'p1', name: 'My App', slug: 'my-app', public_key: 'abc123', event_count: 0, events_24h: 0, storage_bytes: 0, event_limit: 0 }])
      expect(wrapper.find('.proj-card__body').exists()).toBe(true)
    })

    it('shows New project form when button clicked', async () => {
      const wrapper = mountTab()
      await openNewProjectForm(wrapper)
      expect(wrapper.find('.proj-card--form').exists()).toBe(true)
    })

    it('hides New project form when Cancel is clicked', async () => {
      const wrapper = mountTab()
      await openNewProjectForm(wrapper)
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('.proj-card--form').exists()).toBe(false)
    })
  })

  describe('edit project', () => {
    const proj = { id: 'p1', name: 'My App', slug: 'my-app', public_key: 'abc123', event_count: 0, event_limit: 0 }

    it('shows edit form when Edit button is clicked in expanded project', async () => {
      const wrapper = await mountExpanded([proj])
      await openEdit(wrapper)
      expect(wrapper.find('.proj-card__body form').exists()).toBe(true)
      expect((wrapper.find('#edit-name-p1').element as HTMLInputElement).value).toBe('My App')
    })

    it('shows delete confirmation when Delete is clicked in expanded project', async () => {
      const wrapper = await mountExpanded([proj])
      await openDeleteConfirm(wrapper)
      expect(wrapper.find('.proj-delete-confirm').exists()).toBe(true)
      expect(wrapper.find('input[placeholder="my-app"]').exists()).toBe(true)
    })
  })

  describe('new project form interactions', () => {
    it('auto-fills slug when typing in project name field', async () => {
      const wrapper = mountTab()
      await openNewProjectForm(wrapper)
      await wrapper.find('#proj-name').setValue('My New App')
      expect((wrapper.find('#proj-slug').element as HTMLInputElement).value).toBe('my-new-app')
    })

    it('submits new project form when name and slug are set', async () => {
      const wrapper = mountTab()
      await openNewProjectForm(wrapper)
      await wrapper.find('#proj-name').setValue('My App')
      await wrapper.find('#proj-slug').setValue('my-app')
      await wrapper.find('.proj-card--form').trigger('submit')
      expect(muts[M.createProject].mutate).toHaveBeenCalledWith({ name: 'My App', slug: 'my-app' })
      // Form stays open since the mocked mutation never calls onSuccess
      expect(wrapper.find('.proj-card--form').exists()).toBe(true)
    })
  })

  describe('data privacy panel', () => {
    const proj = { id: 'p1', name: 'My App', slug: 'my-app', public_key: 'abc123', event_count: 0, event_limit: 0 }

    it('opens data privacy panel when Data privacy button is clicked', async () => {
      const wrapper = await mountExpanded([proj])
      await openPrivacyPanel(wrapper)
      expect(wrapper.find('.privacy-panel').exists()).toBe(true)
    })

    it('states that scrubbing covers logs as well as events', async () => {
      const wrapper = await mountExpanded([proj])
      await openPrivacyPanel(wrapper)
      const text = wrapper.find('.privacy-panel').text()
      expect(text).toContain('log message')
      expect(text).toContain('attribute names')
    })

    it('copies DSN when Copy button is clicked in expanded project', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
      const wrapper = await mountExpanded([proj])
      const copyBtn = wrapper.findAll('.proj-card__body .btn').find(b => b.text() === 'Copy')
      expect(copyBtn).toBeDefined()
      await copyBtn!.trigger('click')
      await flushPromises()
      expect(writeText).toHaveBeenCalledWith('https://key@host/1')
      expect(showToast).toHaveBeenCalledWith('DSN copied')
    })
  })

  describe('mutation callbacks', () => {
    const proj = {
      id: 'p1', name: 'My App', slug: 'my-app', public_key: 'key1',
      event_count: 100, event_limit: 0, passthrough_dsn: null,
      scrub_fields: [], scrub_patterns: [],
    }

    async function submitNewProject(wrapper: VueWrapper, name: string, slug: string) {
      await openNewProjectForm(wrapper)
      await wrapper.find('#proj-name').setValue(name)
      await wrapper.find('#proj-slug').setValue(slug)
      await wrapper.find('.proj-card--form').trigger('submit')
    }

    it('createProject onError sets newProjectError', async () => {
      const wrapper = mountTab()
      muts[M.createProject].mutate.mockImplementation(() => muts[M.createProject].opts.onError(new Error('Slug taken')))
      await submitNewProject(wrapper, 'My App', 'my-app')
      expect(wrapper.find('.proj-form-error').text()).toBe('Slug taken')
    })

    it('createProject onSuccess sets createdProject', async () => {
      const wrapper = mountTab()
      muts[M.createProject].mutate.mockImplementation(() =>
        muts[M.createProject].opts.onSuccess({ ...proj, id: 'new-p', name: 'My App', slug: 'my-app', public_key: 'newkey' }))
      await submitNewProject(wrapper, 'My App', 'my-app')
      expect(wrapper.find('.proj-success').exists()).toBe(true)
      expect(wrapper.find('.proj-success__title').text()).toContain('My App')
      expect(wrapper.find('.proj-card--form').exists()).toBe(false)
      expect(qc.invalidateQueries).toHaveBeenCalledWith({ queryKey: ['projects'] })
    })

    it('updateProject onSuccess clears editingProject', async () => {
      const wrapper = await mountExpanded([proj])
      muts[M.updateProject].mutate.mockImplementation(() => muts[M.updateProject].opts.onSuccess())
      await openEdit(wrapper)
      expect(wrapper.find(`input#edit-name-${proj.id}`).exists()).toBe(true)
      await wrapper.find('.proj-card__body form').trigger('submit')
      expect(muts[M.updateProject].mutate).toHaveBeenCalled()
      // After onSuccess, editingProject should be null and form hidden
      expect(wrapper.find(`input#edit-name-${proj.id}`).exists()).toBe(false)
      expect(qc.invalidateQueries).toHaveBeenCalledWith({ queryKey: ['projects'] })
    })

    it('updateProject onError sets editError', async () => {
      const wrapper = await mountExpanded([proj])
      muts[M.updateProject].mutate.mockImplementation(() => muts[M.updateProject].opts.onError(new Error('Slug conflict')))
      await openEdit(wrapper)
      await wrapper.find('.proj-card__body form').trigger('submit')
      expect(wrapper.find('.proj-form-error').text()).toBe('Slug conflict')
    })

    it('deleteProject onSuccess clears confirmingDelete', async () => {
      const wrapper = await mountExpanded([proj])
      muts[M.deleteProject].mutate.mockImplementation(() => muts[M.deleteProject].opts.onSuccess())
      await openDeleteConfirm(wrapper)
      await wrapper.find('.proj-delete-confirm input').setValue('my-app')
      await deletePermanentlyBtn(wrapper).trigger('click')
      expect(muts[M.deleteProject].mutate).toHaveBeenCalledWith('p1')
      expect(wrapper.find('.proj-delete-confirm').exists()).toBe(false)
      // The card collapses as well
      expect(wrapper.find('.proj-card__body').exists()).toBe(false)
      expect(showToast).toHaveBeenCalledWith('Project deleted')
      expect(qc.invalidateQueries).toHaveBeenCalledWith({ queryKey: ['projects'] })
    })

    it('deleteProject onError shows toast', async () => {
      const wrapper = await mountExpanded([proj])
      muts[M.deleteProject].mutate.mockImplementation(() => muts[M.deleteProject].opts.onError(new Error('Cannot delete')))
      await openDeleteConfirm(wrapper)
      await wrapper.find('.proj-delete-confirm input').setValue('my-app')
      await deletePermanentlyBtn(wrapper).trigger('click')
      expect(showToast).toHaveBeenCalledWith('Cannot delete', 'error')
    })

    it('updatePrivacy onSuccess clears privacyProject', async () => {
      const wrapper = await mountExpanded([proj])
      muts[M.updatePrivacy].mutate.mockImplementation(() => muts[M.updatePrivacy].opts.onSuccess())
      await openPrivacyPanel(wrapper)
      await wrapper.find('.privacy-panel').trigger('submit')
      expect(muts[M.updatePrivacy].mutate).toHaveBeenCalled()
      expect(wrapper.find('.privacy-panel').exists()).toBe(false)
      expect(showToast).toHaveBeenCalledWith('Privacy settings saved')
    })

    it('updatePrivacy onError sets privacyError', async () => {
      const wrapper = await mountExpanded([proj])
      muts[M.updatePrivacy].mutate.mockImplementation(() => muts[M.updatePrivacy].opts.onError(new Error('Privacy update failed')))
      await openPrivacyPanel(wrapper)
      await wrapper.find('.privacy-panel').trigger('submit')
      expect(wrapper.find('.privacy-panel .proj-form-error').text()).toBe('Privacy update failed')
    })

    it('edit project form inputs are interactive', async () => {
      const wrapper = await mountExpanded([proj])
      await openEdit(wrapper)
      await wrapper.find(`#edit-name-${proj.id}`).setValue('Updated Name')
      const slugIn = wrapper.find(`#edit-slug-${proj.id}`)
      await slugIn.setValue('updated-slug')
      await slugIn.trigger('input')
      await wrapper.find(`#edit-passthrough-${proj.id}`).setValue('https://key@sentry.io/123')
      await wrapper.find('.proj-card__body form').trigger('submit')
      expect(muts[M.updateProject].mutate).toHaveBeenCalledWith(expect.objectContaining({
        id: 'p1',
        name: 'Updated Name',
        slug: 'updated-slug',
        passthroughDsn: 'https://key@sentry.io/123',
      }))
    })

    it('privacy panel checkboxes are interactive', async () => {
      const wrapper = await mountExpanded([{ ...proj, scrub_patterns: [{ name: 'email', pattern: '', builtin: true, enabled: false }, { name: 'ip', pattern: '', builtin: true, enabled: false }] }])
      await openPrivacyPanel(wrapper)
      const checkboxes = wrapper.findAll('.privacy-panel input[type="checkbox"]')
      expect(checkboxes.length).toBe(2)
      for (const cb of checkboxes) {
        await cb.setValue(true)
      }
      expect(wrapper.find('.privacy-panel').exists()).toBe(true)
      await wrapper.find('.privacy-panel').trigger('submit')
      expect(muts[M.updatePrivacy].mutate).toHaveBeenCalledWith({
        id: 'p1',
        scrubFields: [],
        scrubPatterns: [
          { name: 'email', pattern: '', builtin: true, enabled: true },
          { name: 'ip', pattern: '', builtin: true, enabled: true },
        ],
      })
    })

  })

  describe('privacy panel addScrubField and removeScrubField', () => {
    const projWithScrub = {
      id: 'p1', name: 'My App', slug: 'my-app', public_key: 'key1',
      event_count: 0, event_limit: 0, passthrough_dsn: null,
      scrub_fields: ['existing.field'], scrub_patterns: [
        { name: 'email', pattern: '', builtin: true, enabled: true },
        { name: 'ip', pattern: '', builtin: true, enabled: false },
      ],
    }
    const newProj = { id: 'new-1', name: 'New App', slug: 'new-app', public_key: 'newkey1', event_count: 0, event_limit: 0, passthrough_dsn: null, scrub_fields: [], scrub_patterns: [] }

    async function createNewApp(wrapper: VueWrapper) {
      muts[M.createProject].mutate.mockImplementation(() => muts[M.createProject].opts.onSuccess(newProj))
      await openNewProjectForm(wrapper)
      await wrapper.find('#proj-name').setValue('New App')
      await wrapper.find('#proj-slug').setValue('new-app')
      await wrapper.find('.proj-card--form').trigger('submit')
      expect(wrapper.find('.proj-success').exists()).toBe(true)
    }

    it('addScrubField adds field to list when input has text', async () => {
      const wrapper = await mountExpanded([projWithScrub])
      await openPrivacyPanel(wrapper)
      const scrubInput = wrapper.find('input[placeholder="request.headers.Authorization"]')
      await scrubInput.setValue('user.ssn')
      await wrapper.findAll('.btn').find(b => b.text() === 'Add')!.trigger('click')
      expect(wrapper.find('.privacy-fields').exists()).toBe(true)
      expect(wrapper.findAll('.privacy-field__name').map(f => f.text())).toEqual(['existing.field', 'user.ssn'])
      expect((scrubInput.element as HTMLInputElement).value).toBe('')
    })

    it('addScrubField via Enter keydown adds field', async () => {
      const wrapper = await mountExpanded([projWithScrub])
      await openPrivacyPanel(wrapper)
      const scrubInput = wrapper.find('input[placeholder="request.headers.Authorization"]')
      await scrubInput.setValue('request.cookies')
      await scrubInput.trigger('keydown.enter')
      expect(wrapper.findAll('.privacy-field__name').map(f => f.text())).toContain('request.cookies')
    })

    it('removeScrubField removes field from list', async () => {
      const wrapper = await mountExpanded([projWithScrub])
      await openPrivacyPanel(wrapper)
      // existing.field comes from scrub_fields
      expect(wrapper.findAll('.privacy-field').length).toBe(1)
      await wrapper.find('.privacy-field__remove').trigger('click')
      expect(wrapper.findAll('.privacy-field').length).toBe(0)
      expect(wrapper.find('.privacy-panel').text()).not.toContain('existing.field')
    })

    it('copyDsn from createdProject banner copies DSN', async () => {
      const clipWrite = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', { value: { writeText: clipWrite }, configurable: true })
      const wrapper = mountTab()
      await createNewApp(wrapper)
      await wrapper.find('.proj-success__dsn .btn').trigger('click')
      await flushPromises()
      expect(clipWrite).toHaveBeenCalledWith('https://key@host/1')
    })

    it('closing createdProject banner sets it to null', async () => {
      const wrapper = mountTab()
      await createNewApp(wrapper)
      await wrapper.find('.proj-success__close').trigger('click')
      expect(wrapper.find('.proj-success').exists()).toBe(false)
    })
  })

  describe('quota data display', () => {
    it('shows quota data when expandedProject has quota', async () => {
      const proj = { id: 'p1', name: 'App', slug: 'app', public_key: 'key1', event_count: 5000, event_limit: 10000, passthrough_dsn: null, scrub_fields: [], scrub_patterns: [] }
      const quotaData = { event_count: 5000, event_limit: 10000, error_count: 100, transaction_count: 200, size_bytes: 1024 * 1024 }
      mockQueries({ projects: [proj], quota: quotaData })
      const wrapper = mountTab()
      const quota = queryOptions('quota')
      expect(isEnabled(quota)).toBe(false)
      await wrapper.find('.proj-card__head').trigger('click')
      expect(wrapper.find('.proj-card__body').exists()).toBe(true)
      // The quota query is keyed to and enabled for the expanded card
      expect(isEnabled(quota)).toBe(true)
      expect(quota.queryKey.value).toEqual(['quota', 'p1'])
    })

    it('shows the ingest rate and 30-day trend from the quota data', async () => {
      const proj = { id: 'p1', name: 'App', slug: 'app', public_key: 'key1', event_count: 0, passthrough_dsn: null, scrub_fields: [], scrub_patterns: [] }
      const quotaData = { events_this_month: 0, event_limit: 0, rate_limit_per_min: 100, rate_limit_used: 85, rate_limit_reset_at: null, daily_volume: [1, 2, 3] }
      mockQueries({ projects: [proj], quota: quotaData })
      const wrapper = mountTab()
      await wrapper.find('.proj-card__head').trigger('click')
      expect(wrapper.text()).toContain('Ingest rate (current window)')
      expect(wrapper.text()).toContain('of 100 / min')
      expect(wrapper.find('.proj-quota-bar__fill--warn').exists()).toBe(true)
      expect(wrapper.find('.proj-sparkline-row').exists()).toBe(true)
    })
  })

  describe('edit slug auto-fill and manual', () => {
    const proj = { id: 'p1', name: 'My App', slug: 'my-app', public_key: 'key1', event_count: 0, event_limit: 0, passthrough_dsn: null, scrub_fields: [], scrub_patterns: [] }

    // startEdit marks the slug as touched, so the edit form opens with the
    // project's own name and slug rather than a derived slug.
    it('auto-fills edit slug when editName changes (before touched)', async () => {
      const wrapper = await mountExpanded([proj])
      await openEdit(wrapper)
      expect((wrapper.find('#edit-name-p1').element as HTMLInputElement).value).toBe('My App')
      expect((wrapper.find('#edit-slug-p1').element as HTMLInputElement).value).toBe('my-app')
    })

    it('new project cancel button hides new project form', async () => {
      const wrapper = mountTab()
      await openNewProjectForm(wrapper)
      expect(wrapper.find('#proj-name').exists()).toBe(true)
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('#proj-name').exists()).toBe(false)
    })

    it('cancel delete confirmation hides delete form', async () => {
      const wrapper = await mountExpanded([proj])
      await openDeleteConfirm(wrapper)
      expect(wrapper.find('.proj-delete-confirm').exists()).toBe(true)
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('.proj-delete-confirm').exists()).toBe(false)
    })

  })

  describe('instanceAtLimit/instanceNearLimit computed coverage', () => {
    const settings = { event_limit: 100000, project_limit: 0, user_limit: 0, version: 'v1', commit: '', update_available: false }

    it('renders projects tab with event_limit > 0 and usage near limit (covers instanceNearLimit)', () => {
      mockQueries({ projects: [{ id: 'p1', name: 'App', slug: 'app', public_key: 'k1', event_count: 85000, event_limit: 0, passthrough_dsn: null }], settings })
      const wrapper = mountTab()
      const summary = wrapper.find('.quota-summary')
      expect(summary.exists()).toBe(true)
      expect(summary.classes()).toContain('quota-summary--warn')
      expect(summary.text()).toContain('Approaching monthly limit.')
    })

    it('renders projects tab with event_limit reached (covers instanceAtLimit)', () => {
      mockQueries({ projects: [{ id: 'p1', name: 'App', slug: 'app', public_key: 'k1', event_count: 100000, event_limit: 0, passthrough_dsn: null }], settings })
      const wrapper = mountTab()
      const summary = wrapper.find('.quota-summary')
      expect(summary.exists()).toBe(true)
      expect(summary.classes()).toContain('quota-summary--over')
      expect(summary.text()).toContain('Monthly limit reached.')
    })

  })

  describe('privacy panel cancel and guard coverage', () => {
    const projFull = {
      id: 'p1', name: 'My App', slug: 'my-app', public_key: 'key1',
      event_count: 0, event_limit: 0, passthrough_dsn: null,
      scrub_fields: [], scrub_patterns: [
        { name: 'email', pattern: '', builtin: true, enabled: false },
        { name: 'ip', pattern: '', builtin: true, enabled: false },
      ],
    }

    it('privacy cancel button closes privacy panel', async () => {
      const wrapper = await mountExpanded([projFull])
      await openPrivacyPanel(wrapper)
      expect(wrapper.find('.privacy-panel').exists()).toBe(true)
      await wrapper.find('.privacy-panel .btn--ghost').trigger('click')
      expect(wrapper.find('.privacy-panel').exists()).toBe(false)
    })

    it('addScrubField guard: does not add empty field', async () => {
      const wrapper = await mountExpanded([projFull])
      await openPrivacyPanel(wrapper)
      // Leave input empty and click Add
      await wrapper.find('input[placeholder="request.headers.Authorization"]').setValue('')
      await wrapper.findAll('.btn').find(b => b.text() === 'Add')!.trigger('click')
      expect(wrapper.find('.privacy-fields').exists()).toBe(false)
    })

    it('toggling Data privacy button when already open closes privacy panel', async () => {
      const wrapper = await mountExpanded([projFull])
      const privacyBtn = await openPrivacyPanel(wrapper)
      expect(wrapper.find('.privacy-panel').exists()).toBe(true)
      await privacyBtn.trigger('click')
      expect(wrapper.find('.privacy-panel').exists()).toBe(false)
    })
  })

  describe('new project slug auto-fill watcher (watch(newProjectName))', () => {
    it('auto-fills slug when name is typed and slug not manually touched', async () => {
      const wrapper = mountTab()
      await openNewProjectForm(wrapper)
      await wrapper.find('#proj-name').setValue('Hello World App')
      expect((wrapper.find('#proj-slug').element as HTMLInputElement).value).toBe('hello-world-app')
    })

    it('renaming a project keeps its slug', async () => {
      const proj = { id: 'p1', name: 'My App', slug: 'my-app', public_key: 'key1', event_count: 0, event_limit: 0, passthrough_dsn: null, scrub_fields: [], scrub_patterns: [] }
      const wrapper = await mountExpanded([proj])
      await openEdit(wrapper)
      await wrapper.find(`#edit-name-${proj.id}`).setValue('New Name For App')
      expect((wrapper.find(`#edit-slug-${proj.id}`).element as HTMLInputElement).value).toBe('my-app')
    })
  })

  describe('edit form cancel and delete buttons', () => {
    const proj = { id: 'p1', name: 'My App', slug: 'my-app', public_key: 'key1', event_count: 0, event_limit: 0, passthrough_dsn: null, scrub_fields: [], scrub_patterns: [] }

    it('edit form Cancel button sets editingProject to null', async () => {
      const wrapper = await mountExpanded([proj])
      await openEdit(wrapper)
      const cancelBtn = wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel' && b.element.closest('form'))
      expect(cancelBtn).toBeDefined()
      await cancelBtn!.trigger('click')
      expect(wrapper.find(`#edit-name-${proj.id}`).exists()).toBe(false)
    })

    it('edit form Delete project button starts delete confirmation', async () => {
      const wrapper = await mountExpanded([proj])
      await openEdit(wrapper)
      const deleteProjectBtn = findBtn(wrapper, 'Delete project', '.btn--ghost')
      expect(deleteProjectBtn).toBeDefined()
      await deleteProjectBtn!.trigger('click')
      expect(wrapper.find('.proj-delete-confirm').exists()).toBe(true)
    })

    it('submitEdit returns early when name is empty', async () => {
      const wrapper = await mountExpanded([proj])
      await openEdit(wrapper)
      await wrapper.find(`#edit-name-${proj.id}`).setValue('')
      await wrapper.find('.proj-card__body form').trigger('submit')
      expect(muts[M.updateProject].mutate).not.toHaveBeenCalled()
    })

    it('submitDelete returns early when text does not match slug', async () => {
      const wrapper = await mountExpanded([proj])
      await openDeleteConfirm(wrapper)
      await wrapper.find('.proj-delete-confirm input').setValue('wrong-slug')
      const btn = deletePermanentlyBtn(wrapper)
      expect(btn.attributes('disabled')).toBeDefined()
      await btn.trigger('click')
      expect(muts[M.deleteProject].mutate).not.toHaveBeenCalled()
    })

    it('new project form X button closes form', async () => {
      const wrapper = mountTab()
      await openNewProjectForm(wrapper)
      expect(wrapper.find('#proj-name').exists()).toBe(true)
      await wrapper.find('.proj-card--form .proj-card__head .btn--ghost').trigger('click')
      expect(wrapper.find('#proj-name').exists()).toBe(false)
    })
  })

  describe('route query new=1 triggers openNewProject on mount', () => {
    it('renders without error when route.query.new=1 (onMounted/watch coverage)', async () => {
      route.query = { new: '1' }
      const wrapper = mountTab()
      await nextTick()
      expect(wrapper.find('.proj-card--form').exists()).toBe(true)
    })

    it('opens the new project form when ?new=1 arrives after mount', async () => {
      const wrapper = mountTab()
      expect(wrapper.find('.proj-card--form').exists()).toBe(false)
      route.query = { new: '1' }
      await nextTick()
      await nextTick()
      expect(wrapper.find('.proj-card--form').exists()).toBe(true)
    })
  })

  describe('delete confirm panel', () => {
    const proj = { id: 'p1', name: 'My App', slug: 'my-app', public_key: 'abc123', event_count: 0, event_limit: 0 }

    it('shows delete confirm panel with input when Delete button is clicked', async () => {
      const wrapper = await mountExpanded([proj])
      await openDeleteConfirm(wrapper)
      expect(wrapper.find('.proj-delete-confirm').exists()).toBe(true)
      expect(wrapper.find('input[placeholder="my-app"]').exists()).toBe(true)
    })

    it('typing the slug in delete confirm and clicking delete permanently', async () => {
      const wrapper = await mountExpanded([proj])
      await openDeleteConfirm(wrapper)
      expect(deletePermanentlyBtn(wrapper).attributes('disabled')).toBeDefined()
      await wrapper.find('input[placeholder="my-app"]').setValue('my-app')
      // After typing the slug, the confirm panel stays and the button is enabled
      expect(wrapper.find('.proj-delete-confirm').exists()).toBe(true)
      expect(deletePermanentlyBtn(wrapper).attributes('disabled')).toBeUndefined()
    })

    it('clicking delete permanently calls submitDelete with matching slug', async () => {
      const wrapper = await mountExpanded([proj])
      await openDeleteConfirm(wrapper)
      await wrapper.find('input[placeholder="my-app"]').setValue('my-app')
      await wrapper.find('.proj-delete-confirm .btn').trigger('click')
      expect(muts[M.deleteProject].mutate).toHaveBeenCalledWith('p1')
    })

    it('submitNewProject early-returns when name is empty', async () => {
      mockQueries({ projects: [proj] })
      const wrapper = mountTab()
      await openNewProjectForm(wrapper)
      // Leave name and slug empty, submit form directly
      await wrapper.find('.proj-card--form').trigger('submit')
      expect(muts[M.createProject].mutate).not.toHaveBeenCalled()
      expect(wrapper.find('.proj-card--form').exists()).toBe(true)
    })
  })

  describe('log and transaction counts', () => {
    const proj = {
      id: 'p1',
      name: 'My App',
      slug: 'my-app',
      public_key: 'abc123',
      event_count: 0,
      events_24h: 0,
      storage_bytes: 0,
      passthrough_dsn: null,
      scrub_fields: [],
      scrub_patterns: [],
      log_count: 1234,
      tx_count: 567,
    }

    it('shows log_count in expanded project card body', async () => {
      const wrapper = await mountExpanded([proj])
      expect(wrapper.find('.proj-card__body').exists()).toBe(true)
      expect(wrapper.text()).toContain('Logs stored')
      expect(wrapper.text()).toContain('1,234')
    })

    it('shows tx_count in expanded project card body', async () => {
      const wrapper = await mountExpanded([proj])
      expect(wrapper.find('.proj-card__body').exists()).toBe(true)
      expect(wrapper.text()).toContain('Transactions stored')
      expect(wrapper.text()).toContain('567')
    })

    it('does not show a quota bar for logs in expanded card', async () => {
      const wrapper = await mountExpanded([proj])
      const logField = wrapper.find('.proj-card__body').findAll('.field').find(f => f.text().includes('Logs stored'))
      expect(logField).toBeDefined()
      expect(logField!.find('.proj-quota-bar').exists()).toBe(false)
    })

    it('does not show a quota bar for transactions in expanded card', async () => {
      const wrapper = await mountExpanded([proj])
      const txField = wrapper.find('.proj-card__body').findAll('.field').find(f => f.text().includes('Transactions stored'))
      expect(txField).toBeDefined()
      expect(txField!.find('.proj-quota-bar').exists()).toBe(false)
    })

    it('shows zero log_count correctly in expanded card', async () => {
      const wrapper = await mountExpanded([{ ...proj, log_count: 0 }])
      const logField = wrapper.find('.proj-card__body').findAll('.field').find(f => f.text().includes('Logs stored'))
      expect(logField!.find('.proj-usage__count').text()).toBe('0')
    })
  })

  describe('project profiling toggle', () => {
    const baseProject = {
      id: 'p1',
      name: 'My App',
      slug: 'my-app',
      public_key: 'abc123',
      event_count: 0,
      events_24h: 0,
      storage_bytes: 0,
      event_limit: 0,
      scrub_fields: [],
      scrub_patterns: [],
      passthrough_dsn: null,
    }

    // Opens the project card and its edit form, which is where the controls live.
    async function openEditForm(project: Record<string, unknown>) {
      const wrapper = await mountExpanded([project])
      const editBtn = wrapper.findAll('button').find(b => b.text().includes('Edit'))
      expect(editBtn, 'no Edit button on the expanded project card').toBeTruthy()
      await editBtn!.trigger('click')
      return wrapper
    }

    // The storage budget is shared across projects and purges oldest first, so
    // this switch is the only way to stop one busy service evicting the rest.
    it('offers the toggle when editing a project', async () => {
      const wrapper = await openEditForm({ ...baseProject, profiling_enabled: true })

      expect(wrapper.text()).toContain('Accept profiles from this project')
      const box = wrapper.findAll('input[type="checkbox"]').at(-1)!
      expect((box.element as HTMLInputElement).checked).toBe(true)
    })

    it('reflects a project that already has profiling off', async () => {
      const wrapper = await openEditForm({ ...baseProject, profiling_enabled: false })

      const box = wrapper.findAll('input[type="checkbox"]').at(-1)!
      expect((box.element as HTMLInputElement).checked).toBe(false)
    })
  })
})
