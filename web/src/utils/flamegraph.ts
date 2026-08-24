import type { FlameNode } from '@/api/types'

/** A laid-out box. x0/x1 are fractions of the full width, so the caller scales. */
export interface FlameRect {
  node: FlameNode
  depth: number
  x0: number
  x1: number
  /** Stable identity for hover and zoom, since nodes are structurally shared. */
  key: string
}

export interface LayoutOptions {
  /**
   * Draw the given node as the first row. False treats it as the synthetic
   * root the API returns, whose children are the real entry points.
   */
  drawRoot?: boolean
}

/**
 * Lays a call tree out as an icicle: entry points on top, callees below, each
 * box as wide as its share of the samples.
 *
 * Widths come from total_samples rather than self_samples, so a box always
 * covers exactly the children beneath it. Using self would leave a parent
 * narrower than the work it contains.
 */
export function layoutFlame(root: FlameNode | null | undefined, opts: LayoutOptions = {}): FlameRect[] {
  if (!root) return []
  const out: FlameRect[] = []

  const walk = (n: FlameNode, depth: number, x0: number, x1: number, path: string) => {
    out.push({ node: n, depth, x0, x1, key: path })
    const span = x1 - x0
    const denom = n.total_samples || 1
    let cursor = x0
    for (const c of n.children ?? []) {
      const w = span * (c.total_samples / denom)
      walk(c, depth + 1, cursor, cursor + w, `${path}/${c.function}`)
      cursor += w
    }
  }

  if (opts.drawRoot) {
    walk(root, 0, 0, 1, root.function || '<root>')
    return out
  }

  const denom = root.total_samples || 1
  let cursor = 0
  for (const c of root.children ?? []) {
    const w = c.total_samples / denom
    walk(c, 0, cursor, cursor + w, c.function)
    cursor += w
  }
  return out
}

/** Deepest row index in a layout, or -1 when empty. */
export function maxDepth(rects: FlameRect[]): number {
  let d = -1
  for (const r of rects) if (r.depth > d) d = r.depth
  return d
}

/**
 * Converts a sample count to milliseconds using the interval measured at fold
 * time. Returns null when the interval is unknown, so the UI can say nothing
 * rather than print a fabricated duration.
 */
export function samplesToMs(samples: number, sampleIntervalNs: number): number | null {
  if (!sampleIntervalNs) return null
  return (samples * sampleIntervalNs) / 1e6
}

/**
 * Picks a fill for a frame.
 *
 * Application frames get a warm hue keyed off the name so sibling calls stay
 * distinguishable; library frames are muted so the eye lands on your own code.
 *
 * Only an explicit `in_app: false` mutes a frame. Some SDKs, the PHP one among
 * them, never send the field at all, and treating absent as false would grey
 * out the entire graph.
 */
export function frameColor(fn: string, inApp: boolean | undefined, dimmed = false): string {
  if (inApp === false) {
    return dimmed ? 'hsl(220 12% 62% / 0.25)' : 'hsl(220 12% 62% / 0.55)'
  }
  let hash = 0
  for (let i = 0; i < fn.length; i++) hash = (hash * 31 + fn.charCodeAt(i)) | 0
  const hue = Math.abs(hash) % 45 // red through amber
  return dimmed ? `hsl(${hue} 70% 55% / 0.2)` : `hsl(${hue} 78% 55% / 0.85)`
}

/** Case-insensitive match across the identifiers shown on a box. */
export function frameMatches(node: FlameNode, query: string): boolean {
  if (!query) return false
  const q = query.toLowerCase()
  return (
    node.function.toLowerCase().includes(q) ||
    (node.module ?? '').toLowerCase().includes(q) ||
    (node.filename ?? '').toLowerCase().includes(q)
  )
}
