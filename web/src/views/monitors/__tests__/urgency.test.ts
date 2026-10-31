import { describe, it, expect } from 'vitest'
import type { CronMonitor, UptimeMonitor } from '@/api/types'
import { cronFailingSince, monitorVerdict, rankCron, rankUptime, uptimeSince } from '../urgency'

const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString()
const up = (id: string, overrides: Partial<UptimeMonitor> = {}) =>
  ({ id, status: 'active', state: 'up', went_down_at: null, last_ok_at: null, recent_checks: [], ...overrides }) as UptimeMonitor
const cron = (id: string, overrides: Partial<CronMonitor> = {}) =>
  ({ id, status: 'active', state: 'ok', last_checkin_at: null, next_expected_at: null, recent_checkins: [], ...overrides }) as CronMonitor

describe('monitor ranking', () => {
  it('puts down monitors first, longest outage first, and paused monitors last', () => {
    const ranked = rankUptime([
      up('paused', { status: 'paused', state: 'down' }),
      up('healthy-a'),
      up('down-recent', { state: 'down', went_down_at: ago(5) }),
      up('healthy-b'),
      up('down-long', { state: 'down', went_down_at: ago(90) }),
    ])
    expect(ranked.map(m => m.id)).toEqual(['down-long', 'down-recent', 'healthy-a', 'healthy-b', 'paused'])
  })

  it('treats missed and errored cron monitors as failing', () => {
    const ranked = rankCron([cron('ok'), cron('missed', { state: 'missed', next_expected_at: ago(10) }), cron('error', { state: 'error', last_checkin_at: ago(3) })])
    expect(ranked.map(m => m.id)).toEqual(['missed', 'error', 'ok'])
  })

  it('reports an onset only for failing monitors', () => {
    expect(uptimeSince(up('a', { state: 'down', went_down_at: ago(5) }))).not.toBeNull()
    expect(uptimeSince(up('a', { status: 'paused', state: 'down', went_down_at: ago(5) }))).toBeNull()
    expect(cronFailingSince(cron('a'))).toBeNull()
  })
})

describe('monitorVerdict', () => {
  const noun = { one: 'monitor', many: 'monitors' }
  const verb = { one: 'is down', many: 'are down' }
  const items = (failing: number, healthy: number, paused: number) => [
    ...Array(failing).fill({ failing: true, paused: false }),
    ...Array(healthy).fill({ failing: false, paused: false }),
    ...Array(paused).fill({ failing: false, paused: true }),
  ]

  it('leads with the failing clause and counts the rest', () => {
    expect(monitorVerdict(items(1, 5, 1), noun, verb, 'up')).toEqual({ failing: '1 monitor is down.', rest: '5 up, 1 paused.' })
    expect(monitorVerdict(items(2, 0, 0), noun, verb, 'up')).toEqual({ failing: '2 monitors are down.', rest: '' })
  })

  it('says all healthy when nothing is failing', () => {
    expect(monitorVerdict(items(0, 6, 0), noun, verb, 'up')).toEqual({ failing: '', rest: 'All 6 monitors are up.' })
    expect(monitorVerdict(items(0, 1, 0), noun, verb, 'up')).toEqual({ failing: '', rest: 'The monitor is up.' })
    expect(monitorVerdict(items(0, 1, 2), noun, verb, 'up')).toEqual({ failing: '', rest: '1 monitor up. 2 paused.' })
    expect(monitorVerdict(items(0, 0, 2), noun, verb, 'up')).toEqual({ failing: '', rest: '2 paused.' })
  })

  it('counts monitors without a result apart from healthy ones', () => {
    const list = [{ failing: true, paused: false }, { failing: false, paused: false }, { failing: false, paused: false, unknown: true }]
    expect(monitorVerdict(list, noun, verb, 'up')).toEqual({ failing: '1 monitor is down.', rest: '1 up, 1 not checked yet.' })
  })
})
