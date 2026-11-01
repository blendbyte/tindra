// Sentry span and transaction statuses arrive as snake_case codes
// ("internal_error", "deadline_exceeded"). CSS capitalising alone printed
// "Internal_error", so the label is built here.

// The statuses the backend counts as failures in every failure rate
// (internal/storage/transactions.go and friends). Keep the two lists in step.
const FAILED = new Set(['internal_error', 'unavailable', 'data_loss', 'unknown_error', 'deadline_exceeded'])

export function spanStatusLabel(status: string): string {
  if (!status) return 'Unknown'
  if (status === 'ok') return 'OK'
  const words = status.replace(/_/g, ' ')
  return words.charAt(0).toUpperCase() + words.slice(1)
}

// "ok" for success, "error" for a status that counts as a failure, "warn" for
// any other non-ok status (cancelled, not_found, permission_denied, ...).
export function spanStatusTone(status: string): 'ok' | 'error' | 'warn' {
  if (status === 'ok') return 'ok'
  return FAILED.has(status) ? 'error' : 'warn'
}
