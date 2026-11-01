export function formatDuration(ms: number): string {
  if (ms < 1) return '<1ms'
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(ms < 10000 ? 2 : 1)}s`
}

// Throughput per minute. Every performance table prints rates this way, so a
// quiet endpoint reads "<0.01/min" everywhere instead of "0.0/min" on one page
// and "<0.001/min" on another.
export function formatRate(r: number): string {
  if (r === 0) return '0/min'
  if (r > 0 && r < 0.01) return '<0.01/min'
  if (Math.abs(r) >= 100) return `${Math.round(r)}/min`
  return `${r.toFixed(2)}/min`
}

// A percentage given in percent (12.5 means 12.5%). Zero stays "0%", a share
// too small for one decimal reads "<0.1%" rather than a misleading "0.0%", and
// a full share is "100%", not "100.0%".
export function formatPct(v: number): string {
  if (v === 0) return '0%'
  if (v > 0 && v < 0.05) return '<0.1%'
  const fixed = v.toFixed(1)
  return fixed === '100.0' ? '100%' : `${fixed}%`
}

// A rate given as a fraction (0.125 means 12.5%), such as a transaction's
// failure_rate.
export function formatFraction(rate: number): string {
  return formatPct(rate * 100)
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
