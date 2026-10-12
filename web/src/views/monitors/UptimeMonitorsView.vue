<script setup lang="ts">
import { vClickable } from '@/directives/clickable'
import { ref, computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { useFormatters } from '@/composables/useFormatters'
import type { UptimeMonitor, UptimeCheck, UptimeStats } from '@/api/types'
import Icon from '@/components/Icon.vue'
import { useConfirm } from '@/composables/useConfirm'
import MonitorsSubnav from './MonitorsSubnav.vue'
import { useCanManage, useMonitorProjects, useMonitorQueryParams } from './shared'

const qc = useQueryClient()
const { formatRel } = useFormatters()
const { confirm } = useConfirm()

const canManage = useCanManage()

// ---- project list (for create dialog) ----
const { projectList, projectName } = useMonitorProjects()

const queryParams = useMonitorQueryParams()

// ---- uptime monitors ----
const selectedUptimeId = ref<string | null>(null)

const { data: uptimeData, isLoading: uptimeLoading, isError: uptimeError, refetch: refetchUptime } = useQuery({
  queryKey: computed(() => ['uptime-monitors', queryParams.value]),
  queryFn: ({ signal }) => apiFetch<UptimeMonitor[]>(`/api/uptime-monitors?${queryParams.value}`, { signal }),
})
const uptimeMonitors = computed(() => uptimeData.value ?? [])

const { data: uptimeChecksData, isLoading: uptimeChecksLoading, isError: uptimeChecksError, refetch: refetchUptimeChecks } = useQuery({
  queryKey: computed(() => ['uptime-checks', selectedUptimeId.value]),
  queryFn: ({ signal }) =>
    selectedUptimeId.value
      ? apiFetch<UptimeCheck[]>(`/api/uptime-monitors/${selectedUptimeId.value}/checks?limit=50`, { signal })
      : Promise.resolve([]),
  enabled: computed(() => !!selectedUptimeId.value),
})
const uptimeChecks = computed(() => uptimeChecksData.value ?? [])

const { data: uptimeStatsData } = useQuery({
  queryKey: computed(() => ['uptime-stats', selectedUptimeId.value]),
  queryFn: ({ signal }) =>
    selectedUptimeId.value
      ? apiFetch<UptimeStats>(`/api/uptime-monitors/${selectedUptimeId.value}/stats`, { signal })
      : Promise.resolve(null),
  enabled: computed(() => !!selectedUptimeId.value),
})
const uptimeStats = computed(() => uptimeStatsData.value ?? null)

function selectUptime(id: string) {
  selectedUptimeId.value = selectedUptimeId.value === id ? null : id
}

function uptimeStateColor(state: string): string {
  if (state === 'up') return 'var(--success)'
  if (state === 'down') return 'var(--danger)'
  return 'var(--text-3)'
}

function uptimeStateLabel(state: string): string {
  if (state === 'up') return 'Up'
  if (state === 'down') return 'Down'
  return 'Unknown'
}

function uptimeCheckColor(status: string): string {
  if (status === 'up') return 'var(--success)'
  if (status === 'down') return 'var(--danger)'
  return 'var(--text-3)'
}

function fmtUptime(pct: number): string {
  return pct.toFixed(2) + '%'
}

function fmtMs(ms: number | null): string {
  if (ms == null) return '–'
  return ms < 1000 ? `${Math.round(ms)}ms` : `${(ms / 1000).toFixed(2)}s`
}

// ---- uptime create ----
const showUptimeCreate = ref(false)
const newUptime = ref({
  name: '',
  url: '',
  method: 'GET' as 'GET' | 'HEAD',
  interval_secs: 300,
  timeout_secs: 10,
  expected_codes: '200-299',
  body_contains: '',
  project_id: '',
})

const { mutate: createUptime, isPending: creatingUptime } = useMutation({
  mutationFn: () =>
    apiFetch<UptimeMonitor>('/api/uptime-monitors', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: newUptime.value.name,
        url: newUptime.value.url,
        method: newUptime.value.method,
        interval_secs: Number(newUptime.value.interval_secs),
        timeout_secs: Number(newUptime.value.timeout_secs),
        expected_codes: newUptime.value.expected_codes,
        body_contains: newUptime.value.body_contains || undefined,
        project_id: newUptime.value.project_id,
      }),
    }),
  onSuccess(m) {
    qc.invalidateQueries({ queryKey: ['uptime-monitors'] })
    showUptimeCreate.value = false
    newUptime.value = { name: '', url: '', method: 'GET', interval_secs: 300, timeout_secs: 10, expected_codes: '200-299', body_contains: '', project_id: '' }
    selectedUptimeId.value = m.id
  },
})

function cancelUptimeCreate() {
  showUptimeCreate.value = false
  newUptime.value = { name: '', url: '', method: 'GET', interval_secs: 300, timeout_secs: 10, expected_codes: '200-299', body_contains: '', project_id: '' }
}

// ---- uptime edit ----
const editingUptimeId = ref<string | null>(null)
const editUptimeForm = ref({
  name: '',
  url: '',
  method: 'GET' as 'GET' | 'HEAD',
  interval_secs: 300,
  timeout_secs: 10,
  expected_codes: '200-299',
  body_contains: '',
  status: 'active' as 'active' | 'paused',
})

function startUptimeEdit(m: UptimeMonitor) {
  editingUptimeId.value = m.id
  editUptimeForm.value = {
    name: m.name,
    url: m.url,
    method: m.method,
    interval_secs: m.interval_secs,
    timeout_secs: m.timeout_secs,
    expected_codes: m.expected_codes,
    body_contains: m.body_contains ?? '',
    status: m.status,
  }
}

const { mutate: saveUptime, isPending: savingUptime } = useMutation({
  mutationFn: (id: string) =>
    apiFetch<UptimeMonitor>(`/api/uptime-monitors/${id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: editUptimeForm.value.name,
        url: editUptimeForm.value.url,
        method: editUptimeForm.value.method,
        interval_secs: Number(editUptimeForm.value.interval_secs),
        timeout_secs: Number(editUptimeForm.value.timeout_secs),
        expected_codes: editUptimeForm.value.expected_codes,
        body_contains: editUptimeForm.value.body_contains,
        status: editUptimeForm.value.status,
      }),
    }),
  onSuccess() {
    qc.invalidateQueries({ queryKey: ['uptime-monitors'] })
    editingUptimeId.value = null
  },
})

// ---- uptime delete ----
const { mutate: deleteUptime } = useMutation({
  mutationFn: (id: string) => apiFetch(`/api/uptime-monitors/${id}`, { method: 'DELETE' }),
  onSuccess() {
    qc.invalidateQueries({ queryKey: ['uptime-monitors'] })
    selectedUptimeId.value = null
    editingUptimeId.value = null
  },
})

async function confirmDeleteUptime(m: UptimeMonitor) {
  const ok = await confirm({ title: `Delete uptime monitor "${m.name}"?`, message: 'Its check history is deleted too. This cannot be undone.', confirmLabel: 'Delete monitor', danger: true })
  if (ok) deleteUptime(m.id)
}
</script>

<template>
  <div class="page">
    <MonitorsSubnav active="uptime">
      <button
        v-if="canManage"
        class="btn btn--primary"
        @click="showUptimeCreate = !showUptimeCreate"
      >
        <Icon name="plus" :size="12" />
        New monitor
      </button>
    </MonitorsSubnav>

    <!-- Inline create form -->
    <div v-if="showUptimeCreate" class="mon-createbar">
      <div class="mon-createbar__fields mon-createbar__fields--uptime">
        <div class="mon-field">
          <label class="mon-field__label">Name</label>
          <input v-model="newUptime.name" class="mon-field__input" placeholder="Homepage" autofocus />
        </div>
        <div class="mon-field">
          <label class="mon-field__label">Project</label>
          <select v-model="newUptime.project_id" class="mon-field__input">
            <option value="">Select project</option>
            <option v-for="p in projectList" :key="p.id" :value="p.id">{{ p.name }}</option>
          </select>
        </div>
        <div class="mon-field mon-field--url">
          <label class="mon-field__label">URL</label>
          <input v-model="newUptime.url" class="mon-field__input mono" placeholder="https://example.com" />
        </div>
        <div class="mon-field">
          <label class="mon-field__label">Method</label>
          <select v-model="newUptime.method" class="mon-field__input">
            <option value="GET">GET</option>
            <option value="HEAD">HEAD</option>
          </select>
        </div>
        <div class="mon-field">
          <label class="mon-field__label">Interval (secs)</label>
          <input v-model.number="newUptime.interval_secs" class="mon-field__input" type="number" min="60" />
        </div>
        <div class="mon-field">
          <label class="mon-field__label">Timeout (secs)</label>
          <input v-model.number="newUptime.timeout_secs" class="mon-field__input" type="number" min="1" max="60" />
        </div>
        <div class="mon-field">
          <label class="mon-field__label">Expected codes</label>
          <input v-model="newUptime.expected_codes" class="mon-field__input mono" placeholder="200-299" />
        </div>
        <div class="mon-field">
          <label class="mon-field__label">Body contains</label>
          <input v-model="newUptime.body_contains" class="mon-field__input" placeholder="(optional)" />
        </div>
      </div>
      <div class="mon-createbar__actions">
        <button
          class="btn btn--primary"
          :disabled="creatingUptime || !newUptime.name || !newUptime.url || !newUptime.project_id"
          @click="createUptime()"
        >{{ creatingUptime ? 'Creating…' : 'Create' }}</button>
        <button class="btn btn--ghost" @click="cancelUptimeCreate">Cancel</button>
      </div>
    </div>

    <!-- Skeleton -->
    <template v-if="uptimeLoading">
      <div v-for="i in 4" :key="i" class="monrow uprow">
        <span class="skel" style="width:8px;height:8px;border-radius:50%;flex:0 0 8px" />
        <div style="display:flex;flex-direction:column;gap:5px;min-width:0">
          <span class="skel" style="width:160px;height:10px;display:block" />
          <span class="skel" style="width:220px;height:8px;display:block" />
        </div>
        <span class="skel" style="width:170px;height:8px;display:block;border-radius:2px" />
        <span class="skel" style="width:70px;height:10px;display:block" />
        <span class="skel" style="width:70px;height:10px;display:block" />
        <span />
      </div>
    </template>

    <div v-else-if="uptimeError" class="txerror" style="margin: 24px">
      <Icon name="alert-triangle" :size="14" class="txerror__icon" />
      <span>Couldn't load uptime monitors. Check your connection and try again.</span>
      <button class="btn" @click="refetchUptime()">Try again</button>
    </div>

    <!-- Empty state -->
    <div v-else-if="uptimeMonitors.length === 0" class="empty-state">
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
          <Icon name="activity" :size="24" style="color: var(--accent)" />
        </div>
        <h2 class="empty-state__title">No uptime monitors yet</h2>
        <p class="empty-state__body">Create a monitor to periodically probe a URL and alert you if it goes down.</p>
        <div v-if="canManage" class="empty-state__actions">
          <button class="btn btn--primary" @click="showUptimeCreate = true">
            <Icon name="plus" :size="12" />
            New monitor
          </button>
        </div>
      </div>
    </div>

    <!-- List -->
    <template v-else>
      <div class="monrow uprow monrow--header">
        <span />
        <span>Monitor</span>
        <span>Last 20 checks</span>
        <span>Last checked</span>
        <span>Next check</span>
        <span />
      </div>

      <template v-for="m in uptimeMonitors" :key="m.id">
        <div
          class="monrow uprow"
          :class="{ 'monrow--active': selectedUptimeId === m.id, 'monrow--paused': m.status === 'paused' }"
          v-clickable
          @click="selectUptime(m.id)"
        >
          <span class="mon-dot" :style="{ background: m.status === 'paused' ? 'var(--text-3)' : uptimeStateColor(m.state) }" :title="uptimeStateLabel(m.state)" />
          <div class="monrow__main">
            <div class="monrow__name" :title="m.name">{{ m.name }}</div>
            <div class="monrow__sub">
              <span :style="{ color: m.status === 'paused' ? 'var(--text-3)' : uptimeStateColor(m.state) }" class="monrow__state">{{ uptimeStateLabel(m.state) }}</span>
              <template v-if="m.status === 'paused'">
                <span class="monrow__sep">·</span>
                <span class="mon-paused-badge">Paused</span>
              </template>
              <span class="monrow__sep">·</span>
              <span class="monrow__sched mono" :title="m.url" style="font-size:10px">{{ m.url }}</span>
              <span class="monrow__sep">·</span>
              <span class="projtag" :title="projectName(m.project_id)">{{ projectName(m.project_id) }}</span>
            </div>
          </div>
          <div class="mon-timeline">
            <span
              v-for="i in Math.max(0, 20 - m.recent_checks.length)"
              :key="`e${i}`"
              class="mon-tl-dot mon-tl-dot--empty"
            />
            <span
              v-for="dot in m.recent_checks"
              :key="dot.checked_at"
              class="mon-tl-dot"
              :style="{ background: uptimeCheckColor(dot.status) }"
              :title="`${dot.status} · ${formatRel(dot.checked_at)}`"
            />
          </div>
          <span class="monrow__time">{{ m.last_checked_at ? formatRel(m.last_checked_at) : '–' }}</span>
          <span class="monrow__time">{{ m.next_check_at ? formatRel(m.next_check_at) : '–' }}</span>
          <span class="monrow__chevron">
            <Icon :name="selectedUptimeId === m.id ? 'chevron-up' : 'chevron-down'" :size="12" />
          </span>
        </div>

        <!-- Inline detail -->
        <div v-if="selectedUptimeId === m.id" class="mon-detail">
          <!-- Edit form -->
          <div v-if="editingUptimeId === m.id" class="mon-editbar">
            <div class="mon-createbar__fields mon-createbar__fields--uptime">
              <div class="mon-field">
                <label class="mon-field__label">Name</label>
                <input v-model="editUptimeForm.name" class="mon-field__input" />
              </div>
              <div class="mon-field mon-field--url">
                <label class="mon-field__label">URL</label>
                <input v-model="editUptimeForm.url" class="mon-field__input mono" />
              </div>
              <div class="mon-field">
                <label class="mon-field__label">Method</label>
                <select v-model="editUptimeForm.method" class="mon-field__input">
                  <option value="GET">GET</option>
                  <option value="HEAD">HEAD</option>
                </select>
              </div>
              <div class="mon-field">
                <label class="mon-field__label">Interval (secs)</label>
                <input v-model.number="editUptimeForm.interval_secs" class="mon-field__input" type="number" min="60" />
              </div>
              <div class="mon-field">
                <label class="mon-field__label">Timeout (secs)</label>
                <input v-model.number="editUptimeForm.timeout_secs" class="mon-field__input" type="number" min="1" max="60" />
              </div>
              <div class="mon-field">
                <label class="mon-field__label">Expected codes</label>
                <input v-model="editUptimeForm.expected_codes" class="mon-field__input mono" />
              </div>
              <div class="mon-field">
                <label class="mon-field__label">Body contains</label>
                <input v-model="editUptimeForm.body_contains" class="mon-field__input" placeholder="(clear to remove)" />
              </div>
              <div class="mon-field">
                <label class="mon-field__label">Status</label>
                <select v-model="editUptimeForm.status" class="mon-field__input">
                  <option value="active">Active</option>
                  <option value="paused">Paused</option>
                </select>
              </div>
            </div>
            <div class="mon-createbar__actions">
              <button class="btn btn--primary" :disabled="savingUptime" @click="saveUptime(m.id)">
                {{ savingUptime ? 'Saving…' : 'Save' }}
              </button>
              <button class="btn btn--ghost" @click="editingUptimeId = null">Cancel</button>
              <button class="btn btn--ghost" style="color:var(--danger);margin-left:auto" @click="confirmDeleteUptime(m)">
                Delete
              </button>
            </div>
          </div>

          <!-- Detail view -->
          <template v-else>
            <div class="mon-detail__toolbar">
              <div class="mon-detail__meta">
                <span class="mon-detail__kv">
                  <span class="mon-detail__k">Method</span>
                  <span class="mon-detail__v">{{ m.method }}</span>
                </span>
                <span class="mon-detail__kv">
                  <span class="mon-detail__k">Interval</span>
                  <span class="mon-detail__v">{{ m.interval_secs }}s</span>
                </span>
                <span class="mon-detail__kv">
                  <span class="mon-detail__k">Timeout</span>
                  <span class="mon-detail__v">{{ m.timeout_secs }}s</span>
                </span>
                <span class="mon-detail__kv">
                  <span class="mon-detail__k">Expected</span>
                  <span class="mon-detail__v mono">{{ m.expected_codes }}</span>
                </span>
                <span v-if="m.last_status_code" class="mon-detail__kv">
                  <span class="mon-detail__k">Last code</span>
                  <span class="mon-detail__v">{{ m.last_status_code }}</span>
                </span>
                <span v-if="m.last_response_ms != null" class="mon-detail__kv">
                  <span class="mon-detail__k">Last ms</span>
                  <span class="mon-detail__v">{{ fmtMs(m.last_response_ms) }}</span>
                </span>
                <span class="mon-detail__kv">
                  <span class="mon-detail__k">Status</span>
                  <span class="mon-detail__v">{{ m.status }}</span>
                </span>
              </div>
              <div class="mon-detail__actions">
                <button v-if="canManage" class="btn btn--ghost" @click="startUptimeEdit(m)">
                  <Icon name="pencil" :size="12" />
                  Edit
                </button>
              </div>
            </div>

            <!-- Stats bar -->
            <div v-if="uptimeStats" class="up-stats">
              <div class="up-stat">
                <span class="up-stat__label">24h uptime</span>
                <span class="up-stat__value" :style="{ color: uptimeStats.uptime_pct_24h >= 99 ? 'var(--success)' : uptimeStats.uptime_pct_24h >= 95 ? 'var(--warning)' : 'var(--danger)' }">
                  {{ fmtUptime(uptimeStats.uptime_pct_24h) }}
                </span>
              </div>
              <div class="up-stat">
                <span class="up-stat__label">7d uptime</span>
                <span class="up-stat__value" :style="{ color: uptimeStats.uptime_pct_7d >= 99 ? 'var(--success)' : uptimeStats.uptime_pct_7d >= 95 ? 'var(--warning)' : 'var(--danger)' }">
                  {{ fmtUptime(uptimeStats.uptime_pct_7d) }}
                </span>
              </div>
              <div class="up-stat">
                <span class="up-stat__label">30d uptime</span>
                <span class="up-stat__value" :style="{ color: uptimeStats.uptime_pct_30d >= 99 ? 'var(--success)' : uptimeStats.uptime_pct_30d >= 95 ? 'var(--warning)' : 'var(--danger)' }">
                  {{ fmtUptime(uptimeStats.uptime_pct_30d) }}
                </span>
              </div>
              <div class="up-stat">
                <span class="up-stat__label">Avg response</span>
                <span class="up-stat__value">{{ fmtMs(uptimeStats.avg_response_ms_24h) }}</span>
              </div>
            </div>

            <!-- Check history -->
            <div class="mon-ci-head">Check history</div>
            <div v-if="uptimeChecksLoading" class="mon-ci-empty">Loading…</div>
            <div v-else-if="uptimeChecksError" class="mon-ci-empty">
              Couldn't load checks.
              <button class="btn btn--ghost" @click="refetchUptimeChecks()">Try again</button>
            </div>
            <div v-else-if="uptimeChecks.length === 0" class="mon-ci-empty">No checks yet.</div>
            <template v-else>
              <div class="mon-ci-row up-ci-row mon-ci-row--header">
                <span>Status</span>
                <span>Code</span>
                <span>Response</span>
                <span>Error</span>
                <span>Checked</span>
              </div>
              <div v-for="ck in uptimeChecks" :key="ck.id" class="mon-ci-row up-ci-row">
                <div class="mon-ci-status">
                  <span class="mon-dot" :style="{ background: uptimeCheckColor(ck.status) }" />
                  <span :style="{ color: uptimeCheckColor(ck.status) }">{{ ck.status }}</span>
                </div>
                <span style="color:var(--text-2)">{{ ck.status_code ?? '–' }}</span>
                <span style="color:var(--text-2)">{{ fmtMs(ck.response_ms) }}</span>
                <span style="color:var(--text-3);white-space:nowrap;overflow:hidden;text-overflow:ellipsis">{{ ck.error ?? '–' }}</span>
                <span style="color:var(--text-3)">{{ formatRel(ck.checked_at) }}</span>
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
/* ---------- Uptime stats bar ---------- */

.up-stats {
  display: flex;
  gap: 32px;
  padding: 10px 20px 10px 44px;
  border-bottom: 1px solid var(--border-soft);
}

.up-stat {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.up-stat__label {
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-3);
}

.up-stat__value {
  font-size: var(--text-sm);
  font-weight: 600;
  color: var(--text-1);
}

.up-ci-row {
  grid-template-columns: 80px 60px 80px minmax(0,1fr) 100px;
}

.mon-createbar__fields--uptime {
  grid-template-columns: 1fr 1fr 2fr 1fr 1fr 1fr 1fr 1fr;
}

@media (max-width: 900px) {
  .mon-createbar__fields--uptime {
    grid-template-columns: 1fr 1fr 1fr 1fr;
  }
}

@media (max-width: 768px) {
  .mon-createbar__fields--uptime { grid-template-columns: 1fr 1fr; }
}
@media (max-width: 480px) {
  .mon-createbar__fields--uptime { grid-template-columns: 1fr; }
}

@media (max-width: 640px) {
  .up-stats { padding: 10px 16px; gap: 16px; flex-wrap: wrap; }

  .up-ci-row {
    grid-template-columns: 70px 55px 1fr;
    padding: 0 16px;
  }
  .up-ci-row > :nth-child(4) { display: none; }
  .up-ci-row > :nth-child(5) { display: none; }
}
</style>
