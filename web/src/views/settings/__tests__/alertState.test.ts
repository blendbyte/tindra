import { describe, expect, it } from 'vitest'
import { alertVerdict } from '../alertState'

const rules = (...enabled: boolean[]) => enabled.map(e => ({ enabled: e }))

describe('alertVerdict', () => {
  it('flags a setup where nothing can notify anyone', () => {
    expect(alertVerdict([])).toEqual({ text: 'No alert rules yet. Nothing will notify you.', silent: true })
    expect(alertVerdict(rules(false))).toEqual({ text: 'The only rule is paused. Nothing will notify you.', silent: true })
    expect(alertVerdict(rules(false, false, false))).toEqual({ text: 'All 3 rules are paused. Nothing will notify you.', silent: true })
  })

  it('counts rules that are on and paused', () => {
    expect(alertVerdict(rules(true))).toEqual({ text: 'The rule is on.', silent: false })
    expect(alertVerdict(rules(true, true))).toEqual({ text: 'All 2 rules are on.', silent: false })
    expect(alertVerdict(rules(true, false, false))).toEqual({ text: '1 of 3 rules on, 2 paused.', silent: false })
  })
})
