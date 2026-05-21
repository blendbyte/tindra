import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref, computed, reactive, nextTick } from 'vue'
import type { SpanSummary } from '@/api/types'

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

vi.mock('@/stores/projects', () => ({
  useProjectsStore: vi.fn(),
}))

vi.mock('@/stores/performance', () => ({
  usePerformanceStore: vi.fn(),
}))

import { useSpanTable } from '../useSpanTable'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { usePerformanceStore } from '@/stores/performance'

function makeSummary(overrides: Partial<SpanSummary> = {}): SpanSummary {
  return {
    op: 'db.query',
    description: 'SELECT * FROM users',
    sample_count: 100,
    rate: 5.0,
    p50: 10,
    p95: 50,
    total_ms: 1000,
    time_pct: 20,
    error_rate: 0,
    ...overrides,
  }
}

function setupMocks(summaries: SpanSummary[] = [], windowHrs = '24h', envFilter = 'All') {
  vi.mocked(usePerformanceStore).mockReturnValue({
    windowHrs,
    envFilter,
  } as any)

  vi.mocked(useProjectsStore).mockReturnValue({
    selectedIds: [],
  } as any)

  const summariesRef = ref<SpanSummary[]>(summaries)
  const isLoadingRef = ref(false)
  const isErrorRef = ref(false)
  const refetchFn = vi.fn()

  vi.mocked(useQuery)
    .mockReturnValueOnce({
      data: summariesRef,
      isLoading: isLoadingRef,
      isError: isErrorRef,
      refetch: refetchFn,
    } as any)
    .mockReturnValueOnce({
      data: ref(null),
    } as any)

  return { summariesRef, isLoadingRef, isErrorRef, refetchFn }
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('useSpanTable', () => {
  describe('initial state', () => {
    it('defaults sortCol to time_pct and sortDir to desc', () => {
      setupMocks()
      const { sortCol, sortDir } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(sortCol.value).toBe('time_pct')
      expect(sortDir.value).toBe('desc')
    })

    it('defaults search to empty string', () => {
      setupMocks()
      const { search } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(search.value).toBe('')
    })

    it('defaults selectedRow to null', () => {
      setupMocks()
      const { selectedRow } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(selectedRow.value).toBeNull()
    })
  })

  describe('hours computed', () => {
    it.each([
      ['1h', 1],
      ['24h', 24],
      ['7d', 168],
      ['30d', 720],
    ])('maps window %s to %i hours', (windowHrs, expectedHours) => {
      setupMocks([], windowHrs)
      const { hours } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(hours.value).toBe(expectedHours)
    })

    it('defaults to 24 for unknown window value', () => {
      setupMocks([], 'unknown')
      const { hours } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(hours.value).toBe(24)
    })
  })

  describe('toggleSort', () => {
    it('flips direction when clicking the same column', () => {
      setupMocks()
      const { sortCol, sortDir, toggleSort } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(sortDir.value).toBe('desc')
      toggleSort('time_pct')
      expect(sortDir.value).toBe('asc')
      toggleSort('time_pct')
      expect(sortDir.value).toBe('desc')
    })

    it('changes column and sets desc by default for numeric cols', () => {
      setupMocks()
      const { sortCol, sortDir, toggleSort } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      toggleSort('p50')
      expect(sortCol.value).toBe('p50')
      expect(sortDir.value).toBe('desc')
    })

    it('sets asc direction for description column', () => {
      setupMocks()
      const { sortCol, sortDir, toggleSort } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      toggleSort('description')
      expect(sortCol.value).toBe('description')
      expect(sortDir.value).toBe('asc')
    })

    it('sets asc direction for op column', () => {
      setupMocks()
      const { sortCol, sortDir, toggleSort } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      toggleSort('op')
      expect(sortCol.value).toBe('op')
      expect(sortDir.value).toBe('asc')
    })
  })

  describe('sortIcon', () => {
    it('returns empty string for non-active column', () => {
      setupMocks()
      const { sortIcon } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(sortIcon('p50')).toBe('')
    })

    it('returns ↓ for active column in desc order', () => {
      setupMocks()
      const { sortIcon } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(sortIcon('time_pct')).toBe('↓')
    })

    it('returns ↑ for active column in asc order', () => {
      setupMocks()
      const { sortIcon, toggleSort } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      toggleSort('time_pct')
      expect(sortIcon('time_pct')).toBe('↑')
    })
  })

  describe('sorted computed', () => {
    it('sorts numeric columns descending by default', () => {
      const data = [
        makeSummary({ time_pct: 10 }),
        makeSummary({ time_pct: 50 }),
        makeSummary({ time_pct: 25 }),
      ]
      setupMocks(data)
      const { filtered } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(filtered.value[0].time_pct).toBe(50)
      expect(filtered.value[1].time_pct).toBe(25)
      expect(filtered.value[2].time_pct).toBe(10)
    })

    it('sorts description column ascending', () => {
      const data = [
        makeSummary({ description: 'zebra' }),
        makeSummary({ description: 'apple' }),
        makeSummary({ description: 'mango' }),
      ]
      setupMocks(data)
      const { filtered, toggleSort } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      toggleSort('description')
      expect(filtered.value[0].description).toBe('apple')
      expect(filtered.value[1].description).toBe('mango')
      expect(filtered.value[2].description).toBe('zebra')
    })

    it('sorts nullable columns treating null as -1', () => {
      const data = [
        makeSummary({ miss_rate: null }),
        makeSummary({ miss_rate: 50 }),
        makeSummary({ miss_rate: 10 }),
      ]
      setupMocks(data)
      const { filtered, toggleSort } = useSpanTable({
        endpoint: 'db',
        queryKeyPrefix: 'db',
        nullableCols: ['miss_rate'],
      })
      toggleSort('miss_rate')
      expect(filtered.value[0].miss_rate).toBe(50)
      expect(filtered.value[1].miss_rate).toBe(10)
      expect(filtered.value[2].miss_rate).toBeNull()
    })

    it('returns empty array when summaries is undefined', () => {
      setupMocks([])
      const { filtered } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(filtered.value).toEqual([])
    })
  })

  describe('filtered computed', () => {
    it('returns all rows when search is empty', () => {
      const data = [
        makeSummary({ description: 'SELECT users' }),
        makeSummary({ description: 'INSERT orders' }),
      ]
      setupMocks(data)
      const { filtered } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(filtered.value).toHaveLength(2)
    })

    it('filters by description case-insensitively', () => {
      const data = [
        makeSummary({ description: 'SELECT users' }),
        makeSummary({ description: 'INSERT orders' }),
      ]
      setupMocks(data)
      const { filtered, search } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      search.value = 'users'
      expect(filtered.value).toHaveLength(1)
      expect(filtered.value[0].description).toBe('SELECT users')
    })

    it('filters by op case-insensitively', () => {
      const data = [
        makeSummary({ op: 'db.query', description: 'one' }),
        makeSummary({ op: 'http.request', description: 'two' }),
      ]
      setupMocks(data)
      const { filtered, search } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      search.value = 'HTTP'
      expect(filtered.value).toHaveLength(1)
      expect(filtered.value[0].op).toBe('http.request')
    })

    it('trims whitespace from search query', () => {
      const data = [
        makeSummary({ description: 'SELECT users' }),
        makeSummary({ description: 'INSERT orders' }),
      ]
      setupMocks(data)
      const { filtered, search } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      search.value = '  users  '
      expect(filtered.value).toHaveLength(1)
    })
  })

  describe('noData computed', () => {
    it('is true when not loading, no error, and summaries is empty', () => {
      setupMocks([])
      const { noData } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(noData.value).toBe(true)
    })

    it('is false when loading', () => {
      const { isLoadingRef } = setupMocks([])
      isLoadingRef.value = true
      const { noData } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(noData.value).toBe(false)
    })

    it('is false when there is an error', () => {
      const { isErrorRef } = setupMocks([])
      isErrorRef.value = true
      const { noData } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(noData.value).toBe(false)
    })

    it('is false when summaries has rows', () => {
      setupMocks([makeSummary()])
      const { noData } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(noData.value).toBe(false)
    })
  })

  describe('returned values', () => {
    it('exposes perf store reference', () => {
      setupMocks()
      const { perf } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(perf).toBeDefined()
    })

    it('exposes refetch function', () => {
      const { refetchFn } = setupMocks()
      const { refetch } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      expect(refetch).toBe(refetchFn)
    })
  })

  describe('sorted string desc', () => {
    it('sorts description column descending when direction toggled to desc', () => {
      const data = [
        makeSummary({ description: 'apple' }),
        makeSummary({ description: 'zebra' }),
        makeSummary({ description: 'mango' }),
      ]
      setupMocks(data)
      const { filtered, toggleSort } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      toggleSort('description') // sets asc
      toggleSort('description') // toggles to desc
      expect(filtered.value[0].description).toBe('zebra')
      expect(filtered.value[2].description).toBe('apple')
    })
  })

  describe('watch resets search when spanParams changes', () => {
    it('clears search value when env filter changes', async () => {
      const perfMock = reactive({ windowHrs: '24h', envFilter: 'All' })
      vi.mocked(usePerformanceStore).mockReturnValue(perfMock as any)
      vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: [] } as any)
      vi.mocked(useQuery)
        .mockReturnValueOnce({ data: ref([]), isLoading: ref(false), isError: ref(false), refetch: vi.fn() } as any)
        .mockReturnValueOnce({ data: ref(null) } as any)

      const { search } = useSpanTable({ endpoint: 'db', queryKeyPrefix: 'db' })
      search.value = 'my query'
      perfMock.envFilter = 'production'
      await nextTick()
      expect(search.value).toBe('')
    })
  })
})
