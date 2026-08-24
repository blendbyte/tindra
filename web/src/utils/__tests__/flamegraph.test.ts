import { describe, it, expect } from 'vitest'
import {
  layoutFlame,
  maxDepth,
  samplesToMs,
  frameColor,
  frameMatches,
} from '../flamegraph'
import type { FlameNode } from '@/api/types'

function node(fn: string, total: number, self: number, children: FlameNode[] = []): FlameNode {
  return { function: fn, total_samples: total, self_samples: self, children }
}

// The API returns a synthetic root holding the real entry points.
const graph = node('', 10, 0, [
  node('main', 10, 2, [
    node('query', 6, 6),
    node('render', 2, 2),
  ]),
])

describe('layoutFlame', () => {
  it('returns nothing for a missing tree', () => {
    expect(layoutFlame(null)).toEqual([])
    expect(layoutFlame(undefined)).toEqual([])
  })

  it('skips the synthetic root and starts at the entry points', () => {
    const rects = layoutFlame(graph)
    const top = rects.filter(r => r.depth === 0)
    expect(top).toHaveLength(1)
    expect(top[0].node.function).toBe('main')
    expect(top[0].x0).toBe(0)
    expect(top[0].x1).toBe(1)
  })

  it('sizes boxes by their share of samples', () => {
    const rects = layoutFlame(graph)
    const query = rects.find(r => r.node.function === 'query')!
    const render = rects.find(r => r.node.function === 'render')!

    // 6 and 2 of main's 10 samples.
    expect(query.x1 - query.x0).toBeCloseTo(0.6)
    expect(render.x1 - render.x0).toBeCloseTo(0.2)
  })

  it('lays children out side by side without overlapping', () => {
    const rects = layoutFlame(graph)
    const kids = rects.filter(r => r.depth === 1).sort((a, b) => a.x0 - b.x0)
    expect(kids[0].x1).toBeCloseTo(kids[1].x0)
  })

  // A parent narrower than its children would let a callee spill outside the
  // caller, which reads as time appearing from nowhere.
  it('never lets children exceed their parent', () => {
    const rects = layoutFlame(graph)
    const main = rects.find(r => r.node.function === 'main')!
    for (const kid of rects.filter(r => r.depth === 1)) {
      expect(kid.x0).toBeGreaterThanOrEqual(main.x0 - 1e-9)
      expect(kid.x1).toBeLessThanOrEqual(main.x1 + 1e-9)
    }
  })

  it('draws the focused node as the first row when zoomed', () => {
    const main = graph.children![0]
    const rects = layoutFlame(main, { drawRoot: true })
    expect(rects[0].node.function).toBe('main')
    expect(rects[0].depth).toBe(0)
    expect(rects[0].x1 - rects[0].x0).toBe(1)

    // Zooming rescales the children to fill the new width.
    const query = rects.find(r => r.node.function === 'query')!
    expect(query.x1 - query.x0).toBeCloseTo(0.6)
  })

  it('gives sibling frames with the same name distinct keys by path', () => {
    const shared = node('', 4, 0, [
      node('a', 2, 0, [node('helper', 2, 2)]),
      node('b', 2, 0, [node('helper', 2, 2)]),
    ])
    const keys = layoutFlame(shared).map(r => r.key)
    expect(new Set(keys).size).toBe(keys.length)
  })

  it('survives a zero-sample tree without dividing by zero', () => {
    const empty = node('', 0, 0, [node('main', 0, 0)])
    const rects = layoutFlame(empty)
    for (const r of rects) {
      expect(Number.isFinite(r.x0)).toBe(true)
      expect(Number.isFinite(r.x1)).toBe(true)
    }
  })

  it('handles a root with no children', () => {
    expect(layoutFlame(node('', 0, 0))).toEqual([])
  })
})

describe('maxDepth', () => {
  it('reports the deepest row', () => {
    expect(maxDepth(layoutFlame(graph))).toBe(1)
  })

  it('reports -1 when empty', () => {
    expect(maxDepth([])).toBe(-1)
  })
})

describe('samplesToMs', () => {
  it('converts using the measured interval', () => {
    // 10 samples at 10ms.
    expect(samplesToMs(10, 10_000_000)).toBe(100)
  })

  // The fold reports 0 when it could not measure a period. Inventing a
  // duration from a guessed sample rate would be worse than saying nothing.
  it('returns null when the interval is unknown', () => {
    expect(samplesToMs(10, 0)).toBeNull()
  })
})

describe('frameColor', () => {
  it('mutes frames explicitly outside the application', () => {
    const lib = frameColor('json.dumps', false)
    const app = frameColor('json.dumps', true)
    expect(lib).not.toBe(app)
    expect(lib).toContain('220') // the grey hue
  })

  // The PHP SDK never sends in_app. Treating absent as false would grey out
  // every frame in a Laravel profile.
  it('treats an absent in_app as application code', () => {
    expect(frameColor('handler', undefined)).toBe(frameColor('handler', true))
  })

  it('is stable for a given name', () => {
    expect(frameColor('OrderController::index', true)).toBe(frameColor('OrderController::index', true))
  })

  it('distinguishes different names', () => {
    expect(frameColor('alpha', true)).not.toBe(frameColor('zulu', true))
  })

  it('dims when asked', () => {
    expect(frameColor('handler', true, true)).not.toBe(frameColor('handler', true, false))
  })
})

describe('frameMatches', () => {
  const n: FlameNode = {
    function: 'OrderController::index',
    module: 'App\\Http\\Controllers',
    filename: 'app/Http/Controllers/OrderController.php',
    total_samples: 1,
    self_samples: 1,
  }

  it('matches the function name case insensitively', () => {
    expect(frameMatches(n, 'ordercontroller')).toBe(true)
  })

  it('matches the module and filename too', () => {
    expect(frameMatches(n, 'app\\http')).toBe(true)
    expect(frameMatches(n, '.php')).toBe(true)
  })

  it('does not match unrelated text', () => {
    expect(frameMatches(n, 'redis')).toBe(false)
  })

  it('treats an empty query as no match, so nothing is dimmed', () => {
    expect(frameMatches(n, '')).toBe(false)
  })

  it('tolerates missing optional fields', () => {
    const bare: FlameNode = { function: 'main', total_samples: 1, self_samples: 1 }
    expect(frameMatches(bare, 'main')).toBe(true)
    expect(frameMatches(bare, 'nope')).toBe(false)
  })
})
