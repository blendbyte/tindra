<script setup lang="ts">
import { computed } from 'vue'
import { useInvestigationStore } from '@/stores/investigation'
import { formatDuration } from '@/utils/formatters'
import Icon from '@/components/Icon.vue'
import type { TransactionSummary } from '@/api/types'
import { fmt, useWindowLabel } from './shared'

const props = defineProps<{
  loading: boolean
  txSummaries?: TransactionSummary[]
}>()

const investigation = useInvestigationStore()
const windowLabel = useWindowLabel()

// Every figure is weighted by sample count, so busy endpoints count for more.
function weighted(pick: (t: TransactionSummary) => number): number | null {
  const s = props.txSummaries
  if (!s?.length) return null
  const total = s.reduce((acc, t) => acc + t.sample_count, 0)
  if (!total) return null
  return s.reduce((acc, t) => acc + pick(t) * t.sample_count, 0) / total
}

const errorRate = computed(() => weighted(t => t.failure_rate))
const p95 = computed(() => weighted(t => t.p95))
const apdex = computed(() => weighted(t => t.apdex))
const transactions = computed(() => props.txSummaries?.reduce((total, row) => total + row.sample_count, 0) ?? null)

type Tone = 'danger' | 'warning' | ''
const errorTone = computed((): Tone => errorRate.value === null ? '' : errorRate.value > 5 ? 'danger' : errorRate.value > 2 ? 'warning' : '')
const p95Tone = computed((): Tone => p95.value === null ? '' : p95.value > 2000 ? 'danger' : p95.value > 1000 ? 'warning' : '')
const apdexTone = computed((): Tone => apdex.value === null ? '' : apdex.value < 0.7 ? 'danger' : apdex.value < 0.94 ? 'warning' : '')

const vitals = computed(() => [
  { key: 'error-rate', label: 'error rate', value: errorRate.value === null ? null : `${errorRate.value.toFixed(1)}%`, tone: errorTone.value },
  { key: 'p95', label: 'p95 latency', value: p95.value === null ? null : formatDuration(p95.value), tone: p95Tone.value },
  { key: 'transactions', label: 'transactions', value: transactions.value === null ? null : fmt(transactions.value), tone: '' as Tone },
  { key: 'apdex', label: 'apdex · T=500ms', value: apdex.value === null ? null : apdex.value.toFixed(2), tone: apdexTone.value },
])
</script>

<template>
  <section class="db-sec" aria-labelledby="vitals-title">
    <div class="db-sec__head">
      <h2 id="vitals-title" class="db-sec__title">Vitals · {{ windowLabel }}</h2>
      <RouterLink :to="investigation.link('/performance/transactions')" class="db-sec__link">performance <Icon name="arrow-right" :size="11" /></RouterLink>
    </div>
    <dl class="vitals">
      <div v-for="vital in vitals" :key="vital.key" class="vitals__cell" :data-vital="vital.key">
        <dt class="vitals__label">{{ vital.label }}</dt>
        <dd class="vitals__value">
          <span v-if="loading" class="skel" style="display: inline-block; width: 56px; height: 22px" />
          <span v-else-if="vital.value !== null" :class="vital.tone && `vitals__value--${vital.tone}`">{{ vital.value }}</span>
          <span v-else class="vitals__value--muted">–</span>
        </dd>
      </div>
    </dl>
  </section>
</template>

<style scoped src="./section.css"></style>

<style scoped>
.vitals {
  display: grid;
  grid-template-columns: 1fr 1fr;
  margin: 12px 16px 0;
  border: 1px solid var(--border-soft);
  border-radius: 8px;
  overflow: hidden;
}

.vitals__cell {
  display: flex;
  flex-direction: column-reverse;
  gap: 4px;
  padding: 12px 14px;
  background: var(--surface);
}

.vitals__cell:nth-child(odd) { border-right: 1px solid var(--border-soft); }
.vitals__cell:nth-child(-n + 2) { border-bottom: 1px solid var(--border-soft); }

.vitals__label {
  font-size: var(--text-xs);
  color: var(--text-3);
}

.vitals__value {
  margin: 0;
  font-size: var(--text-xl);
  font-weight: 600;
  line-height: 1.1;
  letter-spacing: -0.02em;
  color: var(--text-1);
  font-variant-numeric: tabular-nums;
}

.vitals__value--danger { color: var(--danger); }
.vitals__value--warning { color: var(--warning); }
.vitals__value--muted { color: var(--text-3); font-weight: 400; }
</style>
