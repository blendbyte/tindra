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
    <p v-else style="max-width: 820px; margin: auto">Choose an available project to check its setup.</p>
  </div>
</template>

<style scoped>
.setup-page { padding: 32px 24px 48px; }
.setup-page__project { width: 100%; max-width: 820px; margin: 0 auto 24px; }
.setup-page__project select { max-width: 320px; }
@media (max-width: 600px) { .setup-page { padding: 24px 16px 32px; } }
</style>
