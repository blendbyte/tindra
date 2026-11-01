<script setup lang="ts">
import { computed } from 'vue'
import { useInvestigationStore } from '@/stores/investigation'
import { useProjectsStore } from '@/stores/projects'
import { useFormatters } from '@/composables/useFormatters'
import { useMonitorQueries } from '@/composables/useBriefingData'
import { useNow } from '@/composables/useNow'
import { cronFailing, cronUnchecked, uptimeFailing } from '@/views/monitors/urgency'
import type { CronMonitor, UptimeMonitor } from '@/api/types'
import Icon from '@/components/Icon.vue'
import CheckHistory from './CheckHistory.vue'
import { cronHistory, uptimeHistory, type CheckState } from './briefing'

// When nothing is failing, the monitors themselves take the briefing's place:
// each one with its recent checks, so a calm page still shows what was checked.

const investigation = useInvestigationStore()
const projects = useProjectsStore()
const { formatRel } = useFormatters()
const now = useNow()
const { uptime, cron } = useMonitorQueries()

const LIMIT = 8

interface Row {
  key: string
  name: string
  meta: string[]
  to: string
  history: CheckState[]
  checked: string | null
  unchecked: boolean
  failing: boolean
}

const multiProject = computed(() => projects.projects.length > 1)
const projectNames = computed(() => new Map(projects.projects.map(p => [p.id, p.name])))
function projectName(id: string): string | null {
  return multiProject.value ? projectNames.value.get(id) ?? null : null
}

function uptimeRow(m: UptimeMonitor): Row {
  return {
    key: `uptime-${m.id}`,
    name: m.name,
    meta: [projectName(m.project_id), 'Uptime'].filter((p): p is string => !!p),
    to: investigation.link('/monitors/uptime'),
    history: uptimeHistory(m),
    checked: m.last_checked_at,
    unchecked: m.state === 'unknown' || !m.last_checked_at,
    failing: uptimeFailing(m),
  }
}

function cronRow(m: CronMonitor): Row {
  return {
    key: `cron-${m.id}`,
    name: m.name,
    meta: [projectName(m.project_id), 'Cron'].filter((p): p is string => !!p),
    to: investigation.link('/monitors/cron'),
    history: cronHistory(m),
    checked: m.last_checkin_at,
    unchecked: cronUnchecked(m),
    failing: cronFailing(m),
  }
}

const uptimeList = computed(() => Array.isArray(uptime.data.value) ? uptime.data.value : [])
const cronList = computed(() => Array.isArray(cron.data.value) ? cron.data.value : [])
const active = computed(() => [
  ...uptimeList.value.filter(m => m.status === 'active').map(uptimeRow),
  ...cronList.value.filter(m => m.status === 'active').map(cronRow),
])
const paused = computed(() => uptimeList.value.length + cronList.value.length - active.value.length)
const passing = computed(() => active.value.filter(r => !r.failing && !r.unchecked).length)
const unchecked = computed(() => active.value.filter(r => r.unchecked).length)

// Not yet checked sorts last: the rest have a history worth reading first.
const rows = computed(() => [...active.value].sort((a, b) => Number(a.unchecked) - Number(b.unchecked)).slice(0, LIMIT))
const hidden = computed(() => active.value.length - rows.value.length)

const summary = computed(() => [
  `${passing.value} up`,
  unchecked.value ? `${unchecked.value} not checked yet` : null,
  paused.value ? `${paused.value} paused` : null,
].filter(Boolean).join(' · '))

function age(row: Row): string {
  void now.value
  return row.checked ? formatRel(row.checked) : ''
}
</script>

<template>
  <section v-if="active.length || paused" class="db-sec" aria-labelledby="monitors-title">
    <div class="db-sec__head">
      <h2 id="monitors-title" class="db-sec__title mon__title">Monitors <span class="mon__summary">{{ summary }}</span></h2>
      <RouterLink :to="investigation.link('/monitors')" class="db-sec__link">
        {{ hidden ? `${hidden} more` : 'all monitors' }} <Icon name="arrow-right" :size="11" />
      </RouterLink>
    </div>
    <div v-if="!active.length" class="mon__paused">Every monitor is paused, so nothing is being checked.</div>
    <div v-else class="mon-wrap">
      <ul class="mon">
        <li v-for="row in rows" :key="row.key">
          <RouterLink :to="row.to" class="mon__row">
            <span class="mon__main">
              <span class="mon__name">{{ row.name }}</span>
              <span class="mon__meta">{{ row.meta.join(' · ') }}</span>
            </span>
            <span class="mon__history"><CheckHistory :history="row.history" /></span>
            <span v-if="row.unchecked" class="mon__age mon__age--none">Not checked yet</span>
            <span v-else class="mon__age">{{ age(row) }}</span>
          </RouterLink>
        </li>
      </ul>
    </div>
  </section>
</template>

<style scoped src="./section.css"></style>

<style scoped>
.mon__title { display: flex; flex-wrap: wrap; align-items: baseline; gap: 2px 8px; }

.mon__summary {
  font-family: var(--mono);
  font-size: var(--text-xs);
  font-weight: 500;
  color: var(--text-3);
}

.mon-wrap { container-type: inline-size; }

.mon {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  grid-template-columns: minmax(0, 1fr);
}

.mon__row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 132px 64px;
  align-items: center;
  gap: 12px;
  padding: 9px 16px;
  border-bottom: 1px solid var(--border-soft);
  color: inherit;
  text-decoration: none;
  transition: background-color 120ms cubic-bezier(0.16, 1, 0.3, 1);
}

.mon__row:hover { background: var(--surface-2); }
.mon__row:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }

.mon__main { display: flex; flex-direction: column; gap: 2px; min-width: 0; }

.mon__name {
  font-size: var(--text-sm);
  color: var(--text-1);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.mon__meta {
  font-size: var(--text-xs);
  color: var(--text-3);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.mon__age {
  justify-self: end;
  text-align: right;
  font-family: var(--mono);
  font-size: var(--text-xs);
  font-variant-numeric: tabular-nums;
  color: var(--text-2);
}

.mon__age--none { font-family: inherit; color: var(--text-3); }

.mon__paused {
  padding: 16px;
  font-size: var(--text-sm);
  color: var(--text-3);
}

/* Two columns when there is room, meeting on a soft hairline. */
@container (min-width: 900px) {
  .mon { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .mon li:nth-child(odd) { border-right: 1px solid var(--border-soft); }
}

@container (max-width: 400px) {
  .mon__row { grid-template-columns: minmax(0, 1fr) auto; }
  .mon__history { display: none; }
}

@media (prefers-reduced-motion: reduce) {
  .mon__row { transition: none; }
}
</style>
