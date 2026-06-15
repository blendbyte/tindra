import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { User } from '@/api/types'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const ready = ref(false)
  const requireMfa = ref(true)

  function setUser(u: User | null) {
    user.value = u
  }

  async function init() {
    if (ready.value) return
    try {
      const [meRes, cfgRes] = await Promise.all([
        fetch('/api/me', { credentials: 'include' }),
        fetch('/api/config'),
      ])
      if (meRes.ok) user.value = await meRes.json()
      if (cfgRes.ok) {
        const cfg = await cfgRes.json()
        requireMfa.value = cfg.require_mfa !== false
      }
    } catch { /* network error - treat as unauthenticated */ }
    ready.value = true
  }

  return { user, ready, requireMfa, setUser, init }
})
