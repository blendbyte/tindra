import { describe, expect, it } from 'vitest'
import { spanStatusLabel, spanStatusTone } from '../spanStatus'

describe('spanStatusLabel', () => {
  it('turns snake_case codes into words', () => {
    expect(spanStatusLabel('internal_error')).toBe('Internal error')
    expect(spanStatusLabel('deadline_exceeded')).toBe('Deadline exceeded')
    expect(spanStatusLabel('cancelled')).toBe('Cancelled')
  })

  it('writes ok as OK', () => {
    expect(spanStatusLabel('ok')).toBe('OK')
  })

  it('names a missing status', () => {
    expect(spanStatusLabel('')).toBe('Unknown')
  })
})

describe('spanStatusTone', () => {
  it('is ok only for ok', () => {
    expect(spanStatusTone('ok')).toBe('ok')
  })

  it('marks the statuses the backend counts as failures as errors', () => {
    for (const s of ['internal_error', 'unavailable', 'data_loss', 'unknown_error', 'deadline_exceeded']) {
      expect(spanStatusTone(s)).toBe('error')
    }
  })

  it('warns for other non-ok statuses', () => {
    expect(spanStatusTone('cancelled')).toBe('warn')
    expect(spanStatusTone('not_found')).toBe('warn')
    expect(spanStatusTone('')).toBe('warn')
  })
})
