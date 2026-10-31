import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { useInvestigationStore, RANGE_HOURS } from '@/stores/investigation'
import type { CronMonitor, UptimeMonitor } from '@/api/types'
import { useProjectScope } from './shared'
import type { AttentionIssuesPage } from './briefing'

// Monitor queries behind the briefing. Widgets that call this share one cache
// entry per key, so the briefing and the project list never fetch twice.
export function useMonitorQueries() {
  const { buildQs } = useProjectScope()

  const uptime = useQuery({
    queryKey: computed(() => ['uptime-monitors', buildQs()]),
    queryFn: ({ signal }) => apiFetch<UptimeMonitor[]>(`/api/uptime-monitors?${buildQs()}`, { signal }),
  })

  const cron = useQuery({
    queryKey: computed(() => ['monitors', buildQs()]),
    queryFn: ({ signal }) => apiFetch<CronMonitor[]>(`/api/monitors?${buildQs()}`, { signal }),
  })

  return { uptime, cron }
}

// Regressed issues and issues first seen in the window. With a last visit,
// the server also counts how many of each happened after it.
export function useAttentionQuery(lastVisit: number | null) {
  const investigation = useInvestigationStore()
  const { buildQs } = useProjectScope()

  return useQuery({
    queryKey: computed(() => ['issues', 'attention', investigation.scopeKey, lastVisit]),
    queryFn: ({ signal }) =>
      apiFetch<AttentionIssuesPage>(investigation.request(`/api/issues/attention?${buildQs({
        // 'All' has no bounds; the server caps windows at 90 days.
        hours: String(RANGE_HOURS[investigation.range] ?? 2160),
        ...(investigation.environment === 'All' ? {} : { env: investigation.environment }),
        ...(lastVisit === null ? {} : { last_visit: new Date(lastVisit).toISOString() }),
      })}`), { signal }),
  })
}
