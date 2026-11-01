import { computed, toValue, type MaybeRefOrGetter } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { useInvestigationStore, RANGE_HOURS } from '@/stores/investigation'
import type { CronMonitor, UptimeMonitor } from '@/api/types'
import { useProjectScope } from '@/views/dashboard/shared'
import { brokenMonitors, lastVisitSeen, type AttentionIssuesPage } from '@/views/dashboard/briefing'

// Options for queries the app shell keeps open on every page. The page's
// freshness polling skips them (see isPageQuery), so they refresh on their own
// slower cadence instead of with every page refresh. They share cache entries
// with the dashboard and monitor pages, so they stop whenever the page's own
// polling does (paused, a fixed past range, browsing history); otherwise a
// paused page would still change under the person reading it.
export const SHELL_REFRESH_MS = 60_000
function useShellOptions(shell: boolean) {
  if (!shell) return {}
  const investigation = useInvestigationStore()
  const refetchInterval = computed(() =>
    investigation.paused || investigation.absolute || investigation.browsingHistory ? false : SHELL_REFRESH_MS)
  return { meta: { shell: true }, refetchInterval }
}

// Monitor queries behind the briefing. Widgets that call this share one cache
// entry per key, so the briefing and the project list never fetch twice.
export function useMonitorQueries({ shell = false } = {}) {
  const { buildQs } = useProjectScope()
  const shellOptions = useShellOptions(shell)

  const uptime = useQuery({
    queryKey: computed(() => ['uptime-monitors', buildQs()]),
    queryFn: ({ signal }) => apiFetch<UptimeMonitor[]>(`/api/uptime-monitors?${buildQs()}`, { signal }),
    ...shellOptions,
  })

  const cron = useQuery({
    queryKey: computed(() => ['monitors', buildQs()]),
    queryFn: ({ signal }) => apiFetch<CronMonitor[]>(`/api/monitors?${buildQs()}`, { signal }),
    ...shellOptions,
  })

  return { uptime, cron }
}

// Regressed issues and issues first seen in the window. With a last visit,
// the server also counts how many of each happened after it.
export function useAttentionQuery(lastVisitSource: MaybeRefOrGetter<number | null>, { shell = false } = {}) {
  const lastVisit = computed(() => toValue(lastVisitSource))
  const investigation = useInvestigationStore()
  const { buildQs } = useProjectScope()
  const shellOptions = useShellOptions(shell)

  return useQuery({
    queryKey: computed(() => ['issues', 'attention', investigation.scopeKey, lastVisit.value]),
    queryFn: ({ signal }) =>
      apiFetch<AttentionIssuesPage>(investigation.request(`/api/issues/attention?${buildQs({
        // 'All' has no bounds; the server caps windows at 90 days.
        hours: String(RANGE_HOURS[investigation.range] ?? 2160),
        ...(investigation.environment === 'All' ? {} : { env: investigation.environment }),
        ...(lastVisit.value === null ? {} : { last_visit: new Date(lastVisit.value).toISOString() }),
      })}`), { signal }),
    ...shellOptions,
  })
}

// What the navigation flags on every page: monitors failing now, and issues
// that regressed or are new in the window. It asks with the dashboard's last
// visit, so on the dashboard both read one cache entry.
export function useAttentionCounts() {
  const { uptime, cron } = useMonitorQueries({ shell: true })
  const attention = useAttentionQuery(lastVisitSeen, { shell: true })
  const monitors = computed(() => brokenMonitors(uptime.data.value ?? [], cron.data.value ?? []).length)
  const regressed = computed(() => attention.data.value?.regressed_total ?? 0)
  const issues = computed(() => regressed.value + (attention.data.value?.new_total ?? 0))
  return { monitors, issues, regressed }
}

// Badge text: counts above 99 collapse so the navigation keeps its width.
export function badgeCount(n: number): string {
  return n > 99 ? '99+' : String(n)
}
