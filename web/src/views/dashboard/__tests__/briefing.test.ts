import { describe, it, expect, beforeEach } from 'vitest'
import type { CronMonitor, UptimeMonitor } from '@/api/types'
import {
  brokenMonitors, brokenTitle, formatSpan, healthyMonitorCount, isUnseen, joinParts,
  sinceVisitParts, splitTitle, takeLastVisit, verdict,
} from '../briefing'

const NOW = Date.parse('2026-10-31T12:00:00Z')
const ago = (minutes: number) => new Date(NOW - minutes * 60_000).toISOString()

const uptime = (overrides: Partial<UptimeMonitor> = {}) => ({
  id: 'u', project_id: 'p', name: 'API', status: 'active', state: 'down', went_down_at: ago(10),
  last_ok_at: null, recent_checks: [], ...overrides,
}) as UptimeMonitor

const cron = (overrides: Partial<CronMonitor> = {}) => ({
  id: 'c', project_id: 'p', name: 'Backup', status: 'active', state: 'error', last_checkin_at: ago(5),
  next_expected_at: null, recent_checkins: [], ...overrides,
}) as CronMonitor

describe('brokenMonitors', () => {
  it('keeps active failing monitors, uptime first, longest outage first', () => {
    const result = brokenMonitors(
      [uptime({ id: 'recent', went_down_at: ago(5) }), uptime({ id: 'paused', status: 'paused' }), uptime({ id: 'up', state: 'up' }), uptime({ id: 'long', went_down_at: ago(90) })],
      [cron({ id: 'ok', state: 'ok' }), cron({ id: 'missed', state: 'missed', next_expected_at: ago(30) })],
    )
    expect(result.map(b => b.monitor.id)).toEqual(['long', 'recent', 'missed'])
  })

  it('dates a cron failure from the first error of the trailing streak', () => {
    const [b] = brokenMonitors([], [cron({
      recent_checkins: [
        { status: 'error', received_at: ago(120) },
        { status: 'ok', received_at: ago(60) },
        { status: 'error', received_at: ago(30) },
        { status: 'error', received_at: ago(15) },
      ],
    })])
    expect(b.since).toBe(ago(30))
    expect(b.history).toEqual(['bad', 'ok', 'bad', 'bad'])
  })

  it('treats a response that is not a list as no monitors', () => {
    expect(brokenMonitors({} as never, null as never)).toEqual([])
    expect(healthyMonitorCount({} as never, undefined)).toBe(0)
  })

  it('counts healthy monitors and ignores paused ones', () => {
    expect(healthyMonitorCount(
      [uptime({ state: 'up' }), uptime({ state: 'up', status: 'paused' })],
      [cron({ state: 'ok' }), cron({ state: 'in_progress' }), cron({ state: 'missed' })],
    )).toBe(3)
  })

  it('names what is wrong in each title', () => {
    expect(brokenTitle({ kind: 'uptime', monitor: uptime(), since: null, history: [] })).toBe('API is down')
    expect(brokenTitle({ kind: 'cron', monitor: cron({ state: 'missed' }), since: null, history: [] })).toBe('Backup missed its check-in')
    expect(brokenTitle({ kind: 'cron', monitor: cron(), since: null, history: [] })).toBe('Backup failed its last run')
  })
})

describe('formatting', () => {
  it('formats outage spans compactly', () => {
    expect(formatSpan(ago(0.5), NOW)).toBe('30s')
    expect(formatSpan(ago(12), NOW)).toBe('12m')
    expect(formatSpan(ago(182), NOW)).toBe('3h 2m')
    expect(formatSpan(ago(120), NOW)).toBe('2h')
    expect(formatSpan(ago(60 * 24 * 4), NOW)).toBe('4d')
    expect(formatSpan(null, NOW)).toBe('')
    expect(formatSpan(ago(-5), NOW)).toBe('')
  })

  it('splits exception types from messages only when there is a type', () => {
    expect(splitTitle('TypeError: Cannot read x')).toEqual({ type: 'TypeError', message: 'Cannot read x' })
    expect(splitTitle('App\\Exceptions\\PaymentException: declined')).toEqual({ type: 'App\\Exceptions\\PaymentException', message: 'declined' })
    expect(splitTitle('Payment failed: card declined')).toEqual({ type: '', message: 'Payment failed: card declined' })
    expect(splitTitle('no colon here')).toEqual({ type: '', message: 'no colon here' })
  })

  it('builds the verdict from broken and changed counts, keeping each count with its noun', () => {
    const nb = (text: string) => text.replace(/ /, '\u00a0')
    expect(verdict(0, 0)).toEqual({ broken: '', changed: '', clear: true })
    expect(verdict(1, 1)).toEqual({ broken: nb('1 thing is broken.'), changed: nb('1 issue is new or regressed.'), clear: false })
    expect(verdict(3, 0)).toEqual({ broken: nb('3 things are broken.'), changed: 'No new or regressed issues.', clear: false })
    expect(verdict(0, 4)).toEqual({ broken: '', changed: nb('4 issues are new or regressed.'), clear: false })
  })

  it('joins the since-visit summary like a sentence', () => {
    expect(joinParts(sinceVisitParts({ monitors: 1, regressed: 0, fresh: 0 }))).toBe('1 monitor started failing')
    expect(joinParts(sinceVisitParts({ monitors: 2, regressed: 1, fresh: 3 })))
      .toBe('2 monitors started failing, 1 issue regressed and 3 new issues appeared')
    expect(joinParts([])).toBe('')
  })
})

describe('last visit', () => {
  beforeEach(() => localStorage.clear())

  it('returns null on the first visit and records the current one', () => {
    expect(takeLastVisit(NOW)).toBeNull()
    expect(takeLastVisit(NOW + 1000)).toBe(NOW)
  })

  it('ignores stored values that are invalid or in the future', () => {
    localStorage.setItem('tindra:dashboard:last-visit', 'nonsense')
    expect(takeLastVisit(NOW)).toBeNull()
    localStorage.setItem('tindra:dashboard:last-visit', String(NOW + 60_000))
    expect(takeLastVisit(NOW)).toBeNull()
  })

  it('treats only onsets after the last visit as unseen', () => {
    expect(isUnseen(ago(5), NOW - 10 * 60_000)).toBe(true)
    expect(isUnseen(ago(15), NOW - 10 * 60_000)).toBe(false)
    expect(isUnseen(ago(5), null)).toBe(false)
    expect(isUnseen(null, NOW)).toBe(false)
  })
})
