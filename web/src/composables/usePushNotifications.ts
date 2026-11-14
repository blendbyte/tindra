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
 * Removes this browser's push subscription, on the server and in the
 * browser. Used on logout so the next person on this device does not get
 * the previous user's alerts. Never throws.
 */
export async function forgetPushOnThisDevice(): Promise<void> {
  try {
    const sub = await currentSubscription()
    if (!sub) return
    await apiFetch('/api/push/subscriptions/unsubscribe', {
      method: 'POST',
      body: JSON.stringify({ endpoint: sub.endpoint }),
    }).catch(() => {})
    await sub.unsubscribe()
  } catch {
    /* best effort */
  }
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
    const sub = await currentSubscription().catch(() => null)
    currentEndpoint.value = sub?.endpoint ?? null
    // Keep the server in step when this browser is subscribed under the
    // current key but the server lost it (expired, removed elsewhere).
    if (sub && cfg?.enabled && !devices.value.some(d => d.endpoint === sub.endpoint)
      && sameKey(sub.options?.applicationServerKey, urlBase64ToUint8Array(cfg.public_key))
      && permission.value === 'granted') {
      await save(sub).catch(() => {})
    }
  }

  async function save(sub: PushSubscription) {
    const saved = await apiFetch<PushSubscriptionInfo>('/api/push/subscriptions', {
      method: 'POST',
      body: JSON.stringify(sub.toJSON()),
    })
    devices.value = [saved, ...devices.value.filter(d => d.id !== saved.id)]
    currentEndpoint.value = sub.endpoint
    return saved
  }

  /** Asks for permission and registers this device. Call from a click handler. */
  async function enable(): Promise<PushSubscriptionInfo | null> {
    busy.value = true
    try {
      if (!config.value) config.value = await apiFetch<PushConfig>('/api/push/config')
      if (!config.value?.enabled) return null
      permission.value = await Notification.requestPermission()
      if (permission.value !== 'granted') return null

      const reg = await navigator.serviceWorker.ready
      const key = urlBase64ToUint8Array(config.value.public_key)
      let sub = await reg.pushManager.getSubscription()
      // A subscription made with another key (the server's VAPID key
      // changed) would be rejected by the push service.
      if (sub && !sameKey(sub.options?.applicationServerKey, key)) {
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
        await apiFetch('/api/push/subscriptions/unsubscribe', {
          method: 'POST',
          body: JSON.stringify({ endpoint: sub.endpoint }),
        })
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
