<script setup lang="ts">
import { ref, computed, watch, watchEffect } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery, useInfiniteQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { apiFetch } from '@/api/client'
import type { TransactionSummary, TxTimeseries, TxListPage } from '@/api/types'
import { formatDuration } from '@/utils/formatters'
import { useTimezone } from '@/composables/useTimezone'
import { WINDOW_MAP } from '@/utils/time'
import FilterChip from '@/components/FilterChip.vue'
import TimeseriesChart from '@/components/TimeseriesChart.vue'
import Icon from '@/components/Icon.vue'

const route = useRoute()
const router = useRouter()
const projects = useProjectsStore()
const tz = useTimezone()

const txName = computed(() => route.query.name as string)
const txOp = computed(() => route.query.op as string)
const txProjectId = computed(() => route.query.project_id as string | undefined)
const activeProjectIds = computed(() =>
  txProjectId.value ? [txProjectId.value] : projects.selectedIds
)

const windowHrs = ref('24h')
const envFilter = ref('All')
const hours = computed(() => WINDOW_MAP[windowHrs.value] ?? 24)

const profileParams = computed(() => {
  const p = new URLSearchParams()
  p.set('hours', String(hours.value))
  p.set('name', txName.value)
  if (txOp.value) p.set('op', txOp.value)
  if (envFilter.value !== 'All') p.set('env', envFilter.value)
  for (const id of activeProjectIds.value) p.append('project_id', id)
  return p.toString()
})

const {
  data: summaries,
  isError: isSummariesError,
  refetch: refetchSummaries,
} = useQuery({
  queryKey: computed(() => ['transaction-profile-summaries', profileParams.value]),
  queryFn: () => apiFetch<TransactionSummary[]>(`/api/transactions/summaries?${profileParams.value}`),
})

const {
  data: timeseries,
  isError: isTimeseriesError,
  refetch: refetchTimeseries,
} = useQuery({
  queryKey: computed(() => ['transaction-profile-timeseries', profileParams.value]),
  queryFn: () => apiFetch<TxTimeseries>(`/api/transactions/timeseries?${profileParams.value}`),
})

const samplesParams = computed(() => {
  const p = new URLSearchParams()
  p.set('name', txName.value)
  if (txOp.value) p.set('op', txOp.value)
  if (envFilter.value !== 'All') p.set('environment', envFilter.value)
  for (const id of activeProjectIds.value) p.append('project_id', id)
  return p.toString()
})

const {
  data: samplesData,
  fetchNextPage,
  hasNextPage,
  isFetchingNextPage,
  isLoading: isLoadingSamples,
  isError: isSamplesError,
  refetch: refetchSamples,
} = useInfiniteQuery({
  queryKey: computed(() => ['transaction-profile-samples', samplesParams.value]),
  queryFn: ({ pageParam }) => {
    const params = new URLSearchParams(samplesParams.value)
    if (pageParam) {
      params.set('cursor_time', pageParam.cursor_time)
      params.set('cursor_id', pageParam.cursor_id)
    }
    return apiFetch<TxListPage>(`/api/transactions?${params}`)
  },
  getNextPageParam: (lastPage) => {
    if (!lastPage.next_cursor_time || !lastPage.next_cursor_id) return undefined
    return { cursor_time: lastPage.next_cursor_time, cursor_id: lastPage.next_cursor_id }
  },
  initialPageParam: null as null | { cursor_time: string; cursor_id: string },
})

const allSamples = computed(() => samplesData.value?.pages.flatMap(p => p.transactions) ?? [])

watchEffect(() => {
  if (txName.value) document.title = `${txName.value} - Tindra`
})

// Sort - reset when filters change
type SortCol = 'time' | 'duration' | 'status'
const sortCol = ref<SortCol>('time')
const sortDir = ref<'asc' | 'desc'>('desc')
const selectedIdx = ref<number | null>(null)

watch(samplesParams, () => {
  sortCol.value = 'time'
  sortDir.value = 'desc'
  selectedIdx.value = null
})

function toggleSort(col: SortCol) {
  if (sortCol.value === col) {
    sortDir.value = sortDir.value === 'desc' ? 'asc' : 'desc'
  } else {
    sortCol.value = col
    sortDir.value = col === 'status' ? 'asc' : 'desc'
  }
  selectedIdx.value = null
}

function sortIcon(col: SortCol) {
  if (sortCol.value !== col) return ''
  return sortDir.value === 'desc' ? '↓' : '↑'
}

const sortedSamples = computed(() => {
  const list = [...allSamples.value]
  list.sort((a, b) => {
    let cmp = 0
    if (sortCol.value === 'time') cmp = a.start_timestamp < b.start_timestamp ? -1 : a.start_timestamp > b.start_timestamp ? 1 : 0
    else if (sortCol.value === 'duration') cmp = a.duration_ms - b.duration_ms
    else if (sortCol.value === 'status') cmp = a.status.localeCompare(b.status)
    return sortDir.value === 'desc' ? -cmp : cmp
  })
  return list
})

const maxSampleDuration = computed(() => Math.max(...allSamples.value.map(s => s.duration_ms), 1))

// Keyboard navigation
function handleSamplesKey(e: KeyboardEvent) {
  const n = sortedSamples.value.length
  if (n === 0) return
  if (e.key === 'j' || e.key === 'ArrowDown') {
    e.preventDefault()
    selectedIdx.value = selectedIdx.value === null ? 0 : Math.min(selectedIdx.value + 1, n - 1)
  } else if (e.key === 'k' || e.key === 'ArrowUp') {
    e.preventDefault()
    selectedIdx.value = selectedIdx.value === null ? n - 1 : Math.max(selectedIdx.value - 1, 0)
  } else if (e.key === 'Enter' && selectedIdx.value !== null) {
    router.push(`/transactions/${sortedSamples.value[selectedIdx.value].id}`)
  }
}

// Stats
const stats = computed(() => {
  const list = summaries.value ?? []
  if (list.length === 0) return null
  const totalCount = list.reduce((s, x) => s + x.sample_count, 0)
  const totalTpm = list.reduce((s, x) => s + x.tpm, 0)
  const p50 = totalCount > 0 ? list.reduce((s, x) => s + x.p50 * x.sample_count, 0) / totalCount : 0
  const p95 = totalCount > 0 ? list.reduce((s, x) => s + x.p95 * x.sample_count, 0) / totalCount : 0
  const failureRate = totalCount > 0 ? list.reduce((s, x) => s + x.failure_rate * x.sample_count, 0) / totalCount : 0
  return { totalCount, totalTpm, p50, p95, failureRate }
})


function formatTPM(tpm: number) {
  if (tpm < 0.001) return '<0.001/min'
  if (tpm < 0.01) return `${tpm.toFixed(4)}/min`
  return `${tpm.toFixed(tpm >= 1 ? 2 : 4)}/min`
}

function formatFailureRate(rate: number) {
  if (rate === 0) return '0%'
  if (rate < 0.0001) return '<0.01%'
  return `${(rate * 100).toFixed(2)}%`
}

function formatOkRate(rate: number) {
  return `${Math.round((1 - rate) * 100)}%`
}

function formatTime(iso: string) {
  const d = new Date(iso)
  return d.toLocaleString('en-US', {
    month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
    timeZone: tz.value,
  })
}

function projectName(projectId: string) {
  return (projects.projects as { id: string; name: string }[])?.find((p) => p.id === projectId)?.name ?? projectId
}
</script>

<template>
  <div class="page">
    <!-- Breadcrumb -->
    <div class="detail-breadcrumb">
      <a class="detail-breadcrumb__back" href="#" @click.prevent="router.back()">
        <Icon name="arrow-left" :size="12" />
        Transactions
      </a>
      <div class="detail-breadcrumb__title"><span>{{ txName }}</span></div>
      <div class="detail-breadcrumb__actions">
        <span v-if="txOp" class="optag" :class="`optag--${txOp.split('.')[0]}`">{{ txOp.split('.')[0] }}</span>
      </div>
    </div>

    <!-- Filter bar -->
    <div class="filterbar" style="border-top: none; padding-top: 0">
      <FilterChip
        label="Window"
        :value="windowHrs"
        :options="['1h', '24h', '7d', '30d']"
        @change="windowHrs = $event"
      />
      <FilterChip
        label="Env"
        :value="envFilter"
        :options="['All', 'production', 'staging', 'development']"
        @change="envFilter = $event"
      />
    </div>

    <!-- Stats error -->
    <div v-if="isSummariesError" class="txerror">
      <Icon name="alert-triangle" :size="14" class="txerror__icon" />
      Failed to load transaction stats.
      <button class="btn btn--ghost" @click="refetchSummaries()">Retry</button>
    </div>

    <!-- Stats strip -->
    <div v-if="stats" class="txstats" style="border-top: none">
      <div class="txstat">
        <span class="txstat__label">P50</span>
        <span class="txstat__value">{{ formatDuration(stats.p50) }}</span>
      </div>
      <div class="txstat">
        <span class="txstat__label">P95</span>
        <span class="txstat__value">{{ formatDuration(stats.p95) }}</span>
      </div>
      <div class="txstat">
        <span class="txstat__label">Throughput</span>
        <span class="txstat__value">{{ formatTPM(stats.totalTpm) }}</span>
      </div>
      <div class="txstat">
        <span class="txstat__label">Count</span>
        <span class="txstat__value">{{ stats.totalCount.toLocaleString() }}</span>
      </div>
      <div class="txstat txstat--status">
        <span class="txstat__label">Status</span>
        <div class="txstat-breakdown">
          <div class="txstat-breakdown__bar">
            <span class="txstat-breakdown__ok" :style="{ width: `${(1 - stats.failureRate) * 100}%` }" />
            <span class="txstat-breakdown__err" :style="{ width: `${stats.failureRate * 100}%` }" />
          </div>
          <div class="txstat-breakdown__labels">
            <span>{{ formatOkRate(stats.failureRate) }} ok</span>
            <span v-if="stats.failureRate > 0" class="txstat-breakdown__err-label">
              {{ formatFailureRate(stats.failureRate) }} err
            </span>
          </div>
        </div>
      </div>
    </div>

    <!-- Timeseries error -->
    <div v-if="isTimeseriesError" class="txerror">
      <Icon name="alert-triangle" :size="14" class="txerror__icon" />
      Failed to load timeseries data.
      <button class="btn btn--ghost" @click="refetchTimeseries()">Retry</button>
    </div>

    <!-- Charts (stacked) -->
    <div v-if="timeseries && timeseries.buckets.length > 0" class="txcharts">
      <div class="txchart-panel">
        <div class="txchart-panel__label">
          Duration
          <span class="txchart-panel__legend">
            <span class="txchart-panel__leg"><span class="txchart-panel__leg-dot txchart-panel__leg-dot--p50" />P50</span>
            <span class="txchart-panel__leg"><span class="txchart-panel__leg-dot txchart-panel__leg-dot--p95" />P95</span>
          </span>
        </div>
        <TimeseriesChart
          :times="timeseries.buckets.map(b => b.time)"
          :series="[
            { id: 'p50', label: 'P50', type: 'line', values: timeseries.buckets.map(b => b.p50) },
            { id: 'p95', label: 'P95', type: 'line', values: timeseries.buckets.map(b => b.p95), dashed: true, dimmed: true },
          ]"
          :bucket-size="timeseries.bucket_size"
          :height="160"
          :format-value="(v) => formatDuration(v)"
        />
      </div>
      <div class="txchart-panel txchart-panel--secondary">
        <div class="txchart-panel__label">Throughput</div>
        <TimeseriesChart
          :times="timeseries.buckets.map(b => b.time)"
          :series="[{ id: 'count', label: 'Requests', type: 'bar', values: timeseries.buckets.map(b => b.count) }]"
          :bucket-size="timeseries.bucket_size"
          :height="100"
        />
      </div>
    </div>

    <!-- Samples -->
    <div
      class="tx-samples"
      tabindex="0"
      style="outline: none"
      @keydown="handleSamplesKey"
    >
      <div class="tx-samples__head">
        <span class="tx-samples__title">Samples</span>
        <span v-if="stats?.totalCount" class="tx-samples__count">{{ stats.totalCount.toLocaleString() }}</span>
      </div>

      <!-- Samples error -->
      <div v-if="isSamplesError" class="txerror" style="margin: 12px 24px">
        <Icon name="alert-triangle" :size="14" class="txerror__icon" />
        Failed to load samples.
        <button class="btn btn--ghost" @click="refetchSamples()">Retry</button>
      </div>

      <div class="tx-sample-row tx-sample-row--head">
        <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'time' }" @click="toggleSort('time')">
          Time <em class="col-sort__icon">{{ sortIcon('time') }}</em>
        </button>
        <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'duration' }" @click="toggleSort('duration')">
          Duration <em class="col-sort__icon">{{ sortIcon('duration') }}</em>
        </button>
        <button class="col-sort" :class="{ 'col-sort--active': sortCol === 'status' }" @click="toggleSort('status')">
          Status <em class="col-sort__icon">{{ sortIcon('status') }}</em>
        </button>
        <span class="col-label">Project</span>
        <span class="col-label">Trace ID</span>
      </div>

      <template v-if="isLoadingSamples">
        <div v-for="i in 8" :key="i" class="tx-sample-row tx-sample-row--skeleton">
          <span class="skel" style="width: 120px; height: 10px" />
          <span class="skel" style="width: 60px; height: 10px" />
          <span class="skel" style="width: 40px; height: 10px" />
          <span class="skel" style="width: 70px; height: 10px" />
          <span class="skel" style="width: 140px; height: 10px" />
        </div>
      </template>

      <div
        v-for="(s, i) in sortedSamples"
        :key="s.id"
        class="tx-sample-row"
        :class="{ 'tx-sample-row--selected': selectedIdx === i }"
        @click="router.push(`/transactions/${s.id}`)"
      >
        <span class="tx-sample-row__time">{{ formatTime(s.start_timestamp) }}</span>
        <span class="tx-sample-dur">
          <span
            class="tx-sample-dur__bar"
            :class="s.status !== 'ok' ? 'tx-sample-dur__bar--err' : ''"
            :style="{ transform: `scaleX(${s.duration_ms / maxSampleDuration})` }"
          />
          <span class="tx-sample-dur__val">{{ formatDuration(s.duration_ms) }}</span>
        </span>
        <span><span class="tx-status" :class="`tx-status--${s.status}`">{{ s.status }}</span></span>
        <span class="tx-sample-row__project">{{ projectName(s.project_id) }}</span>
        <span class="tx-sample-row__trace">{{ s.trace_id || s.id }}</span>
      </div>

      <div v-if="!isLoadingSamples && !isSamplesError && allSamples.length === 0" class="tx-samples__empty">
        No samples in this window.
      </div>

      <div v-if="hasNextPage" class="tx-samples__more">
        <button class="btn btn--ghost" :disabled="isFetchingNextPage" @click="fetchNextPage()">
          {{ isFetchingNextPage ? 'Loading…' : 'Load more' }}
        </button>
      </div>
    </div>
  </div>
</template>
