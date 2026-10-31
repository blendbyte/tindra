import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { useQuery } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { useInvestigationStore } from '@/stores/investigation'
import { SHELL_REFRESH_MS, badgeCount, useAttentionCounts, useAttentionQuery, useMonitorQueries } from '../useBriefingData'

vi.mock('@tanstack/vue-query', () => ({ useQuery: vi.fn() }))
vi.mock('@/api/client', () => ({ apiFetch: vi.fn(async () => []) }))
vi.mock('@/views/dashboard/shared', () => ({
  useProjectScope: () => ({ buildQs: (extra: Record<string, string> = {}) => new URLSearchParams({ project_id: 'p1', ...extra }).toString() }),
}))

type Options = { queryKey: { value: unknown[] }; queryFn: (ctx: { signal: AbortSignal }) => Promise<unknown>; meta?: { shell?: boolean }; refetchInterval?: number }
const options = () => vi.mocked(useQuery).mock.calls.map(c => c[0] as unknown as Options)
const signal = new AbortController().signal

beforeEach(() => {
  setActivePinia(createPinia())
  vi.mocked(useQuery).mockReset()
  vi.mocked(useQuery).mockImplementation(() => ({ data: ref(undefined) }) as never)
  vi.mocked(apiFetch).mockClear()
})

describe('badgeCount', () => {
  it('shows counts up to 99 and collapses larger ones', () => {
    expect(badgeCount(1)).toBe('1')
    expect(badgeCount(99)).toBe('99')
    expect(badgeCount(100)).toBe('99+')
  })
})

describe('useMonitorQueries', () => {
  it('fetches both monitor lists for the project scope', async () => {
    useMonitorQueries()
    const [uptime, cron] = options()
    expect(uptime.meta).toBeUndefined()
    await uptime.queryFn({ signal })
    await cron.queryFn({ signal })
    expect(vi.mocked(apiFetch).mock.calls.map(c => c[0])).toEqual(['/api/uptime-monitors?project_id=p1', '/api/monitors?project_id=p1'])
  })

  it('marks shell queries and gives them their own refresh cadence', () => {
    useMonitorQueries({ shell: true })
    for (const o of options()) {
      expect(o.meta).toEqual({ shell: true })
      expect(o.refetchInterval).toBe(SHELL_REFRESH_MS)
    }
  })
})

describe('useAttentionQuery', () => {
  it('asks for the window, environment and last visit', async () => {
    const investigation = useInvestigationStore()
    investigation.environment = 'production'
    const visit = Date.UTC(2026, 9, 31, 8)
    useAttentionQuery(visit)
    const [attention] = options()
    expect(attention.queryKey.value).toContain(visit)
    await attention.queryFn({ signal })
    const url = String(vi.mocked(apiFetch).mock.calls[0][0])
    const params = new URLSearchParams(url.split('?')[1])
    expect(url).toContain('/api/issues/attention?')
    expect(params.get('env')).toBe('production')
    expect(params.get('last_visit')).toBe('2026-10-31T08:00:00.000Z')
    expect(params.get('hours')).toBeTruthy()
  })

  it('leaves out the environment for all environments and the visit on a first visit', async () => {
    useAttentionQuery(null)
    await options()[0].queryFn({ signal })
    const params = new URLSearchParams(String(vi.mocked(apiFetch).mock.calls[0][0]).split('?')[1])
    expect(params.has('env')).toBe(false)
    expect(params.has('last_visit')).toBe(false)
  })
})

describe('useAttentionCounts', () => {
  it('counts failing monitors and adds regressed and new issues', () => {
    vi.mocked(useQuery)
      .mockImplementationOnce(() => ({ data: ref([{ status: 'active', state: 'down' }, { status: 'active', state: 'up' }]) }) as never)
      .mockImplementationOnce(() => ({ data: ref([{ status: 'active', state: 'missed' }]) }) as never)
      .mockImplementationOnce(() => ({ data: ref({ regressed_total: 2, new_total: 3 }) }) as never)
    const counts = useAttentionCounts()
    expect(counts.monitors.value).toBe(2)
    expect(counts.regressed.value).toBe(2)
    expect(counts.issues.value).toBe(5)
    expect(options().every(o => o.meta?.shell)).toBe(true)
  })
})
