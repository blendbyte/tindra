import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { formatDuration, formatRate, formatPct, formatFraction, formatRel, formatTs } from '../formatters'

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
  it('prints two decimals below 100 and whole numbers above', () => {
    expect(formatRate(1)).toBe('1.00/min')
    expect(formatRate(12.345)).toBe('12.35/min')
    expect(formatRate(100.99)).toBe('101/min')
    expect(formatRate(9999.9)).toBe('10000/min')
  })

  it('never prints a busy-looking zero for a quiet endpoint', () => {
    expect(formatRate(0)).toBe('0/min')
    expect(formatRate(0.004)).toBe('<0.01/min')
    expect(formatRate(0.01)).toBe('0.01/min')
  })

  it('handles negative values', () => {
    expect(formatRate(-5)).toBe('-5.00/min')
  })
})

describe('formatPct', () => {
  it('formats to one decimal place with % suffix', () => {
    expect(formatPct(50.6)).toBe('50.6%')
    expect(formatPct(33.33)).toBe('33.3%')
    expect(formatPct(5)).toBe('5.0%')
  })

  it('keeps zero and a full share short', () => {
    expect(formatPct(0)).toBe('0%')
    expect(formatPct(100)).toBe('100%')
    expect(formatPct(99.97)).toBe('100%')
  })

  it('marks shares too small for one decimal instead of rounding them to zero', () => {
    expect(formatPct(0.04)).toBe('<0.1%')
    expect(formatPct(0.05)).toBe('0.1%')
  })

  it('handles negative values', () => {
    expect(formatPct(-1)).toBe('-1.0%')
  })
})

describe('formatFraction', () => {
  it('prints a fraction as a percentage', () => {
    expect(formatFraction(0)).toBe('0%')
    expect(formatFraction(0.0634)).toBe('6.3%')
    expect(formatFraction(1)).toBe('100%')
    expect(formatFraction(0.0001)).toBe('<0.1%')
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

  it('reads future timestamps forward, such as a monitor\'s next check', () => {
    expect(formatRel(isoSecondsAgo(-30))).toBe('just now')
    expect(formatRel(isoSecondsAgo(-5 * 60))).toBe('in 5m')
    expect(formatRel(isoSecondsAgo(-3 * 60 * 60))).toBe('in 3h')
    expect(formatRel(isoSecondsAgo(-12 * 24 * 60 * 60))).toBe('in 12d')
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

  it('applies the given timezone when formatting', () => {
    // 2024-01-15T14:30:00.000Z = 06:30:00 in America/Los_Angeles (UTC-8 in January)
    const iso = '2024-01-15T14:30:00.000Z'
    const utcResult = formatTs(iso, 'UTC')
    const laResult = formatTs(iso, 'America/Los_Angeles')
    expect(utcResult.startsWith('14:30:00')).toBe(true)
    expect(laResult.startsWith('06:30:00')).toBe(true)
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
