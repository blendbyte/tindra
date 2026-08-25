import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import FlameGraph from '../FlameGraph.vue'
import type { FlameGraph as FlameGraphData } from '@/api/types'

// happy-dom has no canvas, no ResizeObserver and no layout, so the drawing and
// hit-testing paths never ran at all. They are the parts most worth covering:
// mapping a pixel back to a frame is exactly the arithmetic that breaks quietly.

type Recorder = { fills: string[]; labels: string[]; strokes: number }

let recorder: Recorder
let observerCb: ((entries: { contentRect: { width: number } }[]) => void) | null = null

const CANVAS_WIDTH = 1000
const ROW_HEIGHT = 20
const ROW_GAP = 1

function installCanvas() {
  recorder = { fills: [], labels: [], strokes: 0 }
  const ctx = {
    fillStyle: '',
    strokeStyle: '',
    lineWidth: 0,
    font: '',
    textBaseline: '',
    globalAlpha: 1,
    setTransform() {},
    clearRect() {},
    save() {},
    restore() {},
    beginPath() {},
    moveTo() {},
    arcTo() {},
    closePath() {},
    rect() {},
    clip() {},
    fill() {
      recorder.fills.push(String(ctx.fillStyle))
    },
    stroke() {
      recorder.strokes++
    },
    fillText(text: string) {
      recorder.labels.push(text)
    },
  }
  HTMLCanvasElement.prototype.getContext = (() => ctx) as never
}

beforeEach(() => {
  installCanvas()
  observerCb = null
  globalThis.ResizeObserver = class {
    constructor(cb: (entries: { contentRect: { width: number } }[]) => void) {
      observerCb = cb
    }
    observe() {}
    unobserve() {}
    disconnect() {}
  } as never
})

afterEach(() => {
  document.querySelectorAll('.flame-tip').forEach(el => el.remove())
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
          module: 'app',
          total_samples: 120,
          self_samples: 20,
          children: [
            { function: 'query', module: 'app.db', total_samples: 100, self_samples: 100 },
          ],
        },
      ],
    },
    ...over,
  }
}

const stubs = { Icon: true }

/** Mounts with a real width so the canvas paths run. */
async function mountSized(data: FlameGraphData = graph()): Promise<VueWrapper> {
  const w = mount(FlameGraph, { props: { graph: data }, global: { stubs }, attachTo: document.body })
  const canvas = w.find('canvas').element as HTMLCanvasElement
  canvas.getBoundingClientRect = () =>
    ({ left: 0, top: 0, right: CANVAS_WIDTH, bottom: 400, width: CANVAS_WIDTH, height: 400 }) as DOMRect
  observerCb?.([{ contentRect: { width: CANVAS_WIDTH } }])
  await nextTick()
  return w
}

/** Centre of the row at the given depth, in viewport coordinates. */
function rowY(depth: number): number {
  return depth * (ROW_HEIGHT + ROW_GAP) + ROW_HEIGHT / 2
}

async function hover(w: VueWrapper, x: number, depth: number) {
  await w.find('canvas').trigger('mousemove', { clientX: x, clientY: rowY(depth) })
  await nextTick()
}

function tip(): HTMLElement | null {
  return document.querySelector('.flame-tip')
}

describe('FlameGraph', () => {
  describe('toolbar', () => {
    it('labels and summarises itself in the toolbar', async () => {
      const bar = (await mountSized()).find('.flame__bar')
      expect(bar.text()).toContain('Flame graph')
      expect(bar.text()).toContain('120 samples')
      expect(bar.text()).toContain('MainThread')
    })

    // v1 profiles carry no thread name, so the summary must not trail a separator.
    it('summarises without a thread name', async () => {
      const w = await mountSized(graph({ thread_name: undefined }))
      expect(w.find('.flame__summary').text()).toBe('120 samples')
    })

    it('shows idle samples only when there are some', async () => {
      expect((await mountSized()).text()).not.toContain('idle')
      expect((await mountSized(graph({ idle_samples: 7 }))).text()).toContain('7 idle')
    })

    // The fold reports 0 when it could not measure a period, and the UI has to
    // say so rather than print durations derived from a guess.
    it('flags unavailable timings', async () => {
      expect((await mountSized(graph({ sample_interval_ns: 0 }))).text()).toContain('no timings')
    })

    it('has a search box and a legend', async () => {
      const w = await mountSized()
      expect(w.find('.flame__input').attributes('aria-label')).toBe('Find a frame')
      expect(w.find('.flame__legend').text()).toContain('library')
    })
  })

  describe('drawing', () => {
    it('paints one box per frame', async () => {
      await mountSized()
      expect(recorder.fills).toHaveLength(2)
    })

    it('labels boxes that are wide enough', async () => {
      await mountSized()
      expect(recorder.labels).toEqual(['main', 'query'])
    })

    // A box narrower than its text would render an unreadable smear.
    it('omits the label on a sliver', async () => {
      await mountSized(graph({
        sample_count: 1000,
        root: {
          function: '',
          total_samples: 1000,
          self_samples: 0,
          children: [
            { function: 'wide', total_samples: 998, self_samples: 998 },
            { function: 'sliver', total_samples: 2, self_samples: 2 },
          ],
        },
      }))
      expect(recorder.labels).toContain('wide')
      expect(recorder.labels).not.toContain('sliver')
    })

    it('draws nothing before a width is known', () => {
      mount(FlameGraph, { props: { graph: graph() }, global: { stubs } })
      expect(recorder.fills).toHaveLength(0)
    })

    // Library frames share one muted fill; application frames are coloured by
    // module, so a mixed graph must not paint everything identically.
    it('distinguishes application frames from library frames', async () => {
      await mountSized(graph({
        root: {
          function: '',
          total_samples: 100,
          self_samples: 0,
          children: [
            { function: 'app_code', module: 'app', in_app: true, total_samples: 50, self_samples: 50 },
            { function: 'lib_code', module: 'json', in_app: false, total_samples: 50, self_samples: 50 },
          ],
        },
      }))
      expect(new Set(recorder.fills).size).toBe(2)
    })
  })

  describe('hover', () => {
    it('maps a pixel to the frame under it', async () => {
      const w = await mountSized()

      await hover(w, 500, 0)
      expect(tip()?.textContent).toContain('main')

      await hover(w, 500, 1)
      expect(tip()?.textContent).toContain('query')
    })

    // query covers 100 of main's 120 samples, so past ~83% of the width the
    // second row is empty and nothing should be reported.
    it('reports nothing past the end of a row', async () => {
      const w = await mountSized()
      await hover(w, 950, 1)
      expect(tip()).toBeNull()
    })

    it('reports nothing below the deepest row', async () => {
      const w = await mountSized()
      await hover(w, 500, 5)
      expect(tip()).toBeNull()
    })

    it('shows self and total in the tooltip', async () => {
      const w = await mountSized()
      await hover(w, 500, 1)
      const text = tip()?.textContent ?? ''
      expect(text).toContain('total')
      expect(text).toContain('self')
      expect(text).toContain('83.3%')
    })

    // Sub-millisecond frames are common in a fast request, and showing "0.0ms"
    // for all of them would flatten exactly the detail profiling is for.
    it('reports sub-millisecond frames in microseconds', async () => {
      const w = await mountSized(graph({ sample_interval_ns: 5_000 }))
      await hover(w, 500, 1)
      expect(tip()?.textContent).toContain('µs')
    })

    it('outlines the hovered box', async () => {
      const w = await mountSized()
      expect(recorder.strokes).toBe(0)
      await hover(w, 500, 0)
      expect(recorder.strokes).toBeGreaterThan(0)
    })

    it('clears the tooltip when the pointer leaves', async () => {
      const w = await mountSized()
      await hover(w, 500, 0)
      expect(tip()).not.toBeNull()

      await w.find('canvas').trigger('mouseleave')
      await nextTick()
      expect(tip()).toBeNull()
    })
  })

  describe('zoom', () => {
    it('hides the breadcrumb until something is zoomed', async () => {
      expect((await mountSized()).find('.flame__crumbs').exists()).toBe(false)
    })

    it('zooms into a frame with callees and shows a breadcrumb', async () => {
      const w = await mountSized()
      await hover(w, 500, 0)
      await w.find('canvas').trigger('click')
      await nextTick()

      expect(w.find('.flame__crumbs').exists()).toBe(true)
      expect(w.find('.flame__crumbs').text()).toContain('main')
    })

    // A leaf fills the view with a single box and shows nothing new.
    it('does not zoom into a leaf', async () => {
      const w = await mountSized()
      await hover(w, 500, 1)
      await w.find('canvas').trigger('click')
      await nextTick()
      expect(w.find('.flame__crumbs').exists()).toBe(false)
    })

    it('leaves zoom alone when nothing is hovered', async () => {
      const w = await mountSized()
      await w.find('canvas').trigger('click')
      await nextTick()
      expect(w.find('.flame__crumbs').exists()).toBe(false)
    })

    it('walks back out one level on Escape', async () => {
      const w = await mountSized()
      await hover(w, 500, 0)
      await w.find('canvas').trigger('click')
      await nextTick()
      expect(w.find('.flame__crumbs').exists()).toBe(true)

      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
      await nextTick()
      expect(w.find('.flame__crumbs').exists()).toBe(false)
    })

    it('resets from the breadcrumb', async () => {
      const w = await mountSized()
      await hover(w, 500, 0)
      await w.find('canvas').trigger('click')
      await nextTick()

      await w.findAll('.flame__crumb')[0].trigger('click')
      await nextTick()
      expect(w.find('.flame__crumbs').exists()).toBe(false)
    })

    // The layout changes under the cursor, and layout keys are per function
    // name, so a stale hit can highlight a box that is not the one it named.
    it('drops the hover when zooming', async () => {
      const w = await mountSized()
      await hover(w, 500, 0)
      expect(tip()).not.toBeNull()

      await w.find('canvas').trigger('click')
      await nextTick()
      expect(tip()).toBeNull()
    })

    it('drops the hover when resetting', async () => {
      const w = await mountSized()
      await hover(w, 500, 0)
      await w.find('canvas').trigger('click')
      await nextTick()

      await hover(w, 500, 1)
      expect(tip()).not.toBeNull()
      await w.findAll('.flame__crumb')[0].trigger('click')
      await nextTick()
      expect(tip()).toBeNull()
    })

    // The breadcrumb is a path, so clicking a middle entry has to truncate to
    // that depth rather than clear everything.
    it('truncates to the clicked breadcrumb entry', async () => {
      const deep = graph({
        root: {
          function: '',
          total_samples: 100,
          self_samples: 0,
          children: [{
            function: 'a', total_samples: 100, self_samples: 0,
            children: [{
              function: 'b', total_samples: 100, self_samples: 0,
              children: [{ function: 'c', total_samples: 100, self_samples: 100 }],
            }],
          }],
        },
      })
      const w = await mountSized(deep)

      await hover(w, 500, 0)
      await w.find('canvas').trigger('click')
      await nextTick()
      await hover(w, 500, 1)
      await w.find('canvas').trigger('click')
      await nextTick()

      // All / a / b
      expect(w.findAll('.flame__crumb')).toHaveLength(3)

      // Click "a": the path keeps a and drops b.
      await w.findAll('.flame__crumb')[1].trigger('click')
      await nextTick()
      const crumbs = w.findAll('.flame__crumb').map(c => c.text())
      expect(crumbs).toEqual(['All', 'a'])
    })

    it('drops the zoom when a different profile arrives', async () => {
      const w = await mountSized()
      await hover(w, 500, 0)
      await w.find('canvas').trigger('click')
      await nextTick()
      expect(w.find('.flame__crumbs').exists()).toBe(true)

      await w.setProps({ graph: graph({ sample_count: 99 }) })
      await nextTick()
      expect(w.find('.flame__crumbs').exists()).toBe(false)
    })
  })

  describe('search', () => {
    it('counts matches and dims the rest', async () => {
      const w = await mountSized()
      expect(w.text()).not.toContain('matched')

      recorder.fills = []
      await w.find('.flame__input').setValue('query')
      await nextTick()

      expect(w.text()).toContain('1 matched')
      expect(new Set(recorder.fills).size).toBe(2)
    })

    it('reports zero matches without throwing', async () => {
      const w = await mountSized()
      await w.find('.flame__input').setValue('nothing-here')
      await nextTick()
      expect(w.text()).toContain('0 matched')
    })
  })

  describe('resilience', () => {
    it('copes with an empty graph', async () => {
      const w = await mountSized(graph({
        sample_count: 0,
        root: { function: '', total_samples: 0, self_samples: 0, children: [] },
      }))
      expect(w.find('canvas').exists()).toBe(true)
      expect(recorder.fills).toHaveLength(0)
    })

    it('survives a canvas with no 2d context', async () => {
      HTMLCanvasElement.prototype.getContext = (() => null) as never
      const w = await mountSized()
      expect(w.find('canvas').exists()).toBe(true)
    })

    it('stops observing when unmounted', async () => {
      const disconnect = vi.fn()
      globalThis.ResizeObserver = class {
        constructor(cb: (entries: { contentRect: { width: number } }[]) => void) {
          observerCb = cb
        }
        observe() {}
        unobserve() {}
        disconnect = disconnect
      } as never

      const w = await mountSized()
      w.unmount()
      expect(disconnect).toHaveBeenCalled()
    })
  })
})
