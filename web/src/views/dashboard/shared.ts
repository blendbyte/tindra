import { computed } from 'vue'
import { useProjectsStore } from '@/stores/projects'
import { useInvestigationStore } from '@/stores/investigation'
import type { DashboardIssuePage, ReleaseHealthPage } from '@/api/types'

export type DashboardIssue = DashboardIssuePage['issues'][number]
export type DashboardRelease = ReleaseHealthPage['releases'][number]

export function fmt(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return String(n)
}

// Every dashboard query is scoped to the selected projects. buildQs turns the
// selection (plus any extra params) into a query string; pKey is the matching
// cache key fragment.
export function useProjectScope() {
  const projects = useProjectsStore()

  function buildQs(extra: Record<string, string> = {}) {
    const p = new URLSearchParams()
    for (const id of [...projects.selectedIds].sort()) p.append('project_id', id)
    for (const [k, v] of Object.entries(extra)) p.set(k, v)
    return p.toString()
  }

  const pKey = computed(() => [...projects.selectedIds].sort().join(','))

  return { buildQs, pKey }
}

export function useWindowLabel() {
  const investigation = useInvestigationStore()
  return computed(() => investigation.absolute ? 'selected range' : investigation.range)
}
