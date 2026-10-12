import { ref, computed, onMounted, onUnmounted, type Ref } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import type { Project, ServerSettings, User } from '@/api/types'
import { useMe } from '@/composables/useMe'
import { useTimezone } from '@/composables/useTimezone'

// Helpers shared by several settings tabs. Each tab calls these itself, so the
// queries run only while a tab that needs them is on screen.

export type SettingsTab = 'tokens' | 'projects' | 'users' | 'alerts' | 'audit' | 'overview' | 'profile'

export function usePermissions() {
  const { data: me } = useMe()
  const canManageProjects = computed(() => me.value?.permissions.manage_projects ?? false)
  const canManageUsers = computed(() => me.value?.permissions.manage_users ?? false)
  const canManageAlerts = computed(() => me.value?.permissions.manage_alerts ?? false)
  return { me, canManageProjects, canManageUsers, canManageAlerts }
}

export function useProjects() {
  const { data: projectsData } = useQuery({
    queryKey: ['projects', 'usage'],
    queryFn: ({ signal }) => apiFetch<Project[]>('/api/projects', { signal }),
  })
  const projects = computed(() => projectsData.value ?? [])
  return { projects }
}

export function useProjectByID(projects: Ref<Project[]>) {
  return computed(() => {
    const m = new Map<string, Project>()
    for (const p of projects.value) m.set(p.id, p)
    return m
  })
}

export function useUsers() {
  const { data: usersData } = useQuery({
    queryKey: ['users'],
    queryFn: ({ signal }) => apiFetch<User[]>('/api/users', { signal }),
  })
  const users = computed(() => usersData.value ?? [])
  return { users }
}

// Server limits and version info.
export function useServerSettings() {
  const { data: settings } = useQuery({
    queryKey: ['settings'],
    queryFn: ({ signal }) => apiFetch<ServerSettings>('/api/settings', { signal }),
  })
  return { settings }
}

// Row action menu (⋯) used by the token, user and invite tables. Any click
// elsewhere on the page closes it.
export function useRowMenu() {
  const openMenuId = ref<string | null>(null)

  function toggleUserMenu(id: string, e: MouseEvent) {
    e.stopPropagation()
    openMenuId.value = openMenuId.value === id ? null : id
  }

  function closeUserMenus() { openMenuId.value = null }

  onMounted(() => document.addEventListener('click', closeUserMenus))
  onUnmounted(() => document.removeEventListener('click', closeUserMenus))

  return { openMenuId, toggleUserMenu, closeUserMenus }
}

// Instance-wide monthly event usage against the server's event limit.
export function useMonthlyEvents(projects: Ref<Project[]>, settings: Ref<ServerSettings | undefined>) {
  const totalMonthlyEvents = computed(() =>
    projects.value.reduce((sum, p) => sum + p.event_count, 0),
  )

  const instanceAtLimit = computed(() => {
    const limit = settings.value?.event_limit ?? 0
    return limit > 0 && totalMonthlyEvents.value >= limit
  })

  const instanceNearLimit = computed(() => {
    const limit = settings.value?.event_limit ?? 0
    if (limit <= 0) return false
    return totalMonthlyEvents.value / limit >= 0.8
  })

  const globalEventPct = computed(() => {
    const limit = settings.value?.event_limit ?? 0
    if (limit <= 0) return 0
    return Math.min(100, Math.round((totalMonthlyEvents.value / limit) * 100))
  })

  return { totalMonthlyEvents, instanceAtLimit, instanceNearLimit, globalEventPct }
}

// Date only, in the signed-in user's timezone.
export function useFormatDate() {
  const tz = useTimezone()
  return (iso: string) => new Date(iso).toLocaleDateString(undefined, { timeZone: tz.value })
}

export function formatBytes(bytes: number): string {
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}
