import { describe, it, expect, beforeAll } from 'vitest'
import { mount } from '@vue/test-utils'
import FlameGraph from '../FlameGraph.vue'
import type { FlameGraph as FlameGraphData } from '@/api/types'

beforeAll(() => {
  // happy-dom has neither, and the component observes its own width and draws
  // on mount. Both are no-ops here: the layout maths is covered separately.
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver
  }
  if (!HTMLCanvasElement.prototype.getContext) {
    HTMLCanvasElement.prototype.getContext = () => null
  }
})

function graph(over: Partial<FlameGraphData> = {}): FlameGraphData {
  return {
    sample_count: 120,
    idle_samples: 0,
    sample_interval_ns: 10_000_000,
    duration_ns: 1_200_000_000,
    thread_name: 'MainThread',
    root: {
      function: '',
      total_samples: 120,
      self_samples: 0,
      children: [
        {
          function: 'main',
          total_samples: 120,
          self_samples: 20,
          children: [{ function: 'query', total_samples: 100, self_samples: 100 }],
        },
      ],
    },
    ...over,
  }
}

const stubs = { Icon: true }

describe('FlameGraph', () => {
  it('renders the sample count and thread', () => {
    const w = mount(FlameGraph, { props: { graph: graph() }, global: { stubs } })
    expect(w.text()).toContain('120 samples')
    expect(w.text()).toContain('MainThread')
  })

  it('renders a canvas', () => {
    const w = mount(FlameGraph, { props: { graph: graph() }, global: { stubs } })
    expect(w.find('canvas').exists()).toBe(true)
  })

  it('shows idle samples only when there are some', () => {
    const without = mount(FlameGraph, { props: { graph: graph() }, global: { stubs } })
    expect(without.text()).not.toContain('idle')

    const with_ = mount(FlameGraph, {
      props: { graph: graph({ idle_samples: 7 }) },
      global: { stubs },
    })
    expect(with_.text()).toContain('7 idle')
  })

  // The fold reports 0 when it could not measure a period, and the UI has to
  // say so rather than print durations derived from a guess.
  it('flags unavailable timings', () => {
    const w = mount(FlameGraph, {
      props: { graph: graph({ sample_interval_ns: 0 }) },
      global: { stubs },
    })
    expect(w.text()).toContain('timings unavailable')
  })

  it('hides the zoom controls until something is zoomed', () => {
    const w = mount(FlameGraph, { props: { graph: graph() }, global: { stubs } })
    expect(w.text()).not.toContain('Reset zoom')
    expect(w.find('.flame__crumbs').exists()).toBe(false)
  })

  it('has a search box', () => {
    const w = mount(FlameGraph, { props: { graph: graph() }, global: { stubs } })
    const input = w.find('.flame__search')
    expect(input.exists()).toBe(true)
    expect(input.attributes('aria-label')).toBe('Find function')
  })

  it('copes with an empty graph', () => {
    const empty = graph({
      sample_count: 0,
      root: { function: '', total_samples: 0, self_samples: 0, children: [] },
    })
    const w = mount(FlameGraph, { props: { graph: empty }, global: { stubs } })
    expect(w.text()).toContain('0 samples')
    expect(w.find('canvas').exists()).toBe(true)
  })
})
