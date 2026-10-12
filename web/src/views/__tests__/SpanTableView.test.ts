import { describe, it, expect } from 'vitest'
import { defineComponent, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import SpanTableView from '../SpanTableView.vue'
import { router } from '@/router'

describe('SpanTableView', () => {
  it('gives each span page a fresh table when switching kinds', async () => {
    let mounted = 0
    const SpanTable = defineComponent({ props: ['kind'], setup: () => { mounted++ }, template: '<div class="table">{{ kind }}</div>' })
    const kind = ref<'db' | 'cache'>('db')
    const wrapper = mount(defineComponent({ components: { SpanTableView }, setup: () => ({ kind }), template: '<SpanTableView :kind="kind" />' }), {
      global: { stubs: { SpanTable } },
    })
    expect(wrapper.text()).toBe('db')
    kind.value = 'cache'
    await nextTick()
    expect(wrapper.text()).toBe('cache')
    // A reused instance would keep the first kind's queries.
    expect(mounted).toBe(2)
  })

  it('routes each performance tab to its kind', () => {
    const kinds = Object.fromEntries(['perf-queries', 'perf-caches', 'perf-jobs'].map(name => [name, (router.getRoutes().find(r => r.name === name)!.props.default as { kind: string }).kind]))
    expect(kinds).toEqual({ 'perf-queries': 'db', 'perf-caches': 'cache', 'perf-jobs': 'jobs' })
  })
})
