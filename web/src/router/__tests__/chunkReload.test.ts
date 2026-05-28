import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRouter, createWebHistory } from 'vue-router'
import { defineComponent } from 'vue'

// Helpers to build a minimal test router with the same onError handler
// that lives in router/index.ts, so changes there should be mirrored here.
function buildChunkErrorRouter() {
  const r = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/', component: defineComponent({ template: '<div />' }) },
    ],
  })
  r.onError((error, to) => {
    const isChunkError =
      error.message?.includes('Failed to fetch dynamically imported module') ||
      error.message?.includes('Importing a module script failed') ||
      error.message?.includes('Unable to preload CSS for') ||
      error.name === 'ChunkLoadError'
    if (isChunkError) {
      window.location.assign(to.fullPath)
    }
  })
  return r
}

describe('chunk reload — vite:preloadError handler (main.ts)', () => {
  let reloadMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    reloadMock = vi.fn()
    vi.stubGlobal('location', { reload: reloadMock, assign: vi.fn(), href: '/' })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('calls window.location.reload() when vite:preloadError fires', () => {
    // Reproduce the handler registered in main.ts
    const handler = (event: Event) => {
      event.preventDefault()
      window.location.reload()
    }
    window.addEventListener('vite:preloadError', handler as EventListener)

    window.dispatchEvent(new Event('vite:preloadError', { cancelable: true }))

    expect(reloadMock).toHaveBeenCalledOnce()

    window.removeEventListener('vite:preloadError', handler as EventListener)
  })

  it('calls event.preventDefault() to suppress the Vite re-throw', () => {
    const handler = (event: Event) => {
      event.preventDefault()
      window.location.reload()
    }
    window.addEventListener('vite:preloadError', handler as EventListener)

    const event = new Event('vite:preloadError', { cancelable: true })
    window.dispatchEvent(event)

    expect(event.defaultPrevented).toBe(true)

    window.removeEventListener('vite:preloadError', handler as EventListener)
  })
})

describe('chunk reload — router.onError handler (router/index.ts)', () => {
  let assignMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    assignMock = vi.fn()
    vi.stubGlobal('location', { assign: assignMock, reload: vi.fn(), href: '/' })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('calls window.location.assign(to.fullPath) on "Failed to fetch dynamically imported module"', async () => {
    const router = buildChunkErrorRouter()
    router.addRoute({
      path: '/lazy-fail',
      component: () => Promise.reject(new TypeError('Failed to fetch dynamically imported module: /assets/Page-abc.js')),
    })

    await router.push('/lazy-fail').catch(() => {})

    expect(assignMock).toHaveBeenCalledWith('/lazy-fail')
  })

  it('calls window.location.assign on "Importing a module script failed"', async () => {
    const router = buildChunkErrorRouter()
    router.addRoute({
      path: '/safari-fail',
      component: () => Promise.reject(new TypeError('Importing a module script failed')),
    })

    await router.push('/safari-fail').catch(() => {})

    expect(assignMock).toHaveBeenCalledWith('/safari-fail')
  })

  it('calls window.location.assign on "Unable to preload CSS for"', async () => {
    const router = buildChunkErrorRouter()
    router.addRoute({
      path: '/css-fail',
      component: () => Promise.reject(new Error('Unable to preload CSS for /assets/Page-abc.css')),
    })

    await router.push('/css-fail').catch(() => {})

    expect(assignMock).toHaveBeenCalledWith('/css-fail')
  })

  it('calls window.location.assign when error.name is ChunkLoadError', async () => {
    const router = buildChunkErrorRouter()
    const err = new Error('chunk load failed')
    err.name = 'ChunkLoadError'
    router.addRoute({
      path: '/chunk-name-fail',
      component: () => Promise.reject(err),
    })

    await router.push('/chunk-name-fail').catch(() => {})

    expect(assignMock).toHaveBeenCalledWith('/chunk-name-fail')
  })

  it('does NOT call window.location.assign for unrelated errors', async () => {
    const router = buildChunkErrorRouter()
    router.addRoute({
      path: '/other-fail',
      component: () => Promise.reject(new Error('some unrelated runtime error')),
    })

    await router.push('/other-fail').catch(() => {})

    expect(assignMock).not.toHaveBeenCalled()
  })
})
