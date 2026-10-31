import { describe, expect, it } from 'vitest'
import { issueVerdict } from '../issueVerdict'

const rel = (iso: string) => `<${iso.slice(11, 16)}>`
const date = (iso: string) => iso.slice(0, 10)
const base = {
  status: 'open' as const,
  first_seen: '2026-10-25T08:00:00Z',
  last_seen: '2026-10-31T11:40:00Z',
  first_release: '3.7.0',
  regressed_at: null,
  release: '3.8.0',
  ignore_until: null,
  ignore_count_limit: null,
  ignore_count: 0,
}
const opts = { newSince: '2026-10-30T12:00:00Z', owner: null, rel, date }
const sentence = (v: ReturnType<typeof issueVerdict>) => `${v.lead} ${v.rest}`

describe('issueVerdict', () => {
  it('leads a regression with when and where it came back, then the owner', () => {
    const v = issueVerdict({ ...base, status: 'regressed', regressed_at: '2026-10-31T10:30:00Z' }, { ...opts, owner: 'Jonas Becker' })
    expect(v.tone).toBe('regressed')
    expect(sentence(v)).toBe('Regressed <10:30> in 3.8.0 after it was resolved. Jonas Becker is on it.')
  })

  it('calls an open issue first seen in the window new, with its first release', () => {
    const v = issueVerdict({ ...base, first_seen: '2026-10-31T11:00:00Z' }, opts)
    expect(v.tone).toBe('new')
    expect(sentence(v)).toBe('New <11:00> in 3.7.0. Nobody is assigned yet.')
  })

  it('describes older open issues as ongoing', () => {
    const v = issueVerdict(base, { ...opts, owner: 'Aiko Tanaka' })
    expect(v.tone).toBe('quiet')
    expect(sentence(v)).toBe('Ongoing since <08:00> and last seen <11:40>. Aiko Tanaka is on it.')
    expect(issueVerdict(base, { ...opts, newSince: null }).tone).toBe('quiet')
  })

  it('says how long an issue stays ignored', () => {
    expect(sentence(issueVerdict({ ...base, status: 'ignored', ignore_until: '2026-11-03T00:00:00Z' }, opts))).toBe('Ignored until 2026-11-03.')
    expect(sentence(issueVerdict({ ...base, status: 'ignored', ignore_count_limit: 50, ignore_count: 49 }, opts))).toBe('Ignored for 1 more event.')
    expect(sentence(issueVerdict({ ...base, status: 'ignored' }, opts))).toBe('Ignored until you change it.')
  })

  it('keeps resolved issues short', () => {
    expect(sentence(issueVerdict({ ...base, status: 'resolved' }, opts))).toBe('Resolved and last seen <11:40>.')
  })
})
