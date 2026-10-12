import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import type { Event as TindraEvent } from '@/api/types'

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { timezone: 'UTC' } }) }))
const { showMock } = vi.hoisted(() => ({ showMock: vi.fn() }))
vi.mock('@/composables/useToast', () => ({ useToast: () => ({ show: showMock }) }))

import IssueStackTrace from '../IssueStackTrace.vue'
import IssueHttpRequest from '../IssueHttpRequest.vue'
import IssueBreadcrumbs from '../IssueBreadcrumbs.vue'
import IssueContexts from '../IssueContexts.vue'
import IssuePackages from '../IssuePackages.vue'

// These sections render the current event, which the view passes in.
const stubs = { Icon: { template: '<span />' }, CodeContext: { name: 'CodeContext', props: ['platform'], template: '<div class="code" />' } }

function event(payload: Record<string, unknown>, id = 'evt-1'): TindraEvent {
  return { id, payload } as TindraEvent
}

beforeEach(() => localStorage.removeItem('tindra:issue:collapsed'))

describe('IssueStackTrace', () => {
  const stackEvent = event({
    platform: 'python',
    exception: {
      values: [{
        type: 'ValueError',
        value: 'bad input',
        stacktrace: {
          frames: [
            { filename: 'lib.py', lineno: 3, function: 'inner', in_app: false, context_line: 'raise' },
            { filename: 'app.py', lineno: 10, function: 'main', in_app: true, context_line: '  run()' },
          ],
        },
      }],
    },
  })

  it('renders nothing without frames', () => {
    expect(mount(IssueStackTrace, { props: { event: event({}) }, global: { stubs } }).find('.section').exists()).toBe(false)
    expect(mount(IssueStackTrace, { props: { event: null }, global: { stubs } }).find('.section').exists()).toBe(false)
  })

  it('lists frames innermost first and folds system frames away', () => {
    const wrapper = mount(IssueStackTrace, { props: { event: stackEvent }, global: { stubs } })
    const frames = wrapper.findAll('.stack__frame')
    expect(frames[0].text()).toContain('app.py')
    expect(frames[1].classes()).toContain('stack__frame--collapsed')
    expect(wrapper.findAllComponents({ name: 'CodeContext' })).toHaveLength(1)
    expect(wrapper.findComponent({ name: 'CodeContext' }).props('platform')).toBe('python')
  })

  it('shows system frames on request', async () => {
    const wrapper = mount(IssueStackTrace, { props: { event: stackEvent }, global: { stubs } })
    await wrapper.findAll('button').find(b => b.text() === 'Show system frames')!.trigger('click')
    expect(wrapper.find('.stack__frame--collapsed').exists()).toBe(false)
    expect(wrapper.findAllComponents({ name: 'CodeContext' })).toHaveLength(2)
  })

  it('copies the raw traceback and flashes Copied', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const wrapper = mount(IssueStackTrace, { props: { event: stackEvent }, global: { stubs } })
    await wrapper.findAll('button').find(b => b.text() === 'Raw')!.trigger('click')
    expect(wrapper.find('.stack__raw').text()).toBe([
      'ValueError: bad input',
      'Traceback (most recent call last):',
      '  File "app.py, line 10, in main"',
      '    run()',
      '  File "lib.py, line 3, in inner"',
      '    raise',
    ].join('\n'))
    await wrapper.findAll('button').find(b => b.text().includes('Copy'))!.trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith(wrapper.find('.stack__raw').text())
    expect(wrapper.text()).toContain('Copied')
    expect(showMock).not.toHaveBeenCalled()
  })
})

describe('IssueHttpRequest', () => {
  const longBody = JSON.stringify(Object.fromEntries(Array.from({ length: 20 }, (_, i) => [`k${i}`, i])))

  it('renders nothing without a request', () => {
    expect(mount(IssueHttpRequest, { props: { event: event({}) }, global: { stubs } }).find('.section').exists()).toBe(false)
  })

  it('renders headers, query string and cookies', () => {
    const wrapper = mount(IssueHttpRequest, {
      props: { event: event({ request: { method: 'GET', url: '/a', headers: { Accept: ['text/html'] }, query_string: { q: '1' }, cookies: { sid: 'abc' } } }) },
      global: { stubs },
    })
    expect(wrapper.find('.section__badge--method').text()).toBe('GET')
    expect(wrapper.findAll('.req-subtitle').map(s => s.text())).toEqual(['Headers', 'Query String', 'Cookies'])
    expect(wrapper.text()).toContain('text/html')
    expect(wrapper.text()).toContain('sid')
  })

  it('collapses the expanded body again when the event changes', async () => {
    const wrapper = mount(IssueHttpRequest, { props: { event: event({ request: { data: longBody } }) }, global: { stubs } })
    await wrapper.find('.req-body__toggle').trigger('click')
    expect(wrapper.find('.req-body__toggle').text()).toBe('Show less')
    await wrapper.setProps({ event: event({ request: { data: longBody } }, 'evt-2') })
    expect(wrapper.find('.req-body__toggle').text()).toBe('Show all 22 lines')
  })
})

describe('IssueBreadcrumbs', () => {
  const crumbs = (id = 'evt-1') => event({ breadcrumbs: { values: [
    { timestamp: 1716739678.943, category: 'http', message: 'GET /a', data: { status: 200 } },
    { timestamp: '2024-01-01T00:00:00Z', type: 'error', data: 'plain' },
  ] } }, id)

  it('counts the crumbs and marks errors', () => {
    const wrapper = mount(IssueBreadcrumbs, { props: { event: crumbs() }, global: { stubs } })
    expect(wrapper.find('.section__count').text()).toBe('2 events leading up to crash')
    expect(wrapper.findAll('.crumb')[1].classes()).toContain('crumb--error')
    expect(wrapper.findAll('.crumb__msg')[1].text()).toBe('plain')
    expect(wrapper.findAll('.crumb__toggle')).toHaveLength(1)
  })

  it('closes expanded crumb data when the event changes', async () => {
    const wrapper = mount(IssueBreadcrumbs, { props: { event: crumbs() }, global: { stubs } })
    await wrapper.find('.crumb__toggle').trigger('click')
    expect(wrapper.find('.crumb__detail').text()).toContain('status')
    await wrapper.setProps({ event: crumbs('evt-2') })
    expect(wrapper.find('.crumb__detail').exists()).toBe(false)
  })
})

describe('IssueContexts', () => {
  it('renders a card per context and hides when there are none', () => {
    const wrapper = mount(IssueContexts, { props: { event: event({ contexts: { runtime: { name: 'CPython' } } }) }, global: { stubs } })
    expect(wrapper.find('.ctx-card__title').text()).toBe('Runtime')
    expect(mount(IssueContexts, { props: { event: event({}) }, global: { stubs } }).find('.section').exists()).toBe(false)
  })
})

describe('IssuePackages', () => {
  it('starts collapsed and lists packages sorted by name once opened', async () => {
    const wrapper = mount(IssuePackages, { props: { event: event({ modules: { zlib: '1', attrs: '2', skip: null } }) }, global: { stubs } })
    expect(wrapper.find('.section__count').text()).toBe('2')
    expect(wrapper.find('.pkg-table').exists()).toBe(false)
    await wrapper.find('.section__head').trigger('click')
    expect(wrapper.findAll('.pkg-table__name').map(n => n.text())).toEqual(['attrs', 'zlib'])
  })

  it('previews ten packages and shows the rest on request', async () => {
    localStorage.setItem('tindra:issue:collapsed', JSON.stringify({ packages: false }))
    const modules = Object.fromEntries(Array.from({ length: 12 }, (_, i) => [`m${String(i).padStart(2, '0')}`, '1']))
    const wrapper = mount(IssuePackages, { props: { event: event({ modules }) }, global: { stubs } })
    expect(wrapper.findAll('.pkg-table tr')).toHaveLength(10)
    expect(wrapper.find('.pkg-table__more').text()).toBe('Show 2 more')
    await wrapper.find('.pkg-table__more').trigger('click')
    expect(wrapper.findAll('.pkg-table tr')).toHaveLength(12)
  })
})
