import { describe, it, expect, beforeEach } from 'vitest'
import { SECTION_DEFAULTS, loadCollapsed, saveCollapsed } from '../collapsedSections'

const KEY = 'tindra:issue:collapsed'

describe('collapsedSections', () => {
  beforeEach(() => localStorage.removeItem(KEY))

  it('starts from the defaults, with only packages collapsed', () => {
    expect(loadCollapsed()).toEqual(SECTION_DEFAULTS)
    expect(Object.entries(loadCollapsed()).filter(([, v]) => v).map(([k]) => k)).toEqual(['packages'])
  })

  it('applies stored choices over the defaults', () => {
    localStorage.setItem(KEY, JSON.stringify({ tags: true, packages: false }))
    expect(loadCollapsed()).toEqual({ ...SECTION_DEFAULTS, tags: true, packages: false })
  })

  it('falls back to the defaults when the stored value is not JSON', () => {
    localStorage.setItem(KEY, '{not json')
    expect(loadCollapsed()).toEqual(SECTION_DEFAULTS)
  })

  it('stores the full map and keeps sections toggled earlier', () => {
    saveCollapsed('tags', true)
    saveCollapsed('request', true)
    expect(JSON.parse(localStorage.getItem(KEY)!)).toEqual({ ...SECTION_DEFAULTS, tags: true, request: true })
  })
})
