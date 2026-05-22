<script setup lang="ts">
import { ref, computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { formatDuration } from '@/utils/formatters'
import { useFormatters } from '@/composables/useFormatters'
import type { CronMonitor, CronCheckin, Project } from '@/api/types'
import { useProjectsStore } from '@/stores/projects'
import { useAuthStore } from '@/stores/auth'
import Icon from '@/components/Icon.vue'
import cronstrue from 'cronstrue'

function humanSchedule(expr: string): string {
  try {
    return cronstrue.toString(expr, { verbose: false })
  } catch {
    return expr
  }
}

const projects = useProjectsStore()
const auth = useAuthStore()
const qc = useQueryClient()
const { formatRel } = useFormatters()

const canManage = computed(() => auth.user?.permissions.manage_projects ?? false)

const selectedMonitorId = ref<string | null>(null)

// ---- project list (for create dialog) ----
const { data: projectList } = useQuery({
  queryKey: ['projects'],
  queryFn: () => apiFetch<Project[]>('/api/projects'),
})

// ---- monitors list ----
const queryParams = computed(() => {
  const p = new URLSearchParams()
  for (const id of projects.selectedIds) p.append('project_id', id)
  return p.toString()
})

const { data: monitorsData, isLoading } = useQuery({
  queryKey: computed(() => ['monitors', queryParams.value]),
  queryFn: () => apiFetch<CronMonitor[]>(`/api/monitors?${queryParams.value}`),
  refetchInterval: 30_000,
})
const monitors = computed(() => monitorsData.value ?? [])

// ---- selected monitor ----
const selectedMonitor = computed(() =>
  monitors.value.find((m) => m.id === selectedMonitorId.value) ?? null,
)

const { data: checkinsData, isLoading: checkinsLoading } = useQuery({
  queryKey: computed(() => ['checkins', selectedMonitorId.value]),
  queryFn: () =>
    selectedMonitorId.value
      ? apiFetch<CronCheckin[]>(`/api/monitors/${selectedMonitorId.value}/checkins?limit=50`)
      : Promise.resolve([]),
  enabled: computed(() => !!selectedMonitorId.value),
  refetchInterval: computed(() => (selectedMonitorId.value ? 30_000 : false)),
})
const checkins = computed(() => checkinsData.value ?? [])

function selectMonitor(id: string) {
  selectedMonitorId.value = selectedMonitorId.value === id ? null : id
}

// ---- state helpers ----
function stateColor(state: string): string {
  if (state === 'ok') return 'var(--success)'
  if (state === 'missed') return 'var(--danger)'
  if (state === 'error') return 'var(--danger)'
  if (state === 'in_progress') return 'var(--warning)'
  return 'var(--text-3)'
}

function stateLabel(state: string): string {
  if (state === 'ok') return 'OK'
  if (state === 'missed') return 'Missed'
  if (state === 'error') return 'Error'
  if (state === 'in_progress') return 'Running'
  return 'Unknown'
}

function checkinStateColor(status: string): string {
  if (status === 'ok') return 'var(--success)'
  if (status === 'error') return 'var(--danger)'
  if (status === 'in_progress') return 'var(--warning)'
  return 'var(--text-3)'
}

function projectName(id: string): string {
  return projectList.value?.find((p) => p.id === id)?.name ?? id.slice(0, 8)
}

function pingURL(id: string): string {
  return `${window.location.origin}/api/cron/${id}`
}

function copyPingURL(id: string) {
  navigator.clipboard.writeText(pingURL(id)).catch(() => {})
}

// ---- create ----
const showCreate = ref(false)
const newMonitor = ref({ name: '', schedule: '0 * * * *', grace_period_secs: 300, project_id: '' })

const { mutate: createMonitor, isPending: creating } = useMutation({
  mutationFn: () =>
    apiFetch<CronMonitor>('/api/monitors', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: newMonitor.value.name,
        schedule: newMonitor.value.schedule,
        grace_period_secs: Number(newMonitor.value.grace_period_secs),
        project_id: newMonitor.value.project_id,
      }),
    }),
  onSuccess(m) {
    qc.invalidateQueries({ queryKey: ['monitors'] })
    showCreate.value = false
    newMonitor.value = { name: '', schedule: '0 * * * *', grace_period_secs: 300, project_id: '' }
    selectedMonitorId.value = m.id
  },
})

// ---- edit ----
const editingId = ref<string | null>(null)
const editForm = ref({ name: '', schedule: '', grace_period_secs: 300, status: 'active' as 'active' | 'paused' })

function startEdit(m: CronMonitor) {
  editingId.value = m.id
  editForm.value = { name: m.name, schedule: m.schedule, grace_period_secs: m.grace_period_secs, status: m.status }
}

const { mutate: saveMonitor, isPending: saving } = useMutation({
  mutationFn: (id: string) =>
    apiFetch<CronMonitor>(`/api/monitors/${id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(editForm.value),
    }),
  onSuccess() {
    qc.invalidateQueries({ queryKey: ['monitors'] })
    editingId.value = null
  },
})

// ---- delete ----
const { mutate: deleteMonitor } = useMutation({
  mutationFn: (id: string) => apiFetch(`/api/monitors/${id}`, { method: 'DELETE' }),
  onSuccess() {
    qc.invalidateQueries({ queryKey: ['monitors'] })
    selectedMonitorId.value = null
    editingId.value = null
  },
})

function confirmDelete(m: CronMonitor) {
  if (confirm(`Delete monitor "${m.name}"? This cannot be undone.`)) {
    deleteMonitor(m.id)
  }
}

function cancelCreate() {
  showCreate.value = false
  newMonitor.value = { name: '', schedule: '0 * * * *', grace_period_secs: 300, project_id: '' }
}
</script>

<template>
  <div class="page">
    <!-- Filter bar -->
    <div class="filterbar">
      <span class="filterbar__spacer" />
      <button v-if="canManage" class="btn btn--primary" @click="showCreate = !showCreate">
        <Icon name="plus" :size="12" />
        New monitor
      </button>
    </div>

    <!-- Inline create form -->
    <div v-if="showCreate" class="mon-createbar">
      <div class="mon-createbar__fields">
        <div class="mon-field">
          <label class="mon-field__label">Name</label>
          <input v-model="newMonitor.name" class="mon-field__input" placeholder="Daily backup" autofocus />
        </div>
        <div class="mon-field">
          <label class="mon-field__label">Project</label>
          <select v-model="newMonitor.project_id" class="mon-field__input">
            <option value="">Select project</option>
            <option v-for="p in projectList" :key="p.id" :value="p.id">{{ p.name }}</option>
          </select>
        </div>
        <div class="mon-field">
          <label class="mon-field__label">Schedule</label>
          <input v-model="newMonitor.schedule" class="mon-field__input mono" placeholder="0 * * * *" />
        </div>
        <div class="mon-field">
          <label class="mon-field__label">Grace (secs)</label>
          <input v-model.number="newMonitor.grace_period_secs" class="mon-field__input" type="number" min="30" />
        </div>
      </div>
      <div class="mon-createbar__actions">
        <button
          class="btn btn--primary"
          :disabled="creating || !newMonitor.name || !newMonitor.project_id || !newMonitor.schedule"
          @click="createMonitor()"
        >{{ creating ? 'Creating…' : 'Create' }}</button>
        <button class="btn btn--ghost" @click="cancelCreate">Cancel</button>
      </div>
    </div>

    <!-- Skeleton loading -->
    <template v-if="isLoading">
      <div v-for="i in 6" :key="i" class="monrow">
        <span class="skel" style="width:8px;height:8px;border-radius:50%;flex:0 0 8px" />
        <div style="display:flex;flex-direction:column;gap:5px;min-width:0">
          <span class="skel" style="width:160px;height:10px;display:block" />
          <span class="skel" style="width:90px;height:8px;display:block" />
        </div>
        <span class="skel" style="width:170px;height:8px;display:block;border-radius:2px" />
        <span class="skel" style="width:70px;height:10px;display:block" />
        <span class="skel" style="width:70px;height:10px;display:block" />
        <span />
      </div>
    </template>

    <!-- Empty state -->
    <div v-else-if="monitors.length === 0" class="empty-state">
      <div class="empty-state__ghosts" aria-hidden="true">
        <div
          v-for="(w, i) in ['62%','48%','74%','55%','68%','44%']"
          :key="i"
          class="ghost-row"
          style="grid-template-columns:14px minmax(0,1fr) 180px 120px 120px 32px"
        >
          <span class="ghost ghost--dot" />
          <div style="display:flex;flex-direction:column;gap:6px">
            <span class="ghost ghost--bar" :style="{ width: w }" />
            <span class="ghost ghost--bar" style="width:80px;height:7px;opacity:0.6" />
          </div>
          <span class="ghost ghost--bar" style="width:110px" />
          <span class="ghost ghost--bar" style="width:64px" />
          <span class="ghost ghost--bar" style="width:64px" />
          <span />
        </div>
      </div>
      <div class="empty-state__card">
        <div class="empty-state__icon">
          <Icon name="clock" :size="24" style="color: var(--accent)" />
        </div>
        <h2 class="empty-state__title">No monitors yet</h2>
        <p class="empty-state__body">Create a monitor to track whether your scheduled jobs check in on time.</p>
        <div v-if="canManage" class="empty-state__actions">
          <button class="btn btn--primary" @click="showCreate = true">
            <Icon name="plus" :size="12" />
            New monitor
          </button>
        </div>
      </div>
    </div>

    <!-- Full-width list -->
    <template v-else>
      <div class="monrow monrow--header">
        <span />
        <span>Monitor</span>
        <span>Last 20 runs</span>
        <span>Last seen</span>
        <span>Next expected</span>
        <span />
      </div>

      <template v-for="m in monitors" :key="m.id">
        <!-- Row -->
        <div
          class="monrow"
          :class="{ 'monrow--active': selectedMonitorId === m.id }"
          @click="selectMonitor(m.id)"
        >
          <span class="mon-dot" :style="{ background: stateColor(m.state) }" :title="stateLabel(m.state)" />
          <div class="monrow__main">
            <div class="monrow__name">{{ m.name }}</div>
            <div class="monrow__sub">
              <span :style="{ color: stateColor(m.state) }" class="monrow__state">{{ stateLabel(m.state) }}</span>
              <span class="monrow__sep">·</span>
              <span class="monrow__sched">{{ humanSchedule(m.schedule) }}</span>
              <span class="monrow__sep">·</span>
              <span class="projtag">{{ projectName(m.project_id) }}</span>
            </div>
          </div>
          <div class="mon-timeline">
            <span
              v-for="i in Math.max(0, 20 - m.recent_checkins.length)"
              :key="`e${i}`"
              class="mon-tl-dot mon-tl-dot--empty"
            />
            <span
              v-for="dot in m.recent_checkins"
              :key="dot.received_at"
              class="mon-tl-dot"
              :style="{ background: checkinStateColor(dot.status) }"
              :title="`${dot.status} · ${formatRel(dot.received_at)}`"
            />
          </div>
          <span class="monrow__time">{{ m.last_checkin_at ? formatRel(m.last_checkin_at) : '–' }}</span>
          <span class="monrow__time">{{ m.next_expected_at ? formatRel(m.next_expected_at) : '–' }}</span>
          <span class="monrow__chevron">
            <Icon :name="selectedMonitorId === m.id ? 'chevron-up' : 'chevron-down'" :size="12" />
          </span>
        </div>

        <!-- Inline detail (expanded) -->
        <div v-if="selectedMonitorId === m.id" class="mon-detail">
          <!-- Edit form -->
          <div v-if="editingId === m.id" class="mon-editbar">
            <div class="mon-createbar__fields">
              <div class="mon-field">
                <label class="mon-field__label">Name</label>
                <input v-model="editForm.name" class="mon-field__input" />
              </div>
              <div class="mon-field">
                <label class="mon-field__label">Schedule</label>
                <input v-model="editForm.schedule" class="mon-field__input mono" />
              </div>
              <div class="mon-field">
                <label class="mon-field__label">Grace (secs)</label>
                <input v-model.number="editForm.grace_period_secs" class="mon-field__input" type="number" min="30" />
              </div>
              <div class="mon-field">
                <label class="mon-field__label">Status</label>
                <select v-model="editForm.status" class="mon-field__input">
                  <option value="active">Active</option>
                  <option value="paused">Paused</option>
                </select>
              </div>
            </div>
            <div class="mon-createbar__actions">
              <button class="btn btn--primary" :disabled="saving" @click="saveMonitor(m.id)">
                {{ saving ? 'Saving…' : 'Save' }}
              </button>
              <button class="btn btn--ghost" @click="editingId = null">Cancel</button>
              <button class="btn btn--ghost" style="color: var(--danger); margin-left: auto" @click="confirmDelete(m)">
                Delete
              </button>
            </div>
          </div>

          <!-- Detail view -->
          <template v-else>
            <div class="mon-detail__toolbar">
              <div class="mon-detail__meta">
                <span class="mon-detail__kv">
                  <span class="mon-detail__k">Grace</span>
                  <span class="mon-detail__v">{{ m.grace_period_secs }}s</span>
                </span>
                <span class="mon-detail__kv">
                  <span class="mon-detail__k">Status</span>
                  <span class="mon-detail__v">{{ m.status }}</span>
                </span>
              </div>
              <div class="mon-detail__actions">
                <button v-if="canManage" class="btn btn--ghost" @click="startEdit(m)">
                  <Icon name="edit" :size="12" />
                  Edit
                </button>
              </div>
            </div>

            <!-- Ping URL -->
            <div class="empty-state__endpoint mon-ping">
              <span class="empty-state__endpoint-label">Ping URL</span>
              <div style="display:flex;align-items:center;gap:8px">
                <code class="mono" style="font-size:var(--text-xs);color:var(--text-2);flex:1;white-space:nowrap;overflow:hidden;text-overflow:ellipsis">{{ pingURL(m.id) }}</code>
                <button class="btn btn--ghost" style="height:22px;padding:0 8px;font-size:var(--text-xs)" @click="copyPingURL(m.id)">
                  <Icon name="copy" :size="11" />
                  Copy
                </button>
              </div>
            </div>

            <!-- Check-in history -->
            <div class="mon-ci-head">Check-in history</div>
            <div v-if="checkinsLoading" class="mon-ci-empty">Loading…</div>
            <div v-else-if="checkins.length === 0" class="mon-ci-empty">No check-ins yet.</div>
            <template v-else>
              <div class="mon-ci-row mon-ci-row--header">
                <span>Status</span>
                <span>Duration</span>
                <span>Environment</span>
                <span>Received</span>
              </div>
              <div v-for="ci in checkins" :key="ci.id" class="mon-ci-row">
                <div class="mon-ci-status">
                  <span class="mon-dot" :style="{ background: checkinStateColor(ci.status) }" />
                  <span :style="{ color: checkinStateColor(ci.status) }">{{ ci.status }}</span>
                </div>
                <span style="color:var(--text-2)">{{ ci.duration_ms != null ? formatDuration(ci.duration_ms) : '–' }}</span>
                <span style="color:var(--text-3)">{{ ci.environment ?? '–' }}</span>
                <span style="color:var(--text-3)">{{ formatRel(ci.received_at) }}</span>
              </div>
            </template>
          </template>
        </div>
      </template>
    </template>
  </div>
</template>

<style scoped>
/* ---------- Full-width monitor rows ---------- */

.monrow {
  display: grid;
  grid-template-columns: 14px minmax(0, 1fr) 220px 110px 110px 32px;
  align-items: center;
  gap: 14px;
  height: 52px;
  padding: 0 20px 0 16px;
  border-bottom: 1px solid var(--border-soft);
  cursor: pointer;
  transition: background 120ms ease;
  font-size: var(--text-sm);
}

.monrow:hover { background: var(--surface); }

.monrow--active { background: var(--accent-soft); }
.monrow--active:hover { background: var(--accent-soft); }

.monrow--header {
  height: 32px;
  font-size: var(--text-xs);
  color: var(--text-3);
  text-transform: uppercase;
  letter-spacing: 0.06em;
  cursor: default;
  border-bottom: 1px solid var(--border);
  background: var(--bg);
  position: sticky;
  top: 92px;
  z-index: 5;
}

.monrow--header:hover { background: var(--bg); }

.monrow__main { min-width: 0; }

.monrow__name {
  font-size: var(--text-sm);
  font-weight: 500;
  color: var(--text-1);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.monrow__sub {
  display: flex;
  align-items: center;
  gap: 5px;
  margin-top: 2px;
}

.monrow__state {
  font-size: var(--text-xs);
  font-weight: 500;
}

.monrow__sep {
  font-size: var(--text-xs);
  color: var(--text-3);
}

.monrow__sched {
  font-size: var(--text-xs);
  color: var(--text-3);
}

/* ---------- Timeline strip ---------- */

.mon-timeline {
  display: flex;
  align-items: center;
  gap: 3px;
}

.mon-tl-dot {
  width: 8px;
  height: 8px;
  border-radius: 2px;
  flex: 0 0 8px;
  cursor: default;
  transition: transform 80ms ease;
}

.mon-tl-dot:hover { transform: scaleY(1.5); }

.mon-tl-dot--empty {
  background: var(--surface-2);
  opacity: 0.5;
}

.monrow__time {
  font-size: var(--text-xs);
  color: var(--text-3);
  white-space: nowrap;
}

.monrow__chevron {
  color: var(--text-3);
  display: flex;
  align-items: center;
  justify-content: flex-end;
  transition: color 120ms;
}

.monrow:hover .monrow__chevron { color: var(--text-2); }

/* ---------- State dot ---------- */

.mon-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: 0 0 8px;
  display: block;
}

/* ---------- Inline detail ---------- */

.mon-detail {
  border-bottom: 1px solid var(--border);
  background: var(--surface);
  overflow: hidden;
}

.mon-detail__toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 20px 10px 44px;
  border-bottom: 1px solid var(--border-soft);
}

.mon-detail__meta {
  display: flex;
  align-items: center;
  gap: 20px;
}

.mon-detail__kv {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: var(--text-xs);
}

.mon-detail__k {
  color: var(--text-3);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  font-size: 10px;
}

.mon-detail__v {
  color: var(--text-2);
  font-weight: 500;
}

.mon-detail__actions {
  display: flex;
  gap: 6px;
}

/* ---------- Ping URL ---------- */

.mon-ping {
  margin: 12px 20px 12px 44px;
  border-radius: 4px;
  width: auto;
}

/* ---------- Check-in head ---------- */

.mon-ci-head {
  padding: 10px 20px 6px 44px;
  font-size: var(--text-xs);
  font-weight: 600;
  color: var(--text-3);
  text-transform: uppercase;
  letter-spacing: 0.06em;
}

/* ---------- Check-in rows ---------- */

.mon-ci-row {
  display: grid;
  grid-template-columns: 100px 90px 130px 1fr;
  align-items: center;
  gap: 14px;
  height: 36px;
  padding: 0 20px 0 44px;
  border-top: 1px solid var(--border-soft);
  font-size: var(--text-xs);
}

.mon-ci-row--header {
  height: 28px;
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-3);
  cursor: default;
}

.mon-ci-status {
  display: flex;
  align-items: center;
  gap: 6px;
  font-weight: 500;
}

.mon-ci-empty {
  padding: 12px 20px 16px 44px;
  font-size: var(--text-sm);
  color: var(--text-3);
}

/* ---------- Create / Edit bar ---------- */

.mon-createbar {
  background: var(--surface);
  border-bottom: 1px solid var(--border);
  padding: 16px 20px;
}

.mon-createbar__fields {
  display: grid;
  grid-template-columns: 1fr 1fr 1fr 1fr;
  gap: 12px;
  margin-bottom: 12px;
}

.mon-createbar__actions {
  display: flex;
  gap: 8px;
}

.mon-editbar {
  padding: 16px 20px 16px 44px;
  border-bottom: 1px solid var(--border-soft);
}

/* ---------- Fields ---------- */

.mon-field {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.mon-field__label {
  font-size: var(--text-xs);
  font-weight: 600;
  color: var(--text-3);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.mon-field__input {
  height: 26px;
  padding: 0 8px;
  font-size: var(--text-sm);
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 3px;
  color: var(--text-1);
  transition: border-color 120ms;
  outline: none;
}

.mon-field__input:focus { border-color: var(--accent); }
.mon-field__input::placeholder { color: var(--text-3); }
select.mon-field__input { cursor: pointer; }

@media (max-width: 768px) {
  .mon-createbar__fields { grid-template-columns: 1fr 1fr; }
}
@media (max-width: 480px) {
  .mon-createbar__fields { grid-template-columns: 1fr; }
}

@media (max-width: 640px) {
  /* Collapse to dot + name/sub + chevron; drop timeline and both time columns */
  .monrow {
    grid-template-columns: 14px minmax(0, 1fr) 32px;
    height: auto;
    padding: 10px 16px;
  }
  .monrow--header { display: none; }
  .mon-timeline { display: none; }
  .monrow__time { display: none; }

  /* Detail panel: drop the 44px left indent */
  .mon-detail__toolbar { padding: 10px 16px; }
  .mon-detail__meta { gap: 12px; flex-wrap: wrap; }
  .mon-ping { margin: 12px 16px; }
  .mon-ci-head { padding: 10px 16px 6px; }
  .mon-ci-empty { padding: 12px 16px 16px; }
  .mon-editbar { padding: 16px; }

  /* Check-in rows: 3 cols (status + duration + received), hide environment */
  .mon-ci-row {
    grid-template-columns: 80px 70px 1fr;
    padding: 0 16px;
  }
  .mon-ci-row > :nth-child(3) { display: none; }
}
</style>
