import { describe, it, expect } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import { usePerformanceStore } from '../performance'
import { useInvestigationStore } from '../investigation'

describe('shared performance context', () => {
  it('defaults to 24h and all environments', () => {
    const state = usePerformanceStore()
    expect(state.windowHrs).toBe('24h'); expect(state.envFilter).toBe('All')
  })
  it('shares changes with other investigation views', () => {
    const perf = usePerformanceStore(), investigation = useInvestigationStore()
    perf.windowHrs = '7d'; perf.envFilter = 'preview'
    expect(investigation.range).toBe('7d'); expect(investigation.environment).toBe('preview')
    investigation.environment = 'eu-west'
    expect(perf.envFilter).toBe('eu-west')
  })
  it('restores custom environments and ranges from this tab session', async () => {
    const perf = usePerformanceStore()
    perf.windowHrs = '30d'; perf.envFilter = 'eu-west'
    await nextTick()
    setActivePinia(createPinia())
    expect(usePerformanceStore().windowHrs).toBe('30d')
    expect(usePerformanceStore().envFilter).toBe('eu-west')
  })
  it('uses identical bounds until refresh, including comparison intervals', () => {
    const state = useInvestigationStore()
    const parse = (comparison = false) => new URL(state.request('/api/transactions?hours=24', comparison), 'http://test').searchParams
    const a = parse(), b = parse(), previous = parse(true)
    expect(a.get('to')).toBe(b.get('to'))
    expect(previous.get('to')).toBe(a.get('from'))
    expect(Date.parse(a.get('to')!) - Date.parse(a.get('from')!)).toBe(86400000)
  })
})
