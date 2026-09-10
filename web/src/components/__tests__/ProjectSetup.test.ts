import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { createPinia } from 'pinia'
import { apiFetch, ApiError } from '@/api/client'
import type { SetupCheck, SetupStatus } from '@/api/setup'
import ProjectSetup from '../ProjectSetup.vue'
import { useAuthStore } from '@/stores/auth'
import type { User } from '@/api/types'

const showToast = vi.fn()
vi.mock('@/api/client', async importOriginal => {
  const original = await importOriginal<typeof import('@/api/client')>()
  return { ...original, apiFetch: vi.fn() }
})
vi.mock('@/composables/useConfig', () => ({ useConfig: () => ({ dsnFor: (key: string, id: string) => `https://${key}@tindra.test/${id}` }) }))
vi.mock('@/composables/useToast', () => ({ useToast: () => ({ show: showToast }) }))

const project = { id: 'project-one', slug: 'app', name: 'App', public_key: 'key-one' }
let status: SetupStatus
let check: SetupCheck
let wrapper: VueWrapper | undefined
let client: QueryClient
beforeEach(() => {
  vi.clearAllMocks()
  sessionStorage.clear()
  check = { id: '123e4567-e89b-42d3-a456-426614174000', created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 86_400_000).toISOString(), received_at: null, event_id: null }
  status = { checked_at: new Date().toISOString(), profiling_enabled: true, receipts: [], observations: [], check: null, examples: {}, profile_transaction_id: null, sourcemap_count: 0, sourcemaps_available: true }
  vi.mocked(apiFetch).mockImplementation(async (path, init) => {
    if (init?.method === 'POST') { status = { ...status, check }; return check as never }
    if (path.includes('setup-sourcemaps')) return { status: 'needs_attention', release: '', frames: [], truncated: false } as never
    return structuredClone(status) as never
  })
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined; client.clear(); vi.useRealTimers(); vi.unstubAllGlobals() })

async function render() {
  wrapper = mount(ProjectSetup, { props: { project }, global: { plugins: [createPinia(), [VueQueryPlugin, { queryClient: client }]], stubs: { Icon: true } } })
  await flushPromises()
  return wrapper
}
async function click(label: string) {
  const button = wrapper!.findAll('button').find(b => b.text() === label)
  expect(button).toBeDefined()
  await button!.trigger('click')
  await flushPromises()
}

describe('Project setup', () => {
  it('generates a correlated app snippet and includes a Node flush', async () => {
    await render()
    expect(wrapper!.text()).toContain('Waiting for your first event')
    await click('Start setup check')
    expect(wrapper!.find('pre').text()).toContain(`scope.setTag("tindra_setup", "${check.id}")`)
    expect(wrapper!.find('pre').text()).toContain('https://key-one@tindra.test/project-one')
    expect(wrapper!.find('pre').text()).toContain('await Sentry.flush(5000)')
    await wrapper!.find('select').setValue('browser')
    expect(wrapper!.find('pre').text()).toContain('@sentry/browser')
    expect(wrapper!.find('pre').text()).not.toContain('Sentry.flush')
  })

  it('does not let background traffic confirm the active test', async () => {
    await render()
    await click('Start setup check')
    status.receipts = [{ kind: 'events', first_received_at: status.checked_at, last_received_at: status.checked_at, latest_id: 'unrelated-event' }]
    await click('Check again')
    expect(wrapper!.find('h2').text()).toBe('Waiting for your test event')
    expect(wrapper!.text()).not.toContain('Test event received')
    status.check = { ...check, received_at: status.checked_at, event_id: 'matching-event' }
    status.examples.test = { id: 'matching-event', issue_id: 'issue-one', received_at: status.checked_at, environment: 'staging', sdk: 'sentry.javascript.node 10', release: 'v1' }
    await click('Check again')
    expect(wrapper!.find('h2').text()).toBe('Test event received')
    expect(wrapper!.text()).toContain('staging')
    expect(wrapper!.find('a[href="/issues/issue-one?event_id=matching-event"]').exists()).toBe(true)
    expect(wrapper!.emitted('continue')).toBeUndefined()
    await click('Continue to dashboard')
    expect(wrapper!.emitted('continue')).toHaveLength(1)
  })

  it('shows queued data and profile drops without claiming storage', async () => {
    status.profiling_enabled = false
    status.observations = [
      { kind: 'events', outcome: 'queued', reason: 'queued', observed_at: status.checked_at },
      { kind: 'profile_chunks', outcome: 'rejected', reason: 'profiling_disabled', observed_at: status.checked_at },
    ]
    await render()
    expect(wrapper!.text()).toContain('Queued')
    expect(wrapper!.text()).toContain('has not confirmed storage')
    expect(wrapper!.text()).toContain('profiling is disabled')
    expect(wrapper!.find('h2').text()).toBe('Waiting for your first event')
  })

  it('retains confirmation after the original event is removed', async () => {
    check.received_at = status.checked_at
    status.check = check
    sessionStorage.setItem(`tindra:setup:${project.id}`, check.id)
    await render()
    expect(wrapper!.find('h2').text()).toBe('Test event received')
    expect(wrapper!.text()).toContain('receipt is preserved')
    expect(wrapper!.text()).not.toContain('View event')
  })

  it('clears test state when switching projects', async () => {
    await render()
    await click('Start setup check')
    status.check = null
    await wrapper!.setProps({ project: { ...project, id: 'project-two', slug: 'other', public_key: 'key-two' } })
    await flushPromises()
    expect(wrapper!.text()).not.toContain(check.id)
    expect(vi.mocked(apiFetch)).toHaveBeenCalledWith('/api/projects/other/setup-status', { signal: expect.any(AbortSignal) })
    expect(wrapper!.find('input').element.value).toBe('https://key-two@tindra.test/project-two')
  })

  it('presents API failure as unavailable status rather than a disconnected app', async () => {
    vi.mocked(apiFetch).mockRejectedValue(new ApiError(500, 'failure'))
    await render()
    expect(wrapper!.find('[role="alert"]').text()).toContain('does not mean your app is disconnected')
    expect(wrapper!.text()).not.toContain('Listening')
  })

  it('does not mark uploaded source maps as verified', async () => {
    status.sourcemap_count = 2
    await render()
    expect(wrapper!.text()).toContain('2 uploaded, not verified')
    await click('Verify source maps')
    expect(wrapper!.text()).toContain('Choose an event first')
  })

  it('explains expired tests and hides their stale snippets', async () => {
    check.expires_at = new Date(Date.now() - 1000).toISOString()
    status.check = check
    sessionStorage.setItem(`tindra:setup:${project.id}`, check.id)
    await render()
    expect(wrapper!.find('h2').text()).toBe('Setup check expired')
    expect(wrapper!.find('pre').exists()).toBe(false)
    expect(wrapper!.text()).toContain('Start a new check')
  })

  it('re-verifies the diagnosed event after an upload when newer traffic arrives', async () => {
    status.examples.events = { id: 'original-event', received_at: status.checked_at }
    await render()
    useAuthStore().user = { permissions: { manage_projects: true } } as User
    await click('Verify source maps')
    status.examples.events = { id: 'newer-event', received_at: status.checked_at }
    await wrapper!.find('input[id$="-release"]').setValue('v1')
    await wrapper!.find('input[id$="-url"]').setValue('~/app.js')
    const file = wrapper!.find('input[type="file"]')
    Object.defineProperty(file.element, 'files', { value: [new File(['{}'], 'app.js.map')] })
    await file.trigger('change')
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true }))
    await wrapper!.find('form').trigger('submit')
    await flushPromises()
    const calls = vi.mocked(apiFetch).mock.calls.filter(([path]) => path.includes('setup-sourcemaps'))
    expect(calls).toHaveLength(2)
    expect(calls[1]![0]).toContain('event_id=original-event')
  })

  it('ignores verification errors after the selected event changes', async () => {
    await render()
    await wrapper!.find('input[id$="-event"]').setValue('old-event')
    let reject!: (error: Error) => void
    vi.mocked(apiFetch).mockImplementationOnce(() => new Promise((_, fail) => { reject = fail }))
    await click('Verify source maps')
    await wrapper!.find('input[id$="-event"]').setValue('new-event')
    reject(new ApiError(404, 'not found'))
    await flushPromises()
    expect(wrapper!.text()).not.toContain('This event was not found')
  })

  it('remembers set up later for this project in the current tab', async () => {
    await render()
    await click('Set up later')
    expect(sessionStorage.getItem(`tindra:setup-dismissed:${project.id}`)).toBe('1')
    expect(wrapper!.emitted('continue')).toHaveLength(1)
  })

  it('reports clipboard failures instead of claiming success', async () => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) } })
    await render()
    await click('Copy DSN')
    expect(showToast).toHaveBeenCalledWith(expect.stringContaining('Could not copy'), 'error')
  })
})

describe('setup recovery and async results', () => {
 it('ignores a setup check created for a project that is no longer selected', async () => {
  await render()
  let resolve!: (value: SetupCheck) => void
  vi.mocked(apiFetch).mockImplementationOnce(() => new Promise(done => { resolve = done }))
  await click('Start setup check')
  await wrapper!.setProps({ project: { ...project, id: 'other', slug: 'other' } })
  resolve(check)
  await flushPromises()
  expect(sessionStorage.getItem('tindra:setup:project-one')).toBeNull()
  expect(wrapper!.text()).not.toContain(check.id)
 })
 it('recovers from an unavailable status and supports other SDKs', async () => {
  vi.mocked(apiFetch).mockRejectedValueOnce(new ApiError(500, 'failed'))
  await render()
  await click('Try again')
  expect(wrapper!.find('h2').text()).toBe('Waiting for your first event')
  await click('Start setup check')
  await wrapper!.find('select').setValue('other')
  expect(wrapper!.text()).toContain('capture an exception with the event tag')
  expect(wrapper!.text()).toContain(check.id)
 })
 it.each([404, 500])('explains source map verification failure %s', async statusCode => {
  await render()
  await wrapper!.find('input[id$="-event"]').setValue('test-event')
  vi.mocked(apiFetch).mockRejectedValueOnce(new ApiError(statusCode, 'failed'))
  await click('Verify source maps')
  expect(wrapper!.text()).toContain(statusCode === 404 ? 'This event was not found' : 'Could not verify source maps')
 })
 it('renders verified and unresolved source map frames', async () => {
  await render()
  await wrapper!.find('input[id$="-event"]').setValue('test-event')
  vi.mocked(apiFetch).mockResolvedValueOnce({ status: 'partially_verified', release: 'v1', truncated: true, frames: [
   { url: '/app.js', normalized_url: '~/app.js', line: 1, column: 0, status: 'verified', source: 'app.ts', original_line: 12 },
   { url: '/vendor.js', normalized_url: '~/vendor.js', line: 2, column: 1, status: 'no_matching_map' },
  ] })
  await click('Verify source maps')
  expect(wrapper!.text()).toContain('Resolved to app.ts:12')
  expect(wrapper!.text()).toContain('No map matches this release and file URL')
 })
 it('reports upload failure and permits a retry', async () => {
  await render()
  useAuthStore().user = { permissions: { manage_projects: true } } as User
  await flushPromises()
  const file = wrapper!.find('input[type="file"]')
  Object.defineProperty(file.element, 'files', { value: [new File(['{}'], 'app.js.map')] })
  await file.trigger('change')
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }))
  await wrapper!.find('form').trigger('submit')
  await flushPromises()
  expect(wrapper!.text()).toContain('Could not upload the source map')
  expect(wrapper!.find('form button').attributes('disabled')).toBeUndefined()
 })
})

it('recovers from a failed setup check and copies the generated snippet', async () => {
 await render()
 vi.mocked(apiFetch).mockRejectedValueOnce(new ApiError(500, 'unavailable'))
 await click('Start setup check')
 expect(wrapper!.text()).toContain('Could not start a setup check')
 await click('Start setup check')
 const writeText = vi.fn().mockResolvedValue(undefined)
 Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
 await click('Copy snippet')
 expect(writeText).toHaveBeenCalledWith(expect.stringContaining(check.id))
 expect(showToast).toHaveBeenCalledWith('Snippet copied')
})

it('keeps setup usable when browser storage is unavailable', async () => {
 const read = vi.fn().mockImplementation(() => { throw new Error('blocked') })
 const write = vi.fn().mockImplementation(() => { throw new Error('blocked') })
 vi.stubGlobal('sessionStorage', { getItem: read, setItem: write })
 try {
  await render()
  await click('Start setup check')
  expect(wrapper!.text()).toContain(check.id)
  expect(read).toHaveBeenCalled()
  expect(write).toHaveBeenCalled()
  await click('Set up later')
  expect(wrapper!.emitted('continue')).toHaveLength(1)
 } finally { vi.unstubAllGlobals() }
})

it('polls for a test event and switches to troubleshooting after a minute', async () => {
 vi.useFakeTimers()
 await render()
 await click('Start setup check')
 await vi.advanceTimersByTimeAsync(61_000)
 await flushPromises()
 expect(wrapper!.text()).toContain('Still waiting.')
 expect(vi.mocked(apiFetch).mock.calls.filter(([path]) => path.includes('setup-status')).length).toBeGreaterThan(3)
})

it('shows recent rejected envelopes and data that needs attention', async () => {
 status.receipts = [{ kind: 'events', first_received_at: status.checked_at, last_received_at: status.checked_at, latest_id: 'event' }]
 status.observations = [
  { kind: 'events', outcome: 'rejected', reason: 'storage_failed', observed_at: new Date(Date.now() + 1000).toISOString() },
  { kind: 'envelope', outcome: 'rejected', reason: 'bad_gzip', observed_at: status.checked_at },
 ]
 await render()
 expect(wrapper!.text()).toContain('Needs attention')
 expect(wrapper!.text()).toContain('could not decompress the request')
})

it.each(['project', 'event'])('discards successful verification when the %s changes', async change => {
 await render()
 await wrapper!.find('input[id$="-event"]').setValue('old-event')
 let resolve!: (data: unknown) => void
 vi.mocked(apiFetch).mockImplementationOnce(() => new Promise(done => { resolve = done }))
 await click('Verify source maps')
 if (change === 'project') await wrapper!.setProps({ project: { ...project, id: 'other', slug: 'other' } })
 else await wrapper!.find('input[id$="-event"]').setValue('new-event')
 resolve({ status: 'verified', release: 'old-release', frames: [], truncated: false })
 await flushPromises()
 expect(wrapper!.text()).not.toContain('old-release')
})

it.each([true, false])('ignores a stale upload result (success: %s)', async success => {
 await render()
 useAuthStore().user = { permissions: { manage_projects: true } } as User
 await flushPromises()
 await wrapper!.find('form').trigger('submit')
 expect(showToast).not.toHaveBeenCalled()
 const file = wrapper!.find('input[type="file"]')
 Object.defineProperty(file.element, 'files', { value: [new File(['{}'], 'app.js.map')] })
 await file.trigger('change')
 let resolve!: (value: unknown) => void
 vi.stubGlobal('fetch', vi.fn(() => new Promise(done => { resolve = done })))
 await wrapper!.find('form').trigger('submit')
 await wrapper!.setProps({ project: { ...project, id: 'other', slug: 'other' } })
 resolve({ ok: success })
 await flushPromises()
 expect(showToast).not.toHaveBeenCalled()
 expect(wrapper!.text()).not.toContain('Could not upload the source map')
})

it('links received transactions and matching profiles to their exact records', async () => {
 status.examples.transactions = { id: 'stored-transaction', received_at: status.checked_at }
 status.profile_transaction_id = 'profiled-transaction'
 status.receipts = ['transactions', 'profile_chunks'].map(kind => ({ kind: kind as 'transactions' | 'profile_chunks', first_received_at: status.checked_at, last_received_at: status.checked_at, latest_id: 'stored-record' }))
 await render()
 await wrapper!.setProps({ focus: 'profiles' })
 expect(wrapper!.find('a[href="/transactions/stored-transaction"]').text()).toBe('View transaction')
 expect(wrapper!.find('a[href="/transactions/profiled-transaction"]').text()).toBe('Open profiled transaction')
 expect(wrapper!.findAll('details').find(d => d.find('summary').text().includes('Profiles'))!.attributes('open')).toBeDefined()
 status.profile_transaction_id = null
 await click('Check again')
 expect(wrapper!.text()).toContain('no matching transaction was found')
})

it('can replace a saved setup check that the server no longer has', async () => {
 sessionStorage.setItem(`tindra:setup:${project.id}`, '00000000-0000-4000-8000-000000000001')
 vi.mocked(apiFetch).mockRejectedValueOnce(new ApiError(404, 'check not found'))
 await render()
 await click('Start a new check')
 expect(wrapper!.find('h2').text()).toBe('Waiting for your test event')
 expect(wrapper!.find('pre').text()).toContain(check.id)
})

it('distinguishes a stored event awaiting issue processing from an expired event', async () => {
 status.check = { ...check, received_at: status.checked_at, event_id: 'processing-event' }
 status.examples.test = { id: 'processing-event', received_at: status.checked_at }
 await render()
 expect(wrapper!.text()).toContain('Its issue link will appear once processing finishes')
 expect(wrapper!.text()).not.toContain('event has been removed or expired')
})

it('explains when source map storage is unavailable', async () => {
 status.sourcemaps_available = false
 await render()
 expect(wrapper!.text()).toContain('Source map storage is not configured on this server')
 expect(wrapper!.find('input[id$="-event"]').exists()).toBe(false)
})
