import type { TransactionSummary } from '@/api/types'

// What changed in the window compared with the window before it. Performance
// pages lead with this instead of a grid of coloured cells: transactions
// whose p95 rose clearly (slower) and transactions failing clearly more
// (failing). Small samples and small absolute moves are ignored, so the list
// names real changes, not noise.

export interface PerfChange {
  summary: TransactionSummary
  kind: 'slower' | 'failing'
  before: number
  after: number
}

// Both windows need this many requests before a change counts.
export const MIN_SAMPLES = 20
// p95 must rise by a quarter and by at least this much to count as slower.
export const SLOWER_RATIO = 1.25
export const SLOWER_MIN_MS = 100
// Error rate (a fraction, 0.02 = 2%) must reach this and rise by at least
// this much.
export const FAILING_MIN_RATE = 0.02
export const FAILING_MIN_RISE = 0.02

const key = (s: TransactionSummary) => `${s.transaction}|${s.op}|${s.project_id}`

// At most `perKind` of each kind, so one kind never hides the other.
export function perfChanges(current: TransactionSummary[], previous: TransactionSummary[], perKind = 3): PerfChange[] {
  const before = new Map(previous.map(s => [key(s), s]))
  const failing: PerfChange[] = []
  const slower: PerfChange[] = []
  for (const s of current) {
    const prev = before.get(key(s))
    if (!prev || s.sample_count < MIN_SAMPLES || prev.sample_count < MIN_SAMPLES) continue
    if (s.failure_rate >= FAILING_MIN_RATE && s.failure_rate - prev.failure_rate >= FAILING_MIN_RISE) {
      failing.push({ summary: s, kind: 'failing', before: prev.failure_rate, after: s.failure_rate })
    } else if (s.p95 >= prev.p95 * SLOWER_RATIO && s.p95 - prev.p95 >= SLOWER_MIN_MS) {
      slower.push({ summary: s, kind: 'slower', before: prev.p95, after: s.p95 })
    }
  }
  // Failing more leads (The Earned Red Rule); within a kind, the biggest move first.
  failing.sort((a, b) => (b.after - b.before) - (a.after - a.before))
  slower.sort((a, b) => b.after / b.before - a.after / a.before)
  return [...failing.slice(0, perKind), ...slower.slice(0, perKind)]
}

// One sentence over the list, failing clause first.
export function perfVerdict(changes: PerfChange[], windowLabel: string): { failing: string; slower: string; clear: string } {
  const failing = changes.filter(c => c.kind === 'failing').length
  const slower = changes.filter(c => c.kind === 'slower').length
  const n = (count: number, one: string, many: string) => `${count} ${count === 1 ? one : many}`
  return {
    failing: failing ? `${n(failing, 'transaction is', 'transactions are')} failing more than the previous ${windowLabel}.` : '',
    slower: slower ? `${n(slower, 'transaction got', 'transactions got')} slower.` : '',
    clear: failing || slower ? '' : `Nothing got slower or started failing more than the previous ${windowLabel}.`,
  }
}

// Error rate colour: red only once errors are frequent, amber when they show,
// quiet otherwise, so a list of small rates does not read as broken.
export function failureClass(rate: number): string {
  return rate >= 0.05 ? 'tx-failure' : rate >= 0.01 ? 'tx-warn' : ''
}
