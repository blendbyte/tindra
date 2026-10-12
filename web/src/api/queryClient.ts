import { MutationCache, QueryClient } from '@tanstack/vue-query'
import { useToast } from '@/composables/useToast'
import { ApiError } from './client'

// mutationErrorMessage turns a failed request into text a user can act on.
// Server errors are plain text for 4xx responses, so those are shown as is.
export function mutationErrorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 403) return 'You do not have permission to do that.'
    const text = err.message.trim()
    if (err.status < 500 && text && text.length <= 200) return text
    return 'Something went wrong on the server. Try again.'
  }
  if (err instanceof TypeError) return 'Could not reach the server. Check your connection and try again.'
  return 'Something went wrong. Try again.'
}

// Briefly reuse telemetry when navigating or refocusing. Polling and explicit
// invalidation still refresh it; identity and administrative queries stay fresh.
//
// Mutations without their own onError still report failures, so a click never
// fails silently.
export function createQueryClient() {
  const { show } = useToast()
  const client = new QueryClient({
    mutationCache: new MutationCache({
      onError: (err, _variables, _context, mutation) => {
        if (mutation.options.onError) return
        show(mutationErrorMessage(err), 'error')
      },
    }),
  })
  for (const key of [
    'issues', 'transactions', 'transaction-summaries', 'transaction-summaries-comp',
    'transaction-timeseries', 'transaction-profile-summaries',
    'transaction-profile-timeseries', 'transaction-profile-samples',
    'user-traces', 'user-pageloads', 'logs', 'trace-logs', 'span-samples',
    'web-vitals-summary', 'web-vitals-pages', 'releases', 'release-issues',
    'release-transactions', 'monitors', 'uptime-monitors', 'checkins',
    'uptime-checks', 'uptime-stats', 'dash-tx', 'dash-tx-counts',
    'span-db-summaries', 'span-db-timeseries', 'span-cache-summaries', 'span-cache-timeseries', 'span-jobs-summaries', 'span-jobs-timeseries',
  ]) {
    client.setQueryDefaults([key], { staleTime: 5_000, refetchOnWindowFocus: false, refetchOnReconnect: false })
  }
  client.setQueryDefaults(['projects'], { staleTime: 5_000 })
  client.setQueryDefaults(['projects', 'metadata'], { staleTime: 30_000 })
  client.setQueryDefaults(['releases', 'metadata'], { staleTime: 30_000 })
  return client
}
