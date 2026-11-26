<script setup lang="ts">
import { ref, watch } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import type { ProjectTeams } from '@/api/types'
import { useToast } from '@/composables/useToast'
import Icon from '@/components/Icon.vue'
import { useTeams } from './shared'

// Which teams a project belongs to. Members of those teams see the project, and so
// does everyone with access to all projects, so a project can never become
// unreachable.
const props = defineProps<{ projectId: string }>()
const emit = defineEmits<{ close: [] }>()

const { show: showToast } = useToast()
const qc = useQueryClient()
const { teams } = useTeams()

const { data: current } = useQuery({
  queryKey: ['project-teams', props.projectId],
  queryFn: ({ signal }) => apiFetch<ProjectTeams>(`/api/projects/${props.projectId}/teams`, { signal }),
})

const teamIds = ref<string[]>([])
const error = ref<string | null>(null)

watch(current, (c) => {
  if (c) teamIds.value = [...c.team_ids]
}, { immediate: true })

const { mutate: save, isPending: saving } = useMutation({
  mutationFn: () => apiFetch<ProjectTeams>(`/api/projects/${props.projectId}/teams`, {
    method: 'PUT',
    body: JSON.stringify({ team_ids: teamIds.value }),
  }),
  onSuccess: () => {
    // The prefix covers this project's teams and the list that shows them on every row.
    qc.invalidateQueries({ queryKey: ['project-teams'] })
    qc.invalidateQueries({ queryKey: ['projects'] })
    showToast('Project teams saved', 'success')
    emit('close')
  },
  onError: () => { error.value = 'Failed to save the project teams' },
})
</script>

<template>
  <form class="privacy-panel" @submit.prevent="error = null; save()">
    <div class="privacy-panel__title">
      <Icon name="shield" :size="13" />
      Teams
    </div>
    <div class="privacy-panel__section">
      <div class="field__label">Teams</div>
      <div v-if="teams.length" class="rule__project-checks">
        <label v-for="t in teams" :key="t.id" class="rule__project-check">
          <input v-model="teamIds" type="checkbox" :value="t.id" />
          {{ t.name }}
        </label>
      </div>
      <div class="field__hint">
        Members of these teams see this project, and so does everyone with access to all projects.
        <template v-if="teams.length === 0">No teams yet: create one in the Teams tab.</template>
      </div>
    </div>
    <div v-if="error" class="proj-form-error">{{ error }}</div>
    <div style="display: flex; gap: 8px; margin-top: 14px">
      <button type="submit" class="btn btn--primary" :disabled="!current || saving">
        {{ saving ? 'Saving…' : 'Save' }}
      </button>
      <button type="button" class="btn btn--ghost" @click="emit('close')">Cancel</button>
    </div>
  </form>
</template>
