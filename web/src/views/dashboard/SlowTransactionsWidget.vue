<script setup lang="ts">
import { computed } from 'vue'
import { investigationQuery } from '@/router/investigation'
import { useInvestigationStore } from '@/stores/investigation'
import { formatDuration } from '@/utils/formatters'
import type { TransactionSummary } from '@/api/types'
import Icon from '@/components/Icon.vue'

const props = defineProps<{
  loading: boolean
  txSummaries?: TransactionSummary[]
}>()

const investigation = useInvestigationStore()

const slowTx = computed(() =>
  [...(props.txSummaries ?? [])].sort((a, b) => b.p95 - a.p95).slice(0, 5),
)
</script>

<template>
  <div class="db-sec">
    <div class="db-sec__head">
      <h2 class="db-sec__title">Slowest transactions</h2>
      <RouterLink :to="investigation.link('/performance/transactions')" class="db-sec__link">open performance <Icon name="arrow-right" :size="11" /></RouterLink>
    </div>
    <template v-if="loading">
      <div v-for="i in 5" :key="i" class="db-tx-row">
        <div>
          <div class="skel" style="width: 70%; height: 10px; margin-bottom: 5px" />
          <div class="skel" style="width: 45%; height: 9px" />
        </div>
        <div class="skel" style="width: 44px; height: 10px" />
      </div>
    </template>
    <div v-else-if="slowTx.length === 0" class="db-empty">
      <Icon name="activity" :size="18" style="color: var(--text-3)" />
      <div>No transaction data in the selected range</div>
    </div>
    <template v-else>
      <RouterLink
        v-for="tx in slowTx"
        :key="`${tx.transaction}-${tx.op}`"
        :to="{ name: 'transaction-profile', query: { ...investigationQuery(investigation), name: tx.transaction, op: tx.op } }"
        class="db-tx-row"
      >
        <span class="db-tx-row__main">
          <span class="db-tx-row__name">{{ tx.transaction }}</span>
          <span class="db-tx-row__meta">
            {{ tx.op.split('.')[0] }} · p50 {{ formatDuration(tx.p50) }} ·
            <span
              class="db-tx-row__pct"
              :class="{ 'db-tx-row__pct--warn': tx.failure_rate > 2, 'db-tx-row__pct--bad': tx.failure_rate > 10 }"
            >{{ tx.failure_rate.toFixed(1) }}% errors</span>
          </span>
        </span>
        <span
          class="db-tx-row__val"
          :class="{ 'db-tx-row__val--warn': tx.p95 > 2000 }"
          :title="`p95 ${formatDuration(tx.p95)}`"
        >{{ formatDuration(tx.p95) }}</span>
      </RouterLink>
    </template>
  </div>
</template>

<style scoped src="./section.css"></style>

<style scoped>
/* ── Transactions list (aside) ──────────────────────────────────────────────── */
/* A compact list for the aside: the route over a quiet line of facts, with
   the p95 that ranks it on the right. */

.db-tx-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  column-gap: 12px;
  padding: 8px 16px;
  border-bottom: 1px solid var(--border-soft);
  align-items: center;
  text-decoration: none;
  color: inherit;
}

.db-tx-row:last-of-type { border-bottom: none; }
.db-tx-row:hover { background: var(--surface-2); }

.db-tx-row__main { display: flex; flex-direction: column; gap: 2px; min-width: 0; }

.db-tx-row__name {
  font-size: var(--text-xs);
  color: var(--text-1);
  font-family: var(--mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.db-tx-row__meta {
  font-size: var(--text-xs);
  color: var(--text-3);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-variant-numeric: tabular-nums;
}

.db-tx-row__val {
  font-size: var(--text-sm);
  color: var(--text-2);
  text-align: right;
  font-variant-numeric: tabular-nums;
}

/* Slow is degraded, not broken: a long P95 is amber at most, so the slowest
   list does not read as five failures (The Earned Red Rule). */
.db-tx-row__val--warn { color: var(--warning); }
.db-tx-row__pct--warn { color: var(--warning); }
.db-tx-row__pct--bad { color: var(--danger); }
</style>
