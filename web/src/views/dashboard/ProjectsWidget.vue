<script setup lang="ts">
import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { apiFetch } from '@/api/client'
import { investigationQuery } from '@/router/investigation'
import { useInvestigationStore } from '@/stores/investigation'
import { useMonitorQueries } from './briefingData'
import { brokenMonitors, plural } from './briefing'

interface ProjectIssueCount {
  project_id: string
  open_issues: number
}

const projects = useProjectsStore()
const investigation = useInvestigationStore()
const { uptime, cron } = useMonitorQueries()

const { data: projectIssueCounts, isFetching } = useQuery({
  queryKey: ['projects', 'stats'],
  queryFn: ({ signal }) => apiFetch<ProjectIssueCount[]>('/api/projects/stats', { signal }),
  enabled: computed(() => projects.projects.length >= 2),
})

// One row per visible project: failing monitors first, then by open issues.
const rows = computed(() => {
  if (projects.projects.length < 2) return []
  const failing = new Map<string, number>()
  for (const b of brokenMonitors(uptime.data.value ?? [], cron.data.value ?? [])) {
    failing.set(b.monitor.project_id, (failing.get(b.monitor.project_id) ?? 0) + 1)
  }
  const open = new Map((projectIssueCounts.value ?? []).map(c => [c.project_id, c.open_issues]))
  return projects.projects
    .filter(p => !projects.selectedIds.length || projects.selectedIds.includes(p.id))
    .map(p => ({ id: p.id, name: p.name, failing: failing.get(p.id) ?? 0, open: open.get(p.id) ?? 0 }))
    .sort((a, b) => b.failing - a.failing || b.open - a.open || a.name.localeCompare(b.name))
})

const loading = computed(() => isFetching.value && !projectIssueCounts.value)
// Without monitor data a project's health is unknown, never shown as healthy.
const monitorsUnknown = computed(() =>
  (!!uptime.isError.value && !uptime.data.value) || (!!cron.isError.value && !cron.data.value))
</script>

<template>
  <section v-if="rows.length" class="db-sec" aria-labelledby="projects-title">
    <div class="db-sec__head">
      <h2 id="projects-title" class="db-sec__title">Projects</h2>
    </div>
    <ul class="proj">
      <li v-for="row in rows" :key="row.id">
        <RouterLink
          :to="{ name: 'issues', query: { ...investigationQuery(investigation), project_id: row.id } }"
          class="proj__row"
        >
          <span class="proj__dot" :class="row.failing ? 'proj__dot--bad' : monitorsUnknown ? 'proj__dot--unknown' : 'proj__dot--ok'" />
          <span class="proj__name">{{ row.name }}</span>
          <span class="proj__stat">
            <span v-if="row.failing" class="proj__failing">{{ plural(row.failing, 'monitor', 'monitors') }} failing · </span>
            <span v-if="loading" class="skel" style="display: inline-block; width: 44px; height: 10px" />
            <template v-else>{{ row.open }} open</template>
          </span>
        </RouterLink>
      </li>
    </ul>
  </section>
</template>

<style scoped src="./section.css"></style>

<style scoped>
.proj { list-style: none; margin: 0; padding: 0; }

.proj__row {
  display: grid;
  grid-template-columns: 8px minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  padding: 9px 16px;
  border-bottom: 1px solid var(--border-soft);
  color: inherit;
  text-decoration: none;
}

.proj li:last-child .proj__row { border-bottom: none; }
.proj__row:hover { background: var(--surface-2); }

.proj__dot { width: 8px; height: 8px; border-radius: 50%; }
.proj__dot--bad { background: var(--danger); }
.proj__dot--ok { background: var(--success); }
.proj__dot--unknown { background: var(--text-3); }

.proj__name {
  font-size: var(--text-sm);
  color: var(--text-1);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.proj__stat {
  font-size: var(--text-xs);
  color: var(--text-3);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.proj__failing { color: var(--danger); }
</style>
