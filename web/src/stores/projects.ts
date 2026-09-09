import { defineStore } from 'pinia'
import { computed } from 'vue'
import { useInvestigationStore } from './investigation'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import type { ProjectMetadata } from '@/api/types'

export const useProjectsStore = defineStore('projects', () => {
  const investigation = useInvestigationStore()
  const selectedIds = computed({ get: () => investigation.projectIds, set: (ids: string[]) => { investigation.projectIds = [...new Set(ids)].sort() } })

  const { data, isFetched: metadataReady, isPending, isError, isSuccess, isFetching, fetchStatus, dataUpdatedAt, refetch } = useQuery({
    queryKey: ['projects', 'metadata'],
    queryFn: ({ signal }) => apiFetch<ProjectMetadata[]>('/api/projects/metadata', { signal }),
  })

  // Keep array consumers compatible without treating the fallback as a server result.
  const projects = computed(() => data.value ?? [])
  const hasLoaded = computed(() => data.value !== undefined)

  // Invalid selections stay explicit instead of silently becoming All projects.
  const invalidIds = computed(() => selectedIds.value.filter(id => !(projects.value ?? []).some(p => p.id === id)))

  function setSelected(ids: string[]) {
    selectedIds.value = ids
  }

  function toggleProject(id: string) {
    if (selectedIds.value.includes(id)) {
      selectedIds.value = selectedIds.value.filter((x) => x !== id)
    } else {
      selectedIds.value = [...selectedIds.value, id]
    }
  }

  return { projects, metadataReady, invalidIds, hasLoaded, isPending, isError, isSuccess, isFetching, fetchStatus, dataUpdatedAt, refetch, selectedIds, setSelected, toggleProject }
})
