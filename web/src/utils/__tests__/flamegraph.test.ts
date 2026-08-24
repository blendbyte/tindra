import { describe, it, expect } from 'vitest'
import {
  layoutFlame,
  maxDepth,
  samplesToMs,
  frameHue,
  isLibraryFrame,
  shortName,
  frameMatches,
  clampTooltip,
  FRAME_HUES,
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

describe('frameHue', () => {
  const node = (over: Partial<FlameNode>): FlameNode =>
    ({ function: 'f', total_samples: 1, self_samples: 1, ...over })

  it('only ever returns a hue from the app palette', () => {
    for (const name of ['alpha', 'beta', 'OrderController', 'json.encoder', '']) {
      expect(FRAME_HUES).toContain(frameHue(node({ function: name })))
    }
  })

  it('is stable for a given frame', () => {
    const n = node({ module: 'app.views' })
    expect(frameHue(n)).toBe(frameHue(n))
  })

  // Colouring by module is what makes a deep graph scannable: frames from the
  // same package group visually instead of speckling.
  it('groups frames from the same module', () => {
    const a = node({ function: 'index', module: 'app.views' })
    const b = node({ function: 'show', module: 'app.views' })
    expect(frameHue(a)).toBe(frameHue(b))
  })

  it('separates different modules', () => {
    const hues = new Set(
      ['app.views', 'json.encoder', 'django.db', 'celery.worker'].map(m => frameHue(node({ module: m })))
    )
    expect(hues.size).toBeGreaterThan(1)
  })

  it('falls back to filename then function when there is no module', () => {
    expect(frameHue(node({ filename: 'a.php' }))).toBe(frameHue(node({ filename: 'a.php', function: 'other' })))
    expect(FRAME_HUES).toContain(frameHue(node({ function: 'bare' })))
  })
})

describe('isLibraryFrame', () => {
  const node = (inApp: boolean | undefined): FlameNode =>
    ({ function: 'f', in_app: inApp, total_samples: 1, self_samples: 1 })

  it('mutes only an explicit false', () => {
    expect(isLibraryFrame(node(false))).toBe(true)
    expect(isLibraryFrame(node(true))).toBe(false)
  })

  // The PHP SDK never sends in_app. Treating absent as false would grey out
  // every frame in a Laravel profile.
  it('treats an absent flag as application code', () => {
    expect(isLibraryFrame(node(undefined))).toBe(false)
  })
})

describe('shortName', () => {
  it('keeps short names intact', () => {
    expect(shortName('main')).toBe('main')
    expect(shortName('app::run')).toBe('app::run')
  })

  it('trims a PHP namespace to its tail', () => {
    expect(shortName('Illuminate\\Database\\Eloquent\\Builder::get'))
      .toBe('Builder::get')
  })

  it('trims a path to its tail', () => {
    expect(shortName('/var/www/app/public/index.php')).toBe('public\\index.php')
  })

  it('leaves a dotted python name alone', () => {
    expect(shortName('reports.services.aggregate')).toBe('reports.services.aggregate')
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

describe('clampTooltip', () => {
  const W = 280
  const H = 108

  it('sits below and right of the cursor when there is room', () => {
    expect(clampTooltip(100, 100, W, H, 1280, 800)).toEqual({ left: 112, top: 112 })
  })

  // Running off the right edge is what stretched the page and raised a
  // horizontal scrollbar, so it flips to the other side of the cursor instead.
  it('flips left near the right edge', () => {
    const { left } = clampTooltip(1270, 100, W, H, 1280, 800)
    expect(left).toBe(1270 - W - 12)
    expect(left + W).toBeLessThanOrEqual(1280)
  })

  it('flips up near the bottom edge', () => {
    const { top } = clampTooltip(100, 780, W, H, 1280, 800)
    expect(top).toBe(780 - H - 12)
    expect(top + H).toBeLessThanOrEqual(800)
  })

  it('flips both ways in the bottom right corner', () => {
    const { left, top } = clampTooltip(1275, 795, W, H, 1280, 800)
    expect(left + W).toBeLessThanOrEqual(1280)
    expect(top + H).toBeLessThanOrEqual(800)
  })

  // A viewport too small to flip into must still not produce negative
  // coordinates, which would push the document out on the other side.
  it('never returns a position off the top or left', () => {
    const { left, top } = clampTooltip(4, 4, W, H, 200, 120)
    expect(left).toBeGreaterThanOrEqual(8)
    expect(top).toBeGreaterThanOrEqual(8)
  })
})
