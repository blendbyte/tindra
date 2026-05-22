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

export function formatRel(iso: string, tz = 'UTC'): string {
  const ms = Date.now() - new Date(iso).getTime()
  const s = Math.floor(ms / 1000)
  if (s < 60) return 'just now'
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  const d = Math.floor(h / 24)
  if (d < 30) return `${d}d ago`
  return new Date(iso).toLocaleDateString(undefined, { timeZone: tz })
}

export function formatTs(iso: string, tz = 'UTC'): string {
  const d = new Date(iso)
  return (
    d.toLocaleTimeString('en-GB', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit', timeZone: tz }) +
    '.' + String(d.getMilliseconds()).padStart(3, '0')
  )
}
