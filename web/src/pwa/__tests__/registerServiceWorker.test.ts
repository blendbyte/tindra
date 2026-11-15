import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { registerServiceWorker, serviceWorkerSupported, NAVIGATE_MESSAGE } from '../registerServiceWorker'

function installServiceWorker() {
  const target = new EventTarget()
  const sw = Object.assign(target, { register: vi.fn(() => Promise.resolve({})) })
  Object.defineProperty(navigator, 'serviceWorker', { value: sw, configurable: true })
  return sw
}

describe('registerServiceWorker', () => {
  let secure: PropertyDescriptor | undefined

  beforeEach(() => {
    secure = Object.getOwnPropertyDescriptor(window, 'isSecureContext')
    Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })
  })

  afterEach(() => {
    if (secure) Object.defineProperty(window, 'isSecureContext', secure)
    else delete (window as any).isSecureContext
    delete (navigator as any).serviceWorker
  })

  it('does nothing without service worker support or a secure context', () => {
    expect(serviceWorkerSupported()).toBe(false)
    const router = { push: vi.fn() }
    expect(() => registerServiceWorker(router)).not.toThrow()

    const sw = installServiceWorker()
    Object.defineProperty(window, 'isSecureContext', { value: false, configurable: true })
    registerServiceWorker(router)
    expect(sw.register).not.toHaveBeenCalled()
  })

  it('registers /sw.js at the root scope once the page has loaded', () => {
    const sw = installServiceWorker()
    registerServiceWorker({ push: vi.fn() })
    if (document.readyState !== 'complete') window.dispatchEvent(new Event('load'))
    expect(sw.register).toHaveBeenCalledWith('/sw.js', { scope: '/' })
  })

  it('routes notification clicks to same-origin paths only', () => {
    const sw = installServiceWorker()
    const router = { push: vi.fn(() => Promise.resolve()) }
    registerServiceWorker(router)
    const send = (data: unknown) => sw.dispatchEvent(new MessageEvent('message', { data }))

    send({ type: NAVIGATE_MESSAGE, url: `${window.location.origin}/issues/abc?x=1#h` })
    send({ type: NAVIGATE_MESSAGE, url: 'https://evil.example.com/issues' })
    send({ type: 'other', url: '/issues' })
    send({ type: NAVIGATE_MESSAGE, url: 42 })
    send(null)

    expect(router.push).toHaveBeenCalledTimes(1)
    expect(router.push).toHaveBeenCalledWith('/issues/abc?x=1#h')
  })
})
