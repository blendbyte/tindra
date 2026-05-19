import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { formatDuration, formatRate, formatPct, formatRel, formatTs } from '../formatters'

describe('formatDuration', () => {
  it('returns <1ms for values less than 1', () => {
    expect(formatDuration(0)).toBe('<1ms')
    expect(formatDuration(0.5)).toBe('<1ms')
    expect(formatDuration(0.999)).toBe('<1ms')
  })

  it('returns rounded ms for values between 1 and 999', () => {
    expect(formatDuration(1)).toBe('1ms')
    expect(formatDuration(100)).toBe('100ms')
    expect(formatDuration(999)).toBe('999ms')
    expect(formatDuration(1.4)).toBe('1ms')
    expect(formatDuration(1.6)).toBe('2ms')
  })

  it('returns seconds with 2 decimal places for 1000–9999ms', () => {
    expect(formatDuration(1000)).toBe('1.00s')
    expect(formatDuration(1500)).toBe('1.50s')
    expect(formatDuration(9999)).toBe('10.00s')
  })

  it('returns seconds with 1 decimal place for 10000ms and above', () => {
    expect(formatDuration(10000)).toBe('10.0s')
    expect(formatDuration(60000)).toBe('60.0s')
    expect(formatDuration(100000)).toBe('100.0s')
  })

  it('boundary: exactly 1ms', () => {
    expect(formatDuration(1)).toBe('1ms')
  })

  it('boundary: exactly 1000ms', () => {
    expect(formatDuration(1000)).toBe('1.00s')
  })

  it('boundary: exactly 10000ms', () => {
    expect(formatDuration(10000)).toBe('10.0s')
  })
})

describe('formatRate', () => {
  it('formats to one decimal place with /min suffix', () => {
    expect(formatRate(0)).toBe('0.0/min')
    expect(formatRate(1)).toBe('1.0/min')
    expect(formatRate(12.34)).toBe('12.3/min')
    expect(formatRate(100.99)).toBe('101.0/min')
  })

  it('handles negative values', () => {
    expect(formatRate(-5)).toBe('-5.0/min')
  })

  it('handles very large values', () => {
    expect(formatRate(9999.9)).toBe('9999.9/min')
  })
})

describe('formatPct', () => {
  it('formats to one decimal place with % suffix', () => {
    expect(formatPct(0)).toBe('0.0%')
    expect(formatPct(100)).toBe('100.0%')
    expect(formatPct(50.6)).toBe('50.6%')
    expect(formatPct(33.33)).toBe('33.3%')
  })

  it('handles negative values', () => {
    expect(formatPct(-1)).toBe('-1.0%')
  })
})

describe('formatRel', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  function isoSecondsAgo(s: number): string {
    return new Date(Date.now() - s * 1000).toISOString()
  }

  it('returns "just now" for timestamps less than 60s ago', () => {
    expect(formatRel(isoSecondsAgo(0))).toBe('just now')
    expect(formatRel(isoSecondsAgo(30))).toBe('just now')
    expect(formatRel(isoSecondsAgo(59))).toBe('just now')
  })

  it('returns minutes ago for timestamps 1–59m ago', () => {
    expect(formatRel(isoSecondsAgo(60))).toBe('1m ago')
    expect(formatRel(isoSecondsAgo(5 * 60))).toBe('5m ago')
    expect(formatRel(isoSecondsAgo(59 * 60))).toBe('59m ago')
  })

  it('returns hours ago for timestamps 1–23h ago', () => {
    expect(formatRel(isoSecondsAgo(60 * 60))).toBe('1h ago')
    expect(formatRel(isoSecondsAgo(12 * 60 * 60))).toBe('12h ago')
    expect(formatRel(isoSecondsAgo(23 * 60 * 60))).toBe('23h ago')
  })

  it('returns days ago for timestamps 1–29d ago', () => {
    expect(formatRel(isoSecondsAgo(24 * 60 * 60))).toBe('1d ago')
    expect(formatRel(isoSecondsAgo(7 * 24 * 60 * 60))).toBe('7d ago')
    expect(formatRel(isoSecondsAgo(29 * 24 * 60 * 60))).toBe('29d ago')
  })

  it('returns a locale date string for timestamps 30+ days ago', () => {
    const old = new Date(Date.now() - 30 * 24 * 60 * 60 * 1000).toISOString()
    const result = formatRel(old)
    expect(result).not.toMatch(/ago/)
    expect(result.length).toBeGreaterThan(0)
  })
})

describe('formatTs', () => {
  it('formats a timestamp as HH:MM:SS.mmm', () => {
    const d = new Date('2024-01-15T14:30:05.042Z')
    const result = formatTs(d.toISOString())
    expect(result).toMatch(/\d{2}:\d{2}:\d{2}\.\d{3}/)
    expect(result.endsWith('.042')).toBe(true)
  })

  it('pads milliseconds with leading zeros', () => {
    const d = new Date('2024-01-15T10:00:00.007Z')
    const result = formatTs(d.toISOString())
    expect(result.endsWith('.007')).toBe(true)
  })

  it('pads single-digit milliseconds correctly', () => {
    const d = new Date('2024-06-01T08:00:00.001Z')
    const result = formatTs(d.toISOString())
    expect(result.endsWith('.001')).toBe(true)
  })

  it('returns a string in the expected format for zero milliseconds', () => {
    const d = new Date('2024-03-10T12:00:00.000Z')
    const result = formatTs(d.toISOString())
    expect(result.endsWith('.000')).toBe(true)
  })
})
