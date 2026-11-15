import type { Router } from 'vue-router'

export const NAVIGATE_MESSAGE = 'tindra:navigate'

/** Browsers only run service workers (and so Web Push) in a secure context. */
export function serviceWorkerSupported(): boolean {
  return typeof window !== 'undefined' && window.isSecureContext && 'serviceWorker' in navigator
}

/**
 * Registers /sw.js and routes notification clicks inside the running app.
 * The worker has no fetch handler, so registering it never changes how
 * assets load.
 */
export function registerServiceWorker(router: Pick<Router, 'push'>): void {
  if (!serviceWorkerSupported()) return

  navigator.serviceWorker.addEventListener('message', (event: MessageEvent) => {
    const data = event.data as { type?: string; url?: string } | null
    if (data?.type !== NAVIGATE_MESSAGE || typeof data.url !== 'string') return
    let target: URL
    try {
      target = new URL(data.url, window.location.origin)
    } catch {
      return
    }
    if (target.origin !== window.location.origin) return
    void router.push(target.pathname + target.search + target.hash)
  })

  const register = () => {
    navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch(() => {
      // Push is optional; the app works without the worker.
    })
  }
  if (document.readyState === 'complete') register()
  else window.addEventListener('load', register, { once: true })
}
