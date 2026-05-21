<script setup lang="ts">
import { ref, reactive, computed, watch, watchEffect, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useToast } from '@/composables/useToast'
import { useConfig } from '@/composables/useConfig'
import { apiFetch } from '@/api/client'
import { formatRel } from '@/utils/formatters'
import type { ApiToken, Invite, InstanceHealth, Project, ProjectQuota, ScrubPattern, ServerSettings, User, UserPermissions, AuditRow, AlertRule, AlertTrigger, AlertChannel } from '@/api/types'
import Icon from '@/components/Icon.vue'
import Sparkline from '@/components/Sparkline.vue'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()
const { show: showToast } = useToast()
const qc = useQueryClient()
const router = useRouter()
const route = useRoute()
const { dsnFor } = useConfig()

type Tab = 'tokens' | 'projects' | 'users' | 'alerts' | 'audit' | 'overview' | 'profile'
const ALL_TABS: Tab[] = ['overview', 'projects', 'alerts', 'users', 'audit', 'tokens', 'profile']

function resolveTab(param: unknown): Tab {
  const p = param === 'team' ? 'users' : param // backward compat with old URL
  return ALL_TABS.includes(p as Tab) ? (p as Tab) : 'overview'
}

const tab = ref<Tab>(resolveTab(route.params.tab))

watch(() => route.params.tab, (t) => { tab.value = resolveTab(t) })


// Label override so 'users' shows as 'Users' (not 'Users' would be same anyway, but keep explicit)
function tabLabel(t: Tab): string {
  return t.charAt(0).toUpperCase() + t.slice(1)
}

function setTab(t: Tab) {
  tab.value = t
  router.replace({ name: 'settings', params: { tab: t } })
}
const showTokenForm = ref(false)
const newTokenName = ref('')
const newTokenProject = ref('')
const newTokenWritable = ref(false)
const flashToken = ref<string | null>(null)

function closeTokenForm() {
  showTokenForm.value = false
  newTokenName.value = ''
  newTokenProject.value = ''
  newTokenWritable.value = false
  flashToken.value = null
}

// Tokens
const { data: tokensData } = useQuery({
  queryKey: ['tokens'],
  queryFn: () => apiFetch<ApiToken[]>('/api/tokens'),
})
const tokens = computed(() => tokensData.value ?? [])

const { data: projectsData } = useQuery({
  queryKey: ['projects'],
  queryFn: () => apiFetch<Project[]>('/api/projects'),
})
const projects = computed(() => projectsData.value ?? [])

const { mutate: createToken, isPending: creatingToken } = useMutation({
  mutationFn: ({ name, project_id, writable }: { name: string; project_id: string; writable: boolean }) =>
    apiFetch<{ token: string; meta: ApiToken }>('/api/tokens', {
      method: 'POST',
      body: JSON.stringify({ name, project_id, writable }),
    }),
  onSuccess: (data) => {
    flashToken.value = data.token
    newTokenName.value = ''
    newTokenProject.value = ''
    newTokenWritable.value = false
    qc.invalidateQueries({ queryKey: ['tokens'] })
  },
  onError: (err) => {
    showToast(err instanceof Error ? err.message : 'Failed to create token.', 'error')
  },
})

const { mutate: revokeToken } = useMutation({
  mutationFn: ({ id }: { id: string; name: string }) =>
    apiFetch(`/api/tokens/${id}`, { method: 'DELETE' }),
  onSuccess: (_, { name }) => {
    qc.invalidateQueries({ queryKey: ['tokens'] })
    showToast(`"${name}" revoked`)
  },
  onError: (err) => {
    showToast(err instanceof Error ? err.message : 'Failed to revoke token.', 'error')
  },
})

function submitCreate(e: Event) {
  e.preventDefault()
  const pid = newTokenProject.value || (projects.value[0]?.id ?? '')
  if (!newTokenName.value.trim() || !pid) return
  createToken({ name: newTokenName.value.trim(), project_id: pid, writable: newTokenWritable.value })
}

function copyFlash() {
  navigator.clipboard?.writeText(flashToken.value!).catch(() => {})
  showToast('Token copied to clipboard')
  closeTokenForm()
}

function revoke(id: string, name: string) {
  revokeToken({ id, name })
  closeUserMenus()
}

// Team
const { data: usersData } = useQuery({
  queryKey: ['users'],
  queryFn: () => apiFetch<User[]>('/api/users'),
})
const users = computed(() => usersData.value ?? [])

const userLimitReached = computed(() => {
  const limit = settings.value?.user_limit ?? 0
  return limit > 0 && users.value.length >= limit
})

const { mutate: deleteUser } = useMutation({
  mutationFn: (id: string) => apiFetch(`/api/users/${id}`, { method: 'DELETE' }),
  onSuccess: () => {
    qc.invalidateQueries({ queryKey: ['users'] })
    showToast('User removed')
  },
})

const { mutate: savePermissions } = useMutation({
  mutationFn: ({ userID, perms }: { userID: string; perms: UserPermissions }) =>
    apiFetch<User>(`/api/users/${userID}/permissions`, {
      method: 'PUT',
      body: JSON.stringify(perms),
    }),
  onSuccess: (updated) => {
    qc.setQueryData(['users'], (old: User[] | undefined) =>
      old?.map((u) => (u.id === updated.id ? updated : u)) ?? [updated],
    )
    qc.setQueryData(['me'], (old: User | undefined) =>
      old?.id === updated.id ? updated : old,
    )
    showToast('Permissions saved')
  },
  onError: () => showToast('Permissions not saved. Try again.'),
})

function togglePerm(user: User, perm: keyof UserPermissions) {
  const updated: UserPermissions = { ...user.permissions, [perm]: !user.permissions[perm] }
  savePermissions({ userID: user.id, perms: updated })
}

// --- Admin user actions ---

// Delete: two-step confirmation
const deleteStep = ref<0 | 1 | 2>(0)
const deleteTarget = ref<User | null>(null)
const deleteEmailInput = ref('')

function startUserDelete(u: User) {
  deleteTarget.value = u
  deleteStep.value = 1
  deleteEmailInput.value = ''
}

function cancelUserDelete() {
  deleteTarget.value = null
  deleteStep.value = 0
  deleteEmailInput.value = ''
}

function proceedUserDelete() {
  if (deleteStep.value === 1) {
    deleteStep.value = 2
    return
  }
  if (deleteStep.value === 2 && deleteEmailInput.value === deleteTarget.value?.email) {
    deleteUser(deleteTarget.value!.id)
    cancelUserDelete()
  }
}

// Set password inline
const setPwTarget = ref<string | null>(null)
const setPwValue = ref('')
const setPwConfirm = ref('')
const setPwError = ref<string | null>(null)

function openSetPw(userID: string) {
  setPwTarget.value = userID
  setPwValue.value = ''
  setPwConfirm.value = ''
  setPwError.value = null
}

function cancelSetPw() {
  setPwTarget.value = null
  setPwValue.value = ''
  setPwConfirm.value = ''
  setPwError.value = null
}

const { mutate: adminSetPassword, isPending: settingPw } = useMutation({
  mutationFn: ({ userID, password }: { userID: string; password: string }) =>
    apiFetch(`/api/users/${userID}/password`, {
      method: 'PUT',
      body: JSON.stringify({ password }),
    }),
  onSuccess: () => {
    cancelSetPw()
    showToast('Password updated')
  },
  onError: (e) => { setPwError.value = e instanceof Error ? e.message : 'Failed to set password.' },
})

function submitSetPw(userID: string) {
  setPwError.value = null
  if (setPwValue.value !== setPwConfirm.value) { setPwError.value = 'Passwords do not match.'; return }
  if (setPwValue.value.length < 12) { setPwError.value = 'Password must be at least 12 characters.'; return }
  adminSetPassword({ userID, password: setPwValue.value })
}

// Remove MFA
const { mutate: adminRemoveMFA } = useMutation({
  mutationFn: (userID: string) => apiFetch(`/api/users/${userID}/mfa`, { method: 'DELETE' }),
  onSuccess: (_, userID) => {
    qc.setQueryData(['users'], (old: User[] | undefined) =>
      old?.map((u) => (u.id === userID ? { ...u, mfa_enabled: false } : u)) ?? [],
    )
    showToast('MFA removed')
  },
  onError: () => showToast('Could not remove MFA. Try again.'),
})

// Send password reset email
type ResetResult = { email_sent: boolean; reset_url: string; email_error?: string }
const resetResult = ref<ResetResult | null>(null)
const resetTarget = ref<User | null>(null)

const { mutate: sendPasswordReset, isPending: sendingReset } = useMutation({
  mutationFn: (userID: string) =>
    apiFetch<ResetResult>(`/api/users/${userID}/password-reset`, { method: 'POST' }),
  onSuccess: (result, userID) => {
    resetTarget.value = users.value.find((u) => u.id === userID) ?? null
    resetResult.value = result
  },
  onError: () => showToast('Could not send reset email. Try again.'),
})

function closeResetResult() {
  resetResult.value = null
  resetTarget.value = null
}

// Row action menu (⋯)
const openMenuId = ref<string | null>(null)

function toggleUserMenu(id: string, e: MouseEvent) {
  e.stopPropagation()
  openMenuId.value = openMenuId.value === id ? null : id
}

function closeUserMenus() { openMenuId.value = null }

onMounted(() => document.addEventListener('click', closeUserMenus))
onUnmounted(() => document.removeEventListener('click', closeUserMenus))

// Audit log
const auditKindFilter = ref('All')
const auditSearch = ref('')

const auditKinds = ['All', 'auth', 'alert', 'issue', 'release', 'token', 'project']

const { data: auditLogData } = useQuery({
  queryKey: computed(() => ['audit', auditKindFilter.value, auditSearch.value]),
  queryFn: () =>
    apiFetch<AuditRow[]>(`/api/audit?kind=${auditKindFilter.value === 'All' ? '' : auditKindFilter.value}&q=${auditSearch.value}`),
  enabled: computed(() => tab.value === 'audit'),
})
const auditLog = computed(() => auditLogData.value ?? [])

// Profile
const { data: me } = useQuery({
  queryKey: ['me'],
  queryFn: () => apiFetch<User>('/api/me'),
})

// Redirect away from gated tabs if the user loses / never had the required permission.
// Must come after `me` is declared to avoid TDZ - watchEffect runs eagerly.
watchEffect(() => {
  if (tab.value === 'tokens' && me.value && !me.value.permissions.manage_projects) {
    setTab('projects')
  }
  if (tab.value === 'overview' && me.value && !me.value.permissions.manage_projects) {
    setTab('projects')
  }
  if (tab.value === 'audit' && me.value && !me.value.permissions.manage_users) {
    setTab('projects')
  }
  if (tab.value === 'alerts' && me.value && !me.value.permissions.manage_alerts) {
    setTab('projects')
  }
})

// Invite form - declared after `me` so the enabled getter can reference it safely
const showInviteForm = ref(false)
const inviteEmail = ref('')
const inviteName = ref('')
const inviteError = ref<string | null>(null)
const inviteResult = ref<{ invite_url: string; email_sent: boolean; email_configured: boolean; email_error: string } | null>(null)

const { data: invitesData } = useQuery({
  queryKey: ['invites'],
  queryFn: () => apiFetch<Invite[]>('/api/invites'),
  enabled: () => !!me.value?.permissions.manage_users,
})
const invites = computed(() => invitesData.value ?? [])

const { mutate: createInvite, isPending: creatingInvite } = useMutation({
  mutationFn: ({ email, name }: { email: string; name: string }) =>
    apiFetch<{ invite_url: string; email_sent: boolean }>('/api/invites', {
      method: 'POST',
      body: JSON.stringify({ email, name }),
    }),
  onSuccess: (result) => {
    inviteResult.value = result
    inviteEmail.value = ''
    inviteName.value = ''
    inviteError.value = null
    qc.invalidateQueries({ queryKey: ['invites'] })
  },
  onError: (e) => {
    inviteError.value = e instanceof Error ? e.message : 'Failed to send invite'
  },
})

function submitInvite(e: Event) {
  e.preventDefault()
  inviteError.value = null
  inviteResult.value = null
  const email = inviteEmail.value.trim()
  if (!email) return
  createInvite({ email, name: inviteName.value.trim() })
}

function closeInviteForm() {
  showInviteForm.value = false
  inviteEmail.value = ''
  inviteName.value = ''
  inviteError.value = null
  inviteResult.value = null
}

const { mutate: revokeInvite } = useMutation({
  mutationFn: (token: string) => apiFetch(`/api/invites/${token}`, { method: 'DELETE' }),
  onSuccess: () => {
    qc.invalidateQueries({ queryKey: ['invites'] })
    showToast('Invite revoked')
  },
})

function inviteURL(token: string): string {
  return `${window.location.origin}/invite/${token}`
}

function copyInviteURL(url: string) {
  navigator.clipboard.writeText(url)
  showToast('Invite link copied')
}

const profileName = ref('')
const profileEmail = ref('')
const profileWeeklyDigest = ref(true)

function initProfile() {
  profileName.value = me.value?.name ?? ''
  profileEmail.value = me.value?.email ?? ''
  profileWeeklyDigest.value = me.value?.weekly_digest ?? true
}

// Initialize profile fields whenever me loads or the profile tab becomes active.
watch([tab, me], () => {
  if (tab.value === 'profile' && me.value) initProfile()
}, { immediate: true })

const { mutate: updateProfile } = useMutation({
  mutationFn: ({ name, email, weekly_digest }: { name: string; email: string; weekly_digest: boolean }) =>
    apiFetch('/api/me', { method: 'PATCH', body: JSON.stringify({ name, email, weekly_digest }) }),
  onSuccess: () => {
    qc.invalidateQueries({ queryKey: ['me'] })
    showToast('Profile saved')
  },
})

// Password change
const pwCurrent = ref('')
const pwNew = ref('')
const pwConfirm = ref('')
const pwError = ref<string | null>(null)

const { mutate: changePassword, isPending: changingPw } = useMutation({
  mutationFn: ({ current_password, new_password }: { current_password: string; new_password: string }) =>
    apiFetch('/api/me/password', { method: 'PATCH', body: JSON.stringify({ current_password, new_password }) }),
  onSuccess: () => {
    pwCurrent.value = ''
    pwNew.value = ''
    pwConfirm.value = ''
    pwError.value = null
    showToast('Password changed')
  },
  onError: (e) => { pwError.value = e instanceof Error ? e.message : 'Failed to change password' },
})

function submitPasswordChange() {
  pwError.value = null
  if (pwNew.value !== pwConfirm.value) { pwError.value = 'Passwords do not match'; return }
  if (pwNew.value.length < 12) { pwError.value = 'Password must be at least 12 characters'; return }
  changePassword({ current_password: pwCurrent.value, new_password: pwNew.value })
}

// MFA setup / disable
const mfaSetupData = ref<{ secret: string; uri: string; qr: string } | null>(null)
const showMFASecret = ref(false)
const mfaCode = ref('')
const mfaSetupError = ref<string | null>(null)
const showDisableMFA = ref(false)
const mfaDisablePassword = ref('')
const mfaDisableError = ref<string | null>(null)

const { mutate: startMFASetup, isPending: loadingMFASetup } = useMutation({
  mutationFn: () => apiFetch<{ secret: string; uri: string }>('/api/auth/mfa/setup'),
  onSuccess: (data) => { mfaSetupData.value = data; mfaCode.value = ''; mfaSetupError.value = null },
})

const { mutate: confirmMFASetup, isPending: confirmingMFA } = useMutation({
  mutationFn: (code: string) =>
    apiFetch('/api/auth/mfa/confirm', { method: 'POST', body: JSON.stringify({ code }) }),
  onSuccess: () => {
    mfaSetupData.value = null
    mfaCode.value = ''
    mfaSetupError.value = null
    qc.invalidateQueries({ queryKey: ['me'] })
    showToast('Two-factor authentication enabled')
  },
  onError: (e) => { mfaSetupError.value = e instanceof Error ? e.message : 'That code didn\'t work. Try again.' },
})

const { mutate: disableMFA, isPending: disablingMFA } = useMutation({
  mutationFn: (password: string) =>
    apiFetch('/api/auth/mfa', { method: 'DELETE', body: JSON.stringify({ password }) }),
  onSuccess: () => {
    showDisableMFA.value = false
    mfaDisablePassword.value = ''
    mfaDisableError.value = null
    qc.invalidateQueries({ queryKey: ['me'] })
    showToast('Two-factor authentication disabled')
  },
  onError: (e) => { mfaDisableError.value = e instanceof Error ? e.message : 'Invalid password' },
})

function copyToClipboard(text: string, label: string) {
  navigator.clipboard?.writeText(text).catch(() => {})
  showToast(`${label} copied`)
}

function cancelMFASetup() {
  mfaSetupData.value = null
  mfaCode.value = ''
  mfaSetupError.value = null
}

// Alert rules state (UI-only for create)
const showNewRule = ref(false)
const expandedRule = ref<string | null>(null)

const newRule = reactive({
  projectIDs: [] as string[],
  name: '',
  trigger: 'new_issue' as AlertTrigger,
  threshold: 100,
  window_mins: 60,
  channel: 'webhook' as AlertChannel,
  webhook_url: '',
  email_to: '',
  cooldown_mins: 60,
  filter_level: '',
  filter_environment: '',
  min_occurrences: 0,
})

function resetNewRule() {
  newRule.projectIDs = []
  newRule.name = ''
  newRule.trigger = 'new_issue'
  newRule.threshold = 100
  newRule.window_mins = 60
  newRule.channel = 'webhook'
  newRule.webhook_url = ''
  newRule.email_to = ''
  newRule.cooldown_mins = 60
  newRule.filter_level = ''
  newRule.filter_environment = ''
  newRule.min_occurrences = 0
}

const alertProjects = computed(() => (projects.value as Project[]) ?? [])

const projectByID = computed(() => {
  const m = new Map<string, Project>()
  for (const p of alertProjects.value) m.set(p.id, p)
  return m
})

const { data: alertRulesData } = useQuery({
  queryKey: ['alert-rules'],
  queryFn: () => apiFetch<{ rules: AlertRule[] }>('/api/alert-rules').then(r => r.rules ?? []),
  enabled: computed(() => tab.value === 'alerts'),
})

const alertRules = computed(() => alertRulesData.value ?? [])

function ruleProjectNames(rule: AlertRule): string {
  if (!rule.project_ids?.length) return 'Global'
  return rule.project_ids.map(id => projectByID.value.get(id)?.name ?? id).join(', ')
}

function triggerLabel(rule: AlertRule): string {
  if (rule.trigger === 'new_issue') return 'New issue'
  if (rule.trigger === 'regressed') return 'Regression'
  if (rule.trigger === 'new_or_regressed') return 'New issue or regression'
  if (rule.trigger === 'always') return 'Always'
  if (rule.trigger === 'event_count') return `>${rule.threshold} events in ${rule.window_mins}m`
  if (rule.trigger === 'cron_missed') return 'Cron monitor missed'
  if (rule.trigger === 'cron_error') return 'Cron monitor error'
  return rule.trigger
}

function openNewRule() {
  showNewRule.value = !showNewRule.value
}

const { mutate: createAlertRule, isPending: creatingRule } = useMutation({
  mutationFn: () => {
    const body: Record<string, unknown> = {
      name: newRule.name,
      trigger: newRule.trigger,
      channel: newRule.channel,
      cooldown_mins: Number(newRule.cooldown_mins),
      project_ids: newRule.projectIDs,
    }
    if (newRule.trigger === 'event_count') {
      body.threshold = Number(newRule.threshold)
      body.window_mins = Number(newRule.window_mins)
    }
    if (newRule.channel === 'email') {
      body.email_to = newRule.email_to
    } else {
      body.webhook_url = newRule.webhook_url
    }
    if (newRule.filter_level) body.filter_level = newRule.filter_level
    if (newRule.filter_environment) body.filter_environment = newRule.filter_environment
    if (newRule.min_occurrences > 0) body.min_occurrences = Number(newRule.min_occurrences)
    return apiFetch<AlertRule>('/api/alert-rules', {
      method: 'POST',
      body: JSON.stringify(body),
    })
  },
  onSuccess: () => {
    showNewRule.value = false
    resetNewRule()
    qc.invalidateQueries({ queryKey: ['alert-rules'] })
    showToast('Alert rule created')
  },
  onError: (err: unknown) => showToast(err instanceof Error ? err.message : 'Failed to create rule'),
})

const { mutate: deleteAlertRule } = useMutation({
  mutationFn: ({ id }: { id: string }) =>
    apiFetch(`/api/alert-rules/${id}`, { method: 'DELETE' }),
  onSuccess: () => {
    qc.invalidateQueries({ queryKey: ['alert-rules'] })
    showToast('Rule deleted')
  },
})

const { mutate: toggleAlertRule } = useMutation({
  mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
    apiFetch(`/api/alert-rules/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({ enabled }),
    }),
  onSuccess: () => qc.invalidateQueries({ queryKey: ['alert-rules'] }),
})

const testingRuleID = ref<string | null>(null)

const { mutate: testAlertRule } = useMutation({
  mutationFn: ({ id }: { id: string }) =>
    apiFetch(`/api/alert-rules/${id}/test`, { method: 'POST' }),
  onMutate: ({ id }) => { testingRuleID.value = id },
  onSettled: () => { testingRuleID.value = null },
  onSuccess: () => showToast('Test alert sent'),
  onError: (err: unknown) => showToast(err instanceof Error ? err.message : 'Test failed'),
})

const editingRuleID = ref<string | null>(null)
const editRule = reactive({
  projectIDs: [] as string[],
  name: '',
  trigger: 'new_issue' as AlertTrigger,
  threshold: 100,
  window_mins: 60,
  channel: 'webhook' as AlertChannel,
  webhook_url: '',
  email_to: '',
  cooldown_mins: 60,
  filter_level: '',
  filter_environment: '',
  min_occurrences: 0,
})

function startEditRule(rule: AlertRule) {
  editingRuleID.value = rule.id
  editRule.projectIDs = rule.project_ids ? [...rule.project_ids] : []
  editRule.name = rule.name
  editRule.trigger = rule.trigger
  editRule.threshold = rule.threshold ?? 100
  editRule.window_mins = rule.window_mins ?? 60
  editRule.channel = rule.channel
  editRule.webhook_url = rule.webhook_url ?? ''
  editRule.email_to = rule.email_to ?? ''
  editRule.cooldown_mins = rule.cooldown_mins
  editRule.filter_level = rule.filter_level ?? ''
  editRule.filter_environment = rule.filter_environment ?? ''
  editRule.min_occurrences = rule.min_occurrences ?? 0
}

function cancelEditRule() {
  editingRuleID.value = null
}

const { mutate: saveAlertRule, isPending: savingRule } = useMutation({
  mutationFn: ({ id }: { id: string }) => {
    const body: Record<string, unknown> = {
      name: editRule.name,
      trigger: editRule.trigger,
      channel: editRule.channel,
      cooldown_mins: Number(editRule.cooldown_mins),
      filter_level: editRule.filter_level || null,
      filter_environment: editRule.filter_environment || null,
      min_occurrences: editRule.min_occurrences > 0 ? Number(editRule.min_occurrences) : null,
      project_ids: editRule.projectIDs,
    }
    if (editRule.trigger === 'event_count') {
      body.threshold = Number(editRule.threshold)
      body.window_mins = Number(editRule.window_mins)
    }
    if (editRule.channel === 'email') {
      body.email_to = editRule.email_to
    } else {
      body.webhook_url = editRule.webhook_url
    }
    return apiFetch(`/api/alert-rules/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    })
  },
  onSuccess: () => {
    editingRuleID.value = null
    qc.invalidateQueries({ queryKey: ['alert-rules'] })
    showToast('Rule updated')
  },
  onError: (err: unknown) => showToast(err instanceof Error ? err.message : 'Failed to save rule'),
})

// Server limits
const { data: settings } = useQuery({
  queryKey: ['settings'],
  queryFn: () => apiFetch<ServerSettings>('/api/settings'),
})

const canManageProjects = computed(() => me.value?.permissions.manage_projects ?? false)
const canManageUsers = computed(() => me.value?.permissions.manage_users ?? false)
const canManageAlerts = computed(() => me.value?.permissions.manage_alerts ?? false)
const visibleTabs = computed(() => ALL_TABS.filter((t) => {
  if (t === 'tokens' || t === 'overview') return canManageProjects.value
  if (t === 'audit') return canManageUsers.value
  if (t === 'alerts') return canManageAlerts.value
  return true
}))

const { data: healthData } = useQuery({
  queryKey: ['instance-health'],
  queryFn: () => apiFetch<InstanceHealth>('/api/instance/health'),
  enabled: computed(() => tab.value === 'overview' && canManageProjects.value),
  staleTime: 60_000,
})

function formatBytes(bytes: number): string {
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

function formatNum(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return n.toLocaleString()
}

function dataAgeDays(iso: string | null | undefined): string {
  if (!iso) return '–'
  const days = Math.floor((Date.now() - new Date(iso).getTime()) / 86_400_000)
  return days === 0 ? 'today' : `${days}d ago`
}

function expiresLabel(oldest: string | null | undefined, retentionDays: number): string {
  if (retentionDays === 0) return 'Forever'
  if (!oldest) return '–'
  const expiresAt = new Date(oldest)
  expiresAt.setDate(expiresAt.getDate() + retentionDays)
  const days = Math.ceil((expiresAt.getTime() - Date.now()) / 86_400_000)
  return days <= 0 ? 'Expired' : `${days}d`
}

function expiresColor(oldest: string | null | undefined, retentionDays: number): string {
  if (!oldest || retentionDays === 0) return 'var(--text-3)'
  const expiresAt = new Date(oldest)
  expiresAt.setDate(expiresAt.getDate() + retentionDays)
  const days = Math.ceil((expiresAt.getTime() - Date.now()) / 86_400_000)
  if (days < 10) return 'var(--danger)'
  if (days < 30) return 'var(--warning)'
  return 'var(--success)'
}

const projectLimitReached = computed(() => {
  const limit = settings.value?.project_limit ?? 0
  return limit > 0 && projects.value.length >= limit
})

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

// Projects config
const expandedProject = ref<string | null>(null)

// Quota data for whichever project card is currently expanded.
// One query, keyed to expandedProject - reruns automatically when a different card opens.
const { data: quotaData } = useQuery({
  queryKey: computed(() => ['quota', expandedProject.value]),
  queryFn: () => apiFetch<ProjectQuota>(`/api/projects/${expandedProject.value}/quota`),
  enabled: computed(() => expandedProject.value !== null && tab.value === 'projects'),
  staleTime: 30_000,
})
const showNewProject = ref(false)
const newProjectName = ref('')
const newProjectSlug = ref('')
const slugTouched = ref(false)
const newProjectError = ref<string | null>(null)
const createdProject = ref<Project | null>(null)

// Auto-open the new-project form when arriving via ?new=1
onMounted(() => { if (tab.value === 'projects' && route.query.new === '1') openNewProject() })
watch(() => route.query.new, (v) => { if (tab.value === 'projects' && v === '1') openNewProject() })

// Edit state
const editingProject = ref<string | null>(null)
const editName = ref('')
const editSlug = ref('')
const editPassthroughDsn = ref('')
const editSlugTouched = ref(false)
const editError = ref<string | null>(null)
const confirmingDelete = ref<string | null>(null)
const deleteConfirmText = ref('')

// Privacy / scrubbing state
const privacyProject = ref<string | null>(null)
const privacyScrubFieldInput = ref('')
const privacyScrubFields = ref<string[]>([])
const privacyBuiltinEmail = ref(false)
const privacyBuiltinIP = ref(false)
const privacyError = ref<string | null>(null)

function openPrivacy(p: Project) {
  privacyProject.value = p.id
  privacyScrubFields.value = [...(p.scrub_fields ?? [])]
  privacyScrubFieldInput.value = ''
  const email = (p.scrub_patterns ?? []).find(x => x.builtin && x.name === 'email')
  const ip = (p.scrub_patterns ?? []).find(x => x.builtin && x.name === 'ip')
  privacyBuiltinEmail.value = email?.enabled ?? false
  privacyBuiltinIP.value = ip?.enabled ?? false
  privacyError.value = null
}

function addScrubField() {
  const f = privacyScrubFieldInput.value.trim()
  if (!f || privacyScrubFields.value.includes(f)) return
  privacyScrubFields.value.push(f)
  privacyScrubFieldInput.value = ''
}

function removeScrubField(f: string) {
  privacyScrubFields.value = privacyScrubFields.value.filter(x => x !== f)
}

function buildScrubPatterns(): ScrubPattern[] {
  return [
    { name: 'email', pattern: '', builtin: true, enabled: privacyBuiltinEmail.value },
    { name: 'ip', pattern: '', builtin: true, enabled: privacyBuiltinIP.value },
  ]
}

watch(newProjectName, (name) => {
  if (!slugTouched.value) {
    newProjectSlug.value = name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '')
  }
})

watch(editName, (name) => {
  if (!editSlugTouched.value) {
    editSlug.value = name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '')
  }
})

const { mutate: createProject, isPending: creatingProject } = useMutation({
  mutationFn: ({ name, slug }: { name: string; slug: string }) =>
    apiFetch<Project>('/api/projects', { method: 'POST', body: JSON.stringify({ name, slug }) }),
  onSuccess: (p) => {
    createdProject.value = p
    showNewProject.value = false
    newProjectName.value = ''
    newProjectSlug.value = ''
    slugTouched.value = false
    newProjectError.value = null
    qc.invalidateQueries({ queryKey: ['projects'] })
  },
  onError: (e) => {
    newProjectError.value = e instanceof Error ? e.message : 'Failed to create project'
  },
})

const { mutate: updateProject, isPending: updatingProject } = useMutation({
  mutationFn: ({ id, name, slug, passthroughDsn }: { id: string; name: string; slug: string; passthroughDsn: string }) =>
    apiFetch<Project>(`/api/projects/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({ name, slug, passthrough_dsn: passthroughDsn || null }),
    }),
  onSuccess: () => {
    editingProject.value = null
    editError.value = null
    qc.invalidateQueries({ queryKey: ['projects'] })
  },
  onError: (e) => {
    editError.value = e instanceof Error ? e.message : 'Failed to update project'
  },
})

const { mutate: deleteProject, isPending: deleteProjectPending } = useMutation({
  mutationFn: (id: string) => apiFetch(`/api/projects/${id}`, { method: 'DELETE' }),
  onSuccess: () => {
    confirmingDelete.value = null
    deleteConfirmText.value = ''
    expandedProject.value = null
    qc.invalidateQueries({ queryKey: ['projects'] })
    showToast('Project deleted')
  },
  onError: (err) => {
    showToast(err instanceof Error ? err.message : 'Failed to delete project.', 'error')
  },
})

const { mutate: updatePrivacy, isPending: updatingPrivacy } = useMutation({
  mutationFn: ({ id, scrubFields, scrubPatterns }: { id: string; scrubFields: string[]; scrubPatterns: ScrubPattern[] }) =>
    apiFetch<Project>(`/api/projects/${id}/privacy`, {
      method: 'PATCH',
      body: JSON.stringify({ scrub_fields: scrubFields, scrub_patterns: scrubPatterns }),
    }),
  onSuccess: () => {
    privacyProject.value = null
    privacyError.value = null
    qc.invalidateQueries({ queryKey: ['projects'] })
    showToast('Privacy settings saved')
  },
  onError: (e) => {
    privacyError.value = e instanceof Error ? e.message : 'Failed to save privacy settings'
  },
})

function submitPrivacy(e: Event, id: string) {
  e.preventDefault()
  privacyError.value = null
  updatePrivacy({ id, scrubFields: privacyScrubFields.value, scrubPatterns: buildScrubPatterns() })
}

function submitNewProject(e: Event) {
  e.preventDefault()
  newProjectError.value = null
  const name = newProjectName.value.trim()
  const slug = newProjectSlug.value.trim()
  if (!name || !slug) return
  createProject({ name, slug })
}

function openNewProject() {
  createdProject.value = null
  showNewProject.value = true
  newProjectError.value = null
}

function startEdit(p: Project) {
  editingProject.value = p.id
  editName.value = p.name
  editSlug.value = p.slug
  editPassthroughDsn.value = p.passthrough_dsn ?? ''
  editSlugTouched.value = true
  editError.value = null
}

function submitEdit(e: Event, id: string) {
  e.preventDefault()
  editError.value = null
  const name = editName.value.trim()
  const slug = editSlug.value.trim()
  if (!name || !slug) return
  updateProject({ id, name, slug, passthroughDsn: editPassthroughDsn.value.trim() })
}

function startDelete(p: Project) {
  confirmingDelete.value = p.id
  deleteConfirmText.value = ''
}

function submitDelete(p: Project) {
  if (deleteConfirmText.value !== p.slug) return
  deleteProject(p.id)
}

function copyDsn(key: string, projectId: string) {
  navigator.clipboard?.writeText(dsnFor(key, projectId)).catch(() => {})
  showToast('DSN copied')
}

function formatDate(iso: string) {
  return new Date(iso).toLocaleDateString()
}


function copyCreateCmd() {
  navigator.clipboard?.writeText('tindra projects create --name "My App" --slug my-app').catch(() => {})
  showToast('Command copied')
}

function actionKindOf(action: string) {
  if (action.startsWith('auth.')) return 'auth'
  if (action.startsWith('alert.')) return 'alert'
  if (action.startsWith('issue.')) return 'issue'
  if (action.startsWith('release.')) return 'release'
  if (action.startsWith('token.')) return 'token'
  if (action.startsWith('project.')) return 'project'
  return 'other'
}
</script>

<template>
  <div class="page">
    <div class="settings">
      <div class="settings__header">
        <h1>Settings</h1>
        <div class="settings__about">
          <a href="https://tindra.sh" target="_blank" rel="noopener" class="settings__about-link">
            <Icon name="globe" :size="11" />
            tindra.sh
          </a>
          <a href="https://tindra.sh/docs" target="_blank" rel="noopener" class="settings__about-link">
            <Icon name="book-open" :size="11" />
            Docs
          </a>
          <a href="https://github.com/blendbyte/tindra" target="_blank" rel="noopener" class="settings__about-link">
            <Icon name="github" :size="11" />
            GitHub
          </a>
          <button class="settings__about-link" @click="ui.shortcutsOpen = true">
            <Icon name="key-round" :size="11" />
            Shortcuts <kbd class="nav__kbd" style="font-size: 10px; padding: 1px 4px">?</kbd>
          </button>
        </div>
      </div>

      <div class="settings__nav">
        <button
          v-for="t in visibleTabs"
          :key="t"
          :aria-current="tab === t ? 'page' : undefined"
          @click="setTab(t); if (t === 'profile') initProfile()"
        >
          {{ tabLabel(t) }}
        </button>
      </div>

      <!-- Overview tab -->
      <template v-if="tab === 'overview'">
        <div class="pane-head">
          <div>
            <div class="pane-head__title">Instance</div>
            <div class="pane-head__sub mono" style="letter-spacing: 0">
              {{ settings?.version ?? 'dev' }}<template v-if="settings?.commit && settings.commit !== 'unknown'"> &middot; {{ settings.commit }}</template>
            </div>
          </div>
          <div class="overview-chips">
            <div class="overview-chip">
              <span class="overview-chip__label">Database</span>
              <span class="overview-chip__value">{{ healthData ? formatBytes(healthData.db_size_bytes) : '–' }}</span>
            </div>
            <div class="overview-chip">
              <span class="overview-chip__label">Retention</span>
              <span class="overview-chip__value">{{ healthData ? (healthData.retention_days > 0 ? `${healthData.retention_days} days` : 'Forever') : '–' }}</span>
            </div>
          </div>
        </div>

        <div class="pane-head">
          <div class="pane-head__title">Data volumes</div>
        </div>
        <div class="proj-grid" style="margin-bottom: 24px">
          <div class="overview-vol-row overview-vol-row--head">
            <span></span>
            <span>Total</span>
            <span>Last 24h</span>
            <span>Oldest</span>
            <span>Expires in</span>
          </div>
          <div class="overview-vol-row">
            <span class="overview-vol-row__name">Errors</span>
            <span class="mono">{{ healthData ? formatNum(healthData.events_total) : '–' }}</span>
            <span class="mono overview-vol-row__rate">+{{ healthData ? formatNum(healthData.events_24h) : '–' }}</span>
            <span class="overview-vol-row__age">{{ dataAgeDays(healthData?.oldest_event_at) }}</span>
            <span
              class="overview-vol-row__expires mono"
              :style="healthData ? { color: expiresColor(healthData.oldest_event_at, healthData.retention_days) } : {}"
            >{{ healthData ? expiresLabel(healthData.oldest_event_at, healthData.retention_days) : '–' }}</span>
          </div>
          <div class="overview-vol-row">
            <span class="overview-vol-row__name">Traces</span>
            <span class="mono">{{ healthData ? formatNum(healthData.tx_total) : '–' }}</span>
            <span class="mono overview-vol-row__rate">+{{ healthData ? formatNum(healthData.tx_24h) : '–' }}</span>
            <span class="overview-vol-row__age">{{ dataAgeDays(healthData?.oldest_tx_at) }}</span>
            <span
              class="overview-vol-row__expires mono"
              :style="healthData ? { color: expiresColor(healthData.oldest_tx_at, healthData.retention_days) } : {}"
            >{{ healthData ? expiresLabel(healthData.oldest_tx_at, healthData.retention_days) : '–' }}</span>
          </div>
          <div class="overview-vol-row">
            <span class="overview-vol-row__name">Logs</span>
            <span class="mono">{{ healthData ? formatNum(healthData.logs_total) : '–' }}</span>
            <span class="mono overview-vol-row__rate">+{{ healthData ? formatNum(healthData.logs_24h) : '–' }}</span>
            <span class="overview-vol-row__age">{{ dataAgeDays(healthData?.oldest_log_at) }}</span>
            <span
              class="overview-vol-row__expires mono"
              :style="healthData ? { color: expiresColor(healthData.oldest_log_at, healthData.retention_days) } : {}"
            >{{ healthData ? expiresLabel(healthData.oldest_log_at, healthData.retention_days) : '–' }}</span>
          </div>
        </div>

        <template v-if="settings">
          <div class="pane-head">
            <div class="pane-head__title">Usage</div>
            <a v-if="settings.billing_url" :href="settings.billing_url" target="_blank" rel="noopener" class="overview-billing-link">
              <Icon name="credit-card" :size="11" />
              Billing
            </a>
          </div>
          <div class="proj-grid">
            <div class="overview-usage-row">
              <div class="overview-usage-row__meta">
                <span class="overview-usage-row__name">Events / month</span>
                <span class="overview-usage-row__count mono">
                  {{ formatNum(totalMonthlyEvents) }}
                  <span class="overview-limit__sep">/</span>
                  <span v-if="settings.event_limit > 0">{{ formatNum(settings.event_limit) }}</span>
                  <span v-else class="overview-limit__unlimited">∞</span>
                </span>
              </div>
              <div class="overview-bar" :class="settings.event_limit === 0 ? 'overview-bar--unlimited' : ''">
                <div
                  v-if="settings.event_limit > 0"
                  class="overview-bar__fill"
                  :class="globalEventPct >= 100 ? 'overview-bar__fill--danger' : globalEventPct >= 80 ? 'overview-bar__fill--warn' : ''"
                  :style="{ width: `${globalEventPct}%` }"
                />
              </div>
            </div>
            <div class="overview-usage-row">
              <div class="overview-usage-row__meta">
                <span class="overview-usage-row__name">Projects</span>
                <span class="overview-usage-row__count mono">
                  {{ projects.length }}
                  <span class="overview-limit__sep">/</span>
                  <span v-if="settings.project_limit > 0">{{ settings.project_limit }}</span>
                  <span v-else class="overview-limit__unlimited">∞</span>
                </span>
              </div>
              <div class="overview-bar" :class="settings.project_limit === 0 ? 'overview-bar--unlimited' : ''">
                <div
                  v-if="settings.project_limit > 0"
                  class="overview-bar__fill"
                  :style="{ width: `${Math.min(100, Math.round(projects.length / settings.project_limit * 100))}%` }"
                />
              </div>
            </div>
            <div class="overview-usage-row">
              <div class="overview-usage-row__meta">
                <span class="overview-usage-row__name">Users</span>
                <span class="overview-usage-row__count mono">
                  {{ users.length }}
                  <span class="overview-limit__sep">/</span>
                  <span v-if="settings.user_limit > 0">{{ settings.user_limit }}</span>
                  <span v-else class="overview-limit__unlimited">∞</span>
                </span>
              </div>
              <div class="overview-bar" :class="settings.user_limit === 0 ? 'overview-bar--unlimited' : ''">
                <div
                  v-if="settings.user_limit > 0"
                  class="overview-bar__fill"
                  :style="{ width: `${Math.min(100, Math.round(users.length / settings.user_limit * 100))}%` }"
                />
              </div>
            </div>
          </div>
        </template>
      </template>

      <!-- Tokens tab -->
      <template v-if="tab === 'tokens'">
        <div class="pane-head">
          <div>
            <div class="pane-head__title">API tokens</div>
            <div class="pane-head__sub">{{ tokens.length }} {{ tokens.length === 1 ? 'token' : 'tokens' }}</div>
          </div>
          <button v-if="canManageProjects && !showTokenForm" class="btn btn--primary" @click="showTokenForm = true">
            <Icon name="plus" :size="12" />
            Create token
          </button>
        </div>

        <form v-if="showTokenForm && canManageProjects" class="proj-card proj-card--form" style="margin-bottom: 16px" @submit="submitCreate">
          <div class="proj-card__head" style="cursor: default">
            <span class="platform-badge"><Icon name="key" :size="11" /></span>
            <div style="min-width: 0; flex: 1; font-size: var(--text-sm); font-weight: 500; color: var(--text-1)">New token</div>
            <button type="button" class="btn btn--ghost" style="padding: 2px 6px" @click="closeTokenForm"><Icon name="x" :size="12" /></button>
          </div>
          <div class="proj-card__body">
            <template v-if="flashToken">
              <div class="token-flash">
                <code class="token-flash__val">{{ flashToken }}</code>
                <span class="token-flash__hint">Copy it now. You won't see it again.</span>
              </div>
              <div style="margin-top: 12px; display: flex; gap: 8px">
                <button type="button" class="btn btn--primary" @click="copyFlash">
                  <Icon name="copy" :size="12" />
                  Copy &amp; dismiss
                </button>
                <button type="button" class="btn btn--ghost" @click="closeTokenForm">Done</button>
              </div>
            </template>
            <template v-else>
              <div class="proj-config-grid">
                <div v-if="projects.length > 1" class="field">
                  <label class="field__label">Project</label>
                  <select v-model="newTokenProject" class="field__input">
                    <option v-for="p in projects" :key="p.id" :value="p.id">{{ p.name }}</option>
                  </select>
                </div>
                <div class="field">
                  <label class="field__label">Token name</label>
                  <input
                    v-model="newTokenName"
                    class="field__input"
                    placeholder="e.g. CI sourcemap upload"
                    autofocus
                  />
                </div>
                <div class="field" style="grid-column: 1 / -1">
                  <label class="field__label">Access</label>
                  <label class="privacy-check">
                    <input type="checkbox" v-model="newTokenWritable" />
                    <span>Allow writes (MCP actions, issue updates)</span>
                  </label>
                </div>
              </div>
              <div style="display: flex; gap: 8px; margin-top: 14px">
                <button
                  type="submit"
                  class="btn btn--primary"
                  :disabled="!newTokenName.trim() || creatingToken"
                >
                  <Icon name="plus" :size="12" />
                  {{ creatingToken ? 'Creating…' : 'Create token' }}
                </button>
                <button type="button" class="btn btn--ghost" @click="closeTokenForm">Cancel</button>
              </div>
            </template>
          </div>
        </form>

        <table class="token-table">
          <thead>
            <tr>
              <th>Name</th>
              <th v-if="projects.length > 1">Project</th>
              <th>Access</th>
              <th>Created</th>
              <th>Last used</th>
              <th>Expires</th>
              <th />
            </tr>
          </thead>
          <tbody>
            <tr v-for="t in tokens" :key="t.id">
              <td>{{ t.name }}</td>
              <td v-if="projects.length > 1" class="muted">{{ projectByID.get(t.project_id)?.name ?? '–' }}</td>
              <td class="muted">{{ t.writable ? 'Read / Write' : 'Read only' }}</td>
              <td class="muted">{{ formatDate(t.created_at) }}</td>
              <td class="muted">{{ t.last_used_at ? formatDate(t.last_used_at) : 'Never' }}</td>
              <td class="muted">{{ t.expires_at ? formatDate(t.expires_at) : 'Never' }}</td>
              <td class="actions" style="position: relative; overflow: visible">
                <div v-if="canManageProjects" class="user-menu" @click.stop>
                  <button
                    class="user-menu__trigger"
                    :class="{ 'user-menu__trigger--open': openMenuId === t.id }"
                    :aria-expanded="openMenuId === t.id"
                    @click="toggleUserMenu(t.id, $event)"
                  >
                    <Icon name="more-horizontal" :size="15" />
                  </button>
                  <div v-if="openMenuId === t.id" class="user-menu__dropdown">
                    <button class="user-menu__item user-menu__item--danger" @click="revoke(t.id, t.name)">
                      <Icon name="trash-2" :size="13" class="user-menu__item-icon" />
                      Revoke
                    </button>
                  </div>
                </div>
              </td>
            </tr>
            <tr v-if="tokens.length === 0">
              <td :colspan="projects.length > 1 ? 6 : 5" style="color: var(--text-3); text-align: center; padding: 24px">
                No tokens yet.
              </td>
            </tr>
          </tbody>
        </table>
      </template>

      <!-- Projects tab -->
      <template v-else-if="tab === 'projects'">
        <div class="pane-head">
          <div>
            <div class="pane-head__title">Projects</div>
            <div class="pane-head__sub">
              <template v-if="(settings?.project_limit ?? 0) > 0">
                {{ projects.length }} / {{ settings!.project_limit }} projects
              </template>
              <template v-else>
                {{ projects.length === 0 ? 'No projects yet' : `${projects.length} project${projects.length === 1 ? '' : 's'}` }}
              </template>
            </div>
          </div>
          <template v-if="canManageProjects">
            <button
              class="btn btn--primary"
              :class="{ 'btn--disabled': projectLimitReached }"
              :disabled="projectLimitReached"
              :title="projectLimitReached ? `Project limit of ${settings!.project_limit} reached` : undefined"
              @click="!projectLimitReached && openNewProject()"
            >
              <Icon name="plus" :size="12" />
              New project
            </button>
          </template>
          <span v-else class="perm-readonly-badge">read-only</span>
        </div>

        <!-- Global monthly event quota -->
        <div v-if="(settings?.event_limit ?? 0) > 0" class="quota-summary" :class="{ 'quota-summary--over': instanceAtLimit, 'quota-summary--warn': !instanceAtLimit && instanceNearLimit }">
          <div class="quota-summary__row">
            <span class="quota-summary__label">Events this month</span>
            <span class="quota-summary__count">{{ totalMonthlyEvents.toLocaleString() }} / {{ settings!.event_limit.toLocaleString() }}</span>
          </div>
          <div class="quota-summary__bar">
            <div
              class="quota-summary__fill"
              :class="{ 'quota-summary__fill--warn': instanceNearLimit && !instanceAtLimit, 'quota-summary__fill--over': instanceAtLimit }"
              :style="{ transform: `scaleX(${globalEventPct / 100})` }"
            />
          </div>
          <div v-if="instanceAtLimit" class="quota-summary__msg quota-summary__msg--over">
            <Icon name="alert-circle" :size="12" />
            Monthly limit reached. New events are being dropped until the month resets.
            <a v-if="settings?.billing_url" :href="settings.billing_url" target="_blank" rel="noopener" class="quota-summary__billing-link">Manage billing</a>
          </div>
          <div v-else-if="instanceNearLimit" class="quota-summary__msg quota-summary__msg--warn">
            <Icon name="alert-triangle" :size="12" />
            Approaching monthly limit.
            <a v-if="settings?.billing_url" :href="settings.billing_url" target="_blank" rel="noopener" class="quota-summary__billing-link">Manage billing</a>
          </div>
        </div>

        <!-- Success banner after creation -->
        <div v-if="createdProject" class="proj-success">
          <div class="proj-success__check">
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M3 8.5l3 3 7-7" />
            </svg>
          </div>
          <div class="proj-success__body">
            <div class="proj-success__title">Project "{{ createdProject.name }}" created</div>
            <div class="proj-success__sub">Copy your DSN and paste it into your SDK configuration.</div>
            <div class="proj-success__dsn">
              <code class="mono">{{ dsnFor(createdProject.public_key, createdProject.id) }}</code>
              <button class="btn" @click="copyDsn(createdProject.public_key, createdProject.id)">
                <Icon name="copy" :size="12" />
                Copy DSN
              </button>
            </div>
            <div class="proj-success__hint">
              Any Sentry-compatible SDK works. Set <code class="mono">dsn</code> to this value and errors will start arriving.
            </div>
          </div>
          <button class="proj-success__close" @click="createdProject = null">
            <Icon name="x" :size="14" />
          </button>
        </div>

        <!-- New project form -->
        <form v-if="showNewProject" class="proj-card proj-card--form" @submit="submitNewProject">
          <div class="proj-card__head" style="cursor: default">
            <span class="platform-badge">
              <Icon name="package" :size="11" />
            </span>
            <div style="min-width: 0; flex: 1; font-size: var(--text-sm); font-weight: 500; color: var(--text-1)">
              New project
            </div>
            <button type="button" class="btn btn--ghost" style="padding: 2px 6px" @click="showNewProject = false">
              <Icon name="x" :size="12" />
            </button>
          </div>
          <div class="proj-card__body">
            <div class="proj-config-grid">
              <div class="field">
                <label class="field__label" for="proj-name">Name</label>
                <input
                  id="proj-name"
                  v-model="newProjectName"
                  class="field__input"
                  placeholder="My App"
                  autofocus
                />
              </div>
              <div class="field">
                <label class="field__label" for="proj-slug">Slug</label>
                <input
                  id="proj-slug"
                  v-model="newProjectSlug"
                  class="field__input mono"
                  placeholder="my-app"
                  @input="slugTouched = true"
                />
              </div>
            </div>
            <div v-if="newProjectError" class="proj-form-error">{{ newProjectError }}</div>
            <div style="display: flex; gap: 8px; margin-top: 14px">
              <button
                type="submit"
                class="btn btn--primary"
                :disabled="!newProjectName.trim() || !newProjectSlug.trim() || creatingProject"
              >
                {{ creatingProject ? 'Creating…' : 'Create project' }}
              </button>
              <button type="button" class="btn btn--ghost" @click="showNewProject = false">Cancel</button>
            </div>
          </div>
        </form>

        <!-- Empty state (no projects, no form) -->
        <div v-if="projects.length === 0 && !showNewProject && !createdProject" class="settings-empty">
          <div class="settings-empty__icon">
            <Icon name="package" :size="28" />
          </div>
          <div class="settings-empty__title">No projects yet</div>
          <div class="settings-empty__body">
            <template v-if="canManageProjects">Create your first project to get a DSN and start capturing errors and traces.</template>
            <template v-else>No projects have been created yet. Ask an admin to create one.</template>
          </div>
          <button v-if="canManageProjects" class="btn btn--primary" style="margin-top: 4px" @click="openNewProject">
            <Icon name="plus" :size="12" />
            Create project
          </button>
        </div>

        <!-- Project list -->
        <div v-if="projects.length > 0" class="proj-grid" :style="showNewProject ? 'margin-top: 12px' : ''">
          <div
            v-for="p in projects"
            :key="p.id"
            class="proj-card"
            :class="{ 'proj-card--open': expandedProject === p.id }"
          >
            <div class="proj-card__head" @click="expandedProject = expandedProject === p.id ? null : p.id">
              <span class="platform-badge">
                <Icon name="package" :size="11" />
              </span>
              <div style="min-width: 0; flex: 1">
                <div class="proj-card__name">{{ p.name }}</div>
                <div class="proj-card__meta mono">{{ p.slug }}</div>
              </div>
              <span class="rule__caret">
                <Icon :name="expandedProject === p.id ? 'chevron-down' : 'chevron-right'" :size="11" />
              </span>
            </div>
            <div v-if="expandedProject === p.id" class="proj-card__body">
              <!-- Delete confirmation (only reachable when canManageProjects) -->
              <template v-if="confirmingDelete === p.id">
                <div class="proj-delete-confirm">
                  <div class="proj-delete-confirm__title">
                    <Icon name="trash" :size="13" style="color: var(--danger)" />
                    Delete "{{ p.name }}"?
                  </div>
                  <div class="proj-delete-confirm__body">
                    This permanently deletes the project and all its events, issues, and transactions.
                    Type the project slug to confirm.
                  </div>
                  <div class="callback-row" style="margin-top: 10px">
                    <input
                      v-model="deleteConfirmText"
                      class="field__input mono"
                      :placeholder="p.slug"
                    />
                    <button
                      class="btn"
                      style="background: var(--danger); color: #fff; border-color: var(--danger)"
                      :disabled="deleteConfirmText !== p.slug || deleteProjectPending"
                      @click="submitDelete(p)"
                    >
                      {{ deleteProjectPending ? 'Deleting…' : 'Delete permanently' }}
                    </button>
                    <button
                      class="btn btn--ghost"
                      @click="confirmingDelete = null; deleteConfirmText = ''"
                    >
                      Cancel
                    </button>
                  </div>
                </div>
              </template>

              <!-- Edit form -->
              <form v-else-if="editingProject === p.id" @submit="(e) => submitEdit(e, p.id)">
                <div class="proj-config-grid">
                  <div class="field">
                    <label class="field__label" :for="`edit-name-${p.id}`">Name</label>
                    <input
                      :id="`edit-name-${p.id}`"
                      v-model="editName"
                      class="field__input"
                      autofocus
                    />
                  </div>
                  <div class="field">
                    <label class="field__label" :for="`edit-slug-${p.id}`">Slug</label>
                    <input
                      :id="`edit-slug-${p.id}`"
                      v-model="editSlug"
                      class="field__input mono"
                      @input="editSlugTouched = true"
                    />
                  </div>
                  <div class="field" style="grid-column: 1 / -1">
                    <label class="field__label" :for="`edit-passthrough-${p.id}`">Passthrough DSN</label>
                    <input
                      :id="`edit-passthrough-${p.id}`"
                      v-model="editPassthroughDsn"
                      class="field__input mono"
                      type="url"
                      placeholder="https://key@sentry.io/123 or https://key@bugsink.example.com/123"
                      autocomplete="off"
                      spellcheck="false"
                    />
                    <span class="field__hint">Incoming events are forwarded here in the background. Leave blank to disable.</span>
                  </div>
                </div>
                <div v-if="editError" class="proj-form-error">{{ editError }}</div>
                <div style="display: flex; gap: 8px; margin-top: 14px; align-items: center">
                  <button
                    type="submit"
                    class="btn btn--primary"
                    :disabled="!editName.trim() || !editSlug.trim() || updatingProject"
                  >
                    {{ updatingProject ? 'Saving…' : 'Save changes' }}
                  </button>
                  <button type="button" class="btn btn--ghost" @click="editingProject = null">Cancel</button>
                  <button
                    type="button"
                    class="btn btn--ghost"
                    style="color: var(--danger); margin-left: auto"
                    @click="startDelete(p)"
                  >
                    <Icon name="trash" :size="12" />
                    Delete project
                  </button>
                </div>
              </form>

              <!-- Read-only view -->
              <template v-else>
                <div class="proj-config-grid">
                  <template v-if="canManageProjects">
                    <div class="field" style="grid-column: 1 / -1">
                      <label class="field__label">DSN</label>
                      <div class="callback-row">
                        <input
                          class="field__input mono"
                          readonly
                          :value="dsnFor(p.public_key, p.id)"
                        />
                        <button class="btn" type="button" @click="copyDsn(p.public_key, p.id)">
                          <Icon name="copy" :size="12" />
                          Copy
                        </button>
                      </div>
                    </div>
                    <div class="field">
                      <label class="field__label">Public key</label>
                      <input class="field__input mono" readonly :value="p.public_key" />
                    </div>
                    <div class="field">
                      <label class="field__label">Slug</label>
                      <input class="field__input mono" readonly :value="p.slug" />
                    </div>
                    <div v-if="p.passthrough_dsn" class="field" style="grid-column: 1 / -1">
                      <label class="field__label">Passthrough DSN</label>
                      <input class="field__input mono" readonly :value="p.passthrough_dsn" />
                    </div>
                  </template>
                  <div class="field" style="grid-column: 1 / -1">
                    <label class="field__label">Events this month</label>
                    <div class="proj-usage">
                      <span class="proj-usage__count">{{ p.event_count.toLocaleString() }}</span>
                      <template v-if="(settings?.event_limit ?? 0) > 0">
                        <span class="proj-usage__sep">of {{ settings!.event_limit.toLocaleString() }}</span>
                      </template>
                    </div>
                    <template v-if="(settings?.event_limit ?? 0) > 0">
                      <div class="proj-quota-bar">
                        <div
                          class="proj-quota-bar__fill"
                          :class="{
                            'proj-quota-bar__fill--warn': p.event_count / settings!.event_limit >= 0.8 && p.event_count < settings!.event_limit,
                            'proj-quota-bar__fill--over': p.event_count >= settings!.event_limit
                          }"
                          :style="{ transform: `scaleX(${Math.min(1, p.event_count / settings!.event_limit)})` }"
                        />
                      </div>
                    </template>
                    <div v-if="quotaData?.daily_volume?.length" class="proj-sparkline-row">
                      <Sparkline :data="quotaData.daily_volume.map(Number)" :width="120" :height="20" class="proj-sparkline-row__chart" />
                      <span class="proj-sparkline-row__label">30-day trend</span>
                    </div>
                  </div>
                  <div v-if="quotaData && quotaData.rate_limit_per_min > 0" class="field" style="grid-column: 1 / -1">
                    <label class="field__label">Ingest rate (current window)</label>
                    <div class="proj-usage">
                      <span class="proj-usage__count">{{ quotaData.rate_limit_used.toLocaleString() }}</span>
                      <span class="proj-usage__sep">of {{ quotaData.rate_limit_per_min.toLocaleString() }} / min</span>
                      <span v-if="quotaData.rate_limit_reset_at" class="proj-usage__reset">
                        resets {{ new Date(quotaData.rate_limit_reset_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) }}
                      </span>
                    </div>
                    <div class="proj-quota-bar">
                      <div
                        class="proj-quota-bar__fill"
                        :class="{
                          'proj-quota-bar__fill--warn': quotaData.rate_limit_used / quotaData.rate_limit_per_min >= 0.8 && quotaData.rate_limit_used < quotaData.rate_limit_per_min,
                          'proj-quota-bar__fill--over': quotaData.rate_limit_used >= quotaData.rate_limit_per_min
                        }"
                        :style="{ transform: `scaleX(${Math.min(1, quotaData.rate_limit_used / quotaData.rate_limit_per_min)})` }"
                      />
                    </div>
                  </div>
                </div>
                <div v-if="canManageProjects" class="sso-actions" style="margin-top: 16px">
                  <button class="btn" @click="startEdit(p)">
                    <Icon name="pencil" :size="12" />
                    Edit
                  </button>
                  <button class="btn" @click="privacyProject === p.id ? privacyProject = null : openPrivacy(p)">
                    <Icon name="shield" :size="12" />
                    Data privacy
                  </button>
                  <button
                    class="btn btn--ghost"
                    style="color: var(--danger); margin-left: auto"
                    @click="startDelete(p)"
                  >
                    <Icon name="trash" :size="12" />
                    Delete
                  </button>
                </div>

                <!-- Data privacy panel -->
                <form v-if="privacyProject === p.id" class="privacy-panel" @submit="(e) => submitPrivacy(e, p.id)">
                  <div class="privacy-panel__title">
                    <Icon name="shield" :size="13" />
                    Data privacy
                  </div>
                  <div class="privacy-panel__section">
                    <div class="field__label">Redact built-in patterns</div>
                    <div class="privacy-panel__checks">
                      <label class="privacy-check">
                        <input type="checkbox" v-model="privacyBuiltinEmail" />
                        <span>Email addresses</span>
                      </label>
                      <label class="privacy-check">
                        <input type="checkbox" v-model="privacyBuiltinIP" />
                        <span>IP addresses</span>
                      </label>
                    </div>
                    <div class="field__hint">Matching values in any event field are replaced with [Filtered] before storage.</div>
                  </div>
                  <div class="privacy-panel__section">
                    <div class="field__label">Block specific field paths</div>
                    <div class="callback-row" style="margin-bottom: 8px">
                      <input
                        v-model="privacyScrubFieldInput"
                        class="field__input mono"
                        placeholder="request.headers.Authorization"
                        @keydown.enter.prevent="addScrubField"
                      />
                      <button type="button" class="btn" @click="addScrubField">Add</button>
                    </div>
                    <div v-if="privacyScrubFields.length > 0" class="privacy-fields">
                      <div v-for="f in privacyScrubFields" :key="f" class="privacy-field">
                        <code class="mono privacy-field__name">{{ f }}</code>
                        <button type="button" class="privacy-field__remove" @click="removeScrubField(f)">
                          <Icon name="x" :size="11" />
                        </button>
                      </div>
                    </div>
                    <div class="field__hint">Use dot notation, e.g. <code class="mono">request.headers.Cookie</code> or <code class="mono">user.email</code>. The entire value at that path is replaced with [Filtered].</div>
                  </div>
                  <div v-if="privacyError" class="proj-form-error">{{ privacyError }}</div>
                  <div style="display: flex; gap: 8px; margin-top: 14px">
                    <button type="submit" class="btn btn--primary" :disabled="updatingPrivacy">
                      {{ updatingPrivacy ? 'Saving…' : 'Save' }}
                    </button>
                    <button type="button" class="btn btn--ghost" @click="privacyProject = null">Cancel</button>
                  </div>
                </form>
              </template>
            </div>
          </div>
        </div>
      </template>

      <!-- Users tab -->
      <template v-else-if="tab === 'users'">
        <div class="pane-head">
          <div>
            <div class="pane-head__title">Users</div>
            <div class="pane-head__sub">
              <template v-if="(settings?.user_limit ?? 0) > 0">
                {{ users.length }} / {{ settings!.user_limit }} users
              </template>
              <template v-else>
                {{ users.length }} user{{ users.length === 1 ? '' : 's' }}
              </template>
            </div>
          </div>
          <template v-if="canManageUsers">
            <button
              class="btn btn--primary"
              :class="{ 'btn--disabled': userLimitReached }"
              :disabled="userLimitReached"
              :title="userLimitReached ? `User limit of ${settings!.user_limit} reached` : undefined"
              @click="!userLimitReached && (showInviteForm = !showInviteForm)"
            >
              <Icon name="plus" :size="12" />
              Invite user
            </button>
          </template>
          <span v-else class="perm-readonly-badge">read-only</span>
        </div>

        <!-- Invite form -->
        <form v-if="showInviteForm && canManageUsers" class="proj-card proj-card--form" style="margin-bottom: 16px" @submit="submitInvite">
          <div class="proj-card__head" style="cursor: default">
            <span class="platform-badge"><Icon name="user" :size="11" /></span>
            <div style="min-width: 0; flex: 1; font-size: var(--text-sm); font-weight: 500; color: var(--text-1)">Invite user</div>
            <button type="button" class="btn btn--ghost" style="padding: 2px 6px" @click="closeInviteForm"><Icon name="x" :size="12" /></button>
          </div>
          <div class="proj-card__body">
            <template v-if="inviteResult">
              <div v-if="inviteResult.email_sent" class="invite-success">
                <Icon name="check" :size="14" class="invite-success__icon" />
                <div>
                  <div class="invite-success__title">Invite sent</div>
                  <div class="invite-success__body">An invitation email is on its way.</div>
                </div>
              </div>
              <div v-else>
                <div v-if="inviteResult.email_error" class="proj-form-error" style="margin-bottom: 10px">
                  Email delivery failed: {{ inviteResult.email_error }}
                </div>
                <div class="invite-url-box">
                  <div class="invite-url-box__label">
                    {{ inviteResult.email_configured ? 'Email failed. Share this link instead:' : 'No email configured. Share this link instead:' }}
                  </div>
                  <div class="invite-url-box__row">
                    <code class="invite-url-box__url">{{ inviteResult.invite_url }}</code>
                    <button type="button" class="btn btn--ghost invite-url-box__copy" @click="copyInviteURL(inviteResult!.invite_url)">
                      <Icon name="copy" :size="12" />
                    </button>
                  </div>
                </div>
              </div>
              <div style="margin-top: 12px; display: flex; gap: 8px">
                <button type="button" class="btn btn--ghost" @click="closeInviteForm">Done</button>
                <button type="button" class="btn btn--ghost" @click="inviteResult = null">Send another</button>
              </div>
            </template>
            <template v-else>
              <div class="proj-config-grid">
                <div class="field">
                  <label class="field__label" for="invite-email">Email</label>
                  <input id="invite-email" v-model="inviteEmail" class="field__input" type="email" placeholder="name@example.com" autofocus />
                </div>
                <div class="field">
                  <label class="field__label" for="invite-name">Name <span class="muted">(optional)</span></label>
                  <input id="invite-name" v-model="inviteName" class="field__input" placeholder="Full name" />
                </div>
              </div>
              <div v-if="inviteError" class="proj-form-error">{{ inviteError }}</div>
              <div style="display: flex; gap: 8px; margin-top: 14px">
                <button type="submit" class="btn btn--primary" :disabled="!inviteEmail.trim() || creatingInvite">
                  {{ creatingInvite ? 'Sending…' : 'Send invite' }}
                </button>
                <button type="button" class="btn btn--ghost" @click="closeInviteForm">Cancel</button>
              </div>
            </template>
          </div>
        </form>

        <!-- Password reset result banner -->
        <div v-if="resetResult" class="proj-card" style="margin-bottom: 16px">
          <div class="proj-card__body">
            <div v-if="resetResult.email_sent" class="invite-success">
              <Icon name="check" :size="14" class="invite-success__icon" />
              <div>
                <div class="invite-success__title">Reset email sent to {{ resetTarget?.email }}</div>
                <div class="invite-success__body">The link expires in 24 hours.</div>
              </div>
            </div>
            <div v-else>
              <div v-if="resetResult.email_error" class="proj-form-error" style="margin-bottom: 10px">
                Email delivery failed: {{ resetResult.email_error }}
              </div>
              <div class="invite-url-box">
                <div class="invite-url-box__label">No email configured. Share this link with {{ resetTarget?.email }}:</div>
                <div class="invite-url-box__row">
                  <code class="invite-url-box__url">{{ resetResult.reset_url }}</code>
                  <button type="button" class="btn btn--ghost invite-url-box__copy" @click="copyToClipboard(resetResult!.reset_url, 'Reset link')">
                    <Icon name="copy" :size="12" />
                  </button>
                </div>
              </div>
            </div>
            <div style="margin-top: 12px">
              <button type="button" class="btn btn--ghost" @click="closeResetResult">Dismiss</button>
            </div>
          </div>
        </div>

        <div v-if="!canManageUsers" class="settings-empty">
          <div class="settings-empty__icon">
            <Icon name="users" :size="28" />
          </div>
          <div class="settings-empty__title">Users</div>
          <div class="settings-empty__body">You don't have permission to view or manage users.</div>
        </div>

        <table v-else class="token-table team-perms-table" style="width: 100%">
          <thead>
            <tr>
              <th style="min-width: 180px">User</th>
              <th class="perm-col" title="Two-factor authentication">2FA</th>
              <th class="perm-col">Projects</th>
              <th class="perm-col">Users</th>
              <th class="perm-col">Alerts</th>
              <th class="perm-col">Issues</th>
              <th class="muted perm-hint">No box = read-only</th>
              <th style="width: 40px" />
            </tr>
          </thead>
          <tbody>
            <template v-for="u in users" :key="u.id">
              <tr :class="{ 'team-row--self': u.id === me?.id }">
                <td>
                  <div class="team-member">
                    <span class="mono" style="font-size: 12px">{{ u.email }}</span>
                    <span v-if="u.name" class="muted" style="font-size: 11px">{{ u.name }}</span>
                    <span v-if="u.id === me?.id" class="team-badge">you</span>
                  </div>
                </td>
                <td class="perm-col">
                  <span v-if="u.mfa_enabled" class="team-badge team-badge--mfa" title="Two-factor authentication enabled">2FA</span>
                </td>
                <td class="perm-col">
                  <input type="checkbox" class="perm-check" :checked="u.permissions.manage_projects" :disabled="!canManageUsers" @change="togglePerm(u, 'manage_projects')" />
                </td>
                <td class="perm-col">
                  <input type="checkbox" class="perm-check" :checked="u.permissions.manage_users" :disabled="!canManageUsers" @change="togglePerm(u, 'manage_users')" />
                </td>
                <td class="perm-col">
                  <input type="checkbox" class="perm-check" :checked="u.permissions.manage_alerts" :disabled="!canManageUsers" @change="togglePerm(u, 'manage_alerts')" />
                </td>
                <td class="perm-col">
                  <input type="checkbox" class="perm-check" :checked="u.permissions.manage_issues" :disabled="!canManageUsers" @change="togglePerm(u, 'manage_issues')" />
                </td>
                <td class="muted" style="font-size: 11px; white-space: nowrap">joined {{ formatDate(u.created_at) }}</td>
                <td class="actions" style="position: relative; overflow: visible">
                  <div v-if="canManageUsers && u.id !== me?.id" class="user-menu" @click.stop>
                    <button
                      class="user-menu__trigger"
                      :class="{ 'user-menu__trigger--open': openMenuId === u.id }"
                      :aria-expanded="openMenuId === u.id"
                      @click="toggleUserMenu(u.id, $event)"
                    >
                      <Icon name="more-horizontal" :size="15" />
                    </button>
                    <div v-if="openMenuId === u.id" class="user-menu__dropdown">
                      <button class="user-menu__item" :disabled="sendingReset" @click="sendPasswordReset(u.id); closeUserMenus()">
                        <Icon name="mail" :size="13" class="user-menu__item-icon" />
                        Send password reset
                      </button>
                      <button class="user-menu__item" @click="openSetPw(u.id); closeUserMenus()">
                        <Icon name="key-round" :size="13" class="user-menu__item-icon" />
                        Set password
                      </button>
                      <button v-if="u.mfa_enabled" class="user-menu__item" @click="adminRemoveMFA(u.id); closeUserMenus()">
                        <Icon name="shield-off" :size="13" class="user-menu__item-icon" />
                        Remove MFA
                      </button>
                      <div class="user-menu__divider" />
                      <button class="user-menu__item user-menu__item--danger" @click="startUserDelete(u); closeUserMenus()">
                        <Icon name="trash-2" :size="13" class="user-menu__item-icon" />
                        Remove user
                      </button>
                    </div>
                  </div>
                </td>
              </tr>

              <!-- Set password inline form -->
              <tr v-if="setPwTarget === u.id" class="team-action-row">
                <td colspan="8">
                  <div class="team-action-panel">
                    <div class="team-action-panel__title">Set password for {{ u.email }}</div>
                    <div class="proj-config-grid" style="margin-top: 10px">
                      <div class="field">
                        <label class="field__label">New password <span class="muted">(min 12 characters)</span></label>
                        <input v-model="setPwValue" type="password" class="field__input" placeholder="New password" autocomplete="new-password" autofocus />
                      </div>
                      <div class="field">
                        <label class="field__label">Confirm password</label>
                        <input v-model="setPwConfirm" type="password" class="field__input" placeholder="Confirm password" autocomplete="new-password" />
                      </div>
                    </div>
                    <div v-if="setPwError" class="proj-form-error" style="margin-top: 8px">{{ setPwError }}</div>
                    <div style="display: flex; gap: 8px; margin-top: 12px">
                      <button class="btn btn--primary" :disabled="settingPw || !setPwValue || !setPwConfirm" @click="submitSetPw(u.id)">
                        {{ settingPw ? 'Saving…' : 'Set password' }}
                      </button>
                      <button class="btn btn--ghost" @click="cancelSetPw">Cancel</button>
                    </div>
                  </div>
                </td>
              </tr>

              <!-- Delete confirmation step 1 -->
              <tr v-else-if="deleteStep === 1 && deleteTarget?.id === u.id" class="team-action-row">
                <td colspan="8">
                  <div class="team-action-panel team-action-panel--danger">
                    <div class="team-action-panel__title">Remove {{ u.email }}?</div>
                    <div class="team-action-panel__body">Their account and all associated data will be permanently deleted.</div>
                    <div style="display: flex; gap: 8px; margin-top: 12px">
                      <button class="btn btn--ghost" style="color: var(--danger)" @click="proceedUserDelete">Yes, continue</button>
                      <button class="btn btn--ghost" @click="cancelUserDelete">Cancel</button>
                    </div>
                  </div>
                </td>
              </tr>

              <!-- Delete confirmation step 2: type email -->
              <tr v-else-if="deleteStep === 2 && deleteTarget?.id === u.id" class="team-action-row">
                <td colspan="8">
                  <div class="team-action-panel team-action-panel--danger">
                    <div class="team-action-panel__title">Final confirmation</div>
                    <div class="team-action-panel__body">Type <strong>{{ u.email }}</strong> to confirm removal.</div>
                    <div style="margin-top: 10px; display: flex; gap: 8px; align-items: center">
                      <input v-model="deleteEmailInput" class="field__input" style="max-width: 280px" :placeholder="u.email" />
                      <button
                        class="btn btn--ghost"
                        style="color: var(--danger)"
                        :disabled="deleteEmailInput !== u.email"
                        @click="proceedUserDelete"
                      >
                        Remove
                      </button>
                      <button class="btn btn--ghost" @click="cancelUserDelete">Cancel</button>
                    </div>
                  </div>
                </td>
              </tr>
            </template>

            <tr v-if="users.length === 0">
              <td colspan="8" style="color: var(--text-3); text-align: center; padding: 24px">No users yet.</td>
            </tr>
          </tbody>
        </table>

        <!-- Pending invites -->
        <template v-if="canManageUsers && invites.length > 0">
          <div class="pane-head" style="margin-top: 28px; margin-bottom: 12px">
            <div class="pane-head__title" style="font-size: var(--text-sm)">Pending invites</div>
          </div>
          <table class="token-table">
            <thead>
              <tr>
                <th>Email</th>
                <th>Name</th>
                <th>Expires</th>
                <th style="width: 40px" />
              </tr>
            </thead>
            <tbody>
              <tr v-for="inv in invites" :key="inv.token">
                <td class="mono" style="font-size: 12px">{{ inv.email }}</td>
                <td class="muted" style="font-size: 12px">{{ inv.name || '–' }}</td>
                <td class="muted" style="font-size: 11px">{{ formatDate(inv.expires_at) }}</td>
                <td class="actions" style="position: relative; overflow: visible">
                  <div class="user-menu" @click.stop>
                    <button
                      class="user-menu__trigger"
                      :class="{ 'user-menu__trigger--open': openMenuId === inv.token }"
                      :aria-expanded="openMenuId === inv.token"
                      @click="toggleUserMenu(inv.token, $event)"
                    >
                      <Icon name="more-horizontal" :size="15" />
                    </button>
                    <div v-if="openMenuId === inv.token" class="user-menu__dropdown">
                      <button class="user-menu__item" @click="copyInviteURL(inviteURL(inv.token)); closeUserMenus()">
                        <Icon name="copy" :size="13" class="user-menu__item-icon" />
                        Copy link
                      </button>
                      <div class="user-menu__divider" />
                      <button class="user-menu__item user-menu__item--danger" @click="revokeInvite(inv.token); closeUserMenus()">
                        <Icon name="trash-2" :size="13" class="user-menu__item-icon" />
                        Revoke
                      </button>
                    </div>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </template>
      </template>

      <!-- Alerts tab -->
      <template v-else-if="tab === 'alerts'">
        <div class="pane-head">
          <div>
            <div class="pane-head__title">Alert rules</div>
            <div class="pane-head__sub">{{ alertRules.length }} {{ alertRules.length === 1 ? 'rule' : 'rules' }}</div>
          </div>
          <button v-if="canManageAlerts" class="btn btn--primary" @click="openNewRule()">
            <Icon name="plus" :size="12" />
            New rule
          </button>
        </div>

        <div v-if="showNewRule" class="alert-create">
          <div class="alert-create__title">Create alert rule</div>
          <div class="alert-create__grid">
            <div class="field">
              <label class="field__label">Name</label>
              <input v-model="newRule.name" class="field__input" placeholder="e.g. High error rate" />
            </div>
            <div v-if="alertProjects.length > 0" class="field" style="grid-column: 1 / -1">
              <label class="field__label">Projects <span style="font-weight: normal; color: var(--text-2)">(none = global)</span></label>
              <div class="rule__project-checks">
                <label v-for="p in alertProjects" :key="p.id" class="rule__project-check">
                  <input type="checkbox" :value="p.id" v-model="newRule.projectIDs" />
                  {{ p.name }}
                </label>
              </div>
            </div>
            <div class="field">
              <label class="field__label">Trigger</label>
              <select v-model="newRule.trigger" class="field__input">
                <option value="new_issue">New issue</option>
                <option value="regressed">Regression (resolved issue breaks again)</option>
                <option value="new_or_regressed">New issue or regression</option>
                <option value="event_count">Event count above threshold</option>
                <option value="cron_missed">Cron monitor missed check-in</option>
                <option value="cron_error">Cron monitor check-in error</option>
              </select>
            </div>
            <template v-if="newRule.trigger === 'event_count'">
              <div class="field">
                <label class="field__label">Threshold (events)</label>
                <input v-model.number="newRule.threshold" class="field__input" type="number" min="1" />
              </div>
              <div class="field">
                <label class="field__label">Window (minutes)</label>
                <input v-model.number="newRule.window_mins" class="field__input" type="number" min="1" />
              </div>
            </template>
            <div class="field">
              <label class="field__label">Channel</label>
              <select v-model="newRule.channel" class="field__input">
                <option value="webhook">Webhook</option>
                <option value="slack">Slack</option>
                <option value="discord">Discord</option>
                <option value="email">Email</option>
              </select>
            </div>
            <div v-if="newRule.channel === 'webhook' || newRule.channel === 'slack' || newRule.channel === 'discord'" class="field" style="grid-column: 1 / -1">
              <label class="field__label">{{ newRule.channel === 'discord' ? 'Discord webhook URL' : newRule.channel === 'slack' ? 'Slack webhook URL' : 'Webhook URL' }}</label>
              <input v-model="newRule.webhook_url" class="field__input" type="url" :placeholder="newRule.channel === 'discord' ? 'https://discord.com/api/webhooks/...' : newRule.channel === 'slack' ? 'https://hooks.slack.com/services/...' : 'https://hooks.example.com/...'" />
            </div>
            <div v-else class="field">
              <label class="field__label">Email to</label>
              <input v-model="newRule.email_to" class="field__input" type="email" placeholder="you@example.com" />
            </div>
            <div class="field">
              <label class="field__label">Cooldown (minutes)</label>
              <input v-model.number="newRule.cooldown_mins" class="field__input" type="number" min="1" />
            </div>
            <div class="field">
              <label class="field__label">Min. level</label>
              <select v-model="newRule.filter_level" class="field__input">
                <option value="">Any level</option>
                <option value="fatal">Fatal only</option>
                <option value="error">Error and above</option>
                <option value="warning">Warning and above</option>
                <option value="info">Info and above</option>
                <option value="performance">Performance issues only</option>
              </select>
            </div>
            <div class="field">
              <label class="field__label">Environment</label>
              <input v-model="newRule.filter_environment" class="field__input" placeholder="e.g. production" />
            </div>
            <div v-if="newRule.trigger !== 'event_count'" class="field">
              <label class="field__label">Min. occurrences</label>
              <input v-model.number="newRule.min_occurrences" class="field__input" type="number" min="0" placeholder="Any" />
            </div>
          </div>
          <div style="display: flex; gap: 8px; margin-top: 14px">
            <button class="btn btn--primary" :disabled="creatingRule" @click="createAlertRule()">
              {{ creatingRule ? 'Creating…' : 'Create rule' }}
            </button>
            <button class="btn btn--ghost" @click="showNewRule = false; resetNewRule()">Cancel</button>
          </div>
        </div>

        <div v-if="alertRules.length === 0 && !showNewRule" style="padding: 32px 0; color: var(--text-2); font-size: var(--text-sm); text-align: center">
          No alert rules configured. Create one to get notified when issues occur.
        </div>

        <div v-for="rule in alertRules" :key="rule.id" class="rule" :class="{ 'rule--paused': !rule.enabled }">
          <div class="rule__head" @click="expandedRule = expandedRule === rule.id ? null : rule.id">
            <button
              v-if="canManageAlerts"
              class="rule__toggle"
              :title="rule.enabled ? 'Disable rule' : 'Enable rule'"
              @click.stop="toggleAlertRule({ id: rule.id, enabled: !rule.enabled })"
            >
              <Icon :name="rule.enabled ? 'check' : 'pause'" :size="11" />
            </button>
            <div class="rule__main">
              <div class="rule__name">
                {{ rule.name }}
                <span v-if="!rule.enabled" class="rule__paused-tag">paused</span>
              </div>
              <div class="rule__cond">
                {{ triggerLabel(rule) }} · {{ rule.channel }} · {{ ruleProjectNames(rule) }}
              </div>
            </div>
            <div class="rule__channels">
              <span class="rule__chan" :title="rule.channel === 'email' ? (rule.email_to ?? '') : (rule.webhook_url ?? '')">
                <Icon :name="rule.channel === 'discord' ? 'discord' : rule.channel === 'slack' ? 'slack' : rule.channel === 'webhook' ? 'send' : 'mail'" :size="11" />
              </span>
            </div>
            <div class="rule__stats">
              <span>{{ rule.last_fired_at ? formatRel(rule.last_fired_at) : 'never fired' }}</span>
            </div>
            <span class="rule__caret">
              <Icon :name="expandedRule === rule.id ? 'chevron-down' : 'chevron-right'" :size="11" />
            </span>
          </div>
          <div v-if="expandedRule === rule.id" class="rule__body">
            <!-- Edit form -->
            <template v-if="editingRuleID === rule.id">
              <div class="alert-create__grid">
                <div class="field">
                  <label class="field__label">Name</label>
                  <input v-model="editRule.name" class="field__input" />
                </div>
                <div v-if="alertProjects.length > 0" class="field" style="grid-column: 1 / -1">
                  <label class="field__label">Projects <span style="font-weight: normal; color: var(--text-2)">(none = global)</span></label>
                  <div class="rule__project-checks">
                    <label v-for="p in alertProjects" :key="p.id" class="rule__project-check">
                      <input type="checkbox" :value="p.id" v-model="editRule.projectIDs" />
                      {{ p.name }}
                    </label>
                  </div>
                </div>
                <div class="field">
                  <label class="field__label">Trigger</label>
                  <select v-model="editRule.trigger" class="field__input">
                    <option value="new_issue">New issue</option>
                    <option value="regressed">Regression (resolved issue breaks again)</option>
                    <option value="new_or_regressed">New issue or regression</option>
                    <option value="event_count">Event count above threshold</option>
                    <option value="cron_missed">Cron monitor missed check-in</option>
                    <option value="cron_error">Cron monitor check-in error</option>
                  </select>
                </div>
                <template v-if="editRule.trigger === 'event_count'">
                  <div class="field">
                    <label class="field__label">Threshold (events)</label>
                    <input v-model.number="editRule.threshold" class="field__input" type="number" min="1" />
                  </div>
                  <div class="field">
                    <label class="field__label">Window (minutes)</label>
                    <input v-model.number="editRule.window_mins" class="field__input" type="number" min="1" />
                  </div>
                </template>
                <div class="field">
                  <label class="field__label">Channel</label>
                  <select v-model="editRule.channel" class="field__input">
                    <option value="webhook">Webhook</option>
                    <option value="slack">Slack</option>
                    <option value="discord">Discord</option>
                    <option value="email">Email</option>
                  </select>
                </div>
                <div v-if="editRule.channel === 'webhook' || editRule.channel === 'slack' || editRule.channel === 'discord'" class="field" style="grid-column: 1 / -1">
                  <label class="field__label">{{ editRule.channel === 'discord' ? 'Discord webhook URL' : editRule.channel === 'slack' ? 'Slack webhook URL' : 'Webhook URL' }}</label>
                  <input v-model="editRule.webhook_url" class="field__input" type="url" :placeholder="editRule.channel === 'discord' ? 'https://discord.com/api/webhooks/...' : editRule.channel === 'slack' ? 'https://hooks.slack.com/services/...' : 'https://hooks.example.com/...'" />
                </div>
                <div v-else class="field">
                  <label class="field__label">Email to</label>
                  <input v-model="editRule.email_to" class="field__input" type="email" placeholder="you@example.com" />
                </div>
                <div class="field">
                  <label class="field__label">Cooldown (minutes)</label>
                  <input v-model.number="editRule.cooldown_mins" class="field__input" type="number" min="1" />
                </div>
                <div class="field">
                  <label class="field__label">Min. level</label>
                  <select v-model="editRule.filter_level" class="field__input">
                    <option value="">Any level</option>
                    <option value="fatal">Fatal only</option>
                    <option value="error">Error and above</option>
                    <option value="warning">Warning and above</option>
                    <option value="info">Info and above</option>
                    <option value="performance">Performance issues only</option>
                  </select>
                </div>
                <div class="field">
                  <label class="field__label">Environment</label>
                  <input v-model="editRule.filter_environment" class="field__input" placeholder="e.g. production" />
                </div>
                <div v-if="editRule.trigger !== 'event_count'" class="field">
                  <label class="field__label">Min. occurrences</label>
                  <input v-model.number="editRule.min_occurrences" class="field__input" type="number" min="0" placeholder="Any" />
                </div>
              </div>
              <div style="display: flex; gap: 8px; margin-top: 14px">
                <button class="btn btn--primary" :disabled="savingRule" @click="saveAlertRule({ id: rule.id })">
                  {{ savingRule ? 'Saving…' : 'Save' }}
                </button>
                <button class="btn btn--ghost" @click="cancelEditRule()">Cancel</button>
                <button
                  class="btn btn--ghost"
                  style="color: var(--danger); margin-left: auto"
                  @click="deleteAlertRule({ id: rule.id })"
                >
                  Delete rule
                </button>
              </div>
            </template>

            <!-- Detail view -->
            <template v-else>
              <div class="rule__detail-grid">
                <span class="rule__detail-k">Projects</span>
                <span class="rule__detail-v">{{ ruleProjectNames(rule) }}</span>
                <span class="rule__detail-k">Trigger</span>
                <span class="rule__detail-v">{{ triggerLabel(rule) }}</span>
                <template v-if="rule.trigger === 'event_count'">
                  <span class="rule__detail-k">Threshold</span>
                  <span class="rule__detail-v">{{ rule.threshold }} events</span>
                  <span class="rule__detail-k">Window</span>
                  <span class="rule__detail-v">{{ rule.window_mins }} minutes</span>
                </template>
                <span class="rule__detail-k">Channel</span>
                <span class="rule__detail-v">{{ rule.channel }}</span>
                <template v-if="rule.channel === 'webhook' || rule.channel === 'slack' || rule.channel === 'discord'">
                  <span class="rule__detail-k">URL</span>
                  <span class="rule__detail-v mono" style="overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{{ rule.webhook_url }}</span>
                </template>
                <template v-else>
                  <span class="rule__detail-k">Email to</span>
                  <span class="rule__detail-v">{{ rule.email_to }}</span>
                </template>
                <span class="rule__detail-k">Cooldown</span>
                <span class="rule__detail-v">{{ rule.cooldown_mins }}m</span>
                <template v-if="rule.filter_level">
                  <span class="rule__detail-k">Min. level</span>
                  <span class="rule__detail-v">{{ rule.filter_level === 'performance' ? 'Performance issues only' : `${rule.filter_level} and above` }}</span>
                </template>
                <template v-if="rule.filter_environment">
                  <span class="rule__detail-k">Environment</span>
                  <span class="rule__detail-v">{{ rule.filter_environment }}</span>
                </template>
                <template v-if="rule.min_occurrences">
                  <span class="rule__detail-k">Min. occurrences</span>
                  <span class="rule__detail-v">{{ rule.min_occurrences }}</span>
                </template>
                <span class="rule__detail-k">Last fired</span>
                <span class="rule__detail-v">{{ rule.last_fired_at ? formatRel(rule.last_fired_at) : 'never' }}</span>
              </div>
              <div v-if="canManageAlerts" class="rule__actions">
                <button
                  class="btn btn--primary"
                  :disabled="testingRuleID === rule.id"
                  @click="testAlertRule({ id: rule.id })"
                >
                  <Icon name="send" :size="12" />
                  {{ testingRuleID === rule.id ? 'Sending…' : 'Send test' }}
                </button>
                <button class="btn" @click="startEditRule(rule)">
                  <Icon name="pencil" :size="12" />
                  Edit
                </button>
                <button
                  class="btn btn--ghost"
                  style="color: var(--danger); margin-left: auto"
                  @click="deleteAlertRule({ id: rule.id })"
                >
                  Delete rule
                </button>
              </div>
            </template>
          </div>
        </div>
      </template>

      <!-- Audit log tab -->
      <template v-else-if="tab === 'audit'">
        <div class="pane-head">
          <div>
            <div class="pane-head__title">Audit log</div>
            <div class="pane-head__sub">{{ auditLog.length }} events</div>
          </div>
        </div>

        <div class="audit-toolbar">
          <button
            v-for="k in auditKinds"
            :key="k"
            class="audit-chip"
            :class="{ 'audit-chip--active': k === auditKindFilter }"
            @click="auditKindFilter = k"
          >
            {{ k }}
          </button>
          <div class="filterbar__search" style="margin-left: auto; min-width: 240px">
            <Icon name="search" :size="12" style="color: var(--text-3)" />
            <input
              v-model="auditSearch"
              placeholder="Search audit log..."
            />
          </div>
        </div>

        <div class="audit-list">
          <div
            v-for="row in auditLog"
            :key="row.id"
            class="audit-row"
          >
            <span class="audit-row__when mono">{{ formatRel(row.created_at) }}</span>
            <span class="audit-row__kind" :class="`audit-row__kind--${actionKindOf(row.event_type)}`">
              {{ actionKindOf(row.event_type) }}
            </span>
            <span class="audit-row__actor">{{ row.actor_email ?? 'system' }}</span>
            <span class="audit-row__action mono">{{ row.event_type }}</span>
            <span class="audit-row__ip mono">{{ row.ip }}</span>
          </div>
          <div v-if="auditLog.length === 0" class="settings-empty">
            <div class="settings-empty__icon">
              <Icon name="shield" :size="28" />
            </div>
            <div class="settings-empty__title">No audit events yet</div>
            <div class="settings-empty__body">Logins, project changes, and token management actions will appear here.</div>
          </div>
        </div>
      </template>

      <!-- Profile tab -->
      <template v-else-if="tab === 'profile'">
        <div class="pane-head">
          <div class="pane-head__title">Profile</div>
        </div>

        <!-- Personal information -->
        <div class="profile-section">
          <div class="profile-section__title">Personal information</div>
          <div class="profile-section__body">
            <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 12px; max-width: 480px">
              <div class="field">
                <label class="field__label">Name</label>
                <input v-model="profileName" class="field__input" :placeholder="me?.name || 'Your name'" />
              </div>
              <div class="field">
                <label class="field__label">Email</label>
                <input v-model="profileEmail" class="field__input" type="email" :placeholder="me?.email ?? 'you@example.com'" />
              </div>
              <div style="grid-column: 1 / -1; display: flex; gap: 8px">
                <button class="btn btn--primary" @click="updateProfile({ name: profileName, email: profileEmail, weekly_digest: profileWeeklyDigest })">Save changes</button>
                <button class="btn btn--ghost" @click="initProfile">Cancel</button>
              </div>
            </div>
          </div>
        </div>

        <!-- Notifications -->
        <div class="profile-section">
          <div class="profile-section__title">Notifications</div>
          <div class="profile-section__body">
            <label class="toggle-row">
              <span class="toggle-row__label">
                Weekly digest
                <span class="toggle-row__hint">Receive a summary of errors and performance every Monday morning.</span>
              </span>
              <button
                class="toggle"
                :class="{ 'toggle--on': profileWeeklyDigest }"
                role="switch"
                :aria-checked="profileWeeklyDigest"
                @click="profileWeeklyDigest = !profileWeeklyDigest; updateProfile({ name: profileName, email: profileEmail, weekly_digest: profileWeeklyDigest })"
              >
                <span class="toggle__knob" />
              </button>
            </label>
          </div>
        </div>

        <!-- Password (only for password-based accounts) -->
        <div v-if="me?.has_password" class="profile-section">
          <div class="profile-section__title">Password</div>
          <div class="profile-section__body">
            <div style="display: flex; flex-direction: column; gap: 12px; max-width: 320px">
              <div class="field">
                <label class="field__label">Current password</label>
                <input v-model="pwCurrent" class="field__input" type="password" autocomplete="current-password" />
              </div>
              <div class="field">
                <label class="field__label">New password</label>
                <input v-model="pwNew" class="field__input" type="password" autocomplete="new-password" placeholder="12+ characters" />
              </div>
              <div class="field">
                <label class="field__label">Confirm new password</label>
                <input v-model="pwConfirm" class="field__input" type="password" autocomplete="new-password" />
              </div>
              <div v-if="pwError" class="profile-error">{{ pwError }}</div>
              <div style="display: flex; gap: 8px">
                <button class="btn btn--primary" :disabled="changingPw || !pwCurrent || !pwNew || !pwConfirm" @click="submitPasswordChange">
                  {{ changingPw ? 'Changing…' : 'Change password' }}
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- Two-factor authentication -->
        <div class="profile-section">
          <div class="profile-section__title">Two-factor authentication</div>
          <div class="profile-section__body">

            <!-- MFA disabled, no setup in progress -->
            <template v-if="!me?.mfa_enabled && !mfaSetupData">
              <div style="display: flex; align-items: center; gap: 16px">
                <div>
                  <div style="font-size: var(--text-sm); color: var(--text-2)">
                    Add a second layer of security to your account using a TOTP authenticator app.
                  </div>
                </div>
              </div>
              <div style="margin-top: 14px">
                <button class="btn btn--primary" :disabled="loadingMFASetup" @click="startMFASetup()">
                  {{ loadingMFASetup ? 'Generating…' : 'Enable two-factor auth' }}
                </button>
              </div>
            </template>

            <!-- MFA setup in progress -->
            <template v-else-if="mfaSetupData">
              <div class="mfa-setup-card">
                <div class="mfa-setup-card__step">
                  <span class="mfa-setup-card__num">1</span>
                  Scan this QR code with your authenticator app (Google Authenticator, Authy, 1Password, etc.).
                </div>
                <div class="mfa-qr">
                  <img :src="mfaSetupData.qr" alt="TOTP QR code" width="180" height="180" class="mfa-qr__img" />
                </div>
                <button class="mfa-secret-toggle" @click="showMFASecret = !showMFASecret">
                  <Icon name="chevron-right" :size="11" :style="showMFASecret ? 'transform:rotate(90deg)' : ''" />
                  Can't scan? Enter the key manually
                </button>
                <div v-if="showMFASecret" class="mfa-setup-card__secret-row">
                  <code class="mfa-setup-card__secret">{{ mfaSetupData.secret }}</code>
                  <button class="btn btn--ghost" style="height: 28px; padding: 0 8px; font-size: var(--text-xs)" @click="copyToClipboard(mfaSetupData!.secret, 'Secret key')">
                    <Icon name="copy" :size="11" /> Copy
                  </button>
                </div>
                <div class="mfa-setup-card__step" style="margin-top: 20px">
                  <span class="mfa-setup-card__num">2</span>
                  Enter the 6-digit code from your app to confirm setup.
                </div>
                <div style="display: flex; gap: 8px; align-items: center; margin-top: 10px">
                  <input
                    v-model="mfaCode"
                    class="field__input mfa-setup-card__code-input"
                    placeholder="000000"
                    maxlength="6"
                    inputmode="numeric"
                    autocomplete="one-time-code"
                    @keydown.enter="confirmMFASetup(mfaCode)"
                  />
                  <button class="btn btn--primary" :disabled="mfaCode.length < 6 || confirmingMFA" @click="confirmMFASetup(mfaCode)">
                    {{ confirmingMFA ? 'Verifying…' : 'Confirm' }}
                  </button>
                  <button class="btn btn--ghost" @click="cancelMFASetup">Cancel</button>
                </div>
                <div v-if="mfaSetupError" class="profile-error" style="margin-top: 8px">{{ mfaSetupError }}</div>
              </div>
            </template>

            <!-- MFA enabled -->
            <template v-else-if="me?.mfa_enabled">
              <div style="display: flex; align-items: center; gap: 12px">
                <span class="mfa-badge">
                  <Icon name="shield-check" :size="12" />
                  Enabled
                </span>
                <span style="font-size: var(--text-sm); color: var(--text-2)">Your account is protected with two-factor authentication.</span>
              </div>
              <div v-if="!showDisableMFA" style="margin-top: 14px">
                <button class="btn btn--ghost" style="color: var(--danger, #ef4444)" @click="showDisableMFA = true; mfaDisableError = null">
                  Disable two-factor auth
                </button>
              </div>
              <div v-else style="margin-top: 14px; display: flex; flex-direction: column; gap: 10px; max-width: 280px">
                <div class="field">
                  <label class="field__label">Confirm your password to disable 2FA</label>
                  <input v-model="mfaDisablePassword" class="field__input" type="password" autocomplete="current-password" />
                </div>
                <div v-if="mfaDisableError" class="profile-error">{{ mfaDisableError }}</div>
                <div style="display: flex; gap: 8px">
                  <button class="btn btn--primary" :disabled="!mfaDisablePassword || disablingMFA" @click="disableMFA(mfaDisablePassword)" style="background: var(--danger, #ef4444); border-color: var(--danger, #ef4444)">
                    {{ disablingMFA ? 'Disabling…' : 'Disable 2FA' }}
                  </button>
                  <button class="btn btn--ghost" @click="showDisableMFA = false; mfaDisablePassword = ''; mfaDisableError = null">Cancel</button>
                </div>
              </div>
            </template>

          </div>
        </div>
      </template>
    </div>
  </div>
</template>
