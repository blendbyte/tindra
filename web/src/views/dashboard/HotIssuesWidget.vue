<script setup lang="ts">
import { vClickable } from '@/directives/clickable'
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useInvestigationStore } from '@/stores/investigation'
import Icon from '@/components/Icon.vue'
import Sparkline from '@/components/Sparkline.vue'
import { fmt, type DashboardIssue } from './shared'

const props = defineProps<{
  loading: boolean
  issues: DashboardIssue[]
}>()

const router = useRouter()
const investigation = useInvestigationStore()

const hotIssues = computed(() =>
  [...props.issues]
    .sort((a, b) => b.event_count - a.event_count)
    .slice(0, 5),
)

function levelColor(level: string): string {
  switch (level) {
    case 'fatal':
    case 'error':
      return 'var(--danger)'
    case 'warning':
      return 'var(--warning)'
    case 'info':
      return 'var(--info)'
    default:
      return 'var(--text-3)'
  }
}
</script>

<template>
  <div class="db-sec">
    <div class="db-sec__head">
      <span class="db-sec__title">Hottest Issues</span>
      <RouterLink :to="investigation.link('/issues')" class="db-sec__link">all issues →</RouterLink>
    </div>
    <template v-if="loading">
      <div v-for="i in 5" :key="i" class="db-issue-row">
        <div class="skel" style="width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0" />
        <div class="skel" style="flex: 1; height: 10px" />
        <div class="skel" style="width: 28px; height: 10px; flex-shrink: 0" />
      </div>
    </template>
    <div v-else-if="hotIssues.length === 0" class="db-empty">
      <Icon name="check-circle" :size="18" style="color: var(--success)" />
      <div>No open issues</div>
    </div>
    <template v-else>
      <div
        v-for="issue in hotIssues"
        :key="issue.id"
        class="db-issue-row"
        v-clickable
        role="link"
        @click="router.push(`/issues/${issue.id}`)"
      >
        <span :class="`leveldot leveldot--${issue.level}`" />
        <div class="db-issue-row__title">{{ issue.title }}</div>
        <div class="db-issue-row__right">
          <span class="db-issue-row__count">{{ fmt(issue.event_count) }}</span>
          <Sparkline
            v-if="issue.sparkline?.length"
            :data="issue.sparkline"
            :width="40"
            :height="14"
            :color="levelColor(issue.level)"
          />
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped src="./section.css"></style>

<style scoped>
/* ── Issues ─────────────────────────────────────────────────────────────────── */

.db-issue-row {
  padding: 9px 16px;
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: center;
  gap: 10px;
  cursor: pointer;
}

.db-issue-row:last-of-type { border-bottom: none; }
.db-issue-row:hover { background: var(--surface-2); }

.db-issue-row__title {
  flex: 1;
  font-size: var(--text-sm);
  color: var(--text-2);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.db-issue-row__right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.db-issue-row__count {
  font-size: var(--text-xs);
  color: var(--text-3);
  font-variant-numeric: tabular-nums;
  min-width: 28px;
  text-align: right;
}
</style>
