<script setup lang="ts">
import { useInvestigationStore } from '@/stores/investigation'
import Icon from '@/components/Icon.vue'
import Sparkline from '@/components/Sparkline.vue'
import { splitTitle } from './briefing'
import { fmt, type DashboardIssue } from './shared'

defineProps<{
  issues: DashboardIssue[]
  openCount: number
}>()

const investigation = useInvestigationStore()
</script>

<template>
  <section v-if="issues.length" class="db-sec" aria-labelledby="busiest-title">
    <div class="db-sec__head">
      <h2 id="busiest-title" class="db-sec__title db-sec__title--quiet">Busiest open issues</h2>
      <RouterLink :to="investigation.link('/issues')" class="db-sec__link">all {{ fmt(openCount) }} open <Icon name="arrow-right" :size="11" /></RouterLink>
    </div>
    <ul class="busy">
      <li v-for="issue in issues" :key="issue.id">
        <RouterLink :to="investigation.link(`/issues/${issue.id}`)" class="busy__row">
          <span :class="`leveldot leveldot--${issue.level}`" />
          <span class="busy__title">
            <span v-if="splitTitle(issue.title).type" class="busy__type mono">{{ splitTitle(issue.title).type }}</span><template v-if="splitTitle(issue.title).type">: </template>{{ splitTitle(issue.title).message }}
          </span>
          <span class="busy__viz">
            <Sparkline v-if="issue.sparkline?.length" :data="issue.sparkline" :width="132" :height="16" color="var(--text-3)" />
          </span>
          <span class="busy__count">{{ fmt(issue.event_count) }}</span>
        </RouterLink>
      </li>
    </ul>
  </section>
</template>

<style scoped src="./section.css"></style>

<style scoped>
.db-sec__title--quiet { color: var(--text-2); }

.busy { list-style: none; margin: 0; padding: 0; }

.busy__row {
  display: grid;
  grid-template-columns: 8px minmax(0, 1fr) 132px 56px;
  align-items: center;
  gap: 12px;
  padding: 10px 16px;
  border-bottom: 1px solid var(--border-soft);
  color: inherit;
  text-decoration: none;
  transition: background-color 120ms cubic-bezier(0.16, 1, 0.3, 1);
}

.busy__row:hover { background: var(--surface-2); }
.busy__row:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }

.busy__title {
  font-size: var(--text-sm);
  color: var(--text-2);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.busy__type { color: var(--accent); font-size: 0.93em; }
.busy__viz { display: flex; align-items: center; }

.busy__count {
  justify-self: end;
  font-family: var(--mono);
  font-size: var(--text-xs);
  font-variant-numeric: tabular-nums;
  color: var(--text-2);
}

@media (max-width: 768px) {
  .busy__row { grid-template-columns: 8px minmax(0, 1fr) auto; align-items: start; }
  .busy__row .leveldot { margin-top: 6px; }
  .busy__viz { display: none; }
  .busy__title { white-space: normal; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; }
}

@media (prefers-reduced-motion: reduce) {
  .busy__row { transition: none; }
}
</style>
