<script setup lang="ts">
import { ref, computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useToast } from '@/composables/useToast'
import { useClipboard } from '@/composables/useClipboard'
import { ApiError, apiFetch } from '@/api/client'
import type { Invite, User, UserPermissions } from '@/api/types'
import Icon from '@/components/Icon.vue'
import { useFormatDate, usePermissions, useRowMenu, useServerSettings, useUsers } from './shared'

const { show: showToast } = useToast()
const { copy } = useClipboard()
const qc = useQueryClient()
const formatDate = useFormatDate()
const { me, canManageUsers } = usePermissions()
const { users } = useUsers()
const { settings } = useServerSettings()
const { openMenuId, toggleUserMenu, closeUserMenus } = useRowMenu()

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
  onError: (e) => {
    // 409: the change would leave nobody able to manage users.
    if (e instanceof ApiError && e.status === 409) showToast(e.message.trim(), 'error')
    else showToast('Permissions not saved. Try again.')
  },
})

function togglePerm(user: User, perm: keyof UserPermissions, e?: Event) {
  // Keep the box on the saved value; a successful save re-renders it, and a
  // refused one must not leave it showing a change that never happened.
  if (e?.target instanceof HTMLInputElement) e.target.checked = user.permissions[perm]
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

// Invite form
const showInviteForm = ref(false)
const inviteEmail = ref('')
const inviteName = ref('')
const inviteError = ref<string | null>(null)
type InviteResult = { invite_url: string; email_sent: boolean; email_configured: boolean; email_error: string }
const inviteResult = ref<InviteResult | null>(null)

const { data: invitesData } = useQuery({
  queryKey: ['invites'],
  queryFn: ({ signal }) => apiFetch<Invite[]>('/api/invites', { signal }),
  enabled: () => !!me.value?.permissions.manage_users,
})
const invites = computed(() => invitesData.value ?? [])

const { mutate: createInvite, isPending: creatingInvite } = useMutation({
  mutationFn: ({ email, name }: { email: string; name: string }) =>
    apiFetch<InviteResult>('/api/invites', {
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
  mutationFn: (id: string) => apiFetch(`/api/invites/${id}`, { method: 'DELETE' }),
  onSuccess: () => {
    qc.invalidateQueries({ queryKey: ['invites'] })
    showToast('Invite revoked')
  },
})

function copyInviteURL(url: string) {
  copy(url, 'Invite link copied')
}

function copyToClipboard(text: string, label: string) {
  copy(text, `${label} copied`)
}
</script>

<template>
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
      <button type="button" class="btn btn--ghost" style="padding: 2px 6px" aria-label="Close invite form" @click="closeInviteForm"><Icon name="x" :size="12" /></button>
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
              <button type="button" class="btn btn--ghost invite-url-box__copy" aria-label="Copy invite link" @click="copyInviteURL(inviteResult!.invite_url)">
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
            <button type="button" class="btn btn--ghost invite-url-box__copy" aria-label="Copy reset link" @click="copyToClipboard(resetResult!.reset_url, 'Reset link')">
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
            <input type="checkbox" class="perm-check" :checked="u.permissions.manage_projects" :disabled="!canManageUsers" @change="togglePerm(u, 'manage_projects', $event)" />
          </td>
          <td class="perm-col">
            <input type="checkbox" class="perm-check" :checked="u.permissions.manage_users" :disabled="!canManageUsers" @change="togglePerm(u, 'manage_users', $event)" />
          </td>
          <td class="perm-col">
            <input type="checkbox" class="perm-check" :checked="u.permissions.manage_alerts" :disabled="!canManageUsers" @change="togglePerm(u, 'manage_alerts', $event)" />
          </td>
          <td class="perm-col">
            <input type="checkbox" class="perm-check" :checked="u.permissions.manage_issues" :disabled="!canManageUsers" @change="togglePerm(u, 'manage_issues', $event)" />
          </td>
          <td class="muted" style="font-size: 11px; white-space: nowrap">joined {{ formatDate(u.created_at) }}</td>
          <td class="actions" style="position: relative; overflow: visible">
            <div v-if="canManageUsers && u.id !== me?.id" class="user-menu" @click.stop>
              <button
                class="user-menu__trigger"
                :class="{ 'user-menu__trigger--open': openMenuId === u.id }"
                aria-label="User actions"
                aria-haspopup="true"
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
        <tr v-for="inv in invites" :key="inv.id">
          <td class="mono" style="font-size: 12px">{{ inv.email }}</td>
          <td class="muted" style="font-size: 12px">{{ inv.name || '–' }}</td>
          <td class="muted" style="font-size: 11px">{{ formatDate(inv.expires_at) }}</td>
          <td class="actions" style="position: relative; overflow: visible">
            <div class="user-menu" @click.stop>
              <button
                class="user-menu__trigger"
                :class="{ 'user-menu__trigger--open': openMenuId === inv.id }"
                aria-label="Invite actions"
                aria-haspopup="true"
                :aria-expanded="openMenuId === inv.id"
                @click="toggleUserMenu(inv.id, $event)"
              >
                <Icon name="more-horizontal" :size="15" />
              </button>
              <div v-if="openMenuId === inv.id" class="user-menu__dropdown">
                <button class="user-menu__item user-menu__item--danger" @click="revokeInvite(inv.id); closeUserMenus()">
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
