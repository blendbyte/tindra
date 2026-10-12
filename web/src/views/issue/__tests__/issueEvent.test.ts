import { describe, it, expect } from 'vitest'
import type { Event as TindraEvent } from '@/api/types'
import { eventHttpRequest, formatRequestBody, unwrapHeaderVal, parseUAContexts, eventContexts } from '../issueEvent'

function event(payload: Record<string, unknown>): TindraEvent {
  return { id: 'evt-1', payload } as TindraEvent
}

describe('eventHttpRequest', () => {
  it('returns null without a request or event', () => {
    expect(eventHttpRequest(undefined)).toBeNull()
    expect(eventHttpRequest(null)).toBeNull()
    expect(eventHttpRequest(event({}))).toBeNull()
  })

  it('picks the request fields', () => {
    const req = eventHttpRequest(event({ request: { method: 'POST', url: '/x', data: '{}', headers: { A: 'b' }, cookies: { c: 'd' } } }))
    expect(req).toMatchObject({ method: 'POST', url: '/x', data: '{}', headers: { A: 'b' }, cookies: { c: 'd' } })
  })
})

describe('formatRequestBody', () => {
  it('pretty-prints JSON strings and objects', () => {
    expect(formatRequestBody('{"a":1}')).toBe('{\n  "a": 1\n}')
    expect(formatRequestBody({ a: 1 })).toBe('{\n  "a": 1\n}')
  })

  it('keeps other strings and drops empty bodies', () => {
    expect(formatRequestBody('a=1&b=2')).toBe('a=1&b=2')
    expect(formatRequestBody(null)).toBe('')
    expect(formatRequestBody(undefined)).toBe('')
  })
})

describe('unwrapHeaderVal', () => {
  it('joins Sentry header arrays and stringifies the rest', () => {
    expect(unwrapHeaderVal(['a', 'b'])).toBe('a, b')
    expect(unwrapHeaderVal(5)).toBe('5')
    expect(unwrapHeaderVal(undefined)).toBe('')
  })
})

describe('parseUAContexts', () => {
  it('detects Mobile Safari from a real iPhone user agent', () => {
    const ua = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1'
    expect(parseUAContexts(ua).browser.rows).toEqual([['name', 'Mobile Safari'], ['version', '17.0']])
  })

  it('recognises Edge before Chrome', () => {
    const ua = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36 Edg/120.0.2210.91'
    const out = parseUAContexts(ua)
    expect(out.browser.rows).toEqual([['name', 'Edge'], ['version', '120.0.2210.91']])
    expect(out.os.rows).toEqual([['name', 'Windows'], ['version', '10.0']])
  })

  it('recognises iOS', () => {
    const ua = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_1 like Mac OS X) AppleWebKit/605.1.15 Version/17.1 Mobile/15E148 Safari/604.1'
    expect(parseUAContexts(ua).os.rows).toEqual([['name', 'iOS'], ['version', '17.1']])
  })

  it('recognises Safari on macOS', () => {
    const ua = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Safari/605.1.15'
    const out = parseUAContexts(ua)
    expect(out.browser.rows).toEqual([['name', 'Safari'], ['version', '17.1']])
    expect(out.os.rows).toEqual([['name', 'macOS'], ['version', '10.15']])
  })

  it('reads the Android device model', () => {
    const out = parseUAContexts('Mozilla/5.0 (Linux; Android 13; Pixel 7 Build) AppleWebKit/537.36 Chrome/120.0.0.0 Mobile Safari/537.36')
    expect(out.device.rows).toEqual([['model', 'Pixel 7']])
    expect(out.browser.rows[0]).toEqual(['name', 'Chrome Mobile'])
  })

  it('returns nothing for an unknown agent', () => {
    expect(parseUAContexts('curl/8.0')).toEqual({})
  })
})

describe('eventContexts', () => {
  it('skips the trace context and meta keys', () => {
    const cards = eventContexts(event({ contexts: { trace: { trace_id: 't' }, os: { name: 'iOS', type: 'os', raw_description: 'x' } } }))
    expect(cards).toEqual([{ key: 'os', label: 'Operating System', icon: 'activity', rows: [['name', 'iOS']] }])
  })

  it('labels unknown contexts by their key', () => {
    const cards = eventContexts(event({ contexts: { cloud_resource: { region: 'eu' } } }))
    expect(cards[0]).toMatchObject({ label: 'Cloud_resource', icon: 'info' })
  })

  it('adds User-Agent cards only for contexts the payload lacks', () => {
    const cards = eventContexts(event({
      contexts: { browser: { name: 'Firefox' } },
      request: { headers: { 'user-agent': ['Mozilla/5.0 (Windows NT 10.0) Chrome/120.0.0.0 Safari/537.36'] } },
    }))
    expect(cards.map(c => c.key)).toEqual(['os', 'browser'])
    expect(cards[1].rows).toEqual([['name', 'Firefox']])
  })
})
