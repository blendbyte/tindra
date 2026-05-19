import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { ref, nextTick } from 'vue'

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

import { useQuery } from '@tanstack/vue-query'
import type { Project } from '@/api/types'

function makeProject(id: string): Project {
  return { id, name: `Project ${id}`, public_key: id, slug: id } as Project
}

describe('projects store', () => {
  let projectsData: ReturnType<typeof ref<Project[]>>

  beforeEach(() => {
    sessionStorage.clear()
    projectsData = ref<Project[]>([])
    vi.mocked(useQuery).mockReturnValue({ data: projectsData } as any)
    setActivePinia(createPinia())
  })

  describe('toggleProject', () => {
    it('adds an id when not selected', async () => {
      const { useProjectsStore } = await import('../projects')
      const store = useProjectsStore()
      store.toggleProject('abc')
      expect(store.selectedIds).toContain('abc')
    })

    it('removes an id when already selected', async () => {
      const { useProjectsStore } = await import('../projects')
      const store = useProjectsStore()
      store.toggleProject('abc')
      store.toggleProject('abc')
      expect(store.selectedIds).not.toContain('abc')
    })

    it('handles multiple ids independently', async () => {
      const { useProjectsStore } = await import('../projects')
      const store = useProjectsStore()
      store.toggleProject('a')
      store.toggleProject('b')
      store.toggleProject('a')
      expect(store.selectedIds).toEqual(['b'])
    })
  })

  describe('setSelected', () => {
    it('replaces the selection', async () => {
      const { useProjectsStore } = await import('../projects')
      const store = useProjectsStore()
      store.toggleProject('old')
      store.setSelected(['x', 'y'])
      expect(store.selectedIds).toEqual(['x', 'y'])
    })
  })

  describe('sessionStorage persistence', () => {
    it('persists selected ids to sessionStorage', async () => {
      const { useProjectsStore } = await import('../projects')
      const store = useProjectsStore()
      store.setSelected(['p1', 'p2'])
      await nextTick()
      const stored = JSON.parse(sessionStorage.getItem('tindra:projectFilter') ?? '[]')
      expect(stored).toEqual(['p1', 'p2'])
    })

    it('hydrates from sessionStorage on init', async () => {
      sessionStorage.setItem('tindra:projectFilter', JSON.stringify(['p1', 'p2']))
      const { useProjectsStore } = await import('../projects')
      const store = useProjectsStore()
      expect(store.selectedIds).toEqual(['p1', 'p2'])
    })
  })

  describe('stale id cleanup', () => {
    it('drops selected ids that no longer exist in the project list', async () => {
      sessionStorage.setItem('tindra:projectFilter', JSON.stringify(['a', 'b', 'c']))
      const { useProjectsStore } = await import('../projects')
      const store = useProjectsStore()
      projectsData.value = [makeProject('a'), makeProject('b')]
      await nextTick()
      expect(store.selectedIds).toEqual(['a', 'b'])
    })

    it('does not modify selection when project list is empty', async () => {
      const { useProjectsStore } = await import('../projects')
      const store = useProjectsStore()
      store.setSelected(['a', 'b'])
      projectsData.value = []
      await nextTick()
      expect(store.selectedIds).toEqual(['a', 'b'])
    })
  })
})
