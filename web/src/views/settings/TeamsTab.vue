<script setup lang="ts">
import { ref } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { vClickable } from '@/directives/clickable'
import { apiFetch, ApiError } from '@/api/client'
import type { Team } from '@/api/types'
import { useToast } from '@/composables/useToast'
import Icon from '@/components/Icon.vue'
import { useAlertImpactConfirm, useTeams, usePermissions, useUsers } from './shared'

const { show: showToast } = useToast()
const confirmAlertImpact = useAlertImpactConfirm()
const qc = useQueryClient()
const { canManageUsers } = usePermissions()
const { teams } = useTeams(canManageUsers)
const { users } = useUsers(canManageUsers)

function invalidate() {
  qc.invalidateQueries({ queryKey: ['teams'] })
  qc.invalidateQueries({ queryKey: ['project-teams'] })
  qc.invalidateQueries({ queryKey: ['projects'] })
  qc.invalidateQueries({ queryKey: ['users'] })
}

function nameTaken(e: unknown): boolean {
  return e instanceof ApiError && e.status === 409
}

// ── Create ────────────────────────────────────────────────────────────────────
const newName = ref('')
const createError = ref<string | null>(null)

const { mutate: createTeam, isPending: creating } = useMutation({
  mutationFn: (name: string) =>
    apiFetch<Team>('/api/teams', { method: 'POST', body: JSON.stringify({ name }) }),
  onSuccess: (team) => {
    invalidate()
    newName.value = ''
    createError.value = null
    openTeam(team)
    showToast(`Team ${team.name} created`, 'success')
  },
  onError: (e) => {
    createError.value = nameTaken(e) ? 'A team with this name already exists' : 'Failed to create team'
  },
})

function submitCreate() {
  const name = newName.value.trim()
  if (name) createTeam(name)
}

// ── Edit ──────────────────────────────────────────────────────────────────────
const editingId = ref<string | null>(null)
const editName = ref('')
const editMembers = ref<string[]>([])
const editError = ref<string | null>(null)

function openTeam(g: Team) {
  editingId.value = g.id
  editName.value = g.name
  editMembers.value = [...g.member_ids]
  editError.value = null
}

function toggleTeam(g: Team) {
  if (editingId.value === g.id) editingId.value = null
  else openTeam(g)
}

const { mutate: saveTeam, isPending: saving } = useMutation({
  mutationFn: async (v: { team: Team; name: string; memberIds: string[] }) => {
    if (v.name !== v.team.name) {
      await apiFetch<Team>(`/api/teams/${v.team.id}`, { method: 'PATCH', body: JSON.stringify({ name: v.name }) })
    }
    return apiFetch<Team>(`/api/teams/${v.team.id}/members`, {
      method: 'PUT',
      body: JSON.stringify({ user_ids: v.memberIds }),
    })
  },
  onSuccess: (team) => {
    invalidate()
    editingId.value = null
    showToast(`Team ${team.name} saved`, 'success')
  },
  onError: (e) => {
    editError.value = nameTaken(e) ? 'A team with this name already exists' : 'Failed to save team'
  },
})

async function submitSave(team: Team) {
  const name = editName.value.trim()
  if (!name) return
  // Taking someone out of the team can take them off alert rules, so say so first.
  const removesMembers = team.member_ids.some((id) => !editMembers.value.includes(id))
  if (removesMembers) {
    const ok = await confirmAlertImpact(`/api/teams/${team.id}/members`, 'PUT', { user_ids: editMembers.value }, {
      title: `Save ${name}?`,
      confirmLabel: 'Save team',
    })
    if (!ok) return
  }
  saveTeam({ team, name, memberIds: editMembers.value })
}

// ── Delete ────────────────────────────────────────────────────────────────────
const { mutate: deleteTeam } = useMutation({
  mutationFn: (g: Team) => apiFetch(`/api/teams/${g.id}`, { method: 'DELETE' }),
  onSuccess: (_, g) => {
    invalidate()
    if (editingId.value === g.id) editingId.value = null
    showToast(`Team ${g.name} deleted`, 'success')
  },
  onError: () => showToast('Failed to delete team', 'error'),
})

async function removeTeam(g: Team) {
  const ok = await confirmAlertImpact(`/api/teams/${g.id}`, 'DELETE', undefined, {
    title: `Delete ${g.name}?`,
    message: 'Its members lose the access it gave them, and its projects are detached from it. Accounts are not affected.',
    confirmLabel: 'Delete team',
    danger: true,
    alwaysAsk: true,
  })
  if (ok) deleteTeam(g)
}
</script>

<template>
  <div class="pane-head">
    <div>
      <div class="pane-head__title">Teams</div>
      <div class="pane-head__sub">
        {{ teams.length }} team{{ teams.length === 1 ? '' : 's' }}. Members of a team see the projects attached to it; attach a project from the Projects tab.
      </div>
    </div>
  </div>

  <div v-if="!canManageUsers" class="settings-empty">
    <div class="settings-empty__icon">
      <Icon name="users" :size="28" />
    </div>
    <div class="settings-empty__title">Teams</div>
    <div class="settings-empty__body">You don't have permission to manage teams.</div>
  </div>

  <template v-else>
    <form class="callback-row" style="margin-bottom: 16px" @submit.prevent="submitCreate">
      <input
        v-model="newName"
        class="field__input"
        maxlength="100"
        placeholder="Team name, e.g. Backend team"
        aria-label="New team name"
      />
      <button type="submit" class="btn btn--primary" :disabled="!newName.trim() || creating">
        <Icon name="plus" :size="12" />
        Create team
      </button>
    </form>
    <div v-if="createError" class="proj-form-error" style="margin-bottom: 16px">{{ createError }}</div>

    <div v-if="teams.length === 0" class="settings-empty">
      <div class="settings-empty__icon">
        <Icon name="users" :size="28" />
      </div>
      <div class="settings-empty__title">No teams yet</div>
      <div class="settings-empty__body">Create a team, add members, then attach projects to it so they can see them.</div>
    </div>

    <div
      v-for="g in teams"
      :key="g.id"
      class="proj-card"
      :class="{ 'proj-card--open': editingId === g.id }"
    >
      <div
        class="proj-card__head"
        v-clickable
        :aria-expanded="editingId === g.id"
        @click="toggleTeam(g)"
      >
        <div style="min-width: 0">
          <div class="proj-card__name" :title="g.name">{{ g.name }}</div>
          <div class="proj-card__meta">{{ g.member_ids.length }} member{{ g.member_ids.length === 1 ? '' : 's' }}</div>
        </div>
        <span class="rule__caret">
          <Icon :name="editingId === g.id ? 'chevron-down' : 'chevron-right'" :size="11" />
        </span>
      </div>

      <div v-if="editingId === g.id" class="proj-card__body">
        <form @submit.prevent="submitSave(g)">
          <div class="field">
            <label class="field__label" :for="`team-name-${g.id}`">Name</label>
            <input :id="`team-name-${g.id}`" v-model="editName" class="field__input" maxlength="100" />
          </div>
          <div class="field">
            <label class="field__label">Members</label>
            <div class="rule__project-checks">
              <label v-for="u in users" :key="u.id" class="rule__project-check">
                <input v-model="editMembers" type="checkbox" :value="u.id" />
                {{ u.name || u.email }}
              </label>
            </div>
          </div>
          <div v-if="editError" class="proj-form-error">{{ editError }}</div>
          <div style="display: flex; gap: 8px; margin-top: 14px">
            <button type="submit" class="btn btn--primary" :disabled="saving || !editName.trim()">
              {{ saving ? 'Saving…' : 'Save' }}
            </button>
            <button type="button" class="btn btn--ghost" @click="editingId = null">Cancel</button>
            <button
              type="button"
              class="btn btn--ghost"
              style="color: var(--danger); margin-left: auto"
              @click="removeTeam(g)"
            >
              <Icon name="trash" :size="12" />
              Delete
            </button>
          </div>
        </form>
      </div>
    </div>
  </template>
</template>
