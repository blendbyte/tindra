import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'
import type { AppUser } from '@/api/types'

const SS_KEY = 'tindra:appUser'

function readStored(): AppUser | null {
  try {
    const raw = sessionStorage.getItem(SS_KEY)
    if (!raw) return null
    const u = JSON.parse(raw) as AppUser
    if (!u || typeof u.identity !== 'string' || !u.identity) return null
    return u
  } catch {
    return null
  }
}

/** Identity from `?user=`, used for the first fetch before the store hydrates. */
export function routeUserIdentity(query: Record<string, unknown> | { user?: unknown }): string {
  const v = query.user
  if (typeof v === 'string') return v
  if (Array.isArray(v) && typeof v[0] === 'string') return v[0]
  return ''
}

export function appUserIdentity(u: {
  id?: string | number | null
  username?: string | null
  email?: string | null
}): string {
  const id = u.id != null ? String(u.id).trim() : ''
  if (id) return id
  const username = (u.username ?? '').trim()
  if (username) return username
  return (u.email ?? '').trim()
}

export function appUserLabel(u: AppUser | null | undefined): string {
  if (!u) return ''
  return u.name || u.username || u.email || u.user_id || u.identity
}

export function appUserInitial(u: AppUser | null | undefined): string {
  const label = appUserLabel(u)
  return label ? label[0]!.toUpperCase() : '?'
}

export const useAppUserStore = defineStore('appUser', () => {
  const selected = ref<AppUser | null>(readStored())

  watch(selected, (u) => {
    try {
      if (u) sessionStorage.setItem(SS_KEY, JSON.stringify(u))
      else sessionStorage.removeItem(SS_KEY)
    } catch {
      /* ignore */
    }
  })

  const identity = computed(() => selected.value?.identity ?? '')
  const label = computed(() => appUserLabel(selected.value))
  const initial = computed(() => appUserInitial(selected.value))

  function select(u: AppUser | null) {
    selected.value = u
  }

  function clear() {
    selected.value = null
  }

  return { selected, identity, label, initial, select, clear }
})
