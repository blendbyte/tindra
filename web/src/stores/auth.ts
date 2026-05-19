import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { User } from '@/api/types'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const ready = ref(false)

  function setUser(u: User | null) {
    user.value = u
  }

  async function init() {
    if (ready.value) return
    try {
      const res = await fetch('/api/me', { credentials: 'include' })
      if (res.ok) user.value = await res.json()
    } catch { /* network error - treat as unauthenticated */ }
    ready.value = true
  }

  return { user, ready, setUser, init }
})
