<script setup lang="ts">
import { RouterLink } from 'vue-router'
import type { MonitorsTab } from './shared'

defineProps<{ active: MonitorsTab }>()
</script>

<template>
  <!-- Sub-navigation -->
  <div class="mon-subnav">
    <RouterLink
      to="/monitors/cron"
      class="mon-subnav__tab"
      :class="{ 'mon-subnav__tab--active': active === 'cron' }"
    >Cron monitors</RouterLink>
    <RouterLink
      to="/monitors/uptime"
      class="mon-subnav__tab"
      :class="{ 'mon-subnav__tab--active': active === 'uptime' }"
    >Uptime monitors</RouterLink>
    <!-- Page actions share the tab row, so monitors add one bar, not two. -->
    <div class="mon-subnav__actions"><slot /></div>
  </div>
</template>

<style scoped>
/* ---------- Sub-navigation (mirrors .perf-subnav) ---------- */

.mon-subnav {
  display: flex;
  align-items: center;
  gap: 0;
  padding: 0 24px;
  border-bottom: 1px solid var(--border);
  background: var(--bg);
  position: sticky;
  top: 48px;
  z-index: 10;
  height: 40px;
  flex: 0 0 40px;
}

.mon-subnav__tab {
  font-size: var(--text-sm);
  color: var(--text-3);
  padding: 10px 14px;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  text-decoration: none;
  white-space: nowrap;
  transition: color 120ms ease;
}

.mon-subnav__tab:hover { color: var(--text-1); }
.mon-subnav__tab--active {
  color: var(--text-1);
  border-bottom-color: var(--accent);
}

.mon-subnav__actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 8px;
  padding-left: 12px;
}
.mon-subnav__actions :deep(.btn) { height: 28px; padding-block: 0; }

@media (max-width: 640px) {
  .mon-subnav { overflow-x: auto; scrollbar-width: none; flex-wrap: nowrap; padding: 0 12px; }
  .mon-subnav__actions { position: sticky; right: 0; background: var(--bg); }
  .mon-subnav::-webkit-scrollbar { display: none; }
}
</style>
