<script setup lang="ts">
import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { useInvestigationStore, RANGE_HOURS } from '@/stores/investigation'
import { formatDuration } from '@/utils/formatters'
import type { TxTimeseries } from '@/api/types'
import Icon from '@/components/Icon.vue'
import TimeseriesChart from '@/components/TimeseriesChart.vue'
import { fmt, useProjectScope, useWindowLabel } from './shared'

const investigation = useInvestigationStore()
const windowLabel = useWindowLabel()
const { buildQs } = useProjectScope()

const { data: timeseries, isFetching } = useQuery({
  queryKey: computed(() => ['dash-tx-series', investigation.scopeKey]),
  queryFn: ({ signal }) =>
    apiFetch<TxTimeseries>(investigation.request(`/api/transactions/timeseries?${buildQs({
      hours: String(RANGE_HOURS[investigation.range] ?? 2160),
      ...(investigation.environment === 'All' ? {} : { env: investigation.environment }),
    })}`), { signal }),
})

const buckets = computed(() => timeseries.value?.buckets ?? [])
const times = computed(() => buckets.value.map(b => b.time))
const loading = computed(() => isFetching.value && !timeseries.value)
// Axis ticks fall between whole requests.
const formatCount = (v: number) => fmt(Math.round(v))
</script>

<template>
  <section class="db-sec" aria-labelledby="trend-title">
    <div class="db-sec__head">
      <h2 id="trend-title" class="db-sec__title">Requests and latency · {{ windowLabel }}</h2>
      <RouterLink :to="investigation.link('/performance/transactions')" class="db-sec__link">all transactions <Icon name="arrow-right" :size="11" /></RouterLink>
    </div>
    <div v-if="loading" class="trend" role="status" aria-label="Loading requests and latency">
      <div class="skel trend__skel" />
      <div class="skel trend__skel" />
    </div>
    <div v-else-if="buckets.length === 0" class="db-empty">
      <Icon name="activity" :size="18" style="color: var(--text-3)" />
      <div>No transaction data in the selected range</div>
    </div>
    <div v-else class="trend">
      <div class="trend__panel">
        <div class="txchart-panel__label">Requests</div>
        <TimeseriesChart
          :times="times"
          :series="[{ id: 'count', label: 'Requests', type: 'bar', values: buckets.map(b => b.count) }]"
          :bucket-size="timeseries!.bucket_size"
          :format-value="formatCount"
        />
      </div>
      <div class="trend__panel">
        <div class="txchart-panel__label">
          Latency
          <span class="txchart-panel__legend">
            <span class="txchart-panel__leg"><span class="txchart-panel__leg-dot txchart-panel__leg-dot--p50" />p50</span>
            <span class="txchart-panel__leg"><span class="txchart-panel__leg-dot txchart-panel__leg-dot--p95" />p95</span>
          </span>
        </div>
        <TimeseriesChart
          :times="times"
          :series="[
            { id: 'p50', label: 'p50', type: 'line', values: buckets.map(b => b.p50) },
            { id: 'p95', label: 'p95', type: 'line', values: buckets.map(b => b.p95), dashed: true, dimmed: true },
          ]"
          :bucket-size="timeseries!.bucket_size"
          :format-value="formatDuration"
        />
      </div>
    </div>
  </section>
</template>

<style scoped src="./section.css"></style>

<style scoped>
.trend {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 320px), 1fr));
  gap: 8px 24px;
  padding: 12px 16px 0;
}

.trend__panel { min-width: 0; }

.trend__skel { height: 120px; border-radius: 3px; }
</style>
