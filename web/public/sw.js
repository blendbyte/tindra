// Tindra service worker: shows Web Push alerts and opens them on click.
// There is deliberately no fetch handler. Nothing is cached, so a deploy can
// never be hidden behind stale assets.

self.addEventListener('install', () => {
  self.skipWaiting()
})

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim())
})

// Takes only string fields from the payload, so anything else (null, a
// number, a payload that is not an object) falls back instead of showing up.
function parsePush(event) {
  const n = { title: 'Tindra', body: '', url: '/', tag: undefined }
  if (!event.data) return n
  let data
  try {
    data = event.data.json()
  } catch {
    return { ...n, body: event.data.text() }
  }
  if (data && typeof data === 'object') {
    for (const key of ['title', 'body', 'url', 'tag']) {
      if (typeof data[key] === 'string' && data[key] !== '') n[key] = data[key]
    }
  }
  return n
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
    // Only a window running the app listens for the message; a tab showing
    // an API response on the same origin does not.
    const app = windows.filter((c) => {
      const u = new URL(c.url)
      return u.origin === self.location.origin && !u.pathname.startsWith('/api/')
    })
    const client = app.find((c) => c.focused) || app[0]
    if (client) {
      // The open app routes in place instead of reloading (see registerServiceWorker.ts).
      client.postMessage({ type: 'tindra:navigate', url })
      await client.focus()
      return
    }
    await self.clients.openWindow(url)
  })())
})

// The server's current VAPID key, which differs from the old subscription's
// when the key was changed. Public, so it works without a session.
async function serverKey() {
  try {
    const res = await fetch('/api/push/config', { credentials: 'omit' })
    if (!res.ok) return null
    const { enabled, public_key: key } = await res.json()
    if (!enabled || !key) return null
    const padding = '='.repeat((4 - (key.length % 4)) % 4)
    const raw = atob((key + padding).replace(/-/g, '+').replace(/_/g, '/'))
    return Uint8Array.from(raw, (c) => c.charCodeAt(0))
  } catch {
    return null
  }
}

function base64url(buffer) {
  let raw = ''
  for (const byte of new Uint8Array(buffer)) raw += String.fromCharCode(byte)
  return btoa(raw).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

// The push service rotated the subscription: subscribe again with the same
// key and move the device on the server to the new endpoint, or this device
// silently stops getting alerts. This can happen long after the session
// ended, so the request carries no cookie; the old subscription's auth
// secret proves it is this device. The server only moves a device it still
// lists, so one removed elsewhere stays removed. Without the old
// subscription there is nothing to move; the device then shows as off in
// the app until it is turned on again.
self.addEventListener('pushsubscriptionchange', (event) => {
  event.waitUntil(serial(async () => {
    const old = event.oldSubscription
    const oldAuth = old && typeof old.getKey === 'function' ? old.getKey('auth') : null
    if (!old || !oldAuth) return
    let sub = event.newSubscription
    if (!sub) {
      const key = await serverKey() || (old.options && old.options.applicationServerKey)
      if (key) sub = await self.registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key })
    }
    if (!sub) return
    const cache = await caches.open(PUSH_CACHE)
    const direct = { ...sub.toJSON(), replaces: old.endpoint, replaces_auth: base64url(oldAuth) }
    // A rotation still waiting for the app (A to B) and this one (B to C)
    // make one move, A to C: the server only knows A. Unless the parked move
    // did reach the server after all (only its answer was lost): then the
    // server knows B. So every endpoint the server might know is kept, oldest
    // first, and each is tried in turn until the server knows one.
    let moves = [direct]
    let parkedKey = null
    for (const req of await cache.keys()) {
      try {
        const parked = JSON.parse(await (await cache.match(req)).text())
        if (Array.isArray(parked.moves) && parked.moves.length && parked.moves[0].endpoint === old.endpoint) {
          moves = [...parked.moves.map((m) => ({ ...direct, replaces: m.replaces, replaces_auth: m.replaces_auth })), direct]
          parkedKey = req
        }
      } catch {
        /* not ours */
      }
    }
    const owed = await sendMoves(moves, postRotation)
    if (parkedKey) await cache.delete(parkedKey)
    if (!owed) return
    // The event does not fire again, so keep what is left for the app to
    // send the next time it is opened (see usePushNotifications.ts).
    await cache.put(PENDING_ROTATION_PREFIX + encodeURIComponent(owed[0].replaces), new Response(JSON.stringify({ moves: owed })))
  }))
})

// The app asks for the parked rotations to be sent when it starts.
self.addEventListener('message', (event) => {
  if (event.data && event.data.type === SEND_PENDING_ROTATIONS) event.waitUntil(serial(sendParkedRotations))
})

const SEND_PENDING_ROTATIONS = 'tindra:send-pending-rotations'

// Rotation work runs one task at a time, so a parked record is never read
// by one task while another rewrites it.
let rotationQueue = Promise.resolve()
function serial(task) {
  const run = rotationQueue.then(task, task)
  rotationQueue = run.catch(() => {})
  return run
}

async function sendParkedRotations() {
  const cache = await caches.open(PUSH_CACHE)
  for (const req of await cache.keys()) {
    if (!new URL(req.url, self.location.origin).pathname.startsWith(PENDING_ROTATION_PREFIX)) continue
    let moves
    try {
      moves = JSON.parse(await (await cache.match(req)).text()).moves
    } catch {
      moves = null
    }
    const owed = Array.isArray(moves) ? await sendMoves(moves, postRotation) : null
    if (owed) await cache.put(req, new Response(JSON.stringify({ moves: owed })))
    else await cache.delete(req)
  }
}

// Tries each move until the server knows its old endpoint (anything but a
// 404). It returns the moves still owed when the server could not be
// reached, or null when nothing is left to do.
async function sendMoves(moves, post) {
  for (let i = 0; i < moves.length; i++) {
    const status = await post(JSON.stringify(moves[i]))
    if (status === OWED) return moves.slice(i)
    if (status !== 404) return null
  }
  return null
}

// Where rotations that could not be sent wait for the app, one per device.
// This is all the worker stores; it never caches responses.
const PUSH_CACHE = 'tindra-push'
const PENDING_ROTATION_PREFIX = '/__tindra/pending-push-rotation/'
const ROTATION_RETRY_DELAYS = [2000, 10000]

// Answers after which sending again cannot help: the device was removed, the
// secret is wrong, or the request is malformed. Anything else (a server that
// is down, busy or slow) leaves the rotation owed.
const FINAL_ROTATION_STATUSES = [400, 401, 403, 404, 409, 410, 422]

const OWED = 'owed'

// Sends a rotation, retrying while the server is unreachable or busy. It
// returns the final status, or OWED when the rotation could not be sent.
async function postRotation(body) {
  for (let attempt = 0; ; attempt++) {
    try {
      const res = await fetch('/api/push/rotate', {
        method: 'POST',
        credentials: 'omit',
        headers: { 'Content-Type': 'application/json' },
        body,
      })
      if (res.ok || FINAL_ROTATION_STATUSES.includes(res.status)) return res.status
    } catch {
      /* offline: retry */
    }
    if (attempt >= ROTATION_RETRY_DELAYS.length) return OWED
    await new Promise((resolve) => setTimeout(resolve, ROTATION_RETRY_DELAYS[attempt]))
  }
}
