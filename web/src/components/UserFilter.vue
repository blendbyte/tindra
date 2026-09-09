<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { useAppUserStore, appUserLabel, appUserInitial } from '@/stores/appUser'
import { useProjectsStore } from '@/stores/projects'
import { apiFetch } from '@/api/client'
import type { AppUser } from '@/api/types'
import Icon from './Icon.vue'

const route = useRoute()
const router = useRouter()
const appUser = useAppUserStore()
const projects = useProjectsStore()

const open = ref(false)
const el = ref<HTMLElement | null>(null)
const search = ref('')
const searchDebounced = ref('')
const inputRef = ref<HTMLInputElement | null>(null)
let searchTimer: ReturnType<typeof setTimeout> | null = null
watch(search, (val) => {
  if (searchTimer) clearTimeout(searchTimer)
  if (!val.trim()) {
    searchDebounced.value = ''
    return
  }
  searchTimer = setTimeout(() => { searchDebounced.value = val.trim() }, 200)
})

function onMouseDown(e: MouseEvent) {
  if (el.value && !el.value.contains(e.target as Node)) open.value = false
}
onMounted(() => document.addEventListener('mousedown', onMouseDown))
onUnmounted(() => {
  document.removeEventListener('mousedown', onMouseDown)
  if (searchTimer) clearTimeout(searchTimer)
})

const projectIds = computed(() => {
  const v = route.query.project_id
  if (typeof v === 'string' && v) return [v]
  if (Array.isArray(v)) return v.filter((x): x is string => typeof x === 'string' && x !== '')
  return projects.selectedIds
})

const listParams = computed(() => {
  const p = new URLSearchParams()
  const q = searchDebounced.value.trim()
  if (q) p.set('q', q)
  for (const id of projectIds.value) p.append('project_id', id)
  return p.toString()
})

const { data: users, isFetching } = useQuery({
  queryKey: computed(() => ['app-users', listParams.value]),
  queryFn: () => apiFetch<AppUser[]>(`/api/app-users?${listParams.value}`),
  enabled: computed(() => open.value),
})

const options = computed(() => users.value ?? [])

let hydrationVersion = 0
let active = true
// Invalidate immediately, including when the selection changes during a fetch.
watch(() => appUser.selected, () => { hydrationVersion++ }, { flush: 'sync' })
watch(() => route.query.user, () => { hydrationVersion++ }, { flush: 'sync' })
watch(projectIds, () => { hydrationVersion++ }, { flush: 'sync' })
onUnmounted(() => { active = false; hydrationVersion++ })

async function hydrateFromRoute() {
  const version = ++hydrationVersion
  const raw = route.query.user
  const ident = typeof raw === 'string' ? raw : ''
  if (!ident) return
  if (appUser.identity === ident) return
  const p = new URLSearchParams({ identity: ident })
  for (const id of projectIds.value) p.append('project_id', id)
  try {
    const found = await apiFetch<AppUser[]>(`/api/app-users?${p}`)
    if (!active || version !== hydrationVersion) return
    const match = found.find((u) => u.identity === ident) ?? found[0]
    appUser.select(match ?? {
      identity: ident,
      user_id: ident,
      username: null,
      email: null,
      name: null,
      last_seen: '',
      project_id: '',
    })
  } catch {
    if (!active || version !== hydrationVersion) return
    appUser.select({
      identity: ident,
      user_id: ident,
      username: null,
      email: null,
      name: null,
      last_seen: '',
      project_id: '',
    })
  }
}

function syncUrl() {
  const cur = typeof route.query.user === 'string' ? route.query.user : ''
  if (cur === appUser.identity) return
  const query = { ...route.query } as Record<string, string | string[] | undefined>
  if (appUser.identity) query.user = appUser.identity
  else delete query.user
  router.replace({ query })
}

onMounted(() => {
  hydrateFromRoute().then(() => {
    if (active && !route.query.user && appUser.identity) syncUrl()
  })
})

watch(() => appUser.identity, syncUrl)
watch(() => route.query.user, (v) => {
  const ident = typeof v === 'string' ? v : ''
  if (!ident) {
    if (appUser.identity) syncUrl()
    return
  }
  if (ident !== appUser.identity) hydrateFromRoute()
})

function pick(u: AppUser) {
  appUser.select(u)
  open.value = false
  search.value = ''
  searchDebounced.value = ''
}

function clear() {
  appUser.clear()
  open.value = false
  search.value = ''
  searchDebounced.value = ''
}

async function toggle() {
  open.value = !open.value
  if (open.value) {
    search.value = ''
    await nextTick()
    inputRef.value?.focus()
  }
}

function optionSub(u: AppUser): string {
  const parts: string[] = []
  if (u.username && u.username !== appUserLabel(u)) parts.push(u.username)
  if (u.email && u.email !== appUserLabel(u)) parts.push(u.email)
  if (u.user_id && u.user_id !== appUserLabel(u) && u.user_id !== u.username) parts.push(u.user_id)
  return parts.join(' · ')
}
</script>

<template>
  <div ref="el" class="user-filter" style="position: relative">
    <button
      v-if="!appUser.selected"
      class="filterchip"
      aria-label="Filter by user"
      @click="toggle"
    >
      <span class="filterchip__label">User:</span>
      <span class="filterchip__value">All</span>
      <Icon name="chevron-down" :size="11" />
    </button>
    <button
      v-else
      class="user-chip"
      :title="appUser.label"
      aria-label="Change user filter"
      @click="toggle"
    >
      <span class="user-chip__avatar">{{ appUser.initial }}</span>
      <span class="user-chip__name">{{ appUser.label }}</span>
      <span
        class="user-chip__clear"
        role="button"
        aria-label="Clear user filter"
        @click.stop="clear"
      >
        <Icon name="x" :size="10" />
      </span>
    </button>

    <div
      v-if="open"
      class="popover"
      style="left: 0; right: auto; min-width: 240px"
    >
      <div class="popover__search">
        <input
          ref="inputRef"
          v-model="search"
          placeholder="Search people…"
          aria-label="Search people"
        />
      </div>
      <div class="popover__list">
        <div
          class="popover__item"
          :class="{ 'popover__item--active': !appUser.selected }"
          @click="clear"
        >
          All users
        </div>
        <div
          v-for="u in options"
          :key="u.identity + u.project_id"
          class="popover__item"
          :class="{ 'popover__item--active': u.identity === appUser.identity }"
          @click="pick(u)"
        >
          <span class="user-chip__avatar user-chip__avatar--sm">{{ appUserInitial(u) }}</span>
          <span class="user-filter__meta">
            <span>{{ appUserLabel(u) }}</span>
            <span v-if="optionSub(u)" class="user-filter__sub">{{ optionSub(u) }}</span>
          </span>
        </div>
        <div v-if="!isFetching && options.length === 0" class="user-filter__empty">
          {{ search.trim() ? 'No matching people' : 'No people yet. set_user() on the SDK, then errors and traces show up here.' }}
        </div>
      </div>
    </div>
  </div>
</template>
