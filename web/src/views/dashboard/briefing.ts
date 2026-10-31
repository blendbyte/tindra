import { ref } from 'vue'
import type { CronMonitor, UptimeMonitor } from '@/api/types'

// The dashboard briefing ranks what needs attention: monitors that are broken
// right now, then issues that regressed or first appeared in the window.

export interface AttentionIssue {
  id: string
  project_id: string
  title: string
  level: string
  status: string
  event_count: number
  user_count: number
  first_seen: string
  last_seen: string
  regressed_at: string | null
  first_release: string | null
  assignee: string | null
  sparkline?: number[]
}

export interface AttentionIssuesPage {
  regressed: AttentionIssue[]
  regressed_total: number
  new: AttentionIssue[]
  new_total: number
  unseen_regressed?: number
  unseen_new?: number
}

export type CheckState = 'ok' | 'bad' | 'pending'

export type BrokenMonitor =
  | { kind: 'uptime'; monitor: UptimeMonitor; since: string | null; history: CheckState[] }
  | { kind: 'cron'; monitor: CronMonitor; since: string | null; history: CheckState[] }

// Onset of a cron failure: the first error of the trailing error streak, or
// when a missed check-in was due.
export function cronSince(m: CronMonitor): string | null {
  if (m.state === 'missed') return m.next_expected_at ?? m.last_checkin_at
  let since: string | null = null
  const checkins = m.recent_checkins ?? []
  for (let i = checkins.length - 1; i >= 0; i--) {
    if (checkins[i].status !== 'error') break
    since = checkins[i].received_at
  }
  return since ?? m.last_checkin_at
}

// Active monitors that are failing now: uptime first, then cron, longest
// outage first within each kind. Paused monitors never count as broken.
// Inputs that are not lists (an error body, a stale cache entry) count as empty.
export function brokenMonitors(uptimeInput: UptimeMonitor[] = [], cronInput: CronMonitor[] = []): BrokenMonitor[] {
  const uptime = Array.isArray(uptimeInput) ? uptimeInput : []
  const cron = Array.isArray(cronInput) ? cronInput : []
  const byOnset = (a: { since: string | null }, b: { since: string | null }) =>
    (a.since ? Date.parse(a.since) : Infinity) - (b.since ? Date.parse(b.since) : Infinity)
  const down: BrokenMonitor[] = uptime
    .filter(m => m.status === 'active' && m.state === 'down')
    .map(m => ({
      kind: 'uptime' as const,
      monitor: m,
      since: m.went_down_at ?? m.last_ok_at,
      history: (m.recent_checks ?? []).map(c => (c.status === 'up' ? 'ok' : 'bad') as CheckState),
    }))
    .sort(byOnset)
  const failing: BrokenMonitor[] = cron
    .filter(m => m.status === 'active' && (m.state === 'missed' || m.state === 'error'))
    .map(m => ({
      kind: 'cron' as const,
      monitor: m,
      since: cronSince(m),
      history: (m.recent_checkins ?? []).map(c => (c.status === 'ok' ? 'ok' : c.status === 'error' ? 'bad' : 'pending') as CheckState),
    }))
    .sort(byOnset)
  return [...down, ...failing]
}

export function healthyMonitorCount(uptimeInput: UptimeMonitor[] = [], cronInput: CronMonitor[] = []): number {
  const uptime = Array.isArray(uptimeInput) ? uptimeInput : []
  const cron = Array.isArray(cronInput) ? cronInput : []
  return uptime.filter(m => m.status === 'active' && m.state === 'up').length +
    cron.filter(m => m.status === 'active' && (m.state === 'ok' || m.state === 'in_progress')).length
}

export function brokenTitle(b: BrokenMonitor): string {
  if (b.kind === 'uptime') return `${b.monitor.name} is down`
  return b.monitor.state === 'missed' ? `${b.monitor.name} missed its check-in` : `${b.monitor.name} failed its last run`
}

// Compact elapsed time for outages: "45s", "12m", "3h 2m", "4d".
export function formatSpan(since: string | null, now = Date.now()): string {
  if (!since) return ''
  const ms = now - Date.parse(since)
  if (!(ms >= 0)) return ''
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return m % 60 > 0 ? `${h}h ${m % 60}m` : `${h}h`
  return `${Math.floor(h / 24)}d`
}

// "TypeError: Cannot read…" splits into the exception type and the message.
// Titles without a type prefix, or with a colon inside a long sentence, stay whole.
export function splitTitle(title: string): { type: string; message: string } {
  const i = title.indexOf(':')
  if (i <= 0 || i > 80 || /\s/.test(title.slice(0, i))) return { type: '', message: title }
  return { type: title.slice(0, i), message: title.slice(i + 1).trim() }
}

export function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`
}

// Like plural, but keeps the count on the same line as its noun.
function pluralKept(n: number, one: string, many: string): string {
  return `${n}\u00a0${n === 1 ? one : many}`
}

// The briefing's verdict. `broken` leads in the danger colour; `changed`
// follows; `clear` stands alone when nothing needs attention.
export function verdict(broken: number, changed: number): { broken: string; changed: string; clear: boolean } {
  return {
    broken: broken > 0 ? `${pluralKept(broken, 'thing is', 'things are')} broken.` : '',
    changed: changed > 0
      ? `${pluralKept(changed, 'issue is', 'issues are')} new or regressed.`
      : broken > 0 ? 'No new or regressed issues.' : '',
    clear: broken === 0 && changed === 0,
  }
}

export function isUnseen(onset: string | null | undefined, lastVisit: number | null): boolean {
  return lastVisit !== null && !!onset && Date.parse(onset) > lastVisit
}

// What changed since the previous visit, as a sentence fragment list.
export function sinceVisitParts(counts: { monitors: number; regressed: number; fresh: number }): string[] {
  const parts: string[] = []
  if (counts.monitors) parts.push(`${plural(counts.monitors, 'monitor', 'monitors')} started failing`)
  if (counts.regressed) parts.push(`${plural(counts.regressed, 'issue', 'issues')} regressed`)
  if (counts.fresh) parts.push(`${plural(counts.fresh, 'new issue', 'new issues')} appeared`)
  return parts
}

export function joinParts(parts: string[]): string {
  if (parts.length <= 1) return parts[0] ?? ''
  return `${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}`
}

const LAST_VISIT_KEY = 'tindra:dashboard:last-visit'

function storedLastVisit(now: number): number | null {
  try {
    const raw = localStorage.getItem(LAST_VISIT_KEY)
    const parsed = raw === null ? NaN : Number(raw)
    return Number.isFinite(parsed) && parsed > 0 && parsed < now ? parsed : null
  } catch { return null }
}

// The last visit the open dashboard compares against. The navigation reads it
// too, so both ask the server the same question and share one cached answer.
export const lastVisitSeen = ref<number | null>(storedLastVisit(Date.now()))

// Reads when the dashboard was last opened in this browser and records now.
// Returns null on the first visit or when storage is unavailable.
export function takeLastVisit(now = Date.now()): number | null {
  const previous = storedLastVisit(now)
  try {
    localStorage.setItem(LAST_VISIT_KEY, String(now))
  } catch { /* storage unavailable */ }
  lastVisitSeen.value = previous
  return previous
}
