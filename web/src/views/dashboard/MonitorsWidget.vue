<script setup lang="ts">
import { computed, ref } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { useInvestigationStore } from '@/stores/investigation'
import type { UptimeMonitor, CronMonitor } from '@/api/types'
import Icon from '@/components/Icon.vue'
import { useProjectScope } from './shared'

const investigation = useInvestigationStore()
const { buildQs } = useProjectScope()

const { data: uptimeMonitors } = useQuery({
  queryKey: computed(() => ['uptime-monitors', buildQs()]),
  queryFn: ({ signal }) => apiFetch<UptimeMonitor[]>(`/api/uptime-monitors?${buildQs()}`, { signal }),

})

const { data: cronMonitors } = useQuery({
  queryKey: computed(() => ['monitors', buildQs()]),
  queryFn: ({ signal }) => apiFetch<CronMonitor[]>(`/api/monitors?${buildQs()}`, { signal }),

})

// ── Monitor summary ───────────────────────────────────────────────────────────

const activeUptime = computed(() =>
  (uptimeMonitors.value ?? []).filter(m => m.status === 'active'),
)
const activeCron = computed(() =>
  (cronMonitors.value ?? []).filter(m => m.status === 'active'),
)
const totalActiveMonitors = computed(() => activeUptime.value.length + activeCron.value.length)
const totalAllMonitors = computed(() => (uptimeMonitors.value?.length ?? 0) + (cronMonitors.value?.length ?? 0))

const downUptime = computed(() => activeUptime.value.filter(m => m.state === 'down'))
const badCron = computed(() => activeCron.value.filter(m => m.state === 'missed' || m.state === 'error'))

const allMonitorsHealthy = computed(
  () => totalActiveMonitors.value > 0 && downUptime.value.length === 0 && badCron.value.length === 0,
)

const monitorsExpanded = ref(false)

const problemMonitors = computed(() => {
  const uptime = downUptime.value.map(m => ({ kind: 'uptime' as const, m }))
  const cron = badCron.value.map(m => ({ kind: 'cron' as const, m }))
  const all = [...uptime, ...cron]
  return monitorsExpanded.value ? all : all.slice(0, 5)
})

const overflowMonitorCount = computed(
  () => Math.max(0, downUptime.value.length + badCron.value.length - 5),
)

function monitorSinceMs(kind: 'uptime' | 'cron', m: UptimeMonitor | CronMonitor): number | null {
  if (kind === 'uptime') {
    const u = m as UptimeMonitor
    const ref = u.went_down_at ?? u.last_ok_at
    return ref ? Date.now() - new Date(ref).getTime() : null
  }
  const c = m as CronMonitor
  const ref = c.next_expected_at ?? c.last_checkin_at
  return ref ? Date.now() - new Date(ref).getTime() : null
}

function formatMonSince(ms: number | null): string {
  if (ms === null || ms < 0) return ''
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  const rm = m % 60
  if (h < 24) return rm > 0 ? `${h}h ${rm}m` : `${h}h`
  const d = Math.floor(h / 24)
  return `${d}d`
}

function monStateColor(kind: 'uptime' | 'cron', state: string): string {
  if (kind === 'uptime') return state === 'down' ? 'var(--danger)' : 'var(--success)'
  if (state === 'missed' || state === 'error') return 'var(--danger)'
  if (state === 'in_progress') return 'var(--warning)'
  return 'var(--success)'
}
</script>

<template>
  <div v-if="totalAllMonitors > 0" class="db-sec db-monitors">
    <div class="db-sec__head">
      <span class="db-sec__title">Monitors</span>
      <RouterLink :to="investigation.link('/monitors')" class="db-sec__link">view all →</RouterLink>
    </div>

    <!-- All paused -->
    <div v-if="totalActiveMonitors === 0" class="db-empty">
      <Icon name="pause-circle" :size="18" style="color: var(--text-3)" />
      <div>All {{ totalAllMonitors }} monitor{{ totalAllMonitors === 1 ? '' : 's' }} paused</div>
    </div>

    <!-- All healthy -->
    <div v-else-if="allMonitorsHealthy" class="db-empty">
      <Icon name="check-circle" :size="18" style="color: var(--success)" />
      <div>All {{ totalActiveMonitors }} monitor{{ totalActiveMonitors === 1 ? '' : 's' }} healthy</div>
    </div>

    <!-- Problems -->
    <template v-else>
      <RouterLink
        v-for="{ kind, m } in problemMonitors"
        :key="m.id"
        :to="investigation.link('/monitors')"
        class="db-mon-row"
      >
        <span class="db-mon-row__dot" :style="{ background: monStateColor(kind, m.state) }" />
        <div class="db-mon-row__main">
          <span class="db-mon-row__name">{{ m.name }}</span>
          <div class="db-mon-row__meta">
            <span class="db-mon-badge" :class="`db-mon-badge--${kind}`">{{ kind }}</span>
            <span v-if="kind === 'cron' && (m as CronMonitor).state === 'missed'">missed</span>
            <span v-else-if="kind === 'cron' && (m as CronMonitor).state === 'error'">error</span>
            <span v-else-if="kind === 'uptime' && (m as UptimeMonitor).last_status_code">
              HTTP {{ (m as UptimeMonitor).last_status_code }}
            </span>
            <span v-else-if="kind === 'uptime' && (m as UptimeMonitor).last_error">
              {{ (m as UptimeMonitor).last_error }}
            </span>
          </div>
        </div>
        <span class="db-mon-row__since" :style="{ color: monStateColor(kind, m.state) }">
          {{ formatMonSince(monitorSinceMs(kind, m)) }}
        </span>
      </RouterLink>

      <button
        v-if="overflowMonitorCount > 0 || monitorsExpanded"
        class="db-mon-overflow"
        @click="monitorsExpanded = !monitorsExpanded"
      >
        <template v-if="monitorsExpanded">Show less</template>
        <template v-else>+{{ overflowMonitorCount }} more</template>
      </button>
    </template>
  </div>
</template>

<style scoped src="./section.css"></style>

<style scoped>
/* ── Monitor summary ────────────────────────────────────────────────────────── */

.db-mon-row {
  display: grid;
  grid-template-columns: 8px 1fr auto;
  align-items: center;
  gap: 12px;
  padding: 8px 16px;
  border-bottom: 1px solid var(--border);
  text-decoration: none;
  color: inherit;
  cursor: pointer;
}

.db-mon-row:last-of-type { border-bottom: none; }
.db-mon-row:hover { background: var(--surface-2); }

.db-mon-row__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
  box-shadow: 0 0 0 2px oklch(from currentcolor l c h / 0.15);
}

.db-mon-row__main {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.db-mon-row__name {
  font-size: var(--text-sm);
  font-weight: 500;
  color: var(--text-1);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.db-mon-row__meta {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: var(--text-xs);
  color: var(--text-3);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.db-mon-badge {
  font-size: 10px;
  font-weight: 600;
  padding: 1px 5px;
  border-radius: 3px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  flex-shrink: 0;
}

.db-mon-badge--uptime {
  background: oklch(from var(--accent) l c h / 0.12);
  color: var(--accent);
}

.db-mon-badge--cron {
  background: oklch(from var(--text-3) l c h / 0.10);
  color: var(--text-2);
}

.db-mon-row__since {
  font-size: var(--text-xs);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  flex-shrink: 0;
  text-align: right;
}

.db-mon-overflow {
  all: unset;
  box-sizing: border-box;
  cursor: pointer;
  display: block;
  width: 100%;
  padding: 7px 16px;
  font-size: var(--text-xs);
  color: var(--accent);
  border-top: 1px solid var(--border);
  text-align: center;
}

.db-mon-overflow:hover { text-decoration: underline; text-underline-offset: 2px; }
</style>
