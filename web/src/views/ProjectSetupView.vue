<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useProjectsStore } from '@/stores/projects'
import ProjectSetup from '@/components/ProjectSetup.vue'

const route = useRoute()
const router = useRouter()
const projects = useProjectsStore()
const project = computed(() => projects.projects.find(p => route.params.projectSlug ? p.slug === route.params.projectSlug : p.id === route.query.project_id))
</script>

<template>
  <div class="page setup-page">
    <div class="setup-page__project">
      <label class="field__label" for="setup-project">Project</label>
      <select id="setup-project" class="field__input" :value="project?.slug ?? ''" @change="router.push(`/projects/${encodeURIComponent(($event.target as HTMLSelectElement).value)}/setup`)">
        <option disabled value="">Choose a project</option>
        <option v-for="p in projects.projects" :key="p.id" :value="p.slug">{{ p.name }}</option>
      </select>
    </div>
    <ProjectSetup v-if="project" :key="project.id" :project="project" :focus="String(route.query.check ?? '')" @continue="router.push('/dashboard')" />
    <p v-else class="setup-page__empty">Choose an available project to check its setup.</p>
  </div>
</template>

<style scoped>
.setup-page__project { display: flex; align-items: center; gap: 12px; padding: 12px 24px; border-bottom: 1px solid var(--border); background: var(--surface); }
.setup-page__project select { width: 220px; height: 28px; padding-left: 10px; font-size: var(--text-xs); }
.setup-page__empty { padding: 24px; color: var(--text-2); }
@media (max-width: 600px) { .setup-page__project { padding: 12px 16px; } }
</style>
