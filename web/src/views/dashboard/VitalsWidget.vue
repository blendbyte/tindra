<script setup lang="ts">
import { computed } from 'vue'
import { useInvestigationStore } from '@/stores/investigation'
import { formatDuration, formatFraction } from '@/utils/formatters'
import Icon from '@/components/Icon.vue'
import { apdexTone, enoughSamples, errorTone, failureRose, p95Rose, p95Tone, type PerformanceOverview, type Tone } from './overview'
import { fmt, useWindowLabel } from './shared'

const props = defineProps<{
  loading: boolean
  overview?: PerformanceOverview
}>()

const investigation = useInvestigationStore()
const windowLabel = useWindowLabel()

const total = computed(() => props.overview?.total)
// Without requests there is no rate or latency to report.
const measured = computed(() => total.value && total.value.requests > 0 ? total.value : null)
const tone = (t: Tone): Tone => measured.value && enoughSamples(measured.value) ? t : ''

const vitals = computed(() => {
  const m = measured.value
  return [
    { key: 'error-rate', label: 'error rate', value: m && formatFraction(m.failure_rate), tone: m ? tone(errorTone(m.failure_rate)) : '', rose: !!m && failureRose(m), was: m && formatFraction(m.prev_failure_rate) },
    { key: 'p95', label: 'p95 latency', value: m && formatDuration(m.p95), tone: m ? tone(p95Tone(m.p95)) : '', rose: !!m && p95Rose(m), was: m && formatDuration(m.prev_p95) },
    { key: 'transactions', label: 'transactions', value: total.value ? fmt(total.value.requests) : null, tone: '', rose: false, was: null },
    { key: 'apdex', label: 'apdex · T=500ms', value: m && m.apdex.toFixed(2), tone: m ? tone(apdexTone(m.apdex)) : '', rose: false, was: null },
  ]
})
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
          <template v-else-if="vital.value !== null">
            <span :class="vital.tone && `vitals__value--${vital.tone}`">{{ vital.value }}</span>
            <span v-if="vital.rose" class="vitals__rose" :title="`Up from ${vital.was} in the previous ${windowLabel}`">
              <Icon name="arrow-up" :size="11" /><span class="sr-only">Up from {{ vital.was }} in the previous {{ windowLabel }}</span>
            </span>
          </template>
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

.vitals__rose { display: inline-flex; margin-left: 4px; vertical-align: 2px; color: var(--text-3); }

.vitals__value--danger { color: var(--danger); }
.vitals__value--warning { color: var(--warning); }
.vitals__value--muted { color: var(--text-3); font-weight: 400; }
</style>
