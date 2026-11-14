import { describe, it, expect, vi, beforeEach } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// Runs public/sw.js against a fake ServiceWorkerGlobalScope.
const source = readFileSync(resolve(__dirname, '../../../public/sw.js'), 'utf8')
const ORIGIN = 'https://tindra.example.com'

type Handler = (event: any) => void

function loadWorker() {
  const handlers: Record<string, Handler> = {}
  const self = {
    location: new URL(`${ORIGIN}/sw.js`),
    addEventListener: (type: string, fn: Handler) => { handlers[type] = fn },
    skipWaiting: vi.fn(),
    clients: {
      claim: vi.fn(() => Promise.resolve()),
      matchAll: vi.fn((): Promise<any[]> => Promise.resolve([])),
      openWindow: vi.fn(() => Promise.resolve(null)),
    },
    registration: {
      showNotification: vi.fn(() => Promise.resolve()),
      pushManager: { subscribe: vi.fn() },
    },
  }
  const fetchMock = vi.fn()
  new Function('self', 'fetch', 'atob', source)(self, fetchMock, atob)
  return { self, handlers, fetchMock }
}

function dispatch(handler: Handler, event: Record<string, unknown>) {
  let pending: Promise<unknown> = Promise.resolve()
  handler({ ...event, waitUntil: (p: Promise<unknown>) => { pending = p } })
  return pending
}

describe('service worker', () => {
  let w: ReturnType<typeof loadWorker>
  beforeEach(() => { w = loadWorker() })

  it('has no fetch handler, so nothing is ever served from a cache', () => {
    expect(w.handlers.fetch).toBeUndefined()
    expect(Object.keys(w.handlers).sort()).toEqual(['activate', 'install', 'notificationclick', 'push', 'pushsubscriptionchange'])
  })

  it('activates immediately', async () => {
    w.handlers.install!({})
    expect(w.self.skipWaiting).toHaveBeenCalled()
    await dispatch(w.handlers.activate!, {})
    expect(w.self.clients.claim).toHaveBeenCalled()
  })

  it('shows the pushed notification', async () => {
    const data = { json: () => ({ title: 'Shop - New issue: boom', body: 'Rule: errors', url: '/issues/1', tag: 'rule-1' }) }
    await dispatch(w.handlers.push!, { data })
    expect(w.self.registration.showNotification).toHaveBeenCalledWith('Shop - New issue: boom', {
      body: 'Rule: errors', tag: 'rule-1', renotify: true, icon: '/icons/icon-192.png', data: { url: '/issues/1' },
    })
  })

  it('always shows something, even for empty or non-JSON pushes', async () => {
    await dispatch(w.handlers.push!, {})
    expect(w.self.registration.showNotification).toHaveBeenLastCalledWith('Tindra', expect.objectContaining({ body: '', renotify: false }))
    await dispatch(w.handlers.push!, { data: { json: () => { throw new Error('x') }, text: () => 'plain' } })
    expect(w.self.registration.showNotification).toHaveBeenLastCalledWith('Tindra', expect.objectContaining({ body: 'plain' }))
  })

  it('routes a click into an open window', async () => {
    const client = { url: `${ORIGIN}/dashboard`, postMessage: vi.fn(), focus: vi.fn(() => Promise.resolve()) }
    w.self.clients.matchAll.mockResolvedValue([{ url: 'https://other.example.com/', postMessage: vi.fn(), focus: vi.fn() }, client])
    const notification = { close: vi.fn(), data: { url: '/issues/1' } }
    await dispatch(w.handlers.notificationclick!, { notification })
    expect(notification.close).toHaveBeenCalled()
    expect(client.postMessage).toHaveBeenCalledWith({ type: 'tindra:navigate', url: `${ORIGIN}/issues/1` })
    expect(client.focus).toHaveBeenCalled()
    expect(w.self.clients.openWindow).not.toHaveBeenCalled()
  })

  it('opens a window when none is open and never leaves the instance', async () => {
    for (const [url, expected] of [
      ['/monitors/uptime?project_id=p', `${ORIGIN}/monitors/uptime?project_id=p`],
      ['https://evil.example.com/phish', `${ORIGIN}/`],
      [undefined, `${ORIGIN}/`],
    ] as const) {
      await dispatch(w.handlers.notificationclick!, { notification: { close: vi.fn(), data: { url } } })
      expect(w.self.clients.openWindow).toHaveBeenLastCalledWith(expected)
    }
  })

  it('re-subscribes and tells the server when the push service rotates the subscription', async () => {
    const key = 'BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8'
    w.fetchMock.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ enabled: true, public_key: key }) })
    w.fetchMock.mockResolvedValue({ ok: true })
    const fresh = { endpoint: 'https://push.example.com/new', toJSON: () => ({ endpoint: 'https://push.example.com/new' }) }
    w.self.registration.pushManager.subscribe.mockResolvedValue(fresh)

    await dispatch(w.handlers.pushsubscriptionchange!, { oldSubscription: { endpoint: 'https://push.example.com/old' } })

    const opts = w.self.registration.pushManager.subscribe.mock.calls[0]![0]
    expect(opts.userVisibleOnly).toBe(true)
    expect(opts.applicationServerKey).toHaveLength(65)
    const posts = w.fetchMock.mock.calls.slice(1).map(([url, init]) => [url, JSON.parse(init.body)])
    expect(posts).toEqual([
      ['/api/push/subscriptions', { endpoint: 'https://push.example.com/new' }],
      ['/api/push/subscriptions/unsubscribe', { endpoint: 'https://push.example.com/old' }],
    ])
  })

  it('skips re-subscribing when push is off or the user is signed out', async () => {
    w.fetchMock.mockResolvedValueOnce({ ok: false })
    await dispatch(w.handlers.pushsubscriptionchange!, {})
    w.fetchMock.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ enabled: false, public_key: '' }) })
    await dispatch(w.handlers.pushsubscriptionchange!, {})
    expect(w.self.registration.pushManager.subscribe).not.toHaveBeenCalled()
  })
})
