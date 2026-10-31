<script setup lang="ts">
import { vClickable } from '@/directives/clickable'
import { ref, computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { formatDuration } from '@/utils/formatters'
import { useFormatters } from '@/composables/useFormatters'
import type { CronMonitor, CronCheckin } from '@/api/types'
import Icon from '@/components/Icon.vue'
import { useConfirm } from '@/composables/useConfirm'
import { useToast } from '@/composables/useToast'
import { useClipboard } from '@/composables/useClipboard'
import cronstrue from 'cronstrue'
import { useConfig } from '@/composables/useConfig'
import MonitorsSubnav from './MonitorsSubnav.vue'
import { useCanManage, useMonitorProjects, useMonitorQueryParams } from './shared'
import { cronFailing, cronFailingSince, monitorVerdict, rankCron } from './urgency'
import { formatSpan } from '@/views/dashboard/briefing'

function humanSchedule(expr: string): string {
  try {
    return cronstrue.toString(expr, { verbose: false })
  } catch {
    return expr
  }
}

const qc = useQueryClient()
const { formatRel } = useFormatters()
const { show: showToast } = useToast()
const { confirm } = useConfirm()
const { copy } = useClipboard()

const canManage = useCanManage()

// ---- project list (for create dialog) ----
const { projectList, projectName } = useMonitorProjects()

const queryParams = useMonitorQueryParams()

// ---- cron monitors ----
const selectedMonitorId = ref<string | null>(null)

const { data: monitorsData, isLoading, isError: monitorsError, refetch: refetchMonitors } = useQuery({
  queryKey: computed(() => ['monitors', queryParams.value]),
  queryFn: ({ signal }) => apiFetch<CronMonitor[]>(`/api/monitors?${queryParams.value}`, { signal }),
})
// Failing monitors first, longest failure first; paused last.
const monitors = computed(() => rankCron(monitorsData.value ?? []))
const verdict = computed(() => monitorVerdict(
  monitors.value.map(m => ({ failing: cronFailing(m), paused: m.status === 'paused', unknown: m.state === 'unknown' || !m.last_checkin_at })),
  { one: 'monitor', many: 'monitors' }, { one: 'is failing', many: 'are failing' }, 'healthy'))

const { data: checkinsData, isLoading: checkinsLoading, isError: checkinsError, refetch: refetchCheckins } = useQuery({
  queryKey: computed(() => ['checkins', selectedMonitorId.value]),
  queryFn: ({ signal }) =>
    selectedMonitorId.value
      ? apiFetch<CronCheckin[]>(`/api/monitors/${selectedMonitorId.value}/checkins?limit=50`, { signal })
      : Promise.resolve([]),
  enabled: computed(() => !!selectedMonitorId.value),
})
const checkins = computed(() => checkinsData.value ?? [])

function selectMonitor(id: string) {
  selectedMonitorId.value = selectedMonitorId.value === id ? null : id
}

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

// Ping URLs are used by jobs outside the browser, so build them from the
// configured public URL rather than whatever host the admin browses with.
const { baseUrl } = useConfig()
function pingURL(id: string): string {
  return `${baseUrl.value || window.location.origin}/api/cron/${id}`
}

function copyPingURL(id: string) {
  copy(pingURL(id), 'Ping URL copied')
}

// ---- cron create ----
const showCreate = ref(false)
const newMonitor = ref({ name: '', slug: '', schedule: '0 * * * *', grace_period_secs: 300, project_id: '' })

const { mutate: createMonitor, isPending: creating } = useMutation({
  mutationFn: () =>
    apiFetch<CronMonitor>('/api/monitors', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: newMonitor.value.name,
        // Empty lets the server derive the slug from the name.
        ...(newMonitor.value.slug.trim() ? { slug: newMonitor.value.slug.trim() } : {}),
        schedule: newMonitor.value.schedule,
        grace_period_secs: Number(newMonitor.value.grace_period_secs),
        project_id: newMonitor.value.project_id,
      }),
    }),
  onSuccess(m) {
    qc.invalidateQueries({ queryKey: ['monitors'] })
    showCreate.value = false
    newMonitor.value = { name: '', slug: '', schedule: '0 * * * *', grace_period_secs: 300, project_id: '' }
    selectedMonitorId.value = m.id
  },
  onError: (err) => showToast(err instanceof Error ? err.message : 'Failed to create monitor.', 'error'),
})

// ---- cron edit ----
const editingId = ref<string | null>(null)
const editForm = ref({ name: '', slug: '', schedule: '', grace_period_secs: 300, status: 'active' as 'active' | 'paused' })

function startEdit(m: CronMonitor) {
  editingId.value = m.id
  editForm.value = { name: m.name, slug: m.slug, schedule: m.schedule, grace_period_secs: m.grace_period_secs, status: m.status }
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
  onError: (err) => showToast(err instanceof Error ? err.message : 'Failed to save monitor.', 'error'),
})

// ---- cron delete ----
const { mutate: deleteMonitor } = useMutation({
  mutationFn: (id: string) => apiFetch(`/api/monitors/${id}`, { method: 'DELETE' }),
  onSuccess() {
    qc.invalidateQueries({ queryKey: ['monitors'] })
    selectedMonitorId.value = null
    editingId.value = null
  },
})

async function confirmDelete(m: CronMonitor) {
  const ok = await confirm({ title: `Delete monitor "${m.name}"?`, message: 'Its check-in history is deleted too. This cannot be undone.', confirmLabel: 'Delete monitor', danger: true })
  if (ok) deleteMonitor(m.id)
}

function cancelCreate() {
  showCreate.value = false
  newMonitor.value = { name: '', slug: '', schedule: '0 * * * *', grace_period_secs: 300, project_id: '' }
}
</script>

<template>
  <div class="page">
    <MonitorsSubnav active="cron">
      <button
        v-if="canManage"
        class="btn btn--primary"
        @click="showCreate = !showCreate"
      >
        <Icon name="plus" :size="12" />
        New monitor
      </button>
    </MonitorsSubnav>

    <!-- Inline create form -->
    <div v-if="showCreate" class="mon-createbar">
      <div class="mon-createbar__fields">
        <div class="mon-field">
          <label class="mon-field__label">Name</label>
          <input v-model="newMonitor.name" class="mon-field__input" placeholder="Daily backup" autofocus />
        </div>
        <div class="mon-field">
          <label class="mon-field__label" for="new-monitor-slug">Slug</label>
          <input id="new-monitor-slug" v-model="newMonitor.slug" class="mon-field__input mono" placeholder="from the name" title="SDKs can send this as monitor_slug" />
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
          <input id="new-monitor-schedule" v-model="newMonitor.schedule" class="mon-field__input mono" placeholder="0 * * * *" />
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

    <div v-else-if="monitorsError" class="txerror" style="margin: 24px">
      <Icon name="alert-triangle" :size="14" class="txerror__icon" />
      <span>Couldn't load cron monitors. Check your connection and try again.</span>
      <button class="btn" @click="refetchMonitors()">Try again</button>
    </div>

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
      <p class="mon-verdict" role="status">
        <span v-if="verdict.failing" class="mon-verdict__failing">{{ verdict.failing }}</span>{{ verdict.failing && verdict.rest ? ' ' : '' }}{{ verdict.rest }}
      </p>
      <div class="monrow monrow--header">
        <span />
        <span>Monitor</span>
        <span>Last 20 runs</span>
        <span>Last seen</span>
        <span>Next expected</span>
        <span />
      </div>

      <template v-for="m in monitors" :key="m.id">
        <div
          class="monrow"
          :class="{ 'monrow--active': selectedMonitorId === m.id, 'monrow--paused': m.status === 'paused', 'monrow--failing': cronFailing(m) }"
          v-clickable
          @click="selectMonitor(m.id)"
        >
          <span class="mon-dot" :style="{ background: m.status === 'paused' ? 'var(--text-3)' : stateColor(m.state) }" :title="stateLabel(m.state)" />
          <div class="monrow__main">
            <div class="monrow__name" :title="m.name">{{ m.name }}</div>
            <div class="monrow__sub">
              <span :style="{ color: m.status === 'paused' ? 'var(--text-3)' : stateColor(m.state) }" class="monrow__state">{{ stateLabel(m.state) }}</span>
              <span v-if="cronFailingSince(m)" class="monrow__since">for {{ formatSpan(cronFailingSince(m)) }}</span>
              <template v-if="m.status === 'paused'">
                <span class="monrow__sep">·</span>
                <span class="mon-paused-badge">Paused</span>
              </template>
              <span class="monrow__sep">·</span>
              <span class="monrow__sched" :title="humanSchedule(m.schedule)">{{ humanSchedule(m.schedule) }}</span>
              <span class="monrow__sep">·</span>
              <span class="projtag" :title="projectName(m.project_id)">{{ projectName(m.project_id) }}</span>
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

        <!-- Inline detail -->
        <div v-if="selectedMonitorId === m.id" class="mon-detail">
          <div v-if="editingId === m.id" class="mon-editbar">
            <div class="mon-createbar__fields">
              <div class="mon-field">
                <label class="mon-field__label">Name</label>
                <input v-model="editForm.name" class="mon-field__input" />
              </div>
              <div class="mon-field">
                <label class="mon-field__label" for="edit-monitor-slug">Slug</label>
                <input id="edit-monitor-slug" v-model="editForm.slug" class="mon-field__input mono" />
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

          <template v-else>
            <div class="mon-detail__toolbar">
              <div class="mon-detail__meta">
                <span class="mon-detail__kv" title="Use as monitor_slug in Sentry SDK check-ins">
                  <span class="mon-detail__k">Slug</span>
                  <span class="mon-detail__v mono">{{ m.slug }}</span>
                </span>
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
                  <Icon name="pencil" :size="12" />
                  Edit
                </button>
              </div>
            </div>

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

            <div class="mon-ci-head">Check-in history</div>
            <div v-if="checkinsLoading" class="mon-ci-empty">Loading…</div>
            <div v-else-if="checkinsError" class="mon-ci-empty">
              Couldn't load check-ins.
              <button class="btn btn--ghost" @click="refetchCheckins()">Try again</button>
            </div>
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
                <span class="mon-ci-environment" :title="ci.environment ?? '–'" style="color:var(--text-3)">{{ ci.environment ?? '–' }}</span>
                <span style="color:var(--text-3)">{{ formatRel(ci.received_at) }}</span>
              </div>
            </template>
          </template>
        </div>
      </template>
    </template>
  </div>
</template>

<style scoped src="./monitors.css"></style>

<style scoped>
/* ---------- Ping URL ---------- */

.mon-ping {
  margin: 12px 20px 12px 44px;
  border-radius: 4px;
  width: auto;
}

.mon-ci-environment {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 640px) {
  .mon-ping { margin: 12px 16px; }
}
</style>
