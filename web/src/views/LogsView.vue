<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { useFormatters } from '@/composables/useFormatters'
import type { Log, LogListPage } from '@/api/types'
import Icon from '@/components/Icon.vue'
import FilterChip from '@/components/FilterChip.vue'
import { useProjectsStore } from '@/stores/projects'

const projects = useProjectsStore()
const { formatTs } = useFormatters()

const levelFilter = ref('All')
const envFilter = ref('All')
const searchQuery = ref('')

const selectedProjectIds = computed(() => projects.selectedIds)

const queryParams = computed(() => {
  const p = new URLSearchParams()
  for (const id of selectedProjectIds.value) p.append('project_id', id)
  if (levelFilter.value !== 'All') p.set('level', levelFilter.value.toLowerCase())
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
        @change="levelFilter = $event"
      />
      <FilterChip
        label="Environment"
        :value="envFilter"
        :options="['All', 'production', 'staging', 'preview', 'development']"
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
            <th class="perf-table__num log-env-col" style="width: 120px"><div class="col-sort">Environment</div></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="i in 12" :key="i" class="perf-table__skel-row">
            <td><span class="skel" style="width: 80px; height: 10px; display: block" /></td>
            <td><span class="skel" style="width: 44px; height: 18px; display: block; border-radius: 3px" /></td>
            <td><span class="skel" :style="{ width: `${40 + (i % 5) * 10}%` }" style="height: 10px; display: block" /></td>
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
              <td>
                <div style="overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text-1)">{{ log.body }}</div>
              </td>
              <td class="perf-table__num log-env-col">
                <span v-if="log.environment" class="envbadge" :class="envBadgeClass(log.environment)">{{ log.environment }}</span>
              </td>
            </tr>

            <!-- Expanded attributes -->
            <tr v-if="expandedId === log.id">
              <td colspan="4" style="padding: 0; border-bottom: 1px solid var(--border)">
                <div class="log-expanded">
                  <div class="log-expanded__meta">
                    <span class="mono" style="font-size: var(--text-xs); color: var(--text-3)">
                      {{ new Date(log.timestamp).toISOString() }}
                    </span>
                    <span v-if="log.trace_id" class="mono" style="font-size: var(--text-xs); color: var(--text-3)">
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
            <td colspan="4" class="muted" style="text-align: center; font-size: var(--text-xs)">
              Showing the first 100 entries. Refine your filters to see more.
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
