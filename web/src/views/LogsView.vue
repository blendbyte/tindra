<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useRoute, useRouter } from 'vue-router'
import { apiFetch } from '@/api/client'
import { useFormatters } from '@/composables/useFormatters'
import type { Log, LogListPage } from '@/api/types'
import Icon from '@/components/Icon.vue'
import FilterChip from '@/components/FilterChip.vue'
import { useProjectsStore } from '@/stores/projects'
import { useAuthStore } from '@/stores/auth'

const projects = useProjectsStore()
const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const { formatTs } = useFormatters()

const LEVELS = ['Fatal', 'Error', 'Warning', 'Info', 'Debug', 'Trace']
const ENVS = ['production', 'staging', 'preview', 'development']

function queryParam(v: unknown): string {
  if (Array.isArray(v)) return String(v[0] ?? '')
  return typeof v === 'string' ? v : ''
}
function queryParamList(v: unknown): string[] {
  if (Array.isArray(v)) return v.filter((x): x is string => typeof x === 'string' && x !== '')
  if (typeof v === 'string' && v) return [v]
  return []
}

function levelFromQuery(raw: string): string {
  const lower = raw.toLowerCase()
  if (lower === 'warn') return 'Warning'
  const cap = lower.charAt(0).toUpperCase() + lower.slice(1)
  return LEVELS.includes(cap) ? cap : 'All'
}

const q = route.query
const minLevelMode = ref(!!queryParam(q.min_level))
const levelFilter = ref(levelFromQuery(queryParam(q.min_level) || queryParam(q.level)))
const envFromQuery = queryParam(q.environment)
const envFilter = ref(envFromQuery || 'All')
const searchQuery = ref(queryParam(q.search))
const pids = queryParamList(q.project_id)
if (pids.length) projects.setSelected(pids)

const envOptions = computed(() => {
  const base = ['All', ...ENVS]
  if (envFilter.value !== 'All' && !base.includes(envFilter.value)) {
    return [...base, envFilter.value]
  }
  return base
})

const canManageAlerts = computed(() => auth.user?.permissions.manage_alerts ?? false)
const selectedProjectIds = computed(() => projects.selectedIds)
// Navbar "All projects" is an empty selection, which still lists every
// project's logs. The alert needs explicit project IDs, so use the full list.
const alertProjectIds = computed(() => {
  if (selectedProjectIds.value.length > 0) return selectedProjectIds.value
  return (projects.projects ?? []).map((p) => p.id)
})
const canAlertOnThis = computed(() => {
  if (alertProjectIds.value.length === 0) return false
  const lv = levelFilter.value
  if (lv === 'Error' || lv === 'Fatal') return true
  return lv === 'Warning' && searchQuery.value.trim() !== ''
})
const alertOnThisHint = computed(() => {
  if (canAlertOnThis.value) return 'Create an alert from these filters'
  if (alertProjectIds.value.length === 0) return 'Create a project first'
  if (levelFilter.value === 'Warning') return 'Add a search — warning alerts need a message match'
  return 'Set Level to Error or Fatal, or Warning with a search'
})

function alertOnThis() {
  const params = new URLSearchParams()
  params.set('new', '1')
  params.set('trigger', 'log_count')
  const lv = levelFilter.value.toLowerCase()
  if (lv === 'error' || lv === 'fatal' || lv === 'warning') params.set('level', lv)
  if (envFilter.value !== 'All') params.set('environment', envFilter.value)
  if (searchQuery.value.trim()) params.set('search', searchQuery.value.trim())
  for (const id of alertProjectIds.value) params.append('project_id', id)
  router.push(`/settings/alerts?${params}`)
}

// The project column only earns its space when the filter leaves more than one
// project in play — with exactly one selected every row would repeat its name.
const showProject = computed(() => selectedProjectIds.value.length !== 1)
const colCount = computed(() => (showProject.value ? 5 : 4))

const projectName = computed(() => {
  const map = new Map((projects.projects ?? []).map((p) => [p.id, p.name]))
  return (id: string) => map.get(id) ?? id
})

const queryParams = computed(() => {
  const p = new URLSearchParams()
  for (const id of selectedProjectIds.value) p.append('project_id', id)
  if (minLevelMode.value && ['Error', 'Fatal', 'Warning'].includes(levelFilter.value)) {
    p.set('min_level', levelFilter.value.toLowerCase())
  } else if (levelFilter.value !== 'All') {
    p.set('level', levelFilter.value.toLowerCase())
  }
  if (envFilter.value !== 'All') p.set('environment', envFilter.value)
  if (searchQuery.value) p.set('search', searchQuery.value)
  return p.toString()
})

const { data, isLoading, isFetching, refetch } = useQuery({
  queryKey: computed(() => ['logs', queryParams.value]),
  queryFn: () => apiFetch<LogListPage>(`/api/logs?${queryParams.value}`),
  refetchInterval: 5000,
})

const logs = computed(() => data.value?.logs ?? [])

const expandedId = ref<string | null>(null)
function toggleRow(id: string) {
  expandedId.value = expandedId.value === id ? null : id
}

function attrEntries(log: Log): [string, unknown][] {
  return Object.entries(log.attributes ?? {})
}

function envBadgeClass(env: string) {
  if (env === 'production') return 'envbadge--prod'
  if (env === 'staging') return 'envbadge--staging'
  return ''
}

watch(queryParams, () => { expandedId.value = null })

let writingQuery = false
watch([levelFilter, envFilter, searchQuery, selectedProjectIds], () => {
  writingQuery = true
  const query: Record<string, string | string[]> = { ...route.query } as Record<string, string | string[]>
  delete query.level
  delete query.min_level
  if (minLevelMode.value && ['Error', 'Fatal', 'Warning'].includes(levelFilter.value)) {
    query.min_level = levelFilter.value.toLowerCase()
  } else if (levelFilter.value !== 'All') {
    query.level = levelFilter.value.toLowerCase()
  }
  if (envFilter.value !== 'All') query.environment = envFilter.value
  else delete query.environment
  if (searchQuery.value) query.search = searchQuery.value
  else delete query.search
  if (selectedProjectIds.value.length) query.project_id = selectedProjectIds.value
  else delete query.project_id
  router.replace({ query })
  queueMicrotask(() => { writingQuery = false })
})

watch(() => route.query, (q) => {
  if (writingQuery) return
  const min = queryParam(q.min_level)
  minLevelMode.value = !!min
  levelFilter.value = levelFromQuery(min || queryParam(q.level))
  envFilter.value = queryParam(q.environment) || 'All'
  searchQuery.value = queryParam(q.search)
  const ids = queryParamList(q.project_id)
  if (ids.length) projects.setSelected(ids)
})

let debounceTimer: ReturnType<typeof setTimeout>
function onSearchInput(e: Event) {
  clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => {
    searchQuery.value = (e.target as HTMLInputElement).value
  }, 300)
}
onUnmounted(() => clearTimeout(debounceTimer))
</script>

<template>
  <div class="page">
    <div class="filterbar">
      <FilterChip
        label="Level"
        :value="levelFilter"
        :options="['All', 'Fatal', 'Error', 'Warning', 'Info', 'Debug', 'Trace']"
        @change="levelFilter = $event; minLevelMode = false"
      />
      <FilterChip
        label="Environment"
        :value="envFilter"
        :options="envOptions"
        @change="envFilter = $event"
      />

      <div class="filterbar__spacer" />

      <div class="filterbar__search">
        <Icon name="search" :size="12" style="color: var(--text-3)" />
        <input
          :value="searchQuery"
          placeholder="Search logs…"
          @input="onSearchInput"
        />
      </div>

      <span
        v-if="canManageAlerts"
        class="alert-on-this"
        v-tooltip="alertOnThisHint"
        :aria-label="alertOnThisHint"
      >
        <button
          class="btn btn--ghost export-menu__trigger"
          :disabled="!canAlertOnThis"
          @click="alertOnThis()"
        >
          Alert on this
        </button>
      </span>

      <button
        class="filterbar__refresh"
        :class="{ 'filterbar__refresh--fetching': isFetching }"
        title="Refresh"
        @click="refetch()"
      >
        <Icon name="refresh-cw" :size="11" :class="{ 'filterbar__refresh-spin': isFetching }" />
      </button>
    </div>

    <!-- Loading skeleton -->
    <div v-if="isLoading" class="perf-table-wrap">
      <table class="perf-table">
        <thead>
          <tr>
            <th style="width: 110px"><div class="col-sort">Time</div></th>
            <th style="width: 80px"><div class="col-sort">Level</div></th>
            <th><div class="col-sort">Message</div></th>
            <th v-if="showProject" class="perf-table__num log-proj-col" style="width: 130px"><div class="col-sort">Project</div></th>
            <th class="perf-table__num log-env-col" style="width: 120px"><div class="col-sort">Environment</div></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="i in 12" :key="i" class="perf-table__skel-row">
            <td><span class="skel" style="width: 80px; height: 10px; display: block" /></td>
            <td><span class="skel" style="width: 44px; height: 18px; display: block; border-radius: 3px" /></td>
            <td class="log-msg-col"><span class="skel" :style="{ width: `${40 + (i % 5) * 10}%` }" style="height: 10px; display: block" /></td>
            <td v-if="showProject"><span class="skel" style="width: 70px; height: 10px; display: block; margin-left: auto" /></td>
            <td><span class="skel" style="width: 60px; height: 10px; display: block; margin-left: auto" /></td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Empty state -->
    <div v-else-if="logs.length === 0" class="empty-state">
      <div class="empty-state__card">
        <div class="empty-state__icon empty-state__icon--ok">
          <Icon name="file-text" :size="28" />
        </div>
        <h2 class="empty-state__title">No logs found</h2>
        <p class="empty-state__body">
          {{ searchQuery || levelFilter !== 'All' || envFilter !== 'All'
            ? 'Try adjusting your filters.'
            : 'Logs will appear here when your SDK sends log envelope items.' }}
        </p>
      </div>
    </div>

    <!-- Log table -->
    <div v-else class="perf-table-wrap">
      <table class="perf-table">
        <thead>
          <tr>
            <th style="width: 110px"><div class="col-sort">Time</div></th>
            <th style="width: 80px"><div class="col-sort">Level</div></th>
            <th><div class="col-sort">Message</div></th>
            <th v-if="showProject" class="perf-table__num log-proj-col" style="width: 130px"><div class="col-sort">Project</div></th>
            <th class="perf-table__num log-env-col" style="width: 120px"><div class="col-sort">Environment</div></th>
          </tr>
        </thead>
        <tbody>
          <template v-for="log in logs" :key="log.id">
            <tr
              class="perf-table__row perf-table__row--clickable"
              :class="{ 'log-row--expanded': expandedId === log.id }"
              @click="toggleRow(log.id)"
            >
              <td class="mono" style="font-size: 11.5px; color: var(--text-3); white-space: nowrap">{{ formatTs(log.timestamp) }}</td>
              <td>
                <span class="tag">
                  <span class="leveldot" :class="`leveldot--${log.level}`" />
                  {{ log.level }}
                </span>
              </td>
              <td class="log-msg-col">
                <div class="log-msg">
                  <span class="log-msg__body">{{ log.body }}</span>
                  <RouterLink
                    v-if="log.transaction_id"
                    class="log-trace-link"
                    :to="`/transactions/${log.transaction_id}`"
                    v-tooltip="'View trace'"
                    @click.stop
                  >
                    <Icon name="activity" :size="10" />
                    Trace
                  </RouterLink>
                </div>
              </td>
              <td v-if="showProject" class="perf-table__num log-proj-col">
                <span class="projtag">{{ projectName(log.project_id) }}</span>
              </td>
              <td class="perf-table__num log-env-col">
                <span v-if="log.environment" class="envbadge" :class="envBadgeClass(log.environment)">{{ log.environment }}</span>
              </td>
            </tr>

            <!-- Expanded attributes -->
            <tr v-if="expandedId === log.id">
              <td :colspan="colCount" style="padding: 0; border-bottom: 1px solid var(--border)">
                <div class="log-expanded">
                  <div class="log-expanded__meta">
                    <span class="mono" style="font-size: var(--text-xs); color: var(--text-3)">
                      {{ new Date(log.timestamp).toISOString() }}
                    </span>
                    <RouterLink
                      v-if="log.trace_id && log.transaction_id"
                      class="mono link"
                      style="font-size: var(--text-xs)"
                      :to="`/transactions/${log.transaction_id}`"
                    >
                      trace: {{ log.trace_id }}
                    </RouterLink>
                    <span v-else-if="log.trace_id" class="mono" style="font-size: var(--text-xs); color: var(--text-3)">
                      trace: {{ log.trace_id }}
                    </span>
                    <span v-if="log.release" class="tag" style="font-size: 10px">{{ log.release }}</span>
                  </div>
                  <div v-if="attrEntries(log).length > 0" class="log-attrs">
                    <div v-for="[k, v] in attrEntries(log)" :key="k" class="log-attr">
                      <span class="log-attr__key mono">{{ k }}</span>
                      <span class="log-attr__val mono">{{ typeof v === 'object' ? JSON.stringify(v) : String(v) }}</span>
                    </div>
                  </div>
                  <div v-else class="muted" style="font-size: var(--text-xs); padding: 4px 0">
                    No attributes
                  </div>
                </div>
              </td>
            </tr>
          </template>

          <tr v-if="data?.has_more">
            <td :colspan="colCount" class="muted" style="text-align: center; font-size: var(--text-xs)">
              Showing the first 100 entries. Refine your filters to see more.
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
