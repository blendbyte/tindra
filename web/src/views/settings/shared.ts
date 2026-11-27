import { ref, computed, onMounted, onUnmounted, type Ref } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { ApiError, apiFetch } from '@/api/client'
import type { AlertImpact, Project, ServerSettings, Team, User } from '@/api/types'
import { useConfirm, type ConfirmOptions } from '@/composables/useConfirm'
import { useMe } from '@/composables/useMe'
import { useTimezone } from '@/composables/useTimezone'
import { useToast } from '@/composables/useToast'

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

// ── Effect of an access change on alert rules ────────────────────────────────

/**
 * Asks the server what a change of access would do to alert rules, without making it.
 * The change is the same request the save sends, with `?dry_run=true`.
 */
export function previewAlertImpact(url: string, method: 'PUT' | 'DELETE', body?: unknown): Promise<AlertImpact> {
  return apiFetch<AlertImpact>(`${url}?dry_run=true`, {
    method,
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  })
}

function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? '' : 's'}`
}

/**
 * Says in a sentence what an access change does to alert rules, for a confirmation.
 * Returns an empty string when it touches none (or the server sent nothing), so the
 * caller can save without asking.
 */
export function describeAlertImpact(impact: AlertImpact | null | undefined): string {
  const removed = impact?.removed_recipients ?? []
  const paused = impact?.paused_rules ?? []
  if (removed.length === 0 && paused.length === 0) return ''
  const rulesByPerson = new Map<string, { name: string; rules: Set<string> }>()
  for (const r of removed) {
    const entry = rulesByPerson.get(r.user_id) ?? { name: r.user_name, rules: new Set<string>() }
    entry.rules.add(r.rule_id)
    rulesByPerson.set(r.user_id, entry)
  }
  const parts: string[] = []
  if (rulesByPerson.size > 0) {
    const people = [...rulesByPerson.values()].map((p) => `${p.name || 'A user'} from ${plural(p.rules.size, 'alert rule')}`)
    parts.push(`This removes ${people.join(', ')}.`)
  }
  if (paused.length > 0) {
    parts.push(`${plural(paused.length, 'push rule')} will be paused (${paused.map((r) => r.name).join(', ')}) because nobody would be left to notify.`)
  }
  return parts.join(' ')
}

/**
 * Before an access change that can take someone off an alert rule: previews it, and
 * asks the admin to confirm when it would remove recipients or pause rules. With
 * `alwaysAsk` (a confirmation the caller needs anyway, like deleting a team) the
 * effect is added to its message instead of a second question. Resolves false when
 * the admin cancels, or when the effect could not be checked, so nothing is paused
 * without them knowing. A change the server refuses outright goes ahead to the save,
 * which reports why.
 */
export function useAlertImpactConfirm() {
  const { confirm } = useConfirm()
  const { show: showToast } = useToast()

  return async function confirmAlertImpact(
    url: string,
    method: 'PUT' | 'DELETE',
    body: unknown,
    options: ConfirmOptions & { alwaysAsk?: boolean },
  ): Promise<boolean> {
    let effect: string
    try {
      effect = describeAlertImpact(await previewAlertImpact(url, method, body))
    } catch (e) {
      // The change itself is refused (400, 409): let the save answer with its own message.
      if (e instanceof ApiError && (e.status === 400 || e.status === 409)) return true
      showToast('Could not check the effect on alert rules. Try again.', 'error')
      return false
    }
    if (!effect && !options.alwaysAsk) return true
    const { alwaysAsk: _alwaysAsk, ...dialog } = options
    return confirm({ ...dialog, message: [options.message, effect].filter(Boolean).join(' ') })
  }
}
