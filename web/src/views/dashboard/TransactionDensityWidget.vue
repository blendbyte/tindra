<script setup lang="ts">
import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { useInvestigationStore } from '@/stores/investigation'
import { useTimezone } from '@/composables/useTimezone'
import type { TxCountTimeseries } from '@/api/types'
import { fmt, useProjectScope } from './shared'

const investigation = useInvestigationStore()
const timezone = useTimezone()
const { buildQs, pKey } = useProjectScope()

// ── Timezone helpers ──────────────────────────────────────────────────────────

function tzDateStr(date: Date, tz: string): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone: tz }).format(date)
}

function tzHour(date: Date, tz: string): number {
  return parseInt(new Intl.DateTimeFormat('en', { hour: 'numeric', hour12: false, timeZone: tz }).format(date), 10) % 24
}

const { data: txTs } = useQuery({
  queryKey: computed(() => ['dash-tx-counts', pKey.value, investigation.environment, investigation.userIdentity]),
  queryFn: ({ signal }) =>
    apiFetch<TxCountTimeseries>(`/api/transactions/counts?${buildQs({ hours: '168', ...(investigation.userIdentity ? { user: investigation.userIdentity } : {}), ...(investigation.environment === 'All' ? {} : { env: investigation.environment }) })}`, { signal }),

})

// ── Heatmap ───────────────────────────────────────────────────────────────────

const dayLabels = computed(() => {
  const tz = timezone.value
  const todayStr = tzDateStr(new Date(), tz)
  const todayMs = new Date(todayStr).getTime()
  return Array.from({ length: 7 }, (_, i) => {
    const ms = todayMs - (6 - i) * 86_400_000
    return new Date(ms).toLocaleDateString('en-US', { weekday: 'short', timeZone: 'UTC' })
  })
})

const heatGrid = computed((): number[][] => {
  const grid: number[][] = Array.from({ length: 7 }, () => Array(24).fill(0))
  const buckets = txTs.value?.buckets
  if (!buckets?.length) return grid
  const tz = timezone.value
  const todayStr = tzDateStr(new Date(), tz)
  const todayMs = new Date(todayStr).getTime()
  for (const b of buckets) {
    const t = new Date(b.time)
    const bucketMs = new Date(tzDateStr(t, tz)).getTime()
    const daysAgo = Math.round((todayMs - bucketMs) / 86_400_000)
    if (daysAgo < 0 || daysAgo > 6) continue
    grid[6 - daysAgo][tzHour(t, tz)] += b.count
  }
  return grid
})

const heatMax = computed(() => Math.max(1, ...heatGrid.value.flat()))

function cellTip(di: number, hi: number, count: number): string {
  const day = dayLabels.value[di]
  const h = String(hi).padStart(2, '0')
  const hNext = hi === 23 ? '24' : String(hi + 1).padStart(2, '0')
  const countStr = count ? `${fmt(count)} event${count === 1 ? '' : 's'}` : 'no events'
  return `${day} ${h}:00–${hNext}:00 · ${countStr}`
}

function cellColor(v: number): string {
  if (!v) return ''
  const r = v / heatMax.value
  if (r < 0.12) return 'oklch(from var(--accent) l c h / 0.20)'
  if (r < 0.30) return 'oklch(from var(--accent) l c h / 0.40)'
  if (r < 0.55) return 'oklch(from var(--accent) l c h / 0.65)'
  if (r < 0.75) return 'var(--accent)'
  if (r < 0.90) return 'var(--warning)'
  return 'var(--danger)'
}
</script>

<template>
  <div class="db-sec">
    <div class="db-sec__head">
      <h2 class="db-sec__title">Transaction density · last 7 days</h2>
      <span class="db-sec__hint">each cell = 1 hour</span>
    </div>
    <div class="db-heatmap">
      <div
        v-for="(row, di) in heatGrid"
        :key="di"
        class="db-heatmap__row"
      >
        <span class="db-heatmap__dlabel">{{ dayLabels[di] }}</span>
        <div
          v-for="(cell, hi) in row"
          :key="hi"
          class="db-heatmap__cell"
          :data-tip="cellTip(di, hi, cell)"
          :style="cellColor(cell) ? { background: cellColor(cell) } : undefined"
        />
      </div>
      <div class="db-heatmap__foot">
        <span class="db-heatmap__leg-lbl">low</span>
        <div class="db-heatmap__cell db-heatmap__cell--legend" style="background: oklch(from var(--accent) l c h / 0.20)" />
        <div class="db-heatmap__cell db-heatmap__cell--legend" style="background: oklch(from var(--accent) l c h / 0.40)" />
        <div class="db-heatmap__cell db-heatmap__cell--legend" style="background: oklch(from var(--accent) l c h / 0.65)" />
        <div class="db-heatmap__cell db-heatmap__cell--legend" style="background: var(--accent)" />
        <div class="db-heatmap__cell db-heatmap__cell--legend" style="background: var(--warning)" />
        <div class="db-heatmap__cell db-heatmap__cell--legend" style="background: var(--danger)" />
        <span class="db-heatmap__leg-lbl">high</span>
      </div>
    </div>
  </div>
</template>

<style scoped src="./section.css"></style>

<style scoped>
/* ── Heatmap ────────────────────────────────────────────────────────────────── */

.db-heatmap {
  padding: 8px 16px 10px;
}

.db-heatmap__row {
  display: flex;
  align-items: center;
  gap: 2px;
  margin-bottom: 2px;
}

.db-heatmap__dlabel {
  font-size: 10px;
  color: var(--text-3);
  opacity: 0.5;
  width: 28px;
  text-align: right;
  padding-right: 5px;
  flex-shrink: 0;
}

.db-heatmap__cell {
  position: relative;
  flex: 1;
  height: 11px;
  border-radius: 2px;
  background: var(--surface-2);
  min-width: 0;
}

.db-heatmap__cell[data-tip]:hover::after {
  content: attr(data-tip);
  position: absolute;
  bottom: calc(100% + 7px);
  left: 50%;
  transform: translateX(-50%);
  background: var(--surface);
  border: 1px solid var(--border);
  color: var(--text-2);
  font-size: 11px;
  line-height: 1.4;
  padding: 4px 8px;
  border-radius: 4px;
  white-space: nowrap;
  z-index: 200;
  pointer-events: none;
  box-shadow: 0 2px 6px oklch(0 0 0 / 0.12);
}

.db-heatmap__cell--legend {
  flex: none;
  width: 11px;
}

.db-heatmap__foot {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-top: 10px;
  justify-content: flex-end;
}

.db-heatmap__leg-lbl {
  font-size: 10px;
  color: var(--text-3);
  opacity: 0.5;
}

@media (max-width: 768px) {
  /* Heatmap: allow horizontal scroll */
  .db-heatmap {
    overflow-x: auto;
    -webkit-overflow-scrolling: touch;
    padding-bottom: 4px;
  }
  .db-heatmap__row { flex-shrink: 0; }
}
</style>
