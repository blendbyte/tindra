import { afterEach, describe, expect, it, vi } from 'vitest'
import { computed, defineComponent, h, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { focusManager, QueryClient, useQuery, VueQueryPlugin } from '@tanstack/vue-query'
import { apiFetch } from '../client'
import { createQueryClient } from '../queryClient'

const cleanups: (() => void)[] = []
afterEach(() => {
  cleanups.reverse().forEach(cleanup => cleanup())
  cleanups.length = 0
  focusManager.setFocused(undefined)
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function client(configured = true) {
  const result = configured ? createQueryClient() : new QueryClient()
  cleanups.push(() => result.clear())
  return result
}

function reader(queryClient: QueryClient, scope = ref('a'), options = {}) {
  const wrapper = mount(defineComponent({
    setup() {
      const query = useQuery({
        queryKey: computed(() => ['issues', 'overview', scope.value]),
        queryFn: ({ signal }) => apiFetch<{ value: string }>(`/api/issues/overview?project_id=${scope.value}`, { signal }),
        retry: false,
        ...options,
      })
      return () => h('div', query.data.value?.value ?? '')
    },
  }), { global: { plugins: [[VueQueryPlugin, { queryClient }]] } })
  let active = true
  const unmount = () => { if (active) { active = false; wrapper.unmount() } }
  cleanups.push(unmount)
  return { wrapper, unmount }
}

function pendingRequests() {
  const requests: { signal: AbortSignal; resolve: (value: string) => void }[] = []
  vi.stubGlobal('fetch', vi.fn((_path, init) => new Promise<Response>((resolve, reject) => {
    const signal = init.signal as AbortSignal
    signal.addEventListener('abort', () => reject(signal.reason), { once: true })
    requests.push({ signal, resolve: value => resolve(Response.json({ value })) })
  })))
  return requests
}

describe('query request lifecycle', () => {
  it('aborts obsolete filters and the last unmounted observer', async () => {
    const requests = pendingRequests()
    const scope = ref('a')
    const view = reader(client(), scope)
    expect(requests).toHaveLength(1)
    scope.value = 'b'
    await flushPromises()
    expect(requests[0]!.signal.aborted).toBe(true)
    expect(requests).toHaveLength(2)
    requests[1]!.resolve('new scope')
    await flushPromises()
    expect(view.wrapper.text()).toBe('new scope')
    scope.value = 'c'
    await flushPromises()
    view.unmount()
    expect(requests[2]!.signal.aborted).toBe(true)
  })

  it('shares a pending request until its last reader leaves', () => {
    const requests = pendingRequests()
    const qc = client()
    const first = reader(qc)
    const second = reader(qc)
    expect(requests).toHaveLength(1)
    first.unmount()
    expect(requests[0]!.signal.aborted).toBe(false)
    second.unmount()
    expect(requests[0]!.signal.aborted).toBe(true)
  })

  it('reduces three immediate visits from three requests to one', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Response.json({ value: 'cached' })))
    for (const configured of [false, true]) {
      vi.mocked(fetch).mockClear()
      const qc = client(configured)
      for (let visit = 0; visit < 3; visit++) {
        const view = reader(qc)
        await flushPromises()
        expect(view.wrapper.text()).toBe('cached')
        view.unmount()
      }
      expect(fetch).toHaveBeenCalledTimes(configured ? 1 : 3)
    }
  })

  it('refetches expired data and invalidates fresh dashboard data after mutations', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Response.json({ value: 'updated' })))
    const qc = client()
    qc.setQueryData(['issues', 'overview', 'a'], { value: 'old' }, { updatedAt: Date.now() - 6_000 })
    const view = reader(qc)
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(1)
    await qc.invalidateQueries({ queryKey: ['issues'] })
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(view.wrapper.text()).toBe('updated')
  })

  it('does not mistake initial empty project metadata for a fresh response', async () => {
    const qc = client()
    const queryFn = vi.fn(async () => [{ id: 'project' }])
    const wrapper = mount(defineComponent({
      setup() {
        const { data } = useQuery({ queryKey: ['projects', 'metadata'], queryFn, initialData: [], initialDataUpdatedAt: 0 })
        return () => h('div', String(data.value.length))
      },
    }), { global: { plugins: [[VueQueryPlugin, { queryClient: qc }]] } })
    cleanups.push(() => wrapper.unmount())
    await flushPromises()
    expect(queryFn).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toBe('1')
  })

  it('preserves polling while pausing hidden-window polls', async () => {
    vi.useFakeTimers()
    focusManager.setFocused(true)
    vi.stubGlobal('fetch', vi.fn(async () => Response.json({ value: 'live' })))
    reader(client(), ref('a'), { refetchInterval: 1_000 })
    await vi.advanceTimersByTimeAsync(0)
    expect(fetch).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1_000)
    expect(fetch).toHaveBeenCalledTimes(2)
    focusManager.setFocused(false)
    await vi.advanceTimersByTimeAsync(2_000)
    expect(fetch).toHaveBeenCalledTimes(2)
    focusManager.setFocused(true)
    await vi.advanceTimersByTimeAsync(1_000)
    expect(fetch).toHaveBeenCalledTimes(3)
  })

  it('keeps identity and settings queries immediately stale', () => {
    const qc = client()
    expect(qc.defaultQueryOptions({ queryKey: ['me'] }).staleTime ?? 0).toBe(0)
    expect(qc.defaultQueryOptions({ queryKey: ['settings'] }).staleTime ?? 0).toBe(0)
  })
})
