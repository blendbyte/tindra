import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

vi.mock('@/api/client', () => ({ apiFetch: vi.fn() }))

import { apiFetch } from '@/api/client'
import { usePushNotifications, forgetPushOnThisDevice, urlBase64ToUint8Array } from '../usePushNotifications'

// A 65-byte key, base64url, like a VAPID public key.
const KEY = 'BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8'
const OTHER_KEY = 'BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4'

function fakeSubscription(endpoint: string, key = KEY) {
  return {
    endpoint,
    options: { applicationServerKey: urlBase64ToUint8Array(key).buffer },
    toJSON: () => ({ endpoint, keys: { p256dh: 'p', auth: 'a' } }),
    unsubscribe: vi.fn(() => Promise.resolve(true)),
  }
}

let current: ReturnType<typeof fakeSubscription> | null
let permission: NotificationPermission
const pushManager = {
  getSubscription: vi.fn(() => Promise.resolve(current)),
  subscribe: vi.fn(() => {
    current = fakeSubscription('https://push.example.com/new')
    return Promise.resolve(current)
  }),
}

const saved = (endpoint: string, id = 'dev-1') => ({ id, endpoint, label: 'Firefox on Linux', failure_count: 0, last_success_at: null, created_at: '2026-11-14T00:00:00Z' })

function mockServer({ enabled = true, devices = [] as ReturnType<typeof saved>[] } = {}) {
  vi.mocked(apiFetch).mockImplementation((async (path: string, init?: RequestInit) => {
    if (path === '/api/push/config') return { enabled, public_key: enabled ? KEY : '' }
    if (path === '/api/push/subscriptions' && !init?.method) return { subscriptions: devices }
    if (path === '/api/push/subscriptions' && init?.method === 'POST') {
      return saved(JSON.parse(String(init.body)).endpoint, 'dev-new')
    }
    return undefined
  }) as any)
}

function setUserAgent(ua: string) {
  Object.defineProperty(navigator, 'userAgent', { value: ua, configurable: true })
}

beforeEach(() => {
  vi.clearAllMocks()
  current = null
  permission = 'default'
  Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })
  const registration = { pushManager }
  Object.defineProperty(navigator, 'serviceWorker', {
    value: { ready: Promise.resolve(registration), getRegistration: vi.fn(() => Promise.resolve(registration)) },
    configurable: true,
  })
  ;(window as any).PushManager = function PushManager() {}
  ;(window as any).Notification = {
    get permission() { return permission },
    requestPermission: vi.fn(() => Promise.resolve(permission === 'default' ? 'granted' : permission)),
  }
  setUserAgent('Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0')
})

afterEach(() => {
  delete (navigator as any).serviceWorker
  delete (window as any).PushManager
  delete (window as any).Notification
  delete (window as any).isSecureContext
  delete (navigator as any).userAgent
})

describe('urlBase64ToUint8Array', () => {
  it('decodes unpadded base64url', () => {
    const bytes = urlBase64ToUint8Array(KEY)
    expect(bytes.length).toBe(65)
    expect(bytes[0]).toBe(4)
    expect(Array.from(urlBase64ToUint8Array('-_8'))).toEqual([251, 255])
  })
})

describe('usePushNotifications', () => {
  it('subscribes with the server key and registers the device', async () => {
    mockServer()
    const push = usePushNotifications()
    await push.refresh()
    expect(push.blocker.value).toBeNull()
    expect(push.enabledHere.value).toBe(false)

    const device = await push.enable()
    expect(device?.id).toBe('dev-new')
    const opts = (pushManager.subscribe.mock.calls[0] as any)[0]
    expect(opts.userVisibleOnly).toBe(true)
    expect(Array.from(opts.applicationServerKey)).toEqual(Array.from(urlBase64ToUint8Array(KEY)))
    expect(vi.mocked(apiFetch)).toHaveBeenCalledWith('/api/push/subscriptions', expect.objectContaining({ method: 'POST' }))
    expect(push.enabledHere.value).toBe(true)
    expect(push.busy.value).toBe(false)
  })

  it('replaces a subscription made with an old key', async () => {
    mockServer()
    const stale = fakeSubscription('https://push.example.com/old', OTHER_KEY)
    current = stale
    const push = usePushNotifications()
    await push.enable()
    expect(stale.unsubscribe).toHaveBeenCalled()
    expect(pushManager.subscribe).toHaveBeenCalled()
    expect(push.currentEndpoint.value).toBe('https://push.example.com/new')
  })

  it('reuses a subscription made with the current key', async () => {
    mockServer()
    current = fakeSubscription('https://push.example.com/same')
    const push = usePushNotifications()
    await push.enable()
    expect(pushManager.subscribe).not.toHaveBeenCalled()
    expect(push.currentEndpoint.value).toBe('https://push.example.com/same')
  })

  it('stops when permission is denied or push is off on the server', async () => {
    mockServer()
    permission = 'denied'
    const push = usePushNotifications()
    expect(await push.enable()).toBeNull()
    expect(pushManager.subscribe).not.toHaveBeenCalled()
    expect(push.blocker.value).toBe('denied')

    permission = 'default'
    mockServer({ enabled: false })
    const off = usePushNotifications()
    await off.refresh()
    expect(off.blocker.value).toBe('disabled')
    expect(await off.enable()).toBeNull()
  })

  it('re-registers a subscribed browser the server forgot', async () => {
    mockServer({ devices: [] })
    permission = 'granted'
    current = fakeSubscription('https://push.example.com/lost')
    const push = usePushNotifications()
    await push.refresh()
    expect(push.devices.value.map(d => d.endpoint)).toEqual(['https://push.example.com/lost'])
    expect(push.enabledHere.value).toBe(true)
  })

  it('disables this device and removes devices', async () => {
    current = fakeSubscription('https://push.example.com/here')
    const here = current
    mockServer({ devices: [saved('https://push.example.com/here', 'd1'), saved('https://push.example.com/phone', 'd2')] })
    const push = usePushNotifications()
    await push.refresh()
    expect(push.enabledHere.value).toBe(true)

    await push.remove('d2')
    expect(vi.mocked(apiFetch)).toHaveBeenCalledWith('/api/push/subscriptions/d2', { method: 'DELETE' })
    expect(here.unsubscribe).not.toHaveBeenCalled()

    await push.disable()
    expect(vi.mocked(apiFetch)).toHaveBeenCalledWith('/api/push/subscriptions/unsubscribe', {
      method: 'POST', body: JSON.stringify({ endpoint: 'https://push.example.com/here' }),
    })
    expect(here.unsubscribe).toHaveBeenCalled()
    expect(push.devices.value).toEqual([])
    expect(push.enabledHere.value).toBe(false)
  })

  it('removing this device from the list also unsubscribes the browser', async () => {
    current = fakeSubscription('https://push.example.com/here')
    const here = current
    mockServer({ devices: [saved('https://push.example.com/here', 'd1')] })
    const push = usePushNotifications()
    await push.refresh()
    await push.remove('d1')
    expect(here.unsubscribe).toHaveBeenCalled()
    expect(push.currentEndpoint.value).toBeNull()
  })

  it('sends a test notification', async () => {
    mockServer()
    await usePushNotifications().sendTest('d 1')
    expect(vi.mocked(apiFetch)).toHaveBeenCalledWith('/api/push/subscriptions/d%201/test', { method: 'POST' })
  })

  it('explains the iOS home screen requirement', () => {
    setUserAgent('Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1')
    window.matchMedia = vi.fn(() => ({ matches: false })) as any
    expect(usePushNotifications().blocker.value).toBe('ios-install')
    window.matchMedia = vi.fn(() => ({ matches: true })) as any
    expect(usePushNotifications().blocker.value).toBeNull()
  })

  it('reports insecure and unsupported browsers', () => {
    delete (window as any).PushManager
    expect(usePushNotifications().blocker.value).toBe('unsupported')
    Object.defineProperty(window, 'isSecureContext', { value: false, configurable: true })
    expect(usePushNotifications().blocker.value).toBe('insecure')
  })
})

describe('forgetPushOnThisDevice', () => {
  it('unsubscribes on the server and in the browser', async () => {
    mockServer()
    current = fakeSubscription('https://push.example.com/bye')
    const bye = current
    await forgetPushOnThisDevice()
    expect(vi.mocked(apiFetch)).toHaveBeenCalledWith('/api/push/subscriptions/unsubscribe', {
      method: 'POST', body: JSON.stringify({ endpoint: 'https://push.example.com/bye' }),
    })
    expect(bye.unsubscribe).toHaveBeenCalled()
  })

  it('never throws', async () => {
    vi.mocked(apiFetch).mockRejectedValue(new Error('offline'))
    current = fakeSubscription('https://push.example.com/x')
    current.unsubscribe.mockRejectedValue(new Error('nope'))
    await expect(forgetPushOnThisDevice()).resolves.toBeUndefined()

    delete (navigator as any).serviceWorker
    await expect(forgetPushOnThisDevice()).resolves.toBeUndefined()
  })
})
