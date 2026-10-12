<script setup lang="ts">
import { ref, computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useToast } from '@/composables/useToast'
import { useClipboard } from '@/composables/useClipboard'
import { apiFetch } from '@/api/client'
import type { ApiToken } from '@/api/types'
import Icon from '@/components/Icon.vue'
import { useFormatDate, usePermissions, useProjectByID, useProjects, useRowMenu } from './shared'

const { show: showToast } = useToast()
const { copy } = useClipboard()
const qc = useQueryClient()
const formatDate = useFormatDate()
const { canManageProjects } = usePermissions()
const { projects } = useProjects()
const projectByID = useProjectByID(projects)
const { openMenuId, toggleUserMenu, closeUserMenus } = useRowMenu()

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

const { data: tokensData } = useQuery({
  queryKey: ['tokens'],
  queryFn: ({ signal }) => apiFetch<ApiToken[]>('/api/tokens', { signal }),
})
const tokens = computed(() => tokensData.value ?? [])

const { mutate: createToken, isPending: creatingToken } = useMutation({
  mutationFn: ({ name, project_id, writable }: { name: string; project_id: string; writable: boolean }) =>
    apiFetch<{ token: string; meta: ApiToken }>('/api/tokens', {
      method: 'POST',
      body: JSON.stringify({ name, project_id, writable }),
    }),
  onSuccess: (data) => {
    if (!data) return
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

const editingToken = ref<ApiToken | null>(null)
const editTokenName = ref('')
const editTokenProject = ref('')
const editTokenWritable = ref(false)

function openEditToken(t: ApiToken) {
  editingToken.value = t
  editTokenName.value = t.name
  editTokenProject.value = t.project_id
  editTokenWritable.value = t.writable
  closeUserMenus()
}

function closeEditToken() {
  editingToken.value = null
}

const { mutate: updateToken, isPending: updatingToken } = useMutation({
  mutationFn: ({ id, name, project_id, writable }: { id: string; name: string; project_id: string; writable: boolean }) =>
    apiFetch<ApiToken>(`/api/tokens/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({ name, project_id, writable }),
    }),
  onSuccess: () => {
    qc.invalidateQueries({ queryKey: ['tokens'] })
    closeEditToken()
    showToast('Token updated')
  },
  onError: (err) => {
    showToast(err instanceof Error ? err.message : 'Failed to update token.', 'error')
  },
})

function submitEditToken(e: Event) {
  e.preventDefault()
  if (!editingToken.value || !editTokenName.value.trim() || !editTokenProject.value) return
  updateToken({
    id: editingToken.value.id,
    name: editTokenName.value.trim(),
    project_id: editTokenProject.value,
    writable: editTokenWritable.value,
  })
}

function submitCreate(e: Event) {
  e.preventDefault()
  const pid = newTokenProject.value || (projects.value[0]?.id ?? '')
  if (!newTokenName.value.trim() || !pid) return
  createToken({ name: newTokenName.value.trim(), project_id: pid, writable: newTokenWritable.value })
}

async function copyFlash() {
  // Keep the token on screen if copying failed; it is shown only once.
  if (await copy(flashToken.value!, 'Token copied to clipboard')) closeTokenForm()
}

function revoke(id: string, name: string) {
  revokeToken({ id, name })
  closeUserMenus()
}
</script>

<template>
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
      <button type="button" class="btn btn--ghost" style="padding: 2px 6px" aria-label="Close new token form" @click="closeTokenForm"><Icon name="x" :size="12" /></button>
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
              aria-label="Token actions"
              aria-haspopup="true"
              :aria-expanded="openMenuId === t.id"
              @click="toggleUserMenu(t.id, $event)"
            >
              <Icon name="more-horizontal" :size="15" />
            </button>
            <div v-if="openMenuId === t.id" class="user-menu__dropdown">
              <button class="user-menu__item" @click="openEditToken(t)">
                <Icon name="pencil" :size="13" class="user-menu__item-icon" />
                Edit
              </button>
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

  <!-- Edit token form -->
  <div v-if="editingToken" class="token-edit-panel">
    <form @submit="submitEditToken">
      <div class="token-edit-panel__title">Edit token</div>
      <div class="fields-grid" style="margin-top: 12px">
        <div class="field">
          <label class="field__label">Name</label>
          <input v-model="editTokenName" class="field__input" type="text" placeholder="Token name" required />
        </div>
        <div class="field" v-if="projects.length > 1">
          <label class="field__label">Project</label>
          <select v-model="editTokenProject" class="field__input">
            <option v-for="p in projects" :key="p.id" :value="p.id">{{ p.name }}</option>
          </select>
        </div>
        <div class="field">
          <label class="field__label">Access</label>
          <label class="checkbox-label">
            <input type="checkbox" v-model="editTokenWritable" />
            Read / Write (uncheck for read-only)
          </label>
        </div>
      </div>
      <div style="display: flex; gap: 8px; margin-top: 14px">
        <button type="submit" class="btn btn--primary" :disabled="updatingToken">
          {{ updatingToken ? 'Saving…' : 'Save changes' }}
        </button>
        <button type="button" class="btn btn--ghost" @click="closeEditToken">Cancel</button>
      </div>
    </form>
  </div>
</template>
