import { describe, expect, it } from 'vitest'
import { apdexTone, degradedVitals, enoughSamples, errorTone, failureRose, p95Rose, p95Tone, type ProjectPerformance } from '../overview'

const row = (overrides: Partial<ProjectPerformance> = {}): ProjectPerformance => ({
  requests: 100, p50: 100, p95: 300, apdex: 0.98, failure_rate: 0,
  prev_requests: 100, prev_p95: 300, prev_failure_rate: 0,
  ...overrides,
})

describe('tones', () => {
  it('reads error rate as a fraction', () => {
    expect(errorTone(0.02)).toBe('')
    expect(errorTone(0.021)).toBe('warning')
    expect(errorTone(0.051)).toBe('danger')
  })

  it('grades p95 and apdex', () => {
    expect([p95Tone(1000), p95Tone(1001), p95Tone(9000)]).toEqual(['', 'warning', 'warning'])
    expect([apdexTone(0.94), apdexTone(0.93), apdexTone(0.69)]).toEqual(['', 'warning', 'danger'])
  })
})

describe('enoughSamples', () => {
  it('needs twenty requests', () => {
    expect(enoughSamples({ requests: 19 })).toBe(false)
    expect(enoughSamples({ requests: 20 })).toBe(true)
  })
})

describe('changes against the previous window', () => {
  it('calls p95 risen only past both the ratio and the absolute bar', () => {
    expect(p95Rose(row({ p95: 400, prev_p95: 300 }))).toBe(true)
    expect(p95Rose(row({ p95: 360, prev_p95: 300 }))).toBe(false)
    // A quarter more, but only 20ms.
    expect(p95Rose(row({ p95: 100, prev_p95: 80 }))).toBe(false)
  })

  it('calls failures risen only once they are frequent and clearly up', () => {
    expect(failureRose(row({ failure_rate: 0.05, prev_failure_rate: 0.01 }))).toBe(true)
    expect(failureRose(row({ failure_rate: 0.015, prev_failure_rate: 0 }))).toBe(false)
    expect(failureRose(row({ failure_rate: 0.05, prev_failure_rate: 0.04 }))).toBe(false)
  })

  it('compares nothing when either window has too few requests', () => {
    expect(p95Rose(row({ p95: 900, requests: 5 }))).toBe(false)
    expect(failureRose(row({ failure_rate: 0.5, prev_requests: 5 }))).toBe(false)
  })
})

describe('degradedVitals', () => {
  it('names the vitals out of range', () => {
    const vitals = degradedVitals(row({ failure_rate: 0.068, apdex: 0.73 }))
    expect(vitals.map(v => [v.key, v.value, v.tone])).toEqual([
      ['error-rate', '6.8%', 'danger'],
      ['apdex', '0.73', 'warning'],
    ])
  })

  it('is empty when healthy, unknown or too few requests to judge', () => {
    expect(degradedVitals(row())).toEqual([])
    expect(degradedVitals(undefined)).toEqual([])
    expect(degradedVitals(row({ requests: 3, failure_rate: 0.5 }))).toEqual([])
  })
})
