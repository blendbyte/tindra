import { ref, computed, onMounted, onUnmounted, type Ref } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import type { Project, ServerSettings, Team, User } from '@/api/types'
import { useMe } from '@/composables/useMe'
import { useTimezone } from '@/composables/useTimezone'

// Helpers shared by several settings tabs. Each tab calls these itself, so the
// queries run only while a tab that needs them is on screen.

export type SettingsTab = 'tokens' | 'projects' | 'users' | 'teams' | 'alerts' | 'audit' | 'overview' | 'profile'

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

// The user list. With projectIds, a people picker: only the users who see all of
// those projects, or only the users who see every project when the list is empty
// (a rule for all projects). Without it, everyone the caller may list.
export function useUsers(enabled: Ref<boolean> | boolean = true, projectIds?: Ref<string[]>) {
  const { data: usersData } = useQuery({
    queryKey: computed(() => (projectIds ? ['users', 'for', [...projectIds.value]] : ['users'])),
    queryFn: ({ signal }) => {
      if (!projectIds) return apiFetch<User[]>('/api/users', { signal })
      const params = new URLSearchParams()
      for (const id of projectIds.value) params.append('project_id', id)
      if (projectIds.value.length === 0) params.set('access', 'all')
      return apiFetch<User[]>(`/api/users?${params}`, { signal })
    },
    enabled,
  })
  const users = computed(() => usersData.value ?? [])
  return { users }
}

// Teams. Open to manage_users and manage_projects, who pick teams for users and
// projects; everyone else gets a 403, so pass enabled for them.
export function useTeams(enabled: Ref<boolean> | boolean = true) {
  const { data: teamsData } = useQuery({
    queryKey: ['teams'],
    queryFn: ({ signal }) => apiFetch<Team[]>('/api/teams', { signal }),
    enabled,
  })
  const teams = computed(() => teamsData.value ?? [])
  return { teams }
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
