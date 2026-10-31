import type { CronMonitor, UptimeMonitor } from '@/api/types'
import { cronSince, plural } from '@/views/dashboard/briefing'

// Monitor lists lead with what is failing (The Urgency Order Rule): failing
// first, longest outage first; then active monitors in their original order;
// paused monitors last.

export function uptimeFailing(m: UptimeMonitor): boolean {
  return m.status === 'active' && m.state === 'down'
}

export function cronFailing(m: CronMonitor): boolean {
  return m.status === 'active' && (m.state === 'missed' || m.state === 'error')
}

export function uptimeSince(m: UptimeMonitor): string | null {
  return uptimeFailing(m) ? m.went_down_at ?? m.last_ok_at : null
}

export function cronFailingSince(m: CronMonitor): string | null {
  return cronFailing(m) ? cronSince(m) : null
}

function rank<T>(items: T[], failing: (m: T) => boolean, paused: (m: T) => boolean, since: (m: T) => string | null): T[] {
  const onset = (m: T) => { const s = since(m); return s ? Date.parse(s) : Infinity }
  const group = (m: T) => failing(m) ? 0 : paused(m) ? 2 : 1
  return items
    .map((m, i) => ({ m, i }))
    .sort((a, b) => group(a.m) - group(b.m) || (group(a.m) === 0 ? onset(a.m) - onset(b.m) : 0) || a.i - b.i)
    .map(({ m }) => m)
}

export function rankUptime(items: UptimeMonitor[]): UptimeMonitor[] {
  return rank(items, uptimeFailing, m => m.status === 'paused', uptimeSince)
}

export function rankCron(items: CronMonitor[]): CronMonitor[] {
  return rank(items, cronFailing, m => m.status === 'paused', cronFailingSince)
}

// One sentence above the list: the failing clause leads, the rest is counts.
// Monitors without a result yet are counted apart, never as healthy.
export function monitorVerdict(
  items: { failing: boolean; paused: boolean; unknown?: boolean }[],
  noun: { one: string; many: string },
  failingVerb: { one: string; many: string },
  healthyWord: string,
): { failing: string; rest: string } {
  const failing = items.filter(i => i.failing).length
  const paused = items.filter(i => i.paused).length
  const unknown = items.filter(i => i.unknown && !i.failing && !i.paused).length
  const healthy = items.length - failing - paused - unknown
  const extras = [unknown ? `${unknown} not checked yet` : '', paused ? `${paused} paused` : ''].filter(Boolean)
  const sentence = (parts: string[]) => parts.length ? `${parts.join(', ').replace(/^./, c => c.toUpperCase())}.` : ''
  if (!failing) {
    if (!healthy) return { failing: '', rest: sentence(extras) }
    const all = healthy === 1 && items.length === 1 ? `The ${noun.one} is ${healthyWord}.`
      : extras.length ? `${plural(healthy, noun.one, noun.many)} ${healthyWord}.` : `All ${plural(healthy, noun.one, noun.many)} are ${healthyWord}.`
    return { failing: '', rest: extras.length ? `${all} ${sentence(extras)}` : all }
  }
  return {
    failing: `${plural(failing, noun.one, noun.many)} ${failing === 1 ? failingVerb.one : failingVerb.many}.`,
    rest: sentence([healthy ? `${healthy} ${healthyWord}` : '', ...extras].filter(Boolean)),
  }
}
