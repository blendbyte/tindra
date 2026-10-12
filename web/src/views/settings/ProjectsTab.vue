<script setup lang="ts">
import { vClickable } from '@/directives/clickable'
import { ref, computed, watch, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useToast } from '@/composables/useToast'
import { useClipboard } from '@/composables/useClipboard'
import { useConfig } from '@/composables/useConfig'
import { useTimezone } from '@/composables/useTimezone'
import { apiFetch } from '@/api/client'
import type { Project, ProjectQuota, ScrubPattern } from '@/api/types'
import Icon from '@/components/Icon.vue'
import Sparkline from '@/components/Sparkline.vue'
import { formatBytes, useMonthlyEvents, usePermissions, useProjects, useServerSettings } from './shared'

const { show: showToast } = useToast()
const { copy } = useClipboard()
const tz = useTimezone()
const qc = useQueryClient()
const route = useRoute()
const { dsnFor } = useConfig()
const { canManageProjects } = usePermissions()
const { projects } = useProjects()
const { settings } = useServerSettings()
const { totalMonthlyEvents, instanceAtLimit, instanceNearLimit, globalEventPct } = useMonthlyEvents(projects, settings)

// ── Project list sort ─────────────────────────────────────────────────────────
function projLsGet(key: string): string | null {
  try { return localStorage.getItem('tindra:settings:proj-' + key) } catch { return null }
}
function projLsSet(key: string, val: string | null) {
  try {
    if (val === null) localStorage.removeItem('tindra:settings:proj-' + key)
    else localStorage.setItem('tindra:settings:proj-' + key, val)
  } catch {}
}
type ProjSortCol = 'name' | 'event_count' | 'events_24h' | 'storage_bytes'
const PROJ_SORT_COLS: ProjSortCol[] = ['name', 'event_count', 'events_24h', 'storage_bytes']
const rawProjSort = projLsGet('sort') ?? 'name'
const projSortCol = ref<ProjSortCol>(PROJ_SORT_COLS.includes(rawProjSort as ProjSortCol) ? rawProjSort as ProjSortCol : 'name')
const projSortDir = ref<'asc' | 'desc'>((projLsGet('dir') ?? 'asc') === 'desc' ? 'desc' : 'asc')

function toggleProjSort(col: ProjSortCol) {
  if (projSortCol.value === col) {
    projSortDir.value = projSortDir.value === 'desc' ? 'asc' : 'desc'
  } else {
    projSortCol.value = col
    projSortDir.value = col === 'name' ? 'asc' : 'desc'
  }
  projLsSet('sort', projSortCol.value !== 'name' ? projSortCol.value : null)
  projLsSet('dir', projSortDir.value !== 'asc' ? projSortDir.value : null)
}
function projSortIcon(col: ProjSortCol): string {
  if (projSortCol.value !== col) return ''
  return projSortDir.value === 'asc' ? '↑' : '↓'
}
const sortedProjects = computed(() => {
  const list = [...projects.value]
  const col = projSortCol.value
  const dir = projSortDir.value === 'asc' ? 1 : -1
  return list.sort((a, b) => {
    if (col === 'name') return dir * a.name.localeCompare(b.name)
    return dir * ((a[col] as number) - (b[col] as number))
  })
})

const projectLimitReached = computed(() => {
  const limit = settings.value?.project_limit ?? 0
  return limit > 0 && projects.value.length >= limit
})

// Projects config
const expandedProject = ref<string | null>(null)

// Quota data for whichever project card is currently expanded.
// One query, keyed to expandedProject - reruns automatically when a different card opens.
const { data: quotaData } = useQuery({
  queryKey: computed(() => ['quota', expandedProject.value]),
  queryFn: ({ signal }) => apiFetch<ProjectQuota>(`/api/projects/${expandedProject.value}/quota`, { signal }),
  enabled: computed(() => expandedProject.value !== null),
  staleTime: 30_000,
})

const showNewProject = ref(false)
const newProjectName = ref('')
const newProjectSlug = ref('')
const slugTouched = ref(false)
const newProjectError = ref<string | null>(null)
const createdProject = ref<Project | null>(null)

// Auto-open the new-project form when arriving via ?new=1
onMounted(() => {
  if (route.query.new === '1') openNewProject()
})
watch(() => route.query.new, () => {
  if (route.query.new === '1') openNewProject()
})

// Edit state
const editingProject = ref<string | null>(null)
const editName = ref('')
const editSlug = ref('')
const editPassthroughDsn = ref('')
const editProfilingEnabled = ref(true)
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
  mutationFn: ({ id, name, slug, passthroughDsn, profilingEnabled }: {
    id: string
    name: string
    slug: string
    passthroughDsn: string
    profilingEnabled: boolean
  }) =>
    apiFetch<Project>(`/api/projects/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({
        name,
        slug,
        passthrough_dsn: passthroughDsn || null,
        profiling_enabled: profilingEnabled,
      }),
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
  editProfilingEnabled.value = p.profiling_enabled
  editError.value = null
}

function submitEdit(e: Event, id: string) {
  e.preventDefault()
  editError.value = null
  const name = editName.value.trim()
  const slug = editSlug.value.trim()
  if (!name || !slug) return
  updateProject({
    id,
    name,
    slug,
    passthroughDsn: editPassthroughDsn.value.trim(),
    profilingEnabled: editProfilingEnabled.value,
  })
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
  copy(dsnFor(key, projectId), 'DSN copied')
}
</script>

<template>
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
        <a class="btn btn--primary" :href="`/projects/${encodeURIComponent(createdProject.slug)}/setup`">Verify setup</a>
        Any Sentry-compatible SDK works. Set <code class="mono">dsn</code> to this value and errors will start arriving.
      </div>
    </div>
    <button class="proj-success__close" aria-label="Dismiss" @click="createdProject = null">
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
      <button type="button" class="btn btn--ghost" style="padding: 2px 6px" aria-label="Close new project form" @click="showNewProject = false">
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
    <!-- Table header -->
    <div class="proj-table-head">
      <button class="col-sort" :class="{ 'col-sort--active': projSortCol === 'name' }" @click="toggleProjSort('name')">
        Project <em class="col-sort__icon">{{ projSortIcon('name') }}</em>
      </button>
      <button class="col-sort proj-table-head__num" :class="{ 'col-sort--active': projSortCol === 'event_count' }" @click="toggleProjSort('event_count')">
        Events / mo <em class="col-sort__icon">{{ projSortIcon('event_count') }}</em>
      </button>
      <button class="col-sort proj-table-head__num" :class="{ 'col-sort--active': projSortCol === 'events_24h' }" @click="toggleProjSort('events_24h')">
        Last 24h <em class="col-sort__icon">{{ projSortIcon('events_24h') }}</em>
      </button>
      <button class="col-sort proj-table-head__num" :class="{ 'col-sort--active': projSortCol === 'storage_bytes' }" @click="toggleProjSort('storage_bytes')">
        ~Storage <em class="col-sort__icon">{{ projSortIcon('storage_bytes') }}</em>
      </button>
      <span></span>
    </div>

    <div
      v-for="p in sortedProjects"
      :key="p.id"
      class="proj-card"
      :class="{ 'proj-card--open': expandedProject === p.id }"
    >
      <div class="proj-card__head proj-card__head--row" v-clickable :aria-expanded="expandedProject === p.id" @click="expandedProject = expandedProject === p.id ? null : p.id">
        <div style="min-width: 0">
          <div class="proj-card__name" :title="p.name">{{ p.name }}</div>
          <div class="proj-card__meta mono">{{ p.slug }}</div>
        </div>
        <span class="proj-card__stat mono">{{ (p.event_count ?? 0).toLocaleString() }}</span>
        <span class="proj-card__stat mono">+{{ (p.events_24h ?? 0).toLocaleString() }}</span>
        <span class="proj-card__stat mono">{{ formatBytes(p.storage_bytes ?? 0) }}</span>
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
                class="btn btn--danger"
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
            <div class="field" style="grid-column: 1 / -1">
              <label class="field__label">Profiling</label>
              <label class="checkbox-label">
                <input type="checkbox" v-model="editProfilingEnabled" />
                Accept profiles from this project
              </label>
              <span class="field__hint">
                The storage budget is shared across every project, so one busy service can
                crowd out the rest. Turn this off to stop accepting its profiles without
                touching its SDK config.
              </span>
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
          <a class="btn" style="margin-bottom: 16px" :href="`/projects/${encodeURIComponent(p.slug)}/setup`">Check setup</a>
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
            <div class="field">
              <label class="field__label">Logs stored</label>
              <div class="proj-usage">
                <span class="proj-usage__count">{{ (p.log_count ?? 0).toLocaleString() }}</span>
              </div>
            </div>
            <div class="field">
              <label class="field__label">Transactions stored</label>
              <div class="proj-usage">
                <span class="proj-usage__count">{{ (p.tx_count ?? 0).toLocaleString() }}</span>
              </div>
            </div>
            <div v-if="quotaData && quotaData.rate_limit_per_min > 0" class="field" style="grid-column: 1 / -1">
              <label class="field__label">Ingest rate (current window)</label>
              <div class="proj-usage">
                <span class="proj-usage__count">{{ quotaData.rate_limit_used.toLocaleString() }}</span>
                <span class="proj-usage__sep">of {{ quotaData.rate_limit_per_min.toLocaleString() }} / min</span>
                <span v-if="quotaData.rate_limit_reset_at" class="proj-usage__reset">
                  resets {{ new Date(quotaData.rate_limit_reset_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', timeZone: tz }) }}
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
              <div class="field__hint">Matching values in any event field, log message or log attribute are replaced with [Filtered] before storage.</div>
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
                  <button type="button" class="privacy-field__remove" :aria-label="`Remove ${f}`" @click="removeScrubField(f)">
                    <Icon name="x" :size="11" />
                  </button>
                </div>
              </div>
              <div class="field__hint">Use dot notation, e.g. <code class="mono">request.headers.Cookie</code> or <code class="mono">user.email</code>. The entire value at that path is replaced with [Filtered]. On logs, paths are matched against attribute names.</div>
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
