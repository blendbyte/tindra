import type { Event as TindraEvent } from '@/api/types'

// Helpers that read the payload of an issue event. The HTTP request and the
// context sections share them, because the context cards fall back to the
// browser and OS named in the request's User-Agent header.

export interface HttpRequest {
  method: string | undefined
  url: string | undefined
  headers: Record<string, unknown> | undefined
  data: unknown
  query_string: unknown
  cookies: Record<string, unknown> | undefined
  env: Record<string, string> | undefined
}

export interface ContextCard {
  key: string
  label: string
  icon: string
  rows: [string, string][]
}

export function eventHttpRequest(event: TindraEvent | null | undefined): HttpRequest | null {
  const req = event?.payload?.request as Record<string, unknown> | undefined
  if (!req || typeof req !== 'object') return null
  return {
    method: req.method as string | undefined,
    url: req.url as string | undefined,
    headers: req.headers as Record<string, unknown> | undefined,
    data: req.data,
    query_string: req.query_string,
    cookies: req.cookies as Record<string, unknown> | undefined,
    env: req.env as Record<string, string> | undefined,
  }
}

export function formatRequestBody(data: unknown): string {
  if (data === null || data === undefined) return ''
  if (typeof data === 'string') {
    try { return JSON.stringify(JSON.parse(data), null, 2) } catch { return data }
  }
  return JSON.stringify(data, null, 2)
}

// Sentry sends header values as ["value"] arrays; unwrap to plain string.
export function unwrapHeaderVal(v: unknown): string {
  if (Array.isArray(v)) return v.map(String).join(', ')
  return String(v ?? '')
}

const CONTEXT_LABELS: Record<string, string> = {
  runtime: 'Runtime',
  os: 'Operating System',
  browser: 'Browser',
  device: 'Device',
  app: 'App',
  gpu: 'GPU',
  culture: 'Culture',
}

const CONTEXT_ICONS: Record<string, string> = {
  browser: 'globe',
  device: 'squares',
  os: 'activity',
  runtime: 'zap',
  app: 'package',
  gpu: 'activity',
}

const CTX_SKIP_KEYS = new Set(['type', 'raw_description'])

function payloadContexts(event: TindraEvent | null | undefined): ContextCard[] {
  const raw = event?.payload?.contexts as Record<string, unknown> | undefined
  if (!raw || typeof raw !== 'object') return []

  return Object.entries(raw)
    .filter(([key, val]) => key !== 'trace' && val !== null && typeof val === 'object' && !Array.isArray(val))
    .map(([key, val]) => {
      const obj = val as Record<string, unknown>
      const rows: [string, string][] = Object.entries(obj)
        .filter(([k, v]) => !CTX_SKIP_KEYS.has(k) && v !== null && v !== undefined && typeof v !== 'object')
        .map(([k, v]) => [k.replace(/_/g, ' '), String(v)])
      return {
        key,
        label: CONTEXT_LABELS[key] ?? key.charAt(0).toUpperCase() + key.slice(1),
        icon: CONTEXT_ICONS[key] ?? 'info',
        rows,
      }
    })
    .filter((c) => c.rows.length > 0)
}

// Derive browser / OS / device from User-Agent when not in payload contexts.
export function parseUAContexts(ua: string): Record<string, Omit<ContextCard, 'key'>> {
  const out: Record<string, Omit<ContextCard, 'key'>> = {}

  // Browser
  let bName = '', bVersion = ''
  const edgeM = ua.match(/Edg(?:e|A|iOS)?\/(\S+)/)
  const chromeM = ua.match(/(?:Chrome|CrMo|CriOS)\/(\d+(?:\.\d+)*)/)
  const ffM = ua.match(/Firefox\/(\d+(?:\.\d+)*)/)
  // iOS puts a build token between them: "Version/17.0 Mobile/15E148 Safari/604.1".
  const safM = ua.match(/Version\/(\S+)\s+(?:Mobile(?:\/\S+)?\s+)?Safari/)
  if (edgeM) { bName = 'Edge'; bVersion = edgeM[1] }
  else if (chromeM) { bName = ua.includes('Mobile') ? 'Chrome Mobile' : 'Chrome'; bVersion = chromeM[1] }
  else if (ffM) { bName = ua.includes('Mobile') ? 'Firefox Mobile' : 'Firefox'; bVersion = ffM[1] }
  else if (safM) { bName = ua.includes('Mobile') ? 'Mobile Safari' : 'Safari'; bVersion = safM[1] }
  if (bName) {
    const rows: [string, string][] = [['name', bName]]
    if (bVersion) rows.push(['version', bVersion])
    out.browser = { label: 'Browser', icon: 'globe', rows }
  }

  // OS
  let osName = '', osVersion = ''
  const androidM = ua.match(/Android (\d+(?:[._]\d+)*)/)
  const iosM = ua.match(/(?:iPhone|iPad)[^;]*; CPU (?:iPhone )?OS (\d+_\d+)/)
  const winM = ua.match(/Windows NT (\d+\.\d+)/)
  const macM = ua.match(/Mac OS X (\d+[._]\d+)/)
  if (androidM) { osName = 'Android'; osVersion = androidM[1] }
  else if (iosM) { osName = ua.includes('iPad') ? 'iPadOS' : 'iOS'; osVersion = iosM[1].replace(/_/g, '.') }
  else if (winM) { osName = 'Windows'; osVersion = winM[1] }
  else if (macM) { osName = 'macOS'; osVersion = macM[1].replace(/[_]/g, '.') }
  else if (/Linux/.test(ua) && !androidM) osName = 'Linux'
  if (osName) {
    const rows: [string, string][] = [['name', osName]]
    if (osVersion) rows.push(['version', osVersion])
    out.os = { label: 'Operating System', icon: 'activity', rows }
  }

  // Device (Android model from UA)
  const devM = ua.match(/Android \d+(?:[._]\d+)*;\s*([^)]+)\)/)
  if (devM) {
    const model = devM[1].trim().replace(/\s+Build$/, '')
    if (model && model !== 'wv') {
      out.device = { label: 'Device', icon: 'squares', rows: [['model', model]] }
    }
  }

  return out
}

// eventContexts lists the context cards of an event: the payload's contexts,
// preceded by browser, device and OS cards parsed from the User-Agent header
// for any of those the payload does not carry itself.
export function eventContexts(event: TindraEvent | null | undefined): ContextCard[] {
  const list = payloadContexts(event)
  const existingKeys = new Set(list.map(c => c.key))

  const headers = eventHttpRequest(event)?.headers
  if (headers) {
    const uaEntry = Object.entries(headers).find(([k]) => k.toLowerCase() === 'user-agent')
    const ua = uaEntry ? unwrapHeaderVal(uaEntry[1]) : ''
    if (ua) {
      const derived = parseUAContexts(ua)
      for (const key of ['browser', 'device', 'os'] as const) {
        if (!existingKeys.has(key) && derived[key]) {
          list.unshift({ key, ...derived[key] })
        }
      }
    }
  }
  return list
}
