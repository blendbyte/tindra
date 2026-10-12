<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import type { AppUser, Event as TindraEvent, Issue } from '@/api/types'
import { useAppUserStore, appUserIdentity } from '@/stores/appUser'

// The application user the current event was reported for. A user with an
// identity links to their issues, traces and logs.
const props = defineProps<{ issue: Issue; event?: TindraEvent | null }>()

const router = useRouter()
const appUser = useAppUserStore()

const eventUser = computed(() => {
  const u = props.event?.payload?.user as Record<string, unknown> | undefined
  if (!u || typeof u !== 'object') return null
  return {
    id:         u.id         != null ? String(u.id)         : null,
    username:   u.username   != null ? String(u.username)   : null,
    email:      u.email      != null ? String(u.email)      : null,
    name:       u.name       != null ? String(u.name)       : null,
    ip_address: u.ip_address != null ? String(u.ip_address) : null,
  }
})

const eventUserInitial = computed(() => {
  const u = eventUser.value
  if (!u) return '?'
  const label = u.name || u.username || u.email || u.id || '?'
  return String(label)[0].toUpperCase()
})

const eventUserLabel = computed(() => {
  const u = eventUser.value
  if (!u) return null
  return u.name || u.username || u.email || u.id
})

function eventUserAsAppUser(): AppUser | null {
  const u = eventUser.value
  if (!u) return null
  const identity = appUserIdentity({ id: u.id, username: u.username, email: u.email })
  if (!identity) return null
  return {
    identity,
    user_id: u.id,
    username: u.username,
    email: u.email,
    name: u.name,
    last_seen: '',
    project_id: props.issue.project_id ?? '',
  }
}

const lensUser = computed(() => eventUserAsAppUser())

function lensTo(path: string) {
  const u = lensUser.value
  if (!u) return
  appUser.select(u)
  router.push({ path, query: { user: u.identity } })
}
</script>

<template>
  <div v-if="eventUser" class="section user-card">
    <div class="section__head" style="cursor: default">
      <h2 class="section__title">User</h2>
    </div>
    <button v-if="lensUser" class="user-card__body user-card__body--btn" type="button" @click="lensTo('/issues')">
      <div class="user-card__avatar">{{ eventUserInitial }}</div>
      <div class="user-card__info">
        <span class="user-card__name">{{ eventUserLabel }}</span>
        <span v-if="eventUser.email && eventUser.email !== eventUserLabel" class="user-card__sub">{{ eventUser.email }}</span>
      </div>
    </button>
    <div v-else class="user-card__body">
      <div class="user-card__avatar">{{ eventUserInitial }}</div>
      <div class="user-card__info">
        <span class="user-card__name">{{ eventUserLabel }}</span>
        <span v-if="eventUser.email && eventUser.email !== eventUserLabel" class="user-card__sub">{{ eventUser.email }}</span>
      </div>
    </div>
    <div class="user-card__rows">
      <template v-if="eventUser.id">
        <span class="user-card__key">ID</span>
        <span class="user-card__val mono">{{ eventUser.id }}</span>
      </template>
      <template v-if="eventUser.username && eventUser.username !== eventUserLabel">
        <span class="user-card__key">Username</span>
        <span class="user-card__val mono">{{ eventUser.username }}</span>
      </template>
      <template v-if="eventUser.ip_address">
        <span class="user-card__key">IP</span>
        <span class="user-card__val mono">{{ eventUser.ip_address }}</span>
      </template>
    </div>
    <div v-if="lensUser" class="user-card__links">
      <button type="button" class="user-card__link" @click="lensTo('/issues')">Issues</button>
      <button type="button" class="user-card__link" @click="lensTo('/performance/transactions')">Traces</button>
      <button type="button" class="user-card__link" @click="lensTo('/logs')">Logs</button>
    </div>
  </div>
</template>
