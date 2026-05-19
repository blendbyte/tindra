<script setup lang="ts">
import { useSpanTable } from '@/composables/useSpanTable'
import { formatDuration, formatRate, formatPct } from '@/utils/formatters'
import FilterChip from '@/components/FilterChip.vue'
import TimeseriesChart from '@/components/TimeseriesChart.vue'
import PerformanceSubnav from '@/components/PerformanceSubnav.vue'
import SpanSamplesPanel from '@/components/SpanSamplesPanel.vue'
import Icon from '@/components/Icon.vue'

const { perf, hours, timeseries, isLoading, isError, refetch, sortCol, toggleSort, sortIcon, filtered, noData, search, selectedRow } =
  useSpanTable({ endpoint: 'cache', queryKeyPrefix: 'span-cache', nullableCols: ['miss_rate'] })
</script>

<template>
  <div class="page">
    <PerformanceSubnav />

    <div class="filterbar">
      <FilterChip label="Window" :value="perf.windowHrs" :options="['1h', '24h', '7d', '30d']" @change="perf.windowHrs = $event" />
      <FilterChip label="Env" :value="perf.envFilter" :options="['All', 'production', 'staging', 'development']" @change="perf.envFilter = $event" />
      <div class="filterbar__spacer" />
      <div class="filterbar__search">
        <Icon name="search" :size="12" style="color: var(--text-3)" />
        <input v-model="search" placeholder="Filter keys…" aria-label="Filter keys" />
      </div>
    </div>

    <div v-if="isError" class="txerror">
      <Icon name="alert-circle" :size="16" class="txerror__icon" />
      <span>Couldn't load cache data. Check your connection and try again.</span>
      <button class="btn" @click="refetch()">Retry</button>
    </div>

    <div v-if="timeseries && timeseries.buckets.length > 0" class="txcharts">
      <div class="txchart-panel">
        <div class="txchart-panel__label">RPM</div>
        <TimeseriesChart
          :times="timeseries.buckets.map(b => b.time)"
          :series="[{ id: 'count', label: 'RPM', type: 'bar', values: timeseries.buckets.map(b => b.count) }]"
          :bucket-size="timeseries.bucket_size"
        />
      </div>
      <div class="txchart-panel">
        <div class="txchart-panel__label">P50 duration</div>
        <TimeseriesChart
          :times="timeseries.buckets.map(b => b.time)"
          :series="[{ id: 'p50', label: 'P50', type: 'line', values: timeseries.buckets.map(b => b.p50) }]"
          :bucket-size="timeseries.bucket_size"
          :format-value="v => formatDuration(v)"
        />
      </div>
    </div>

    <div v-if="noData" class="empty-state">
      <div class="empty-state__card">
        <div class="empty-state__icon empty-state__icon--ok"><Icon name="zap" :size="28" /></div>
        <h2 class="empty-state__title">No cache spans in this window</h2>
        <p class="empty-state__body">Instrument your app with Sentry SDK cache instrumentation.</p>
      </div>
    </div>

    <template v-else-if="!isError">
      <div class="perf-table-wrap">
        <table class="perf-table">
          <thead>
            <tr>
              <th><button class="col-sort" :class="{ 'col-sort--active': sortCol === 'description' }" @click="toggleSort('description')">Description <em class="col-sort__icon">{{ sortIcon('description') }}</em></button></th>
              <th><button class="col-sort" :class="{ 'col-sort--active': sortCol === 'op' }" @click="toggleSort('op')">Op <em class="col-sort__icon">{{ sortIcon('op') }}</em></button></th>
              <th class="perf-table__num"><button class="col-sort" :class="{ 'col-sort--active': sortCol === 'rate' }" @click="toggleSort('rate')">RPM <em class="col-sort__icon">{{ sortIcon('rate') }}</em></button></th>
              <th class="perf-table__num"><button class="col-sort" :class="{ 'col-sort--active': sortCol === 'p50' }" @click="toggleSort('p50')">P50 <em class="col-sort__icon">{{ sortIcon('p50') }}</em></button></th>
              <th class="perf-table__num"><button class="col-sort" :class="{ 'col-sort--active': sortCol === 'p95' }" @click="toggleSort('p95')">P95 <em class="col-sort__icon">{{ sortIcon('p95') }}</em></button></th>
              <th class="perf-table__num"><button class="col-sort" :class="{ 'col-sort--active': sortCol === 'time_pct' }" @click="toggleSort('time_pct')">Time % <em class="col-sort__icon">{{ sortIcon('time_pct') }}</em></button></th>
              <th class="perf-table__num"><button class="col-sort" :class="{ 'col-sort--active': sortCol === 'miss_rate' }" @click="toggleSort('miss_rate')">Miss rate <em class="col-sort__icon">{{ sortIcon('miss_rate') }}</em></button></th>
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
                <td><span class="skel" style="width: 40px; height: 10px; display: inline-block" /></td>
                <td><span class="skel" style="width: 30px; height: 10px; display: inline-block" /></td>
              </tr>
            </template>
            <tr v-for="row in filtered" :key="`${row.op}:${row.description}`" class="perf-table__row perf-table__row--clickable" @click="selectedRow = row">
              <td class="perf-table__desc"><span class="mono" style="font-size: var(--text-sm); color: var(--text-1)">{{ row.description }}</span></td>
              <td><span class="optag" :class="`optag--${row.op.split('.')[0]}`">{{ row.op }}</span></td>
              <td class="perf-table__num">{{ formatRate(row.rate) }}</td>
              <td class="perf-table__num">{{ formatDuration(row.p50) }}</td>
              <td class="perf-table__num">{{ formatDuration(row.p95) }}</td>
              <td class="perf-table__num">
                <div class="perf-timepct">
                  <div class="perf-timepct__bar"><div class="perf-timepct__fill" :style="{ width: `${Math.min(row.time_pct, 100)}%` }" /></div>
                  <span>{{ formatPct(row.time_pct) }}</span>
                </div>
              </td>
              <td class="perf-table__num" :class="row.miss_rate != null && row.miss_rate > 30 ? 'tx-failure' : ''">
                {{ row.miss_rate == null ? '–' : formatPct(row.miss_rate) }}
              </td>
              <td class="perf-table__num" :class="row.error_rate > 0 ? 'tx-failure' : ''">{{ formatPct(row.error_rate) }}</td>
            </tr>
            <tr v-if="!isLoading && filtered.length === 0 && search">
              <td colspan="8" style="padding: 24px; text-align: center; color: var(--text-3); font-size: var(--text-sm)">No keys match "{{ search }}"</td>
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
