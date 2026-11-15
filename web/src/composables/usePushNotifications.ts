import { computed, ref } from 'vue'
import { apiFetch } from '@/api/client'
import type { PushConfig, PushSubscriptionInfo } from '@/api/types'
import { serviceWorkerSupported } from '@/pwa/registerServiceWorker'

/** Why push cannot be turned on in this browser, or null when it can. */
export type PushBlocker = 'insecure' | 'unsupported' | 'ios-install' | 'disabled' | 'denied' | null

export function urlBase64ToUint8Array(base64: string): Uint8Array<ArrayBuffer> {
  const padding = '='.repeat((4 - (base64.length % 4)) % 4)
  const raw = atob((base64 + padding).replace(/-/g, '+').replace(/_/g, '/'))
  const out = new Uint8Array(new ArrayBuffer(raw.length))
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

function sameKey(a: ArrayBuffer | null | undefined, b: Uint8Array): boolean {
  if (!a) return false
  const x = new Uint8Array(a)
  return x.length === b.length && x.every((v, i) => v === b[i])
}

function isIOS(): boolean {
  const ua = navigator.userAgent
  // iPadOS reports itself as a Mac with touch.
  return /iPhone|iPad|iPod/.test(ua) || (ua.includes('Macintosh') && navigator.maxTouchPoints > 1)
}

function isStandalone(): boolean {
  return window.matchMedia?.('(display-mode: standalone)').matches || (navigator as any).standalone === true
}

function pushSupported(): boolean {
  return serviceWorkerSupported() && 'PushManager' in window && 'Notification' in window
}

async function currentSubscription(): Promise<PushSubscription | null> {
  if (!pushSupported()) return null
  const reg = await navigator.serviceWorker.getRegistration('/')
  return (await reg?.pushManager.getSubscription()) ?? null
}

/**
 * This browser's push endpoint, or null when it has none or the browser
 * takes longer than timeoutMs to say. Never throws.
 */
export async function pushEndpointOnThisDevice(timeoutMs = 2000): Promise<string | null> {
  const endpoint = currentSubscription().then(sub => sub?.endpoint ?? null, () => null)
  return within(endpoint, timeoutMs, null)
}

/** Must match SEND_PENDING_ROTATIONS in public/sw.js. */
export const SEND_PENDING_ROTATIONS = 'tindra:send-pending-rotations'

/**
 * Asks the service worker to send the subscription rotations it could not
 * deliver (the server was down when the push service rotated). The worker
 * keeps and sends them itself; the app calls this once on start. Never
 * throws.
 */
export async function sendPendingRotation(): Promise<void> {
  try {
    if (!serviceWorkerSupported()) return
    const reg = await navigator.serviceWorker.getRegistration('/')
    reg?.active?.postMessage({ type: SEND_PENDING_ROTATIONS })
  } catch {
    /* next start */
  }
}

/** The label for this device when the server cannot tell from the User-Agent. */
function clientDeviceLabel(): string | undefined {
  // iPadOS reports itself as a Mac.
  const ua = navigator.userAgent
  return ua.includes('Macintosh') && navigator.maxTouchPoints > 1 ? 'Safari on iPadOS' : undefined
}

/** How long enable() waits for the service worker before giving up. */
export const SERVICE_WORKER_TIMEOUT_MS = 10_000

// serviceWorker.ready never settles when registration failed (blocked by an
// extension, sw.js not served), which would leave the switch busy forever.
async function serviceWorkerReady(): Promise<ServiceWorkerRegistration> {
  const reg = await within<ServiceWorkerRegistration | null>(navigator.serviceWorker.ready, SERVICE_WORKER_TIMEOUT_MS, null)
  if (!reg) throw new Error('The background worker for notifications did not start. Reload the page and try again.')
  return reg
}

function unsubscribeOnServer(endpoint: string) {
  return apiFetch('/api/push/subscriptions/unsubscribe', {
    method: 'POST',
    body: JSON.stringify({ endpoint }),
  })
}

/** Resolves to fallback when p takes longer than ms. */
function within<T>(p: Promise<T>, ms: number, fallback: T): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  const late = new Promise<T>(resolve => { timer = setTimeout(() => resolve(fallback), ms) })
  return Promise.race([p, late]).finally(() => clearTimeout(timer))
}

/**
 * Drops this browser's push subscription, so the next person on this device
 * does not get the signed-out user's alerts. The server side goes with the
 * logout request (see pushEndpointOnThisDevice). Capped at timeoutMs, so
 * signing out never hangs on a stalled push service. Never throws.
 */
export async function forgetPushOnThisDevice(timeoutMs = 3000): Promise<void> {
  const drop = (async () => {
    try {
      await (await currentSubscription())?.unsubscribe()
    } catch {
      /* best effort */
    }
  })()
  await within(drop, timeoutMs, undefined)
}

export function usePushNotifications() {
  const config = ref<PushConfig | null>(null)
  const devices = ref<PushSubscriptionInfo[]>([])
  const currentEndpoint = ref<string | null>(null)
  const permission = ref<NotificationPermission>(
    typeof Notification !== 'undefined' ? Notification.permission : 'default',
  )
  const busy = ref(false)

  const blocker = computed<PushBlocker>(() => {
    if (typeof window !== 'undefined' && !window.isSecureContext) return 'insecure'
    // iOS only exposes Web Push to apps added to the home screen.
    if (isIOS() && !isStandalone()) return 'ios-install'
    if (!pushSupported()) return 'unsupported'
    if (config.value && !config.value.enabled) return 'disabled'
    if (permission.value === 'denied') return 'denied'
    return null
  })

  const enabledHere = computed(
    () => !!currentEndpoint.value && devices.value.some(d => d.endpoint === currentEndpoint.value),
  )

  async function refresh() {
    const [cfg, list] = await Promise.all([
      apiFetch<PushConfig>('/api/push/config'),
      apiFetch<{ subscriptions: PushSubscriptionInfo[] }>('/api/push/subscriptions'),
    ])
    config.value = cfg
    devices.value = list?.subscriptions ?? []
    // A browser subscription the server does not list (removed from another
    // device, or made while someone else was signed in) shows as off. It is
    // never registered again without the user turning it on here.
    const sub = await currentSubscription().catch(() => null)
    // A subscription made with another key (the VAPID key changed) is
    // rejected by its push service, so it shows as off: turning it on
    // subscribes again with the current key.
    const current = sub && cfg?.enabled && sameKey(sub.options?.applicationServerKey, urlBase64ToUint8Array(cfg.public_key))
    currentEndpoint.value = current ? sub.endpoint : null
  }

  async function save(sub: PushSubscription) {
    const saved = await apiFetch<PushSubscriptionInfo>('/api/push/subscriptions', {
      method: 'POST',
      body: JSON.stringify({ ...sub.toJSON(), label: clientDeviceLabel() }),
    })
    // apiFetch resolves to nothing when the session ended (it is on its way
    // to the login page then).
    if (!saved?.id) throw new Error('Could not register this device. Sign in again and retry.')
    devices.value = [saved, ...devices.value.filter(d => d.id !== saved.id)]
    currentEndpoint.value = sub.endpoint
    return saved
  }

  /** Asks for permission and registers this device. Call from a click handler. */
  async function enable(): Promise<PushSubscriptionInfo | null> {
    busy.value = true
    try {
      // Ask first: browsers only show the prompt while the click that
      // started this is recent, and a network request can outlast that.
      permission.value = await Notification.requestPermission()
      if (permission.value !== 'granted') return null
      if (!config.value) config.value = await apiFetch<PushConfig>('/api/push/config')
      if (!config.value?.enabled) return null

      const reg = await serviceWorkerReady()
      const key = urlBase64ToUint8Array(config.value.public_key)
      let sub = await reg.pushManager.getSubscription()
      // A subscription made with another key (the server's VAPID key
      // changed) would be rejected by the push service. Its old endpoint
      // goes on the server too, or it would linger as a second device.
      if (sub && !sameKey(sub.options?.applicationServerKey, key)) {
        const stale = sub.endpoint
        await unsubscribeOnServer(stale).catch(() => {})
        devices.value = devices.value.filter(d => d.endpoint !== stale)
        await sub.unsubscribe()
        sub = null
      }
      sub ??= await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key })
      return await save(sub)
    } finally {
      busy.value = false
    }
  }

  /** Unregisters this device. */
  async function disable() {
    busy.value = true
    try {
      const sub = await currentSubscription()
      if (sub) {
        await unsubscribeOnServer(sub.endpoint)
        await sub.unsubscribe()
        devices.value = devices.value.filter(d => d.endpoint !== sub.endpoint)
      }
      currentEndpoint.value = null
    } finally {
      busy.value = false
    }
  }

  async function remove(id: string) {
    await apiFetch(`/api/push/subscriptions/${encodeURIComponent(id)}`, { method: 'DELETE' })
    const removed = devices.value.find(d => d.id === id)
    devices.value = devices.value.filter(d => d.id !== id)
    // Removing this device from the list also unsubscribes the browser.
    if (removed && removed.endpoint === currentEndpoint.value) {
      const sub = await currentSubscription().catch(() => null)
      await sub?.unsubscribe().catch(() => {})
      currentEndpoint.value = null
    }
  }

  async function sendTest(id: string) {
    await apiFetch(`/api/push/subscriptions/${encodeURIComponent(id)}/test`, { method: 'POST' })
  }

  return { config, devices, currentEndpoint, permission, busy, blocker, enabledHere, refresh, enable, disable, remove, sendTest }
}
