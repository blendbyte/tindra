import { describe, it, expect, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useIssueNavStore } from '../issueNav'

describe('issueNav store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('starts with empty ids', () => {
    const store = useIssueNavStore()
    expect(store.ids).toEqual([])
  })

  describe('set()', () => {
    it('replaces ids', () => {
      const store = useIssueNavStore()
      store.set(['a', 'b', 'c'])
      expect(store.ids).toEqual(['a', 'b', 'c'])
    })

    it('clears ids when given empty array', () => {
      const store = useIssueNavStore()
      store.set(['a', 'b'])
      store.set([])
      expect(store.ids).toEqual([])
    })

    it('replaces previous list entirely', () => {
      const store = useIssueNavStore()
      store.set(['a', 'b'])
      store.set(['x', 'y', 'z'])
      expect(store.ids).toEqual(['x', 'y', 'z'])
    })
  })

  describe('prevId()', () => {
    it('returns null when ids is empty', () => {
      const store = useIssueNavStore()
      expect(store.prevId('a')).toBeNull()
    })

    it('returns null for an unknown id', () => {
      const store = useIssueNavStore()
      store.set(['a', 'b', 'c'])
      expect(store.prevId('x')).toBeNull()
    })

    it('returns null for the first id', () => {
      const store = useIssueNavStore()
      store.set(['a', 'b', 'c'])
      expect(store.prevId('a')).toBeNull()
    })

    it('returns the previous id for a middle element', () => {
      const store = useIssueNavStore()
      store.set(['a', 'b', 'c'])
      expect(store.prevId('b')).toBe('a')
    })

    it('returns the previous id for the last element', () => {
      const store = useIssueNavStore()
      store.set(['a', 'b', 'c'])
      expect(store.prevId('c')).toBe('b')
    })
  })

  describe('nextId()', () => {
    it('returns null when ids is empty', () => {
      const store = useIssueNavStore()
      expect(store.nextId('a')).toBeNull()
    })

    it('returns null for an unknown id', () => {
      const store = useIssueNavStore()
      store.set(['a', 'b', 'c'])
      expect(store.nextId('x')).toBeNull()
    })

    it('returns null for the last id', () => {
      const store = useIssueNavStore()
      store.set(['a', 'b', 'c'])
      expect(store.nextId('c')).toBeNull()
    })

    it('returns the next id for the first element', () => {
      const store = useIssueNavStore()
      store.set(['a', 'b', 'c'])
      expect(store.nextId('a')).toBe('b')
    })

    it('returns the next id for a middle element', () => {
      const store = useIssueNavStore()
      store.set(['a', 'b', 'c'])
      expect(store.nextId('b')).toBe('c')
    })
  })

  it('prevId and nextId work together for sequential navigation', () => {
    const store = useIssueNavStore()
    store.set(['first', 'second', 'third'])
    expect(store.prevId('second')).toBe('first')
    expect(store.nextId('second')).toBe('third')
  })
})
