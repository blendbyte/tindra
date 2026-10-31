import { describe, it, expect } from 'vitest'
import { badgeCount } from '../useBriefingData'

describe('badgeCount', () => {
  it('shows counts up to 99 and collapses larger ones', () => {
    expect(badgeCount(1)).toBe('1')
    expect(badgeCount(99)).toBe('99')
    expect(badgeCount(100)).toBe('99+')
  })
})
