<script setup lang="ts">
import { computed } from 'vue'
import { formatDuration } from '@/utils/formatters'
import type { TransactionSummary } from '@/api/types'
import { fmt, useWindowLabel } from './shared'

const props = defineProps<{
  loading: boolean
  openCount: number
  txSummaries?: TransactionSummary[]
}>()

const windowLabel = useWindowLabel()

const errorRate = computed((): number | null => {
  const s = props.txSummaries
  if (!s?.length) return null
  const total = s.reduce((acc, t) => acc + t.sample_count, 0)
  if (!total) return null
  return s.reduce((acc, t) => acc + t.failure_rate * t.sample_count, 0) / total
})

const p95Weighted = computed((): number | null => {
  const s = props.txSummaries
  if (!s?.length) return null
  const total = s.reduce((acc, t) => acc + t.sample_count, 0)
  if (!total) return null
  return s.reduce((acc, t) => acc + t.p95 * t.sample_count, 0) / total
})

const apdex = computed((): number | null => {
  const s = props.txSummaries
  if (!s?.length) return null
  const total = s.reduce((acc, t) => acc + t.sample_count, 0)
  if (!total) return null
  return s.reduce((acc, t) => acc + t.apdex * t.sample_count, 0) / total
})

const events24h = computed(() => props.txSummaries?.reduce((total, row) => total + row.sample_count, 0) ?? null)
</script>

<template>
  <div class="db-kpis">
    <div class="db-kpi">
      <div class="db-kpi__label">Open Issues</div>
      <div v-if="loading" class="skel" style="width: 48px; height: 28px; margin-bottom: 4px" />
      <div v-else class="db-kpi__value">{{ fmt(openCount) }}</div>
      <div class="db-kpi__sub">
        <span :class="openCount > 0 ? 'leveldot leveldot--fatal' : 'leveldot leveldot--ok'" />
        open &amp; regressed
      </div>
    </div>

    <div class="db-kpi">
      <div class="db-kpi__label">Error Rate</div>
      <div v-if="loading" class="skel" style="width: 48px; height: 28px; margin-bottom: 4px" />
      <div
        v-else-if="errorRate !== null"
        class="db-kpi__value"
        :style="{
          color:
            errorRate > 5
              ? 'var(--danger)'
              : errorRate > 2
                ? 'var(--warning)'
                : undefined,
        }"
      >{{ errorRate.toFixed(1) }}%</div>
      <div v-else class="db-kpi__value db-kpi__value--muted">–</div>
      <div class="db-kpi__sub">weighted · {{ windowLabel }}</div>
    </div>

    <div class="db-kpi">
      <div class="db-kpi__label">P95 Latency</div>
      <div v-if="loading" class="skel" style="width: 48px; height: 28px; margin-bottom: 4px" />
      <div
        v-else-if="p95Weighted !== null"
        class="db-kpi__value"
        :style="{
          color:
            p95Weighted > 2000
              ? 'var(--danger)'
              : p95Weighted > 1000
                ? 'var(--warning)'
                : undefined,
        }"
      >{{ formatDuration(p95Weighted) }}</div>
      <div v-else class="db-kpi__value db-kpi__value--muted">–</div>
      <div class="db-kpi__sub">weighted average · {{ windowLabel }}</div>
    </div>

    <div class="db-kpi">
      <div class="db-kpi__label">Apdex</div>
      <div v-if="loading" class="skel" style="width: 48px; height: 28px; margin-bottom: 4px" />
      <div
        v-else-if="apdex !== null"
        class="db-kpi__value"
        :style="{
          color:
            apdex < 0.70
              ? 'var(--danger)'
              : apdex < 0.94
                ? 'var(--warning)'
                : undefined,
        }"
      >{{ apdex.toFixed(2) }}</div>
      <div v-else class="db-kpi__value db-kpi__value--muted">–</div>
      <div class="db-kpi__sub">weighted · T=500ms · {{ windowLabel }}</div>
    </div>

    <div class="db-kpi">
      <div class="db-kpi__label">Transactions / {{ windowLabel }}</div>
      <div v-if="loading" class="skel" style="width: 48px; height: 28px; margin-bottom: 4px" />
      <div v-else-if="events24h !== null" class="db-kpi__value">{{ fmt(events24h) }}</div>
      <div v-else class="db-kpi__value db-kpi__value--muted">–</div>
      <div class="db-kpi__sub">across all endpoints</div>
    </div>
  </div>
</template>

<style scoped>
/* ── KPI strip ──────────────────────────────────────────────────────────────── */

.db-kpis {
  display: grid;
  grid-template-columns: repeat(5, 1fr);
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}

.db-kpi {
  padding: 10px 16px;
  border-right: 1px solid var(--border);
}

.db-kpi:last-child {
  border-right: none;
}

.db-kpi__label {
  font-size: var(--text-xs);
  font-weight: 500;
  text-transform: uppercase;
  letter-spacing: 0.07em;
  color: var(--text-3);
  margin-bottom: 8px;
}

.db-kpi__value {
  font-size: var(--text-2xl);
  font-weight: 700;
  color: var(--text-1);
  letter-spacing: -0.04em;
  line-height: 1;
  margin-bottom: 6px;
  font-variant-numeric: tabular-nums;
}

.db-kpi__value--muted {
  color: var(--text-3);
  font-weight: 400;
}

.db-kpi__sub {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: var(--text-xs);
  color: var(--text-3);
}

@media (max-width: 768px) {
  /* KPI strip: 2-column grid (5 cards → 2+2+1) */
  .db-kpis {
    grid-template-columns: 1fr 1fr;
  }
  .db-kpi:nth-child(2) { border-right: none; }
  .db-kpi:nth-child(3) { border-top: 1px solid var(--border); }
  .db-kpi:nth-child(4) { border-top: 1px solid var(--border); border-right: none; }
  .db-kpi:nth-child(5) { border-top: 1px solid var(--border); grid-column: 1 / -1; border-right: none; }
}

@media (max-width: 480px) {
  /* KPI strip: single column on very small phones */
  .db-kpis {
    grid-template-columns: 1fr 1fr;
  }
  .db-kpi {
    padding: 12px 14px;
  }
  .db-kpi__value {
    font-size: var(--text-xl);
  }
}
</style>
