export function formatDuration(ms: number): string {
  if (ms < 1) return '<1ms'
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(ms < 10000 ? 2 : 1)}s`
}

export function formatRate(r: number): string {
  return `${r.toFixed(1)}/min`
}

export function formatPct(v: number): string {
  return `${v.toFixed(1)}%`
}

// "5m ago" for the past and "in 5m" for the future (a monitor's next check).
// Within a minute either way it is "just now", which also absorbs clock skew.
export function formatRel(iso: string, tz = 'UTC'): string {
  const ms = Date.now() - new Date(iso).getTime()
  const s = Math.floor(Math.abs(ms) / 1000)
  if (s < 60) return 'just now'
  const rel = (n: number, unit: string) => ms < 0 ? `in ${n}${unit}` : `${n}${unit} ago`
  const m = Math.floor(s / 60)
  if (m < 60) return rel(m, 'm')
  const h = Math.floor(m / 60)
  if (h < 24) return rel(h, 'h')
  const d = Math.floor(h / 24)
  if (d < 30) return rel(d, 'd')
  return new Date(iso).toLocaleDateString(undefined, { timeZone: tz })
}

export function formatTs(iso: string, tz = 'UTC'): string {
  const d = new Date(iso)
  return (
    d.toLocaleTimeString('en-GB', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit', timeZone: tz }) +
    '.' + String(d.getMilliseconds()).padStart(3, '0')
  )
}
