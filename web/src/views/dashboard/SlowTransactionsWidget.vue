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
        <div class="skel" style="flex: 1; height: 10px" />
        <div class="skel" style="width: 44px; height: 10px" />
        <div class="skel" style="width: 44px; height: 10px" />
        <div class="skel" style="width: 80px; height: 5px; border-radius: 3px" />
      </div>
    </template>
    <div v-else-if="slowTx.length === 0" class="db-empty">
      <Icon name="activity" :size="18" style="color: var(--text-3)" />
      <div>No transaction data in the selected range</div>
    </div>
    <template v-else>
      <div class="db-tx-head">
        <span>Op</span>
        <span>Transaction</span>
        <span>P50</span>
        <span>P95</span>
        <span>Error rate</span>
      </div>
      <RouterLink
        v-for="tx in slowTx"
        :key="`${tx.transaction}-${tx.op}`"
        :to="{ name: 'transaction-profile', query: { ...investigationQuery(investigation), name: tx.transaction, op: tx.op } }"
        class="db-tx-row"
      >
        <span class="optag" :class="`optag--${tx.op.split('.')[0]}`">{{ tx.op.split('.')[0] }}</span>
        <span class="db-tx-row__name">{{ tx.transaction }}</span>
        <span class="db-tx-row__val">{{ formatDuration(tx.p50) }}</span>
        <span
          class="db-tx-row__val"
          :class="{ 'db-tx-row__val--warn': tx.p95 > 1000, 'db-tx-row__val--bad': tx.p95 > 2000 }"
        >{{ formatDuration(tx.p95) }}</span>
        <div class="db-tx-row__bar">
          <div class="db-tx-row__bar-bg">
            <div
              class="db-tx-row__bar-fill"
              :class="{ 'db-tx-row__bar-fill--warn': tx.failure_rate > 2 }"
              :style="{ width: `${Math.min(100, tx.failure_rate * 10)}%` }"
            />
          </div>
          <span
            class="db-tx-row__pct"
            :class="{ 'db-tx-row__pct--warn': tx.failure_rate > 2 }"
          >{{ tx.failure_rate.toFixed(1) }}%</span>
        </div>
      </RouterLink>
    </template>
  </div>
</template>

<style scoped src="./section.css"></style>

<style scoped>
/* ── Transactions table ─────────────────────────────────────────────────────── */

.db-tx-head {
  display: grid;
  grid-template-columns: 80px 1fr 72px 72px 120px;
  column-gap: 12px;
  padding: 7px 16px;
  font-size: var(--text-xs);
  color: var(--text-3);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  border-bottom: 1px solid var(--border);
}
.db-tx-head span:nth-child(n+3) { text-align: right; }

.db-tx-row {
  display: grid;
  grid-template-columns: 80px 1fr 72px 72px 120px;
  column-gap: 12px;
  padding: 8px 16px;
  border-bottom: 1px solid var(--border);
  align-items: center;
  text-decoration: none;
  color: inherit;
  cursor: pointer;
}

.db-tx-row:last-of-type { border-bottom: none; }
.db-tx-row:hover { background: var(--surface-2); }

.db-tx-row__name {
  font-size: var(--text-xs);
  color: var(--text-2);
  font-family: var(--mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  padding-right: 12px;
}

.db-tx-row__val {
  font-size: var(--text-sm);
  color: var(--text-3);
  text-align: right;
  font-variant-numeric: tabular-nums;
}

.db-tx-row__val--warn { color: var(--warning); }
.db-tx-row__val--bad  { color: var(--danger); }

.db-tx-row__bar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding-left: 10px;
}

.db-tx-row__bar-bg {
  flex: 1;
  height: 4px;
  background: var(--surface-2);
  border-radius: 2px;
  overflow: hidden;
}

.db-tx-row__bar-fill {
  height: 100%;
  background: var(--accent);
  border-radius: 2px;
}

.db-tx-row__bar-fill--warn { background: var(--danger); }

.db-tx-row__pct {
  font-size: 10px;
  color: var(--text-3);
  width: 30px;
  text-align: right;
  font-variant-numeric: tabular-nums;
}

@media (max-width: 768px) {
  /* Transactions table: drop P50 + error-rate bar, keep op + name + P95 */
  .db-tx-head,
  .db-tx-row {
    grid-template-columns: 80px 1fr 72px;
  }
  .db-tx-head > :nth-child(3),
  .db-tx-row > :nth-child(3) { display: none; } /* P50 */
  .db-tx-head > :nth-child(5),
  .db-tx-row > :nth-child(5) { display: none; } /* error rate bar */
}

.db-tx-row__pct--warn { color: var(--danger); }
</style>
