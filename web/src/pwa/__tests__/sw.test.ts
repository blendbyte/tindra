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
  // Like Cache Storage: keys() gives requests, the rest takes either.
  const stored = new Map<string, string>()
  const path = (req: string | { url: string }) => (typeof req === 'string' ? req : new URL(req.url).pathname)
  const cache = {
    put: async (req: string | { url: string }, res: Response) => { stored.set(path(req), await res.text()) },
    keys: async () => [...stored.keys()].map(k => ({ url: ORIGIN + k })),
    match: async (req: string | { url: string }) => (stored.has(path(req)) ? new Response(stored.get(path(req))) : undefined),
    delete: async (req: string | { url: string }) => stored.delete(path(req)),
  }
  const caches = { open: vi.fn(async () => cache) }
  new Function('self', 'fetch', 'atob', 'btoa', 'caches', source)(self, fetchMock, atob, btoa, caches)
  return { self, handlers, fetchMock, stored }
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
    expect(Object.keys(w.handlers).sort()).toEqual(['activate', 'install', 'message', 'notificationclick', 'push', 'pushsubscriptionchange'])
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

  it('only takes string fields from the payload', async () => {
    for (const json of [{ title: null, body: 3, url: '', tag: {} }, 'hi', null, [1, 2]]) {
      await dispatch(w.handlers.push!, { data: { json: () => json } })
      expect(w.self.registration.showNotification).toHaveBeenLastCalledWith('Tindra', {
        body: '', tag: undefined, renotify: false, icon: '/icons/icon-192.png', data: { url: '/' },
      })
    }
  })

  it('always shows something, even for empty or non-JSON pushes', async () => {
    await dispatch(w.handlers.push!, {})
    expect(w.self.registration.showNotification).toHaveBeenLastCalledWith('Tindra', expect.objectContaining({ body: '', renotify: false }))
    await dispatch(w.handlers.push!, { data: { json: () => { throw new Error('x') }, text: () => 'plain' } })
    expect(w.self.registration.showNotification).toHaveBeenLastCalledWith('Tindra', expect.objectContaining({ body: 'plain' }))
  })

  it('skips same-origin windows that do not run the app and prefers the focused one', async () => {
    const api = { url: `${ORIGIN}/api/issues/1`, postMessage: vi.fn(), focus: vi.fn() }
    const background = { url: `${ORIGIN}/issues`, focused: false, postMessage: vi.fn(), focus: vi.fn() }
    const focused = { url: `${ORIGIN}/dashboard`, focused: true, postMessage: vi.fn(), focus: vi.fn(() => Promise.resolve()) }
    w.self.clients.matchAll.mockResolvedValue([api, background, focused])
    await dispatch(w.handlers.notificationclick!, { notification: { close: vi.fn(), data: { url: '/issues/1' } } })
    expect(api.postMessage).not.toHaveBeenCalled()
    expect(background.postMessage).not.toHaveBeenCalled()
    expect(focused.postMessage).toHaveBeenCalledWith({ type: 'tindra:navigate', url: `${ORIGIN}/issues/1` })

    w.self.clients.matchAll.mockResolvedValue([api])
    await dispatch(w.handlers.notificationclick!, { notification: { close: vi.fn(), data: { url: '/issues/1' } } })
    expect(w.self.clients.openWindow).toHaveBeenCalledWith(`${ORIGIN}/issues/1`)
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

  const OLD_AUTH = new Uint8Array([251, 255, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13])
  const oldSubscription = (extra: Record<string, unknown> = {}) => ({
    endpoint: 'https://push.example.com/old',
    getKey: (name: string) => (name === 'auth' ? OLD_AUTH.buffer : null),
    options: { applicationServerKey: new Uint8Array(65).buffer },
    ...extra,
  })

  const SERVER_KEY = 'BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8'
  const serverConfig = (enabled = true) => ({ ok: true, json: () => Promise.resolve({ enabled, public_key: enabled ? SERVER_KEY : '' }) })

  it('moves the device to the rotated subscription without a session', async () => {
    w.fetchMock.mockResolvedValueOnce(serverConfig())
    w.fetchMock.mockResolvedValue({ ok: true })
    const fresh = { endpoint: 'https://push.example.com/new', toJSON: () => ({ endpoint: 'https://push.example.com/new' }) }
    w.self.registration.pushManager.subscribe.mockResolvedValue(fresh)
    const old = oldSubscription()

    await dispatch(w.handlers.pushsubscriptionchange!, { oldSubscription: old })

    // The server's current key, not the old subscription's (it may have changed).
    const opts = w.self.registration.pushManager.subscribe.mock.calls[0]![0]
    expect(opts.userVisibleOnly).toBe(true)
    expect(Array.from(opts.applicationServerKey as Uint8Array).slice(0, 3)).toEqual([4, 254, 51])
    expect(w.fetchMock.mock.calls[0]).toEqual(['/api/push/config', { credentials: 'omit' }])
    expect(w.fetchMock).toHaveBeenCalledTimes(2)
    const [url, init] = w.fetchMock.mock.calls[1]!
    expect(url).toBe('/api/push/rotate')
    expect(init.credentials).toBe('omit')
    expect(JSON.parse(init.body)).toEqual({
      endpoint: 'https://push.example.com/new',
      replaces: 'https://push.example.com/old',
      replaces_auth: '-_8AAQIDBAUGBwgJCgsMDQ',
    })
  })

  it('falls back to the old key when the server key cannot be read', async () => {
    w.fetchMock.mockResolvedValueOnce({ ok: false })
    w.fetchMock.mockResolvedValue({ ok: true })
    w.self.registration.pushManager.subscribe.mockResolvedValue({ endpoint: 'https://push.example.com/new', toJSON: () => ({ endpoint: 'https://push.example.com/new' }) })
    const old = oldSubscription()
    await dispatch(w.handlers.pushsubscriptionchange!, { oldSubscription: old })
    expect(w.self.registration.pushManager.subscribe).toHaveBeenCalledWith({ userVisibleOnly: true, applicationServerKey: old.options.applicationServerKey })
    expect(w.fetchMock.mock.calls[1]![0]).toBe('/api/push/rotate')
  })

  it('uses the new subscription the browser hands over', async () => {
    w.fetchMock.mockResolvedValue({ ok: true })
    const fresh = { endpoint: 'https://push.example.com/handed', toJSON: () => ({ endpoint: 'https://push.example.com/handed' }) }
    await dispatch(w.handlers.pushsubscriptionchange!, { oldSubscription: oldSubscription(), newSubscription: fresh })
    expect(w.self.registration.pushManager.subscribe).not.toHaveBeenCalled()
    expect(JSON.parse(w.fetchMock.mock.calls[0]![1].body)).toMatchObject({ endpoint: 'https://push.example.com/handed', replaces: 'https://push.example.com/old' })
  })

  it('does nothing without the old subscription or its secret, so nothing removed comes back', async () => {
    await dispatch(w.handlers.pushsubscriptionchange!, { oldSubscription: null })
    await dispatch(w.handlers.pushsubscriptionchange!, { oldSubscription: { endpoint: 'https://push.example.com/old' } })
    await dispatch(w.handlers.pushsubscriptionchange!, { oldSubscription: oldSubscription({ getKey: () => null }) })
    expect(w.fetchMock).not.toHaveBeenCalled()
    expect(w.self.registration.pushManager.subscribe).not.toHaveBeenCalled()
  })

  it('gives up when it cannot subscribe again', async () => {
    w.fetchMock.mockResolvedValueOnce(serverConfig(false))
    await dispatch(w.handlers.pushsubscriptionchange!, { oldSubscription: oldSubscription({ options: {} }) })
    expect(w.fetchMock).toHaveBeenCalledTimes(1)
    expect(w.self.registration.pushManager.subscribe).not.toHaveBeenCalled()
  })

  it('retries the rotation and keeps it for the app when the server stays away', async () => {
    vi.useFakeTimers()
    try {
      w.fetchMock.mockResolvedValueOnce(serverConfig())
      w.fetchMock.mockResolvedValueOnce({ ok: false, status: 502 })
      w.fetchMock.mockRejectedValueOnce(new TypeError('offline'))
      w.fetchMock.mockResolvedValueOnce({ ok: false, status: 408 })
      w.self.registration.pushManager.subscribe.mockResolvedValue({ endpoint: 'https://push.example.com/new', toJSON: () => ({ endpoint: 'https://push.example.com/new' }) })
      const done = dispatch(w.handlers.pushsubscriptionchange!, { oldSubscription: oldSubscription() })
      await vi.advanceTimersByTimeAsync(12_000)
      await done
      expect(w.fetchMock.mock.calls.filter(([url]) => url === '/api/push/rotate')).toHaveLength(3)
      const key = '/__tindra/pending-push-rotation/' + encodeURIComponent('https://push.example.com/old')
      expect(JSON.parse(w.stored.get(key)!)).toMatchObject({ moves: [{ replaces: 'https://push.example.com/old' }] })
    } finally {
      vi.useRealTimers()
    }
  })

  it('stops retrying on a final answer', async () => {
    w.fetchMock.mockResolvedValueOnce(serverConfig())
    w.fetchMock.mockResolvedValueOnce({ ok: false, status: 404 })
    w.self.registration.pushManager.subscribe.mockResolvedValue({ endpoint: 'https://push.example.com/new', toJSON: () => ({ endpoint: 'https://push.example.com/new' }) })
    await dispatch(w.handlers.pushsubscriptionchange!, { oldSubscription: oldSubscription() })
    expect(w.fetchMock).toHaveBeenCalledTimes(2)
    expect(w.stored.size).toBe(0)
  })

  it('joins a rotation still waiting for the app with the next one', async () => {
    // A to B is parked; now B rotates to C. The server only knows A.
    w.stored.set('/__tindra/pending-push-rotation/a', JSON.stringify({ moves: [{ endpoint: 'https://push.example.com/old', replaces: 'https://push.example.com/a', replaces_auth: 'secret-a' }] }))
    w.stored.set('/__tindra/pending-push-rotation/x', JSON.stringify({ moves: [{ endpoint: 'https://push.example.com/other', replaces: 'https://push.example.com/x', replaces_auth: 'secret-x' }] }))
    w.fetchMock.mockResolvedValueOnce(serverConfig())
    w.fetchMock.mockResolvedValue({ ok: true, status: 204 })
    w.self.registration.pushManager.subscribe.mockResolvedValue({ endpoint: 'https://push.example.com/c', toJSON: () => ({ endpoint: 'https://push.example.com/c' }) })
    await dispatch(w.handlers.pushsubscriptionchange!, { oldSubscription: oldSubscription() })
    expect(JSON.parse(w.fetchMock.mock.calls[1]![1].body)).toEqual({
      endpoint: 'https://push.example.com/c', replaces: 'https://push.example.com/a', replaces_auth: 'secret-a',
    })
    expect([...w.stored.keys()]).toEqual(['/__tindra/pending-push-rotation/x'], 'the joined one is done, the unrelated one stays')
  })

  it('falls back to the newer endpoint when the parked move had reached the server', async () => {
    w.stored.set('/__tindra/pending-push-rotation/a', JSON.stringify({ moves: [{ endpoint: 'https://push.example.com/old', replaces: 'https://push.example.com/a', replaces_auth: 'secret-a' }] }))
    w.fetchMock.mockResolvedValueOnce(serverConfig())
    w.fetchMock.mockResolvedValueOnce({ ok: false, status: 404 })
    w.fetchMock.mockResolvedValueOnce({ ok: true, status: 204 })
    w.self.registration.pushManager.subscribe.mockResolvedValue({ endpoint: 'https://push.example.com/c', toJSON: () => ({ endpoint: 'https://push.example.com/c' }) })
    await dispatch(w.handlers.pushsubscriptionchange!, { oldSubscription: oldSubscription() })
    expect(JSON.parse(w.fetchMock.mock.calls[2]![1].body)).toEqual({
      endpoint: 'https://push.example.com/c', replaces: 'https://push.example.com/old', replaces_auth: '-_8AAQIDBAUGBwgJCgsMDQ',
    })
    expect(w.stored.size).toBe(0)
  })

  it('parks a joined move with every fallback when the server stays away', async () => {
    vi.useFakeTimers()
    try {
      w.stored.set('/__tindra/pending-push-rotation/a', JSON.stringify({ moves: [{ endpoint: 'https://push.example.com/old', replaces: 'https://push.example.com/a', replaces_auth: 'secret-a' }] }))
      w.fetchMock.mockResolvedValueOnce(serverConfig())
      w.fetchMock.mockResolvedValue({ ok: false, status: 503 })
      w.self.registration.pushManager.subscribe.mockResolvedValue({ endpoint: 'https://push.example.com/c', toJSON: () => ({ endpoint: 'https://push.example.com/c' }) })
      const done = dispatch(w.handlers.pushsubscriptionchange!, { oldSubscription: oldSubscription() })
      await vi.advanceTimersByTimeAsync(12_000)
      await done
      expect([...w.stored.keys()]).toEqual(['/__tindra/pending-push-rotation/' + encodeURIComponent('https://push.example.com/a')])
      const parked = JSON.parse([...w.stored.values()][0]!)
      expect(parked.moves).toMatchObject([
        { endpoint: 'https://push.example.com/c', replaces: 'https://push.example.com/a', replaces_auth: 'secret-a' },
        { endpoint: 'https://push.example.com/c', replaces: 'https://push.example.com/old', replaces_auth: '-_8AAQIDBAUGBwgJCgsMDQ' },
      ])
    } finally {
      vi.useRealTimers()
    }
  })

  it('keeps every endpoint the server might know across three rotations', async () => {
    // A to B was sent and reached the server, only its answers were lost;
    // B to C was parked joined with it. Now C rotates to D, and the server
    // (which knows B) is back: the B move must still be among the tries.
    w.stored.set('/__tindra/pending-push-rotation/a', JSON.stringify({ moves: [
      { endpoint: 'https://push.example.com/old', replaces: 'https://push.example.com/a', replaces_auth: 'secret-a' },
      { endpoint: 'https://push.example.com/old', replaces: 'https://push.example.com/b', replaces_auth: 'secret-b' },
    ] }))
    w.fetchMock.mockResolvedValueOnce(serverConfig())
    w.fetchMock.mockResolvedValueOnce({ ok: false, status: 404 }) // A unknown
    w.fetchMock.mockResolvedValueOnce({ ok: true, status: 204 }) // B known
    w.self.registration.pushManager.subscribe.mockResolvedValue({ endpoint: 'https://push.example.com/d', toJSON: () => ({ endpoint: 'https://push.example.com/d' }) })
    await dispatch(w.handlers.pushsubscriptionchange!, { oldSubscription: oldSubscription() })
    const sent = w.fetchMock.mock.calls.slice(1).map(([, init]) => JSON.parse(init.body).replaces)
    expect(sent).toEqual(['https://push.example.com/a', 'https://push.example.com/b'])
    expect(w.stored.size).toBe(0)
  })

  it('skips the key request when the browser hands over the new subscription', async () => {
    w.fetchMock.mockResolvedValue({ ok: true, status: 204 })
    const fresh = { endpoint: 'https://push.example.com/handed', toJSON: () => ({ endpoint: 'https://push.example.com/handed' }) }
    await dispatch(w.handlers.pushsubscriptionchange!, { oldSubscription: oldSubscription(), newSubscription: fresh })
    expect(w.fetchMock.mock.calls.map(([url]) => url)).toEqual(['/api/push/rotate'])
  })

  describe('sending parked rotations when the app asks', () => {
    const ask = () => dispatch(w.handlers.message!, { data: { type: 'tindra:send-pending-rotations' } })
    const key = (k: string) => '/__tindra/pending-push-rotation/' + k

    it('sends each and forgets it once the server answered', async () => {
      w.stored.set(key('a'), JSON.stringify({ moves: [{ endpoint: 'https://push.example.com/b', replaces: 'https://push.example.com/a' }] }))
      w.stored.set(key('x'), JSON.stringify({ moves: [{ endpoint: 'https://push.example.com/y', replaces: 'https://push.example.com/x' }] }))
      w.stored.set('/unrelated', 'x')
      w.fetchMock.mockResolvedValue({ ok: false, status: 404 })
      await ask()
      expect(w.fetchMock).toHaveBeenCalledTimes(2)
      expect([...w.stored.keys()]).toEqual(['/unrelated'])
    })

    it('tries the next move on a 404 and keeps what is still owed', async () => {
      vi.useFakeTimers()
      try {
        w.stored.set(key('a'), JSON.stringify({ moves: [
          { endpoint: 'https://push.example.com/c', replaces: 'https://push.example.com/a' },
          { endpoint: 'https://push.example.com/c', replaces: 'https://push.example.com/b' },
        ] }))
        w.fetchMock.mockResolvedValueOnce({ ok: false, status: 404 })
        w.fetchMock.mockResolvedValue({ ok: false, status: 503 })
        const done = ask()
        await vi.advanceTimersByTimeAsync(12_000)
        await done
        expect(JSON.parse(w.stored.get(key('a'))!)).toEqual({ moves: [{ endpoint: 'https://push.example.com/c', replaces: 'https://push.example.com/b' }] })
      } finally {
        vi.useRealTimers()
      }
    })

    it('ignores other messages', async () => {
      w.stored.set(key('a'), JSON.stringify({ moves: [{ replaces: 'https://push.example.com/a' }] }))
      dispatch(w.handlers.message!, { data: { type: 'other' } })
      dispatch(w.handlers.message!, { data: null })
      await Promise.resolve()
      expect(w.fetchMock).not.toHaveBeenCalled()
    })

    it('runs one task at a time, so a rotation and a flush never race on a record', async () => {
      let release!: () => void
      w.fetchMock.mockImplementationOnce(() => new Promise(r => { release = () => r({ ok: true, status: 204 }) }))
      w.fetchMock.mockResolvedValue({ ok: true, status: 204 })
      w.stored.set(key('a'), JSON.stringify({ moves: [{ endpoint: 'https://push.example.com/b', replaces: 'https://push.example.com/a' }] }))
      const flush = ask()
      const rotation = dispatch(w.handlers.pushsubscriptionchange!, {
        oldSubscription: oldSubscription(),
        newSubscription: { endpoint: 'https://push.example.com/n', toJSON: () => ({ endpoint: 'https://push.example.com/n' }) },
      })
      await new Promise(r => setTimeout(r, 0))
      expect(w.fetchMock).toHaveBeenCalledTimes(1)
      release()
      await flush
      await rotation
      expect(w.fetchMock).toHaveBeenCalledTimes(2)
    })
  })
})
