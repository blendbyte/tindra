<script setup lang="ts">
import { vClickable } from '@/directives/clickable'
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useInvestigationStore } from '@/stores/investigation'
import { useFormatters } from '@/composables/useFormatters'
import Icon from '@/components/Icon.vue'
import { useProjectsStore } from '@/stores/projects'
import type { DashboardRelease } from './shared'

const props = defineProps<{
  loading: boolean
  releases: DashboardRelease[]
}>()

const router = useRouter()
const investigation = useInvestigationStore()
const { formatRel } = useFormatters()

const recentReleases = computed(() => props.releases.slice(0, 5))

// The same version can ship in several projects, so name the project when
// more than one is in view.
const projects = useProjectsStore()
function projectName(id: string): string | null {
  if (projects.projects.length < 2) return null
  return projects.projects.find(p => p.id === id)?.name ?? null
}
</script>

<template>
  <div class="db-sec">
    <div class="db-sec__head">
      <h2 class="db-sec__title">Release health</h2>
      <RouterLink :to="investigation.link('/releases')" class="db-sec__link">all releases <Icon name="arrow-right" :size="11" /></RouterLink>
    </div>
    <template v-if="loading">
      <div v-for="i in 4" :key="i" class="db-release-row">
        <div class="skel" style="width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0" />
        <div style="flex: 1">
          <div class="skel" style="width: 90px; height: 10px; margin-bottom: 5px" />
          <div class="skel" style="width: 60px; height: 9px" />
        </div>
        <div class="skel" style="width: 50px; height: 18px; border-radius: 3px" />
      </div>
    </template>
    <div v-else-if="recentReleases.length === 0" class="db-empty">
      <Icon name="package" :size="18" style="color: var(--text-3)" />
      <div>No releases yet</div>
    </div>
    <template v-else>
      <div
        v-for="rel in recentReleases"
        :key="rel.id"
        class="db-release-row"
        v-clickable
        role="link"
        @click="router.push(`/releases/${rel.id}`)"
      >
        <div
          class="db-release-row__dot"
          :class="{
            'db-release-row__dot--bad': rel.regressed_issues > 0,
            'db-release-row__dot--ok': rel.regressed_issues === 0 && rel.new_issues === 0,
          }"
        />
        <div class="db-release-row__info">
          <span class="db-release-row__version mono">{{ rel.version }}</span>
          <span v-if="projectName(rel.project_id)" class="db-release-row__project">{{ projectName(rel.project_id) }}</span>
        </div>
        <span class="db-release-row__time">{{ formatRel(rel.deployed_at) }}</span>
        <span
          v-if="rel.regressed_issues > 0"
          class="rel-pill rel-pill--bad"
        >{{ rel.regressed_issues }} regressed</span>
        <span
          v-else-if="rel.new_issues > 0"
          class="rel-pill rel-pill--bad"
        >{{ rel.new_issues }} new</span>
        <span v-else class="rel-pill rel-pill--ok">Clean</span>
      </div>
    </template>
  </div>
</template>

<style scoped src="./section.css"></style>

<style scoped>
/* ── Releases ───────────────────────────────────────────────────────────────── */

.db-release-row {
  padding: 8px 16px;
  border-bottom: 1px solid var(--border-soft);
  display: flex;
  align-items: center;
  gap: 10px;
  cursor: pointer;
}

.db-release-row:last-of-type { border-bottom: none; }
.db-release-row:hover { background: var(--surface-2); }

.db-release-row__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--accent);
  flex-shrink: 0;
}

.db-release-row__dot--bad { background: var(--danger); }
.db-release-row__dot--ok  { background: var(--success); }

.db-release-row__info {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: baseline;
  gap: 8px;
  white-space: nowrap;
  overflow: hidden;
}

.db-release-row__project {
  font-size: var(--text-xs);
  color: var(--text-3);
  overflow: hidden;
  text-overflow: ellipsis;
}

.db-release-row__version {
  font-size: var(--text-sm);
  color: var(--accent);
  flex-shrink: 0;
}

.db-release-row__time {
  font-size: var(--text-xs);
  color: var(--text-3);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.rel-pill {
  font-size: 10px;
  font-weight: 500;
  padding: 2px 7px;
  border-radius: 3px;
  white-space: nowrap;
  flex-shrink: 0;
}

.rel-pill--bad {
  background: oklch(from var(--danger) l c h / 0.12);
  color: var(--danger);
}

.rel-pill--ok {
  background: oklch(from var(--success) l c h / 0.12);
  color: var(--success);
}
</style>
