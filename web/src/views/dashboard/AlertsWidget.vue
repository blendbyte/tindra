<script setup lang="ts">
import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { useFormatters } from '@/composables/useFormatters'
import { useMe } from '@/composables/useMe'
import type { AlertRule } from '@/api/types'
import Icon from '@/components/Icon.vue'
import { alertVerdict } from '@/views/settings/alertState'

const { formatRel } = useFormatters()

const { data: me } = useMe()
const canManageAlerts = computed(() => me.value?.permissions.manage_alerts ?? false)

const { data: alertRules, isLoading: alertsLoading } = useQuery({
  queryKey: ['alert-rules'],
  queryFn: async ({ signal }) => {
    const res = await apiFetch<{ rules: AlertRule[] }>('/api/alert-rules', { signal })
    return res?.rules ?? []
  },

})

const firedAlerts = computed(() =>
  [...(alertRules.value ?? [])]
    .filter((r): r is AlertRule & { last_fired_at: string } => r.last_fired_at !== null)
    .sort((a, b) => new Date(b.last_fired_at).getTime() - new Date(a.last_fired_at).getTime())
    .slice(0, 5),
)

// Nothing fired is only good news while some rule could fire.
const verdict = computed(() => alertVerdict(alertRules.value ?? []))
</script>

<template>
  <div class="db-sec">
    <div class="db-sec__head">
      <h2 class="db-sec__title">Recent alerts</h2>
      <RouterLink v-if="canManageAlerts" to="/alerts" class="db-sec__link">configure <Icon name="arrow-right" :size="11" /></RouterLink>
    </div>
    <template v-if="alertsLoading">
      <div v-for="i in 3" :key="i" class="db-alert-row">
        <div class="skel" style="width: 20px; height: 20px; border-radius: 4px; flex-shrink: 0" />
        <div style="flex: 1">
          <div class="skel" style="width: 80%; height: 10px; margin-bottom: 5px" />
          <div class="skel" style="width: 50%; height: 9px" />
        </div>
      </div>
    </template>
    <div v-else-if="firedAlerts.length === 0 && verdict.silent" class="db-silent">
      <Icon name="alert-triangle" :size="14" />
      <span>{{ verdict.text }}</span>
    </div>
    <div v-else-if="firedAlerts.length === 0" class="db-empty">
      <Icon name="check-circle" :size="18" style="color: var(--success)" />
      <div>No alerts fired recently</div>
    </div>
    <template v-else>
      <div
        v-for="a in firedAlerts"
        :key="a.id"
        class="db-alert-row"
      >
        <div class="db-alert-row__icon">
          <Icon name="zap" :size="11" style="color: var(--text-3)" />
        </div>
        <div class="db-alert-row__body">
          <div class="db-alert-row__name">{{ a.name }}</div>
          <div class="db-alert-row__time">{{ formatRel(a.last_fired_at) }} · {{ a.channel }}</div>
        </div>
      </div>
    </template>
    <div v-if="!alertsLoading && (firedAlerts.length || !verdict.silent)" class="db-sec__foot" :class="{ 'db-sec__foot--warning': verdict.silent }">
      {{ verdict.text }}
    </div>
  </div>
</template>

<style scoped src="./section.css"></style>

<style scoped>
/* ── Alerts ─────────────────────────────────────────────────────────────────── */

/* Every rule paused or none set up: said plainly, in the warning colour. */
.db-silent {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 14px 16px;
  font-size: var(--text-sm);
  color: var(--warning);
}
.db-silent :deep(svg) { flex-shrink: 0; margin-top: 2px; }
.db-sec__foot--warning { color: var(--warning); }

.db-alert-row {
  padding: 9px 16px;
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.db-alert-row:last-of-type { border-bottom: none; }

.db-alert-row__icon {
  width: 20px;
  height: 20px;
  border-radius: 4px;
  background: var(--surface-2);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.db-alert-row__name {
  font-size: var(--text-sm);
  color: var(--text-2);
  margin-bottom: 2px;
}

.db-alert-row__time {
  font-size: var(--text-xs);
  color: var(--text-3);
}
</style>
