// Tindra service worker: shows Web Push alerts and opens them on click.
// There is deliberately no fetch handler. Nothing is cached, so a deploy can
// never be hidden behind stale assets.

self.addEventListener('install', () => {
  self.skipWaiting()
})

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim())
})

function parsePush(event) {
  const fallback = { title: 'Tindra', body: '', url: '/', tag: undefined }
  if (!event.data) return fallback
  try {
    return { ...fallback, ...event.data.json() }
  } catch {
    return { ...fallback, body: event.data.text() }
  }
}

self.addEventListener('push', (event) => {
  const n = parsePush(event)
  // Browsers (Safari in particular) revoke push permission when a push does
  // not show a notification, so always show one.
  event.waitUntil(
    self.registration.showNotification(n.title, {
      body: n.body,
      tag: n.tag,
      renotify: Boolean(n.tag),
      icon: '/icons/icon-192.png',
      data: { url: n.url },
    }),
  )
})

// Only paths on this instance are opened; anything else falls back to "/".
function targetURL(raw) {
  try {
    const url = new URL(raw || '/', self.location.origin)
    return url.origin === self.location.origin ? url.href : self.location.origin + '/'
  } catch {
    return self.location.origin + '/'
  }
}

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const url = targetURL(event.notification.data && event.notification.data.url)
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
    const client = windows.find((c) => new URL(c.url).origin === self.location.origin)
    if (client) {
      // The open app routes in place instead of reloading (see registerServiceWorker.ts).
      client.postMessage({ type: 'tindra:navigate', url })
      await client.focus()
      return
    }
    await self.clients.openWindow(url)
  })())
})

// The push service rotated the subscription: subscribe again with the same
// key and tell the server, or this device silently stops getting alerts.
self.addEventListener('pushsubscriptionchange', (event) => {
  event.waitUntil((async () => {
    const res = await fetch('/api/push/config', { credentials: 'include' })
    if (!res.ok) return
    const { enabled, public_key: key } = await res.json()
    if (!enabled || !key) return
    const padding = '='.repeat((4 - (key.length % 4)) % 4)
    const raw = atob((key + padding).replace(/-/g, '+').replace(/_/g, '/'))
    const applicationServerKey = Uint8Array.from(raw, (c) => c.charCodeAt(0))
    const sub = await self.registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey })
    await fetch('/api/push/subscriptions', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(sub.toJSON()),
    })
    if (event.oldSubscription && event.oldSubscription.endpoint !== sub.endpoint) {
      await fetch('/api/push/subscriptions/unsubscribe', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ endpoint: event.oldSubscription.endpoint }),
      })
    }
  })())
})
