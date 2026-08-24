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
  // Sample count and thread live in the section heading beside "Flame graph",
  // matching the Errors and Logs panels. Repeating them in the toolbar crowded
  // out the search box.
  it('leaves the summary counts to the section heading', () => {
    const w = mount(FlameGraph, { props: { graph: graph() }, global: { stubs } })
    expect(w.text()).not.toContain('120 samples')
    expect(w.text()).not.toContain('MainThread')
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
    expect(w.text()).toContain('no timings')
  })

  it('hides the breadcrumb until something is zoomed', () => {
    const w = mount(FlameGraph, { props: { graph: graph() }, global: { stubs } })
    expect(w.find('.flame__crumbs').exists()).toBe(false)
  })

  // Inside the plot the tooltip stretched the panel and raised scrollbars
  // whenever the cursor neared an edge, so it has to live outside it.
  it('keeps the tooltip out of the scrolling plot area', () => {
    const w = mount(FlameGraph, { props: { graph: graph() }, global: { stubs } })
    expect(w.find('.flame__plot .flame-tip').exists()).toBe(false)
  })

  it('has a search box', () => {
    const w = mount(FlameGraph, { props: { graph: graph() }, global: { stubs } })
    const input = w.find('.flame__input')
    expect(input.exists()).toBe(true)
    expect(input.attributes('aria-label')).toBe('Find a frame')
  })

  it('reports how many frames a search matched', async () => {
    const w = mount(FlameGraph, { props: { graph: graph() }, global: { stubs } })
    expect(w.text()).not.toContain('matched')

    await w.find('.flame__input').setValue('query')
    expect(w.text()).toContain('1 matched')

    await w.find('.flame__input').setValue('nothing-here')
    expect(w.text()).toContain('0 matched')
  })

  // The legend is the only thing telling you why some frames are grey.
  it('explains the muted colour', () => {
    const w = mount(FlameGraph, { props: { graph: graph() }, global: { stubs } })
    expect(w.find('.flame__legend').exists()).toBe(true)
    expect(w.text()).toContain('library')
  })

  it('copes with an empty graph', () => {
    const empty = graph({
      sample_count: 0,
      root: { function: '', total_samples: 0, self_samples: 0, children: [] },
    })
    const w = mount(FlameGraph, { props: { graph: empty }, global: { stubs } })
    expect(w.find('canvas').exists()).toBe(true)
    expect(w.find('.flame__input').exists()).toBe(true)
  })
})
