import { ref, computed, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { usePerformanceStore } from '@/stores/performance'
import { apiFetch } from '@/api/client'
import type { SpanSummary, SpanTimeseries } from '@/api/types'

const WINDOW_MAP: Record<string, number> = { '1h': 1, '24h': 24, '7d': 168, '30d': 720 }

export function useSpanTable(config: {
  endpoint: string
  queryKeyPrefix: string
  nullableCols?: string[]
}) {
  const projects = useProjectsStore()
  const perf = usePerformanceStore()

  const hours = computed(() => WINDOW_MAP[perf.windowHrs] ?? 24)

  const spanParams = computed(() => {
    const p = new URLSearchParams()
    p.set('hours', String(hours.value))
    if (perf.envFilter !== 'All') p.set('env', perf.envFilter)
    for (const id of projects.selectedIds) p.append('project_id', id)
    return p.toString()
  })

  const { data: summaries, isLoading, isError, refetch } = useQuery({
    queryKey: computed(() => [`${config.queryKeyPrefix}-summaries`, spanParams.value]),
    queryFn: () => apiFetch<SpanSummary[]>(`/api/spans/${config.endpoint}?${spanParams.value}`),
  })

  const { data: timeseries } = useQuery({
    queryKey: computed(() => [`${config.queryKeyPrefix}-timeseries`, spanParams.value]),
    queryFn: () => apiFetch<SpanTimeseries>(`/api/spans/${config.endpoint}/timeseries?${spanParams.value}`),
  })

  const sortCol = ref('time_pct')
  const sortDir = ref<'asc' | 'desc'>('desc')

  function toggleSort(col: string) {
    if (sortCol.value === col) {
      sortDir.value = sortDir.value === 'desc' ? 'asc' : 'desc'
    } else {
      sortCol.value = col
      sortDir.value = col === 'description' || col === 'op' ? 'asc' : 'desc'
    }
  }

  function sortIcon(col: string) {
    if (sortCol.value !== col) return ''
    return sortDir.value === 'desc' ? '↓' : '↑'
  }

  const sorted = computed(() => {
    const list = [...(summaries.value ?? [])]
    const nullable = config.nullableCols ?? []
    list.sort((a, b) => {
      const col = sortCol.value
      if (col === 'description' || col === 'op') {
        const cmp = a[col as 'description' | 'op'].localeCompare(b[col as 'description' | 'op'])
        return sortDir.value === 'asc' ? cmp : -cmp
      }
      const key = col as keyof SpanSummary
      const av = nullable.includes(col) ? ((a[key] as number | null | undefined) ?? -1) : (a[key] as number)
      const bv = nullable.includes(col) ? ((b[key] as number | null | undefined) ?? -1) : (b[key] as number)
      const diff = av - bv
      return sortDir.value === 'desc' ? -diff : diff
    })
    return list
  })

  const search = ref('')
  watch(spanParams, () => { search.value = '' })

  const filtered = computed(() => {
    const q = search.value.trim().toLowerCase()
    return q
      ? sorted.value.filter(r => r.description.toLowerCase().includes(q) || r.op.toLowerCase().includes(q))
      : sorted.value
  })

  const noData = computed(() => !isLoading.value && !isError.value && (summaries.value ?? []).length === 0)

  const selectedRow = ref<SpanSummary | null>(null)

  return {
    perf,
    hours,
    summaries,
    timeseries,
    isLoading,
    isError,
    refetch,
    sortCol,
    sortDir,
    toggleSort,
    sortIcon,
    filtered,
    noData,
    search,
    selectedRow,
  }
}
