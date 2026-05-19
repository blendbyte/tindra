import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import type { Project } from '@/api/types'

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

  const { data: projects } = useQuery({
    queryKey: ['projects'],
    queryFn: () => apiFetch<Project[]>('/api/projects'),
    initialData: [],
  })

  // Drop stale IDs that no longer correspond to real projects.
  watch(projects, (ps) => {
    if (!ps || ps.length === 0) return
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

  return { projects, selectedIds, setSelected, toggleProject }
})
