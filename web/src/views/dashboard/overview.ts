import { formatDuration, formatFraction } from '@/utils/formatters'
import { FAILING_MIN_RATE, FAILING_MIN_RISE, MIN_SAMPLES, SLOWER_MIN_MS, SLOWER_RATIO } from '@/views/performance/changes'

// The performance side of the dashboard: one rollup per project, and their
// total, for the window and the window before it. Rates are fractions.

export interface ProjectPerformance {
  project_id?: string
  requests: number
  p50: number
  p95: number
  apdex: number
  failure_rate: number
  prev_requests: number
  prev_p95: number
  prev_failure_rate: number
  trend?: number[]
}

export interface PerformanceOverview {
  projects: ProjectPerformance[]
  total: ProjectPerformance
}

export type Tone = 'danger' | 'warning' | ''

export const errorTone = (rate: number): Tone => rate > 0.05 ? 'danger' : rate > 0.02 ? 'warning' : ''
// Slow is degraded, not broken, so latency is amber at most (The Earned Red Rule).
export const p95Tone = (ms: number): Tone => ms > 1000 ? 'warning' : ''
export const apdexTone = (apdex: number): Tone => apdex < 0.7 ? 'danger' : apdex < 0.94 ? 'warning' : ''

// A handful of requests says nothing about health, so one failure in three
// requests does not turn a quiet project red.
export function enoughSamples(row: Pick<ProjectPerformance, 'requests'>): boolean {
  return row.requests >= MIN_SAMPLES
}

function comparable(row: ProjectPerformance): boolean {
  return row.requests >= MIN_SAMPLES && row.prev_requests >= MIN_SAMPLES
}

// The same bars the transactions page uses to call something slower or failing.
export function p95Rose(row: ProjectPerformance): boolean {
  return comparable(row) && row.p95 >= row.prev_p95 * SLOWER_RATIO && row.p95 - row.prev_p95 >= SLOWER_MIN_MS
}

export function failureRose(row: ProjectPerformance): boolean {
  return comparable(row) && row.failure_rate >= FAILING_MIN_RATE && row.failure_rate - row.prev_failure_rate >= FAILING_MIN_RISE
}

export interface DegradedVital {
  key: string
  label: string
  value: string
  tone: Tone
}

// Vitals outside their healthy range. "All clear" cannot be said beside them.
export function degradedVitals(total?: ProjectPerformance): DegradedVital[] {
  if (!total || !enoughSamples(total)) return []
  const vitals: DegradedVital[] = [
    { key: 'error-rate', label: 'error rate', value: formatFraction(total.failure_rate), tone: errorTone(total.failure_rate) },
    { key: 'p95', label: 'p95', value: formatDuration(total.p95), tone: p95Tone(total.p95) },
    { key: 'apdex', label: 'apdex', value: total.apdex.toFixed(2), tone: apdexTone(total.apdex) },
  ]
  return vitals.filter(v => v.tone)
}
