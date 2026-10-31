<script lang="ts">
// The span pages share one table; each kind only changes labels, the endpoint
// and whether the cache miss rate column is shown.
const KINDS = {
  db: {
    endpoint: 'db', queryKeyPrefix: 'span-db', rateLabel: 'QPM',
    filterLabel: 'Filter queries', noun: 'queries', dataNoun: 'query',
    emptyIcon: 'database', emptyTitle: 'No database spans in this window',
    emptyBody: 'Instrument your app using a Sentry SDK with database query tracking enabled.',
  },
  cache: {
    endpoint: 'cache', queryKeyPrefix: 'span-cache', rateLabel: 'RPM',
    filterLabel: 'Filter keys', noun: 'keys', dataNoun: 'cache',
    emptyIcon: 'zap', emptyTitle: 'No cache spans in this window',
    emptyBody: 'Instrument your app with Sentry SDK cache instrumentation.',
  },
  jobs: {
    endpoint: 'jobs', queryKeyPrefix: 'span-jobs', rateLabel: 'TPM',
    filterLabel: 'Filter jobs', noun: 'jobs', dataNoun: 'job',
    emptyIcon: 'clock', emptyTitle: 'No job or queue spans in this window',
    emptyBody: 'Instrument your workers using a Sentry SDK with task/queue integration.',
  },
} as const

export type SpanTableKind = keyof typeof KINDS
</script>

<script setup lang="ts">
import { vClickable } from '@/directives/clickable'
import { useSpanTable } from '@/composables/useSpanTable'
import { formatDuration, formatRate, formatPct } from '@/utils/formatters'
import TimeseriesChart from '@/components/TimeseriesChart.vue'
import PerformanceSubnav from '@/components/PerformanceSubnav.vue'
import SpanSamplesPanel from '@/components/SpanSamplesPanel.vue'
import Icon from '@/components/Icon.vue'

// kind is read once: SpanTableView remounts this component when it changes.
const props = defineProps<{ kind: SpanTableKind }>()
const config = KINDS[props.kind]
const showMissRate = props.kind === 'cache'

const { perf, hours, timeseries, isLoading, isError, refetch, sortCol, toggleSort, sortIcon, filtered, noData, search, selectedRow } =
  useSpanTable({ endpoint: config.endpoint, queryKeyPrefix: config.queryKeyPrefix, nullableCols: showMissRate ? ['miss_rate'] : [] })
</script>


<template>
  <div class="page">
    <PerformanceSubnav>
      <template #actions>
        <div class="filterbar__search">
          <Icon name="search" :size="12" style="color: var(--text-3)" />
          <input v-model="search" :placeholder="`${config.filterLabel}…`" :aria-label="config.filterLabel" />
        </div>
      </template>
    </PerformanceSubnav>

    <div v-if="isError" class="txerror">
      <Icon name="alert-circle" :size="16" class="txerror__icon" />
      <span>Couldn't load {{ config.dataNoun }} data. Check your connection and try again.</span>
      <button class="btn" @click="refetch()">Retry</button>
    </div>

    <div v-if="!isError && timeseries && timeseries.buckets.length > 0" class="txcharts">
      <div class="txchart-panel">
        <div class="txchart-panel__label">{{ config.rateLabel }}</div>
        <TimeseriesChart
          :times="timeseries.buckets.map(b => b.time)"
          :series="[{ id: 'count', label: config.rateLabel, type: 'bar', values: timeseries.buckets.map(b => b.count) }]"
          :bucket-size="timeseries.bucket_size"
        />
      </div>
    </div>

    <div v-if="noData" class="empty-state">
      <div class="empty-state__card">
        <div class="empty-state__icon empty-state__icon--ok"><Icon :name="config.emptyIcon" :size="28" /></div>
        <h2 class="empty-state__title">{{ config.emptyTitle }}</h2>
        <p class="empty-state__body">{{ config.emptyBody }}</p>
      </div>
    </div>

    <template v-else-if="!isError">
      <div class="perf-table-wrap">
        <table class="perf-table perf-table--spans">
          <thead>
            <tr>
              <th><button class="col-sort" :class="{ 'col-sort--active': sortCol === 'description' }" @click="toggleSort('description')">Description <em class="col-sort__icon">{{ sortIcon('description') }}</em></button></th>
              <th><button class="col-sort" :class="{ 'col-sort--active': sortCol === 'op' }" @click="toggleSort('op')">Op <em class="col-sort__icon">{{ sortIcon('op') }}</em></button></th>
              <th class="perf-table__num"><button class="col-sort" :class="{ 'col-sort--active': sortCol === 'rate' }" @click="toggleSort('rate')">{{ config.rateLabel }} <em class="col-sort__icon">{{ sortIcon('rate') }}</em></button></th>
              <th class="perf-table__num"><button class="col-sort" :class="{ 'col-sort--active': sortCol === 'p50' }" @click="toggleSort('p50')">P50 <em class="col-sort__icon">{{ sortIcon('p50') }}</em></button></th>
              <th class="perf-table__num"><button class="col-sort" :class="{ 'col-sort--active': sortCol === 'p95' }" @click="toggleSort('p95')">P95 <em class="col-sort__icon">{{ sortIcon('p95') }}</em></button></th>
              <th class="perf-table__num"><button class="col-sort" :class="{ 'col-sort--active': sortCol === 'time_pct' }" @click="toggleSort('time_pct')">Time % <em class="col-sort__icon">{{ sortIcon('time_pct') }}</em></button></th>
              <th v-if="showMissRate" class="perf-table__num"><button class="col-sort" :class="{ 'col-sort--active': sortCol === 'miss_rate' }" @click="toggleSort('miss_rate')">Miss rate <em class="col-sort__icon">{{ sortIcon('miss_rate') }}</em></button></th>
              <th class="perf-table__num"><button class="col-sort" :class="{ 'col-sort--active': sortCol === 'error_rate' }" @click="toggleSort('error_rate')">Error % <em class="col-sort__icon">{{ sortIcon('error_rate') }}</em></button></th>
            </tr>
          </thead>
          <tbody>
            <template v-if="isLoading">
              <tr v-for="i in 8" :key="i" class="perf-table__skel-row">
                <td><span class="skel" style="width: 60%; height: 10px; display: inline-block" /></td>
                <td><span class="skel" style="width: 50px; height: 10px; display: inline-block" /></td>
                <td><span class="skel" style="width: 40px; height: 10px; display: inline-block" /></td>
                <td><span class="skel" style="width: 40px; height: 10px; display: inline-block" /></td>
                <td><span class="skel" style="width: 40px; height: 10px; display: inline-block" /></td>
                <td><span class="skel" style="width: 60px; height: 10px; display: inline-block" /></td>
                <td v-if="showMissRate"><span class="skel" style="width: 40px; height: 10px; display: inline-block" /></td>
                <td><span class="skel" style="width: 30px; height: 10px; display: inline-block" /></td>
              </tr>
            </template>
            <tr v-for="row in filtered" :key="`${row.op}:${row.description}`" class="perf-table__row perf-table__row--clickable" v-clickable @click="selectedRow = row">
              <td class="perf-table__desc"><span class="mono" style="font-size: var(--text-sm); color: var(--text-1)">{{ row.description }}</span></td>
              <td><span class="optag" :class="`optag--${row.op.split('.')[0]}`">{{ row.op }}</span></td>
              <td class="perf-table__num">{{ formatRate(row.rate) }}</td>
              <td class="perf-table__num">{{ formatDuration(row.p50) }}</td>
              <td class="perf-table__num" data-label="p95">{{ formatDuration(row.p95) }}</td>
              <td class="perf-table__num">
                <div class="perf-timepct">
                  <div class="perf-timepct__bar"><div class="perf-timepct__fill" :style="{ width: `${Math.min(row.time_pct, 100)}%` }" /></div>
                  <span>{{ formatPct(row.time_pct) }}</span>
                </div>
              </td>
              <td v-if="showMissRate" class="perf-table__num" :class="row.miss_rate != null && row.miss_rate > 30 ? 'tx-failure' : ''">
                {{ row.miss_rate == null ? '–' : formatPct(row.miss_rate) }}
              </td>
              <td class="perf-table__num" :class="row.error_rate > 0 ? 'tx-failure' : ''">{{ formatPct(row.error_rate) }}</td>
            </tr>
            <tr v-if="!isLoading && filtered.length === 0 && search">
              <td :colspan="showMissRate ? 8 : 7" style="padding: 24px; text-align: center; color: var(--text-3); font-size: var(--text-sm)">No {{ config.noun }} match "{{ search }}"</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </div>

  <Teleport to="body">
    <SpanSamplesPanel v-if="selectedRow" :row="selectedRow" :hours="hours" :env="perf.envFilter" @close="selectedRow = null" />
  </Teleport>
</template>
