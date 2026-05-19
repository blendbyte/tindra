import { describe, it, expect } from 'vitest'
import cronstrue from 'cronstrue'

function humanSchedule(expr: string): string {
  try {
    return cronstrue.toString(expr, { verbose: false })
  } catch {
    return expr
  }
}

describe('humanSchedule', () => {
  it('renders every-N-minutes expressions', () => {
    expect(humanSchedule('*/2 * * * *')).toMatch(/every 2 minutes/i)
    expect(humanSchedule('*/15 * * * *')).toMatch(/every 15 minutes/i)
  })

  it('renders hourly', () => {
    expect(humanSchedule('0 * * * *')).toMatch(/every hour/i)
  })

  it('renders a specific time', () => {
    expect(humanSchedule('0 2 * * *')).toMatch(/2:00 AM/i)
  })

  it('renders a weekly schedule', () => {
    expect(humanSchedule('0 9 * * 1')).toMatch(/monday/i)
  })

  it('returns the raw expression for invalid input', () => {
    expect(humanSchedule('not-a-cron')).toBe('not-a-cron')
  })

  it('returns the raw expression for empty string', () => {
    expect(humanSchedule('')).toBe('')
  })
})
