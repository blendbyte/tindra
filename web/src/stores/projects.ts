import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import type { ProjectMetadata } from '@/api/types'

export const useProjectsStore = defineStore('projects', () => {
  const selectedIds = ref<string[]>(
    (() => {
      try {
        const raw = sessionStorage.getItem('tindra:projectFilter')
        return raw ? (JSON.parse(raw) as string[]) : []
      } catch {
        return []
      }
    })(),
  )

  watch(selectedIds, (ids) => {
    try {
      sessionStorage.setItem('tindra:projectFilter', JSON.stringify(ids))
    } catch {}
  })

  const { data, isPending, isError, isSuccess, isFetching, fetchStatus, dataUpdatedAt, refetch } = useQuery({
    queryKey: ['projects', 'metadata'],
    queryFn: ({ signal }) => apiFetch<ProjectMetadata[]>('/api/projects/metadata', { signal }),
  })

  // Keep array consumers compatible without treating the fallback as a server result.
  const projects = computed(() => data.value ?? [])
  const hasLoaded = computed(() => data.value !== undefined)

  // Drop stale IDs that no longer correspond to real projects.
  watch(data, (ps) => {
    if (!ps) return
    const valid = new Set(ps.map((p) => p.id))
    const cleaned = selectedIds.value.filter((id) => valid.has(id))
    if (cleaned.length !== selectedIds.value.length) selectedIds.value = cleaned
  })

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

  return { projects, hasLoaded, isPending, isError, isSuccess, isFetching, fetchStatus, dataUpdatedAt, refetch, selectedIds, setSelected, toggleProject }
})
