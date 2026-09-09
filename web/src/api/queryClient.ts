import { QueryClient } from '@tanstack/vue-query'

// Briefly reuse telemetry when navigating or refocusing. Polling and explicit
// invalidation still refresh it; identity and administrative queries stay fresh.
export function createQueryClient() {
  const client = new QueryClient()
  for (const key of [
    'issues', 'transactions', 'transaction-summaries', 'transaction-summaries-comp',
    'transaction-timeseries', 'transaction-profile-summaries',
    'transaction-profile-timeseries', 'transaction-profile-samples',
    'user-traces', 'user-pageloads', 'logs', 'trace-logs', 'span-samples',
    'web-vitals-summary', 'web-vitals-pages', 'releases', 'release-issues',
    'release-transactions', 'monitors', 'uptime-monitors', 'checkins',
    'uptime-checks', 'uptime-stats', 'dash-tx', 'dash-tx-counts',
  ]) {
    client.setQueryDefaults([key], { staleTime: 5_000 })
  }
  client.setQueryDefaults(['projects'], { staleTime: 5_000 })
  client.setQueryDefaults(['projects', 'metadata'], { staleTime: 30_000 })
  client.setQueryDefaults(['releases', 'metadata'], { staleTime: 30_000 })
  return client
}
