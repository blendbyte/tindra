import { describe, it, expect, vi, beforeEach } from 'vitest'
import { reactive } from 'vue'

vi.mock('@/stores/projects', () => ({
  useProjectsStore: vi.fn(),
}))

import { useProjectsStore } from '@/stores/projects'
import { useInvestigationStore } from '@/stores/investigation'
import { fmt, useProjectScope, useWindowLabel } from '../shared'

describe('fmt', () => {
  it('leaves small counts as they are', () => {
    expect(fmt(0)).toBe('0')
    expect(fmt(999)).toBe('999')
  })

  it('abbreviates thousands and millions to one decimal', () => {
    expect(fmt(1_000)).toBe('1.0k')
    expect(fmt(5_250)).toBe('5.3k')
    expect(fmt(1_500_000)).toBe('1.5M')
  })
})

describe('useProjectScope', () => {
  const store = reactive({ selectedIds: [] as string[] })

  beforeEach(() => {
    store.selectedIds = []
    vi.mocked(useProjectsStore).mockReturnValue(store as any)
  })

  it('builds an empty query string when no project is selected', () => {
    const { buildQs, pKey } = useProjectScope()
    expect(buildQs()).toBe('')
    expect(pKey.value).toBe('')
  })

  it('sorts selected projects and appends extra params', () => {
    store.selectedIds = ['p2', 'p1']
    const { buildQs, pKey } = useProjectScope()
    expect(buildQs()).toBe('project_id=p1&project_id=p2')
    expect(buildQs({ hours: '24', env: '' })).toBe('project_id=p1&project_id=p2&hours=24&env=')
    expect(pKey.value).toBe('p1,p2')
  })

  it('tracks selection changes in the cache key', () => {
    const { pKey } = useProjectScope()
    store.selectedIds = ['p3']
    expect(pKey.value).toBe('p3')
  })
})

describe('useWindowLabel', () => {
  it('shows the preset range, or "selected range" for absolute bounds', () => {
    const investigation = useInvestigationStore()
    const label = useWindowLabel()
    investigation.setRange('7d')
    expect(label.value).toBe('7d')
    investigation.absolute = { from: '2026-01-01T00:00:00Z', to: '2026-01-02T00:00:00Z' }
    expect(label.value).toBe('selected range')
  })
})
