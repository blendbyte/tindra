import { ref, computed, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'
import { usePerformanceStore } from '@/stores/performance'
import { apiFetch } from '@/api/client'
import { useInvestigationStore } from '@/stores/investigation'
import type { SpanSummary, SpanTimeseries } from '@/api/types'
import { WINDOW_MAP } from '@/utils/time'

export function useSpanTable(config: {
  endpoint: string
  queryKeyPrefix: string
  nullableCols?: string[]
}) {
  const projects = useProjectsStore()
  const investigation = useInvestigationStore()
  const perf = usePerformanceStore()

  const hours = computed(() => WINDOW_MAP[perf.windowHrs] ?? 24)

  const spanParams = computed(() => {
    const p = new URLSearchParams()
    p.set('hours', String(hours.value))
    if (perf.envFilter !== 'All') p.set('env', perf.envFilter)
    for (const id of projects.selectedIds) p.append('project_id', id)
    return p.toString()
  })

  const { data: summaries, isLoading, isError: summariesError, refetch: refetchSummaries } = useQuery({
    queryKey: computed(() => [`${config.queryKeyPrefix}-summaries`, investigation.scopeKey, spanParams.value]),
    staleTime: 5_000,
    queryFn: ({ signal }) => apiFetch<SpanSummary[]>(investigation.request(`/api/spans/${config.endpoint}?${spanParams.value}`), { signal }),
  })

  const { data: timeseries, isError: timeseriesError, refetch: refetchTimeseries } = useQuery({
    queryKey: computed(() => [`${config.queryKeyPrefix}-timeseries`, investigation.scopeKey, spanParams.value]),
    staleTime: 5_000,
    queryFn: ({ signal }) => apiFetch<SpanTimeseries>(investigation.request(`/api/spans/${config.endpoint}/timeseries?${spanParams.value}`), { signal }),
  })

  const isError = computed(() => summariesError.value || timeseriesError.value)
  function refetch() { return Promise.all([refetchSummaries(), refetchTimeseries()]) }

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
  watch(() => investigation.scopeKey, () => { selectedRow.value = null })

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
