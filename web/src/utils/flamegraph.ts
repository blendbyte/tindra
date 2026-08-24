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
 * Hues taken from the same vocabulary the span waterfall uses, so a flame graph
 * and a waterfall on the same page read as one system.
 */
export const FRAME_HUES = [285, 230, 155, 60, 340, 195, 255, 48]

/**
 * Picks a hue for a frame.
 *
 * Keyed on module rather than function name so related frames share a colour,
 * which is what makes a deep graph scannable. Falls back through filename to
 * function so unsymbolicated frames stay distinguishable.
 */
export function frameHue(node: FlameNode): number {
  const key = node.module || node.filename || node.function
  let hash = 0
  for (let i = 0; i < key.length; i++) hash = (hash * 31 + key.charCodeAt(i)) | 0
  return FRAME_HUES[Math.abs(hash) % FRAME_HUES.length]
}

/**
 * Reports whether a frame should be muted as library code.
 *
 * Only an explicit false counts. Some SDKs, the PHP one among them, never send
 * the field at all, and treating absent as false would grey out a whole graph.
 */
export function isLibraryFrame(node: FlameNode): boolean {
  return node.in_app === false
}

/**
 * Trims a fully qualified name down to its identifying tail. PHP and Python
 * both produce names far too long for a box, and the head is the disposable
 * part.
 */
export function shortName(fn: string): string {
  const parts = fn.split(/\\|::|\//)
  if (parts.length <= 2) return fn
  return parts.slice(-2).join(fn.includes('::') ? '::' : '\\')
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

/**
 * Places the hover tooltip in viewport coordinates.
 *
 * The tooltip is fixed and teleported to the body precisely so no scroll
 * container can clip it or be stretched by it, which is what happens when a
 * tooltip lives inside the plot and the cursor nears an edge. That only works
 * if the coordinates are clamped to the viewport here.
 */
export function clampTooltip(
  x: number,
  y: number,
  w: number,
  h: number,
  viewportW: number,
  viewportH: number,
  pad = 12,
  edge = 8,
): { left: number; top: number } {
  let left = x + pad
  let top = y + pad
  // Flip to the other side of the cursor rather than letting it run off.
  if (left + w > viewportW - edge) left = x - w - pad
  if (top + h > viewportH - edge) top = y - h - pad
  return { left: Math.max(edge, left), top: Math.max(edge, top) }
}
