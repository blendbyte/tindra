import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { ref } from 'vue'

const { showToast, invalidateQueries, replaceMock } = vi.hoisted(() => ({
  showToast: vi.fn(),
  invalidateQueries: vi.fn(),
  replaceMock: vi.fn(),
}))

let route: { params: Record<string, string>; query: Record<string, unknown> } = { params: { tab: 'alerts' }, query: {} }

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: vi.fn(), replace: replaceMock })),
  useRoute: vi.fn(() => route),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(),
  useQueryClient: vi.fn(() => ({ invalidateQueries, setQueryData: vi.fn() })),
}))

vi.mock('@/composables/useToast', () => ({ useToast: vi.fn(() => ({ show: showToast })) }))
vi.mock('@/api/client', () => ({ apiFetch: vi.fn() }))
vi.mock('@/stores/auth', () => ({ useAuthStore: vi.fn() }))
vi.mock('@/utils/formatters', () => ({ formatRel: vi.fn(() => '2m ago') }))

import AlertsTab from '../AlertsTab.vue'
import { useMutation } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { adminUser, isEnabled, mockQueries, queryOptions, recordMutations, runMutations, stubs } from './harness'

// Order of the useMutation calls inside AlertsTab.
const M = { createAlertRule: 0, deleteAlertRule: 1, toggleAlertRule: 2, testAlertRule: 3, saveAlertRule: 4 } as const

beforeEach(() => {
  vi.clearAllMocks()
  route = { params: { tab: 'alerts' }, query: {} }
  vi.mocked(useMutation).mockImplementation(() => ({ mutate: vi.fn(), isPending: ref(false) } as any))
  mockQueries()
})

type Wrapper = ReturnType<typeof mountTab>

function mountTab() {
  return mount(AlertsTab, { global: { stubs } })
}

const makeRule = (id: string, name: string) => ({
  id,
  name,
  trigger: 'new_issue' as const,
  channel: 'webhook' as const,
  enabled: true,
  threshold: 100,
  window_mins: 60,
  cooldown_mins: 60,
  project_ids: [] as string[],
  webhook_url: 'https://hook.example.com',
  email_to: null,
  last_fired_at: null,
  filter_level: null,
  filter_environment: null,
  min_occurrences: 0,
})

const makeRuleWithTrigger = (trigger: string, threshold = 100, window_mins = 60) => ({
  ...makeRule(`r-${trigger}`, trigger),
  trigger,
  threshold,
  window_mins,
  webhook_url: '',
})

const baseRule = {
  id: 'r1', name: 'My Rule', trigger: 'new_issue' as const, threshold: 100,
  window_mins: 60, channel: 'webhook' as const, webhook_url: 'https://hook.example.com',
  email_to: '', cooldown_mins: 60, enabled: true, last_fired_at: null,
  filter_level: null, filter_environment: null, min_occurrences: 0, project_ids: [] as string[],
}

const logCountProj = { id: 'p1', name: 'App', slug: 'app', public_key: 'k', event_count: 0, event_limit: 0 }

async function openNewRule(wrapper: Wrapper) {
  await wrapper.findAll('.btn').find(b => b.text().includes('New rule'))!.trigger('click')
}

async function expandRule(wrapper: Wrapper) {
  await wrapper.find('.rule__head').trigger('click')
}

async function editRule(wrapper: Wrapper) {
  await expandRule(wrapper)
  await wrapper.findAll('.rule__actions .btn').find(b => b.text().includes('Edit'))!.trigger('click')
}

function selectWith(wrapper: Wrapper, optionText: string) {
  return wrapper.findAll('select.field__input').find(s => s.findAll('option').some(o => o.text() === optionText || o.text().includes(optionText)))!
}

function searchInput(wrapper: Wrapper) {
  return wrapper.findAll('input').find(i => i.attributes('placeholder')?.includes('Logs search'))!
}

function windowInput(wrapper: Wrapper) {
  return wrapper.findAll('input[type="number"]').find(i => i.attributes('max') === '60')!
}

function inputValue(el: { element: Element }) {
  return (el.element as HTMLInputElement).value
}

async function clickCreate(wrapper: Wrapper) {
  await wrapper.findAll('.btn--primary').find(b => b.text().includes('Create rule'))!.trigger('click')
}

async function clickSave(wrapper: Wrapper) {
  await wrapper.findAll('.btn--primary').find(b => b.text().includes('Save'))!.trigger('click')
}

function requestBody(url: string) {
  const call = vi.mocked(apiFetch).mock.calls.find(c => String(c[0]) === url)
  expect(call).toBeTruthy()
  return JSON.parse(String(call![1]?.body))
}

describe('AlertsTab', () => {
  describe('direct render', () => {
    it('renders Alert rules heading on alerts tab', () => {
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('Alert rules')
    })

    it('shows create alert button', () => {
      const wrapper = mountTab()
      expect(wrapper.findAll('.btn').some(b => b.text().includes('New rule'))).toBe(true)
    })

    it('hides the management controls without manage_alerts', async () => {
      mockQueries({
        me: { ...adminUser, permissions: { ...adminUser.permissions, manage_alerts: false } },
        'alert-rules': [makeRule('r1', 'High Error Rate')],
      })
      const wrapper = mountTab()
      expect(wrapper.findAll('.btn').some(b => b.text().includes('New rule'))).toBe(false)
      expect(wrapper.find('.rule__toggle').exists()).toBe(false)
      await expandRule(wrapper)
      expect(wrapper.find('.rule__body').exists()).toBe(true)
      expect(wrapper.find('.rule__actions').exists()).toBe(false)
    })
  })

  describe('with rules data', () => {
    it('says in the warning colour when every rule is paused', () => {
      mockQueries({ 'alert-rules': [{ ...makeRule('r1', 'A'), enabled: false }, { ...makeRule('r2', 'B'), enabled: false }] })
      const sub = mountTab().find('.pane-head__sub')
      expect(sub.text()).toBe('All 2 rules are paused. Nothing will notify you.')
      expect(sub.classes()).toContain('pane-head__sub--warning')
    })

    it('renders rule names when alert rules exist', () => {
      mockQueries({ 'alert-rules': [makeRule('r1', 'High Error Rate')] })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('High Error Rate')
    })

    it('shows rule trigger label', () => {
      mockQueries({ 'alert-rules': [makeRule('r1', 'High Error Rate')] })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('New issue')
    })

    it('expands rule body when rule head is clicked', async () => {
      mockQueries({ 'alert-rules': [makeRule('r1', 'High Error Rate')] })
      const wrapper = mountTab()
      await expandRule(wrapper)
      expect(wrapper.find('.rule__body').exists()).toBe(true)
    })

    it('collapses rule body when rule head is clicked again', async () => {
      mockQueries({ 'alert-rules': [makeRule('r1', 'High Error Rate')] })
      const wrapper = mountTab()
      await expandRule(wrapper)
      await expandRule(wrapper)
      expect(wrapper.find('.rule__body').exists()).toBe(false)
    })

    it('labels global rules and rules whose projects were all deleted', () => {
      mockQueries({
        'alert-rules': [
          { ...makeRule('r1', 'Everywhere'), is_global: true },
          { ...makeRule('r2', 'Orphaned'), is_global: false },
        ],
      })
      const wrapper = mountTab()
      const heads = wrapper.findAll('.rule__head')
      expect(heads.find(h => h.text().includes('Everywhere'))!.text()).toContain('Global')
      expect(heads.find(h => h.text().includes('Orphaned'))!.text()).toContain('No projects (inactive)')
    })

    it('opens edit form when Edit button is clicked in expanded rule', async () => {
      mockQueries({ 'alert-rules': [makeRule('r1', 'High Error Rate')] })
      const wrapper = mountTab()
      await editRule(wrapper)
      expect(wrapper.find('.alert-create__grid').exists()).toBe(true)
    })

    it('loads delivery history only for the expanded rule', async () => {
      mockQueries({
        'alert-rules': [makeRule('r1', 'High Error Rate')],
        'alert-firings': [{
          id: 'f1', status: 'failed', attempt: 3, fired_at: '2024-01-01T00:00:00Z',
          trigger: 'new_issue', item_count: 2, status_code: 500, error: 'boom',
        }],
      })
      const wrapper = mountTab()
      const options = queryOptions('alert-firings')
      expect(isEnabled(options)).toBe(false)
      await expandRule(wrapper)
      expect(isEnabled(options)).toBe(true)
      expect(options.queryKey.value).toEqual(['alert-firings', 'r1'])
      const row = wrapper.find('.rule__history-row')
      expect(row.text()).toContain('New issue')
      expect(row.text()).toContain('2 items')
      expect(row.text()).toContain('failed after 3 attempts')
      expect(row.text()).toContain('HTTP 500')
      expect(row.text()).toContain('boom')
    })
  })

  describe('alert rule form interactions', () => {
    it('opens new alert rule form when New rule is clicked', async () => {
      const wrapper = mountTab()
      await openNewRule(wrapper)
      expect(wrapper.find('.alert-create__grid').exists()).toBe(true)
    })

    it('hides new rule form when Cancel is clicked', async () => {
      const wrapper = mountTab()
      await openNewRule(wrapper)
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('.alert-create__grid').exists()).toBe(false)
    })
  })

  describe('additional trigger labels', () => {
    it('shows Regression label for regressed trigger', () => {
      mockQueries({ 'alert-rules': [makeRuleWithTrigger('regressed')] })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('Regression')
    })

    it('shows "New issue or regression" for new_or_regressed trigger', () => {
      mockQueries({ 'alert-rules': [makeRuleWithTrigger('new_or_regressed')] })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('New issue or regression')
    })

    it('shows threshold for event_count trigger', () => {
      mockQueries({ 'alert-rules': [makeRuleWithTrigger('event_count', 50, 60)] })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('>50 events')
    })

    it('shows threshold for log_count trigger', () => {
      mockQueries({ 'alert-rules': [makeRuleWithTrigger('log_count', 10, 5)] })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('>10 logs')
    })

    it('shows "Cron monitor missed" for cron_missed trigger', () => {
      mockQueries({ 'alert-rules': [makeRuleWithTrigger('cron_missed')] })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('Cron monitor missed')
    })

    it('shows "Cron monitor error" for cron_error trigger', () => {
      mockQueries({ 'alert-rules': [makeRuleWithTrigger('cron_error')] })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('Cron monitor error')
    })

    it('shows project name in expanded rule when rule has project_ids', async () => {
      const rule = { ...makeRuleWithTrigger('new_issue'), project_ids: ['p1'] }
      const proj = { id: 'p1', name: 'My App', slug: 'my-app', public_key: 'k', event_count: 0, event_limit: 0 }
      mockQueries({ 'alert-rules': [rule], projects: [proj] })
      const wrapper = mountTab()
      await expandRule(wrapper)
      expect(wrapper.find('.rule__detail-grid').text()).toContain('My App')
    })
  })

  describe('cancelEditRule', () => {
    it('closes edit form when Cancel is clicked in expanded rule edit', async () => {
      mockQueries({ 'alert-rules': [makeRule('r1', 'My Rule')] })
      const wrapper = mountTab()
      await editRule(wrapper)
      expect(wrapper.find('.alert-create__grid').exists()).toBe(true)
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('.alert-create__grid').exists()).toBe(false)
      expect(wrapper.find('.rule__detail-grid').exists()).toBe(true)
    })
  })

  describe('mutation callbacks', () => {
    it('createAlertRule onSuccess hides form', async () => {
      const muts = recordMutations()
      const wrapper = mountTab()
      const create = muts[M.createAlertRule]
      create.mutate.mockImplementation(() => create.opts.onSuccess())
      await openNewRule(wrapper)
      await clickCreate(wrapper)
      expect(create.mutate).toHaveBeenCalled()
      expect(wrapper.find('.alert-create__grid').exists()).toBe(false)
      expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['alert-rules'] })
      expect(showToast).toHaveBeenCalledWith('Alert rule created')
    })

    it('createAlertRule onError shows toast', async () => {
      const muts = recordMutations()
      const wrapper = mountTab()
      const create = muts[M.createAlertRule]
      create.mutate.mockImplementation(() => create.opts.onError(new Error('Rule creation failed')))
      await openNewRule(wrapper)
      await clickCreate(wrapper)
      expect(showToast).toHaveBeenCalledWith('Rule creation failed')
    })

    it('testAlertRule onSuccess and onError callbacks', async () => {
      mockQueries({ 'alert-rules': [baseRule] })
      const muts = recordMutations()
      const wrapper = mountTab()
      const test = muts[M.testAlertRule]
      test.mutate.mockImplementation((args: unknown) => test.opts.onMutate(args))
      await expandRule(wrapper)
      await wrapper.findAll('.btn--primary').find(b => b.text().includes('Send test'))!.trigger('click')
      expect(test.mutate).toHaveBeenCalledWith({ id: 'r1' })
      const pending = wrapper.find('.rule__actions .btn--primary')
      expect(pending.text()).toContain('Sending')
      expect(pending.attributes('disabled')).toBeDefined()
      test.opts.onSuccess()
      test.opts.onSettled()
      await flushPromises()
      expect(showToast).toHaveBeenCalledWith('Test alert sent')
      expect(wrapper.find('.rule__actions .btn--primary').text()).toContain('Send test')
    })

    it('testAlertRule onError shows toast', async () => {
      mockQueries({ 'alert-rules': [baseRule] })
      const muts = recordMutations()
      const wrapper = mountTab()
      const test = muts[M.testAlertRule]
      test.mutate.mockImplementation((args: unknown) => {
        test.opts.onMutate(args)
        test.opts.onError(new Error('Test failed'))
        test.opts.onSettled()
      })
      await expandRule(wrapper)
      await wrapper.findAll('.btn--primary').find(b => b.text().includes('Send test'))!.trigger('click')
      expect(showToast).toHaveBeenCalledWith('Test failed')
    })

    it('saveAlertRule onError shows toast', async () => {
      mockQueries({ 'alert-rules': [baseRule] })
      const muts = recordMutations()
      const wrapper = mountTab()
      const save = muts[M.saveAlertRule]
      save.mutate.mockImplementation(() => save.opts.onError(new Error('Save failed')))
      await editRule(wrapper)
      await clickSave(wrapper)
      expect(save.mutate).toHaveBeenCalledWith({ id: 'r1' })
      expect(showToast).toHaveBeenCalledWith('Save failed')
    })

    it('shows event_count detail fields in rule detail view', async () => {
      mockQueries({ 'alert-rules': [{ ...baseRule, trigger: 'event_count' as const, threshold: 50, window_mins: 30 }] })
      const wrapper = mountTab()
      await expandRule(wrapper)
      expect(wrapper.text()).toContain('Threshold')
      expect(wrapper.text()).toContain('50')
    })

    it('shows email channel detail in rule detail view', async () => {
      mockQueries({ 'alert-rules': [{ ...baseRule, channel: 'email' as const, email_to: 'team@example.com', webhook_url: '' }] })
      const wrapper = mountTab()
      await expandRule(wrapper)
      expect(wrapper.text()).toContain('Email to')
      expect(wrapper.text()).toContain('team@example.com')
    })

    it('log_count form requires a project and shows the search field', async () => {
      mockQueries({ projects: [logCountProj] })
      const wrapper = mountTab()
      await openNewRule(wrapper)
      await selectWith(wrapper, 'Log count').setValue('log_count')
      expect(wrapper.text()).toContain('at least one required')
      expect(wrapper.text()).toContain('Search')
      const createBtn = () => wrapper.findAll('.btn--primary').find(b => b.text().includes('Create rule'))!
      expect(createBtn().attributes('disabled')).toBeDefined()
      await wrapper.find('input[type="checkbox"]').setValue(true)
      expect(createBtn().attributes('disabled')).toBeUndefined()
    })

    it('expands a log_count rule and shows search in the detail pane', async () => {
      const rule = {
        ...baseRule, trigger: 'log_count' as const, threshold: 10, window_mins: 5,
        filter_search: 'stripe', filter_level: 'error', project_ids: ['p1'],
      }
      mockQueries({ 'alert-rules': [rule], projects: [logCountProj] })
      const wrapper = mountTab()
      await expandRule(wrapper)
      expect(wrapper.text()).toContain('Search')
      expect(wrapper.text()).toContain('stripe')
      expect(wrapper.text()).toContain('10 logs')
    })

    it('edit form for log_count shows search and required-project hint', async () => {
      const rule = {
        ...baseRule, trigger: 'log_count' as const, threshold: 10, window_mins: 5,
        filter_search: 'stripe', filter_level: 'error', project_ids: ['p1'],
      }
      mockQueries({ 'alert-rules': [rule], projects: [logCountProj] })
      const wrapper = mountTab()
      await editRule(wrapper)
      expect(wrapper.text()).toContain('at least one required')
      expect(wrapper.text()).toContain('Threshold (logs)')
      expect(inputValue(searchInput(wrapper))).toBe('stripe')
    })

    it('prefills a log_count rule from the alerts query string', async () => {
      route = {
        params: { tab: 'alerts' },
        query: { new: '1', trigger: 'log_count', level: 'error', search: 'stripe', environment: 'production', project_id: 'p1' },
      }
      mockQueries({ projects: [logCountProj] })
      const wrapper = mountTab()
      await wrapper.vm.$nextTick()
      expect(wrapper.text()).toContain('Create alert rule')
      expect(wrapper.text()).toContain('Threshold (logs)')
      expect(inputValue(selectWith(wrapper, 'Fatal only'))).toBe('error')
      expect(inputValue(searchInput(wrapper))).toBe('stripe')
      expect(inputValue(wrapper.find('input[placeholder="e.g. production"]'))).toBe('production')
      expect((wrapper.find('input[type="checkbox"]').element as HTMLInputElement).checked).toBe(true)
      expect(replaceMock).toHaveBeenCalledWith({ name: 'alerts' })
    })

    it('create mutation for log_count sends filter_search', async () => {
      vi.mocked(apiFetch).mockResolvedValue({ id: 'new-rule' } as any)
      mockQueries({ projects: [logCountProj] })
      runMutations()
      const wrapper = mountTab()
      await openNewRule(wrapper)
      await selectWith(wrapper, 'Log count').setValue('log_count')
      await wrapper.find('input[placeholder="e.g. High error rate"]').setValue('stripe errors')
      await wrapper.find('input[type="checkbox"]').setValue(true)
      await searchInput(wrapper).setValue('stripe')
      await clickCreate(wrapper)
      const body = requestBody('/api/alert-rules')
      expect(body.trigger).toBe('log_count')
      expect(body.filter_search).toBe('stripe')
      expect(body.project_ids).toContain('p1')
    })

    it('new rule form shows log_count fields when trigger changed', async () => {
      const wrapper = mountTab()
      await openNewRule(wrapper)
      const triggerSelect = selectWith(wrapper, 'Log count')
      expect(triggerSelect).toBeTruthy()
      await triggerSelect.setValue('log_count')
      expect(wrapper.text()).toContain('Threshold (logs)')
      expect(wrapper.text()).toContain('Search')
    })

    it('prefills log_count from array query params and fatal level', async () => {
      route = {
        params: { tab: 'alerts' },
        query: {
          new: '1',
          trigger: ['log_count'],
          level: ['fatal'],
          search: ['timeout'],
          environment: 'staging',
          project_id: ['p1', ''],
        },
      }
      mockQueries({ projects: [logCountProj] })
      const wrapper = mountTab()
      await wrapper.vm.$nextTick()
      expect(wrapper.text()).toContain('Create alert rule')
      expect(wrapper.text()).toContain('Threshold (logs)')
      expect(inputValue(searchInput(wrapper))).toBe('timeout')
      expect(inputValue(selectWith(wrapper, 'Fatal only'))).toBe('fatal')
      expect(inputValue(wrapper.find('input[placeholder="e.g. production"]'))).toBe('staging')
      expect((wrapper.find('input[type="checkbox"]').element as HTMLInputElement).checked).toBe(true)
    })

    it('create mutation rejects log_count without a project', async () => {
      mockQueries({ projects: [logCountProj] })
      const muts = recordMutations()
      const wrapper = mountTab()
      await openNewRule(wrapper)
      await selectWith(wrapper, 'Log count').setValue('log_count')
      expect(() => muts[M.createAlertRule].opts.mutationFn()).toThrow('Pick at least one project')
    })

    it('create mutation sends null filter_search when the box is blank', async () => {
      vi.mocked(apiFetch).mockResolvedValue({ id: 'new-rule' } as any)
      mockQueries({ projects: [logCountProj] })
      runMutations()
      const wrapper = mountTab()
      await openNewRule(wrapper)
      await selectWith(wrapper, 'Log count').setValue('log_count')
      await wrapper.find('input[placeholder="e.g. High error rate"]').setValue('errors')
      await wrapper.find('input[type="checkbox"]').setValue(true)
      await clickCreate(wrapper)
      expect(requestBody('/api/alert-rules').filter_search).toBeNull()
    })

    it('edit form coerces window and level when switching to log_count', async () => {
      const rule = {
        ...baseRule, trigger: 'event_count' as const, threshold: 50, window_mins: 120,
        filter_level: 'info', project_ids: ['p1'],
      }
      mockQueries({ 'alert-rules': [rule], projects: [logCountProj] })
      const wrapper = mountTab()
      await editRule(wrapper)
      await selectWith(wrapper, 'Log count').setValue('log_count')
      expect(wrapper.text()).toContain('Threshold (logs)')
      expect(inputValue(windowInput(wrapper))).toBe('5')
      expect(inputValue(selectWith(wrapper, 'Fatal only'))).toBe('error')
    })

    it('edit form fills log_count defaults when threshold and search are null', async () => {
      const rule = {
        ...baseRule, trigger: 'log_count' as const, threshold: null, window_mins: null,
        filter_search: null, filter_level: 'error', project_ids: ['p1'],
      }
      mockQueries({ 'alert-rules': [rule], projects: [logCountProj] })
      const wrapper = mountTab()
      await editRule(wrapper)
      expect(inputValue(windowInput(wrapper))).toBe('5')
      expect(inputValue(wrapper.findAll('input[type="number"]')[0])).toBe('10')
      expect(inputValue(searchInput(wrapper))).toBe('')
    })

    it('save mutation for log_count sends filter_search and window', async () => {
      vi.mocked(apiFetch).mockResolvedValue({ id: 'r1' } as any)
      const rule = {
        ...baseRule, trigger: 'log_count' as const, threshold: 10, window_mins: 5,
        filter_search: 'old', filter_level: 'error', filter_environment: 'production', project_ids: ['p1'],
      }
      mockQueries({ 'alert-rules': [rule], projects: [logCountProj] })
      runMutations()
      const wrapper = mountTab()
      await editRule(wrapper)
      await searchInput(wrapper).setValue('  stripe  ')
      await clickSave(wrapper)
      const body = requestBody('/api/alert-rules/r1')
      expect(body.trigger).toBe('log_count')
      expect(body.filter_search).toBe('stripe')
      expect(body.window_mins).toBe(5)
      expect(body.filter_environment).toBe('production')
    })

    it('save mutation rejects log_count without a project', async () => {
      const rule = {
        ...baseRule, trigger: 'log_count' as const, threshold: 10, window_mins: 5,
        filter_level: 'error', project_ids: ['p1'],
      }
      mockQueries({ 'alert-rules': [rule], projects: [logCountProj] })
      const muts = recordMutations()
      const wrapper = mountTab()
      await editRule(wrapper)
      await wrapper.find('input[type="checkbox"]').setValue(false)
      expect(() => muts[M.saveAlertRule].opts.mutationFn({ id: 'r1' })).toThrow('Pick at least one project')
    })

    it('warns when the current log count would fire the rule immediately', async () => {
      mockQueries({ projects: [logCountProj], 'logs-count': { count: 500, window_mins: 5 } })
      const wrapper = mountTab()
      await openNewRule(wrapper)
      await selectWith(wrapper, 'Log count').setValue('log_count')
      await wrapper.find('input[type="checkbox"]').setValue(true)
      expect(wrapper.text()).toContain('500 matching logs in the last 5m (this would fire immediately)')
      wrapper.unmount()
    })

    it('debounces the log count preview and shows a singular match', async () => {
      vi.useFakeTimers()
      try {
        mockQueries({ projects: [logCountProj], 'logs-count': { count: 1, window_mins: 5 } })
        const wrapper = mountTab()
        const options = queryOptions('logs-count')
        await openNewRule(wrapper)
        await selectWith(wrapper, 'Log count').setValue('log_count')
        await wrapper.find('input[type="checkbox"]').setValue(true)
        await searchInput(wrapper).setValue('stripe')
        expect(isEnabled(options)).toBe(false)
        await vi.advanceTimersByTimeAsync(300)
        expect(isEnabled(options)).toBe(true)
        const key = 'project_id=p1&level=error&window_mins=5&search=stripe'
        expect(options.queryKey.value).toEqual(['logs-count', key])
        expect(wrapper.text()).toContain('1 matching log in the last 5m')
        options.queryFn({ signal: new AbortController().signal })
        expect(vi.mocked(apiFetch).mock.calls.at(-1)![0]).toBe(`/api/logs/count?${key}`)
        wrapper.unmount()
      } finally {
        vi.useRealTimers()
      }
    })

    it('new rule form shows event_count fields when trigger changed', async () => {
      const muts = recordMutations()
      const wrapper = mountTab()
      await openNewRule(wrapper)
      await selectWith(wrapper, 'Event count').setValue('event_count')
      expect(wrapper.text()).toContain('Threshold (events)')
      const numInputs = wrapper.findAll('input[type="number"]')
      expect(numInputs.length).toBeGreaterThan(1)
      await numInputs[0].setValue('200')
      await numInputs[1].setValue('30')
      muts[M.createAlertRule].opts.mutationFn()
      const body = requestBody('/api/alert-rules')
      expect(body.threshold).toBe(200)
      expect(body.window_mins).toBe(30)
    })

    it('new rule form shows email field when channel set to email', async () => {
      const muts = recordMutations()
      const wrapper = mountTab()
      await openNewRule(wrapper)
      await selectWith(wrapper, 'Email').setValue('email')
      expect(wrapper.find('input[type="url"]').exists()).toBe(false)
      await wrapper.find('input[type="email"]').setValue('alert@example.com')
      muts[M.createAlertRule].opts.mutationFn()
      const body = requestBody('/api/alert-rules')
      expect(body.channel).toBe('email')
      expect(body.email_to).toBe('alert@example.com')
      expect(body).not.toHaveProperty('webhook_url')
    })

    it('new rule form interacts with all filter fields', async () => {
      const muts = recordMutations()
      const wrapper = mountTab()
      await openNewRule(wrapper)
      const cooldown = wrapper.findAll('input[type="number"]').find(i => (i.element as HTMLInputElement).min === '1')!
      await cooldown.setValue('30')
      await selectWith(wrapper, 'Any level').setValue('error')
      await wrapper.find('input[placeholder="e.g. production"]').setValue('production')
      await wrapper.find('input[placeholder="Any"]').setValue('5')
      muts[M.createAlertRule].opts.mutationFn()
      const body = requestBody('/api/alert-rules')
      expect(body.cooldown_mins).toBe(30)
      expect(body.filter_level).toBe('error')
      expect(body.filter_environment).toBe('production')
      expect(body.min_occurrences).toBe(5)
    })

    it('edit rule form shows all fields for event_count + email channel rule', async () => {
      const eventEmailRule = {
        ...baseRule, id: 'r2', trigger: 'event_count' as const, threshold: 50,
        window_mins: 30, channel: 'email' as const, email_to: 'a@b.com', webhook_url: '',
      }
      mockQueries({ 'alert-rules': [eventEmailRule] })
      runMutations()
      const wrapper = mountTab()
      await editRule(wrapper)
      expect(inputValue(wrapper.find('input[type="email"]'))).toBe('a@b.com')
      await wrapper.findAll('input.field__input')[0].setValue('New Name')
      const triggerSel = selectWith(wrapper, 'Event count')
      await triggerSel.setValue('event_count')
      for (const ni of wrapper.findAll('input[type="number"]')) await ni.setValue('100')
      const channelSel = selectWith(wrapper, 'Email')
      await channelSel.setValue('email')
      await wrapper.find('input[type="email"]').setValue('new@example.com')
      await channelSel.setValue('webhook')
      await wrapper.find('input[type="url"]').setValue('https://hooks.example.com/')
      await selectWith(wrapper, 'Any level').setValue('error')
      await wrapper.find('input[placeholder="e.g. production"]').setValue('staging')
      await triggerSel.setValue('new_issue')
      await wrapper.find('input[placeholder="Any"]').setValue('3')
      await clickSave(wrapper)
      const body = requestBody('/api/alert-rules/r2')
      expect(body).toMatchObject({
        name: 'New Name',
        trigger: 'new_issue',
        channel: 'webhook',
        webhook_url: 'https://hooks.example.com/',
        cooldown_mins: 100,
        filter_level: 'error',
        filter_environment: 'staging',
        min_occurrences: 3,
        filter_search: null,
      })
      expect(body).not.toHaveProperty('threshold')
      expect(body).not.toHaveProperty('email_to')
    })
  })

  describe('deleteAlertRule and toggleAlertRule callbacks', () => {
    const alertRule = { ...baseRule, name: 'Test Rule' }

    it('deleteAlertRule onSuccess invalidates alert-rules query', async () => {
      mockQueries({ 'alert-rules': [alertRule] })
      const muts = recordMutations()
      const wrapper = mountTab()
      const del = muts[M.deleteAlertRule]
      del.mutate.mockImplementation(() => del.opts.onSuccess())
      await expandRule(wrapper)
      await wrapper.findAll('.btn--ghost').find(b => b.text().includes('Delete rule'))!.trigger('click')
      expect(del.mutate).toHaveBeenCalledWith({ id: 'r1' })
      expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['alert-rules'] })
      expect(showToast).toHaveBeenCalledWith('Rule deleted')
    })

    it('toggleAlertRule onSuccess invalidates alert-rules query', async () => {
      mockQueries({ 'alert-rules': [alertRule] })
      const muts = recordMutations()
      const wrapper = mountTab()
      const toggle = muts[M.toggleAlertRule]
      toggle.mutate.mockImplementation(() => toggle.opts.onSuccess())
      await wrapper.find('.rule__toggle').trigger('click')
      expect(toggle.mutate).toHaveBeenCalledWith({ id: 'r1', enabled: false })
      expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['alert-rules'] })
      // The toggle does not expand the rule.
      expect(wrapper.find('.rule__body').exists()).toBe(false)
    })

    it('saveAlertRule onSuccess closes edit form', async () => {
      mockQueries({ 'alert-rules': [alertRule] })
      const muts = recordMutations()
      const wrapper = mountTab()
      const save = muts[M.saveAlertRule]
      save.mutate.mockImplementation(() => save.opts.onSuccess())
      await editRule(wrapper)
      await clickSave(wrapper)
      expect(wrapper.find('.alert-create__grid').exists()).toBe(false)
      expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['alert-rules'] })
      expect(showToast).toHaveBeenCalledWith('Rule updated')
    })

    it('new rule cancel button hides form and resets', async () => {
      const wrapper = mountTab()
      await openNewRule(wrapper)
      expect(wrapper.find('.alert-create').exists()).toBe(true)
      await wrapper.find('input[placeholder="e.g. High error rate"]').setValue('Draft')
      await wrapper.findAll('.btn--ghost').find(b => b.text() === 'Cancel')!.trigger('click')
      expect(wrapper.find('.alert-create').exists()).toBe(false)
      await openNewRule(wrapper)
      expect(inputValue(wrapper.find('input[placeholder="e.g. High error rate"]'))).toBe('')
    })

    it('new rule with slack channel shows webhook url field', async () => {
      const wrapper = mountTab()
      await openNewRule(wrapper)
      await selectWith(wrapper, 'Slack').setValue('slack')
      expect(wrapper.text()).toContain('Slack webhook URL')
      const urlInput = wrapper.find('input[type="url"]')
      expect(urlInput.attributes('placeholder')).toBe('https://hooks.slack.com/services/...')
      await urlInput.setValue('https://hooks.slack.com/services/T0/B0/xxx')
      expect(inputValue(urlInput)).toBe('https://hooks.slack.com/services/T0/B0/xxx')
    })

    it('new rule with discord channel shows discord webhook url field', async () => {
      const wrapper = mountTab()
      await openNewRule(wrapper)
      await selectWith(wrapper, 'Discord').setValue('discord')
      expect(wrapper.text()).toContain('Discord webhook URL')
      const urlInput = wrapper.find('input[type="url"]')
      expect(urlInput.attributes('placeholder')).toBe('https://discord.com/api/webhooks/...')
      await urlInput.setValue('https://discord.com/api/webhooks/123/abc')
      expect(inputValue(urlInput)).toBe('https://discord.com/api/webhooks/123/abc')
    })
  })

  describe('various trigger and channel labels', () => {
    const rule = (overrides: Record<string, unknown>) => ({ ...makeRule('r1', 'Rule'), ...overrides })

    it('triggerLabel shows correct label for regressed', () => {
      mockQueries({ 'alert-rules': [rule({ name: 'Regress Rule', trigger: 'regressed' })] })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('Regression')
    })

    it('triggerLabel shows correct label for new_or_regressed', () => {
      mockQueries({
        'alert-rules': [rule({
          name: 'NOR Rule', trigger: 'new_or_regressed', channel: 'email', webhook_url: '',
          email_to: 'a@b.com', last_fired_at: '2024-01-01T00:00:00Z',
        })],
      })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('New issue or regression')
    })

    it('triggerLabel shows correct label for always', () => {
      mockQueries({
        'alert-rules': [rule({
          name: 'Always Rule', trigger: 'always', channel: 'slack', enabled: false,
          project_ids: ['p1'], webhook_url: 'https://hooks.slack.com/x',
        })],
      })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('Always')
      expect(wrapper.find('.rule__cond').text()).toContain('always')
    })

    it('triggerLabel shows correct label for cron_missed', () => {
      mockQueries({
        'alert-rules': [rule({
          name: 'Cron Rule', trigger: 'cron_missed', channel: 'discord', webhook_url: 'https://discord.com/api/webhooks/x',
        })],
      })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('Cron monitor missed')
    })

    it('triggerLabel shows correct label for cron_error', () => {
      mockQueries({ 'alert-rules': [rule({ name: 'Cron Err Rule', trigger: 'cron_error', webhook_url: 'https://hook.x.com' })] })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('Cron monitor error')
    })
  })

  describe('with projects (covers project checkboxes in rule forms)', () => {
    const p1 = { id: 'p1', name: 'App 1', slug: 'app-1', public_key: 'k1', event_count: 0, event_limit: 0, passthrough_dsn: null }
    const alertRuleWithProj = { ...baseRule, name: 'Rule with Projects', project_ids: ['p1'] }

    it('new rule form shows project checkboxes when alertProjects exist', async () => {
      mockQueries({ projects: [p1] })
      const muts = recordMutations()
      const wrapper = mountTab()
      await openNewRule(wrapper)
      expect(wrapper.findAll('.rule__project-check').length).toBeGreaterThan(0)
      await wrapper.find('.rule__project-check input[type="checkbox"]').setValue(true)
      await wrapper.find('.alert-create__grid input.field__input').setValue('Test Rule Name')
      muts[M.createAlertRule].opts.mutationFn()
      const body = requestBody('/api/alert-rules')
      expect(body.name).toBe('Test Rule Name')
      expect(body.project_ids).toEqual(['p1'])
    })

    it('edit rule form shows project checkboxes when alertProjects exist', async () => {
      mockQueries({ 'alert-rules': [alertRuleWithProj], projects: [p1] })
      const muts = recordMutations()
      const wrapper = mountTab()
      await editRule(wrapper)
      const checkbox = wrapper.find('.rule__project-check input')
      expect((checkbox.element as HTMLInputElement).checked).toBe(true)
      await checkbox.setValue(false)
      expect(wrapper.find('.alert-create__grid').exists()).toBe(true)
      muts[M.saveAlertRule].opts.mutationFn({ id: 'r1' })
      expect(requestBody('/api/alert-rules/r1').project_ids).toEqual([])
    })

    it('ruleProjectNames shows project name for rule with project_ids', () => {
      mockQueries({ 'alert-rules': [alertRuleWithProj], projects: [p1] })
      const wrapper = mountTab()
      expect(wrapper.find('.rule__cond').text()).toContain('App 1')
    })
  })

  describe('edit rule with projects', () => {
    const alertProj = { id: 'p1', name: 'My App', slug: 'my-app', public_key: 'k', event_count: 0, event_limit: 0 }
    const ruleWithProj = { ...makeRule('r1', 'High Error Rate'), project_ids: ['p1'] }

    beforeEach(() => {
      mockQueries({ 'alert-rules': [ruleWithProj], projects: [alertProj] })
    })

    it('shows project checkboxes in edit form when alertProjects exist', async () => {
      const wrapper = mountTab()
      await editRule(wrapper)
      expect(wrapper.find('.rule__project-checks').exists()).toBe(true)
      expect(wrapper.find('.rule__project-checks').text()).toContain('My App')
    })

    it('shows delete rule button in edit form', async () => {
      const wrapper = mountTab()
      await editRule(wrapper)
      expect(wrapper.findAll('.btn--ghost').some(b => b.text().includes('Delete rule'))).toBe(true)
    })

    it('clicking Delete rule in edit form calls deleteAlertRule', async () => {
      const muts = recordMutations()
      const wrapper = mountTab()
      await editRule(wrapper)
      await wrapper.findAll('.btn--ghost').find(b => b.text().includes('Delete rule'))!.trigger('click')
      expect(muts[M.deleteAlertRule].mutate).toHaveBeenCalledWith({ id: 'r1' })
    })

    it('shows project checkboxes in new rule form when alertProjects exist', async () => {
      const wrapper = mountTab()
      await openNewRule(wrapper)
      expect(wrapper.find('.alert-create .rule__project-checks').exists()).toBe(true)
    })
  })
})
