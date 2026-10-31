import { describe, expect, it } from 'vitest'
import type { TransactionSummary } from '@/api/types'
import { failureClass, perfChanges, perfVerdict } from '../changes'

const tx = (transaction: string, overrides: Partial<TransactionSummary> = {}): TransactionSummary => ({
  transaction, op: 'http.server', project_id: 'p', sample_count: 50, tpm: 1, p50: 100, p95: 400, apdex: 0.9, failure_rate: 0.005, time_spent_ms: 0, ...overrides,
})

describe('perfChanges', () => {
  it('names transactions that got clearly slower or fail clearly more, failing first', () => {
    const previous = [tx('/a'), tx('/b'), tx('/c'), tx('/d'), tx('/e', { p95: 40 })]
    const current = [
      tx('/a', { p95: 900 }),                  // +125%: slower
      tx('/b', { p95: 450 }),                  // +12%: noise
      tx('/c', { failure_rate: 0.06 }),        // +5.5 points: failing
      tx('/d', { p95: 600 }),                  // +50%: slower, less than /a
      tx('/e', { p95: 120 }),                  // tripled, but only 80ms more
      tx('/new', { p95: 5000 }),               // nothing to compare with
    ]
    expect(perfChanges(current, previous).map(c => `${c.kind}:${c.summary.transaction}`))
      .toEqual(['failing:/c', 'slower:/a', 'slower:/d'])
  })

  it('ignores small samples and keys by transaction, operation and project', () => {
    const previous = [tx('/a', { project_id: 'other' }), tx('/b', { sample_count: 12 })]
    const current = [tx('/a', { p95: 2000 }), tx('/b', { p95: 2000 })]
    expect(perfChanges(current, previous)).toEqual([])
  })

  it('keeps at most three of each kind, so failing never hides slower', () => {
    const previous = Array.from({ length: 8 }, (_, i) => tx(`/${i}`))
    const current = previous.map((s, i) => i < 4 ? { ...s, failure_rate: 0.1 } : { ...s, p95: 2000 })
    expect(perfChanges(current, previous).map(c => c.kind)).toEqual(['failing', 'failing', 'failing', 'slower', 'slower', 'slower'])
  })
})

describe('perfVerdict', () => {
  it('leads with what fails more, then what got slower, and is calm when nothing changed', () => {
    const changes = perfChanges([tx('/a', { p95: 900 }), tx('/c', { failure_rate: 0.06 })], [tx('/a'), tx('/c')])
    expect(perfVerdict(changes, '24h')).toEqual({
      failing: '1 transaction is failing more than the previous 24h.',
      slower: '1 transaction got slower.',
      clear: '',
    })
    expect(perfVerdict([], '24h').clear).toBe('Nothing got slower or started failing more than the previous 24h.')
  })
})

describe('failureClass', () => {
  it('keeps red for frequent errors', () => {
    expect(failureClass(0.004)).toBe('')
    expect(failureClass(0.02)).toBe('tx-warn')
    expect(failureClass(0.05)).toBe('tx-failure')
  })
})
