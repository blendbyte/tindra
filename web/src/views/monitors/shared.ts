import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import type { ProjectMetadata } from '@/api/types'
import { useProjectsStore } from '@/stores/projects'
import { useAuthStore } from '@/stores/auth'

export type MonitorsTab = 'cron' | 'uptime'

// Creating, editing and deleting monitors needs the manage_projects permission.
export function useCanManage() {
  const auth = useAuthStore()
  return computed(() => auth.user?.permissions.manage_projects ?? false)
}

// Project list for the create forms' project picker and the row project tags.
export function useMonitorProjects() {
  const { data: projectList } = useQuery({
    queryKey: ['projects', 'metadata'],
    queryFn: ({ signal }) => apiFetch<ProjectMetadata[]>('/api/projects/metadata', { signal }),
  })

  function projectName(id: string): string {
    return projectList.value?.find((p) => p.id === id)?.name ?? id.slice(0, 8)
  }

  return { projectList, projectName }
}

// Monitor lists follow the global project selection.
export function useMonitorQueryParams() {
  const projects = useProjectsStore()
  return computed(() => {
    const p = new URLSearchParams()
    for (const id of [...projects.selectedIds].sort()) p.append('project_id', id)
    return p.toString()
  })
}
