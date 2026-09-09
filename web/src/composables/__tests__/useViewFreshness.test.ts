import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { createPinia } from 'pinia'
import { mount, flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { QueryClient, VueQueryPlugin, useQuery } from '@tanstack/vue-query'
import { isTelemetryQuery, useViewFreshness } from '../useViewFreshness'
import { useInvestigationStore } from '@/stores/investigation'

let cleanup = () => {}
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks() })
async function setup() {
  vi.useFakeTimers(); vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
  const pinia=createPinia(), state=useInvestigationStore(pinia)
  const client=new QueryClient({defaultOptions:{queries:{retry:false,gcTime:Infinity,refetchOnWindowFocus:false,refetchOnReconnect:false}}})
  const router=createRouter({history:createMemoryHistory(),routes:[{path:'/logs',component:{template:'<div />'}}]})
  await router.push('/logs')
  const table=vi.fn(async()=>[]), chart=vi.fn(async()=>[]), metadata=vi.fn(async()=>[])
  let freshness!: ReturnType<typeof useViewFreshness>
  const wrapper=mount(defineComponent({setup(){
    useQuery({queryKey:['logs','table'],queryFn:table})
    useQuery({queryKey:['transaction-timeseries','chart'],queryFn:chart})
    useQuery({queryKey:['projects','metadata'],queryFn:metadata})
    freshness=useViewFreshness()
    return ()=>h('div')
  }}),{global:{plugins:[pinia,router,[VueQueryPlugin,{queryClient:client}]]}})
  await flushPromises()
  cleanup=()=>{wrapper.unmount();client.clear()}
  return {state,freshness,table,chart,metadata,client,router}
}
describe('view freshness',()=>{
 it('refreshes every active panel on cadence, excluding metadata',async()=>{
   const {table,chart,metadata}=await setup()
   await vi.advanceTimersByTimeAsync(5000);await flushPromises()
   expect(table).toHaveBeenCalledTimes(2);expect(chart).toHaveBeenCalledTimes(2);expect(metadata).toHaveBeenCalledTimes(1)
 })
 it('pauses while browsing older pages and refreshes latest explicitly',async()=>{
   const {state,table,freshness}=await setup()
   state.browsingHistory=true
   await vi.advanceTimersByTimeAsync(10000)
   expect(table).toHaveBeenCalledTimes(1)
   await freshness.refresh()
   expect(state.browsingHistory).toBe(false);expect(table).toHaveBeenCalledTimes(2)
 })
 it('keeps the oldest successful timestamp when one panel fails',async()=>{
   const {freshness,chart}=await setup()
   const before=freshness.updatedAt.value
   chart.mockRejectedValueOnce(new Error('offline'))
   await vi.advanceTimersByTimeAsync(5000);await flushPromises()
   expect(freshness.failed.value).toHaveLength(1)
   expect(freshness.updatedAt.value).toBe(before)
 })
 it('does not poll while paused or in a fixed historical interval',async()=>{
   const {state,table}=await setup()
   state.paused=true;await vi.advanceTimersByTimeAsync(10000)
   state.paused=false;state.absolute={from:'2025-12-01T00:00:00Z',to:'2025-12-02T00:00:00Z'}
   await vi.advanceTimersByTimeAsync(10000)
   expect(table).toHaveBeenCalledTimes(1)
 })
 it('suspends hidden and offline polling, then refreshes once when due', async () => {
   const { table } = await setup()
   const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
   document.dispatchEvent(new Event('visibilitychange'))
   await vi.advanceTimersByTimeAsync(10000)
   expect(table).toHaveBeenCalledTimes(1)
   const online = vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(false)
   visibility.mockReturnValue('visible'); window.dispatchEvent(new Event('offline'))
   await vi.advanceTimersByTimeAsync(10000)
   expect(table).toHaveBeenCalledTimes(1)
   online.mockReturnValue(true); window.dispatchEvent(new Event('online')); await flushPromises()
   expect(table).toHaveBeenCalledTimes(2)
 })

})

it('discards older infinite pages before a coordinated refresh', async () => {
  const { client, freshness, table } = await setup()
  client.setQueryData(['logs', 'table'], { pages: [['latest'], ['older']], pageParams: [null, 'cursor'] })
  table.mockImplementationOnce(async () => {
    expect(client.getQueryData(['logs', 'table'])).toEqual({ pages: [['latest']], pageParams: [null] })
    return []
  })
  await freshness.refresh()
  expect(table).toHaveBeenCalledTimes(2)
})


it('includes current-state dashboard panels but excludes metadata and administrative queries', () => {
  for (const queryKey of [['alert-rules'], ['projects', 'stats'], ['logs']]) {
    expect(isTelemetryQuery({ queryKey } as any)).toBe(true)
  }
  for (const queryKey of [['projects', 'metadata'], ['releases', 'metadata'], ['users']]) {
    expect(isTelemetryQuery({ queryKey } as any)).toBe(false)
  }
})
it('calculates age and resets pagination pause when navigating', async () => {
  const { freshness, state, router } = await setup()
  state.paused = true
  await vi.advanceTimersByTimeAsync(3000)
  expect(freshness.age.value).toBe(3)
  state.browsingHistory = true
  router.addRoute({ path: '/issues', component: { template: '<div />' } })
  await router.push('/issues')
  await flushPromises()
  expect(state.browsingHistory).toBe(false)
  expect(freshness.interval.value).toBe(30000)
})
it('does not launch another refresh while offline or already fetching', async () => {
  const { freshness, table } = await setup()
  vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(false)
  window.dispatchEvent(new Event('offline'))
  await freshness.refresh()
  expect(table).toHaveBeenCalledTimes(1)
  vi.restoreAllMocks()
  window.dispatchEvent(new Event('online'))
  let resolve!: (value: never[]) => void
  table.mockImplementationOnce(() => new Promise<never[]>(done => { resolve = done }))
  const pending = freshness.refresh()
  await flushPromises()
  await freshness.refresh()
  expect(table).toHaveBeenCalledTimes(2)
  resolve([])
  await pending
})
