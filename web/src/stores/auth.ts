import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { User } from '@/api/types'

export interface AppConfig {
  public_url: string
  require_mfa: boolean
}

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const ready = ref(false)
  const requireMfa = ref(true)
  // The startup /api/config response, reused by useConfig instead of a second fetch.
  const config = ref<AppConfig | null>(null)

  function setUser(u: User | null) {
    user.value = u
  }

  let inflightInit: Promise<void> | null = null

  // refresh reloads the user after sign-in without clearing ready, which would
  // hide the whole app until the next navigation and make the form flash.
  async function refresh() {
    try {
      const [meRes, cfgRes] = await Promise.all([
        fetch('/api/me', { credentials: 'include' }),
        fetch('/api/config'),
      ])
      if (meRes.ok) user.value = await meRes.json()
      if (cfgRes.ok) {
        config.value = await cfgRes.json()
        requireMfa.value = config.value?.require_mfa !== false
      }
    } catch { /* network error - treat as unauthenticated */ }
  }

  async function init() {
    if (ready.value) return
    if (inflightInit) return inflightInit
    inflightInit = (async () => {
      await refresh()
      ready.value = true
      inflightInit = null
    })()
    return inflightInit
  }

  return { user, ready, requireMfa, config, setUser, init, refresh }
})
