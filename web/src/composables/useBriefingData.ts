import { computed, toValue, type MaybeRefOrGetter } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { useInvestigationStore, RANGE_HOURS } from '@/stores/investigation'
import type { CronMonitor, UptimeMonitor } from '@/api/types'
import { useProjectScope } from '@/views/dashboard/shared'
import { brokenMonitors, lastVisitSeen, type AttentionIssuesPage } from '@/views/dashboard/briefing'

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
export function useAttentionQuery(lastVisitSource: MaybeRefOrGetter<number | null>) {
  const lastVisit = computed(() => toValue(lastVisitSource))
  const investigation = useInvestigationStore()
  const { buildQs } = useProjectScope()

  return useQuery({
    queryKey: computed(() => ['issues', 'attention', investigation.scopeKey, lastVisit.value]),
    queryFn: ({ signal }) =>
      apiFetch<AttentionIssuesPage>(investigation.request(`/api/issues/attention?${buildQs({
        // 'All' has no bounds; the server caps windows at 90 days.
        hours: String(RANGE_HOURS[investigation.range] ?? 2160),
        ...(investigation.environment === 'All' ? {} : { env: investigation.environment }),
        ...(lastVisit.value === null ? {} : { last_visit: new Date(lastVisit.value).toISOString() }),
      })}`), { signal }),
  })
}

// What the navigation flags on every page: monitors failing now, and issues
// that regressed or are new in the window. It asks with the dashboard's last
// visit, so on the dashboard both read one cache entry.
export function useAttentionCounts() {
  const { uptime, cron } = useMonitorQueries()
  const attention = useAttentionQuery(lastVisitSeen)
  const monitors = computed(() => brokenMonitors(uptime.data.value ?? [], cron.data.value ?? []).length)
  const regressed = computed(() => attention.data.value?.regressed_total ?? 0)
  const issues = computed(() => regressed.value + (attention.data.value?.new_total ?? 0))
  return { monitors, issues, regressed }
}

// Badge text: counts above 99 collapse so the navigation keeps its width.
export function badgeCount(n: number): string {
  return n > 99 ? '99+' : String(n)
}
