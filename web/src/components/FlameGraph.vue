<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import type { FlameGraph, FlameNode } from '@/api/types'
import {
  layoutFlame,
  maxDepth,
  samplesToMs,
  frameMatches,
  clampTooltip,
  frameHue,
  isLibraryFrame,
  shortName,
  type FlameRect,
} from '@/utils/flamegraph'
import Icon from '@/components/Icon.vue'

const props = defineProps<{ graph: FlameGraph }>()

const ROW_HEIGHT = 20
const ROW_GAP = 1
const MIN_LABEL_PX = 38
const RADIUS = 2

const canvasRef = ref<HTMLCanvasElement | null>(null)
const wrapRef = ref<HTMLElement | null>(null)
const width = ref(0)
const query = ref('')
const hovered = ref<FlameRect | null>(null)
const pointer = ref({ x: 0, y: 0 })

// Zoom is a stack so Escape and the breadcrumb walk back one level at a time
// rather than jumping straight to the top.
const zoomStack = ref<FlameNode[]>([])
const focus = computed(() => zoomStack.value[zoomStack.value.length - 1] ?? null)

const rects = computed(() =>
  focus.value
    ? layoutFlame(focus.value, { drawRoot: true })
    : layoutFlame(props.graph.root)
)

const rows = computed(() => maxDepth(rects.value) + 1)
const height = computed(() => Math.max(ROW_HEIGHT, rows.value * (ROW_HEIGHT + ROW_GAP)))
const matchCount = computed(() => {
  const q = query.value.trim()
  if (!q) return 0
  return rects.value.filter(r => frameMatches(r.node, q)).length
})

function msLabel(samples: number): string {
  const ms = samplesToMs(samples, props.graph.sample_interval_ns)
  if (ms === null) return `${samples} samples`
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`
  if (ms >= 1) return `${ms.toFixed(ms >= 10 ? 0 : 1)}ms`
  return `${(ms * 1000).toFixed(0)}µs`
}

function pctOf(samples: number): string {
  const total = props.graph.sample_count || 1
  return `${((samples / total) * 100).toFixed(1)}%`
}

// Canvas cannot read CSS variables, so the palette is resolved from the
// component's own custom properties once per draw. That keeps the fills on the
// app's oklch scale and correct in both themes without duplicating the theme
// logic in JavaScript.
type Palette = {
  l: string
  c: string
  muted: string
  mutedDim: string
  label: string
  labelMuted: string
  stroke: string
  dimAlpha: string
}

function palette(): Palette {
  const s = getComputedStyle(wrapRef.value ?? document.documentElement)
  const v = (name: string, fallback: string) => s.getPropertyValue(name).trim() || fallback
  return {
    l: v('--flame-l', '0.62'),
    c: v('--flame-c', '0.13'),
    muted: v('--flame-muted', 'oklch(0.45 0.02 285)'),
    mutedDim: v('--flame-muted-dim', 'oklch(0.45 0.02 285 / 0.28)'),
    label: v('--flame-label', 'oklch(0.14 0.02 285)'),
    labelMuted: v('--flame-label-muted', 'oklch(0.98 0.01 285)'),
    stroke: v('--flame-stroke', 'oklch(0.97 0.008 285)'),
    dimAlpha: v('--flame-dim-alpha', '0.22'),
  }
}

function fillFor(node: FlameNode, p: Palette, dimmed: boolean): string {
  if (isLibraryFrame(node)) return dimmed ? p.mutedDim : p.muted
  const hue = frameHue(node)
  return dimmed
    ? `oklch(${p.l} ${p.c} ${hue} / ${p.dimAlpha})`
    : `oklch(${p.l} ${p.c} ${hue})`
}

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  const rr = Math.min(r, w / 2, h / 2)
  ctx.beginPath()
  ctx.moveTo(x + rr, y)
  ctx.arcTo(x + w, y, x + w, y + h, rr)
  ctx.arcTo(x + w, y + h, x, y + h, rr)
  ctx.arcTo(x, y + h, x, y, rr)
  ctx.arcTo(x, y, x + w, y, rr)
  ctx.closePath()
}

function draw() {
  const canvas = canvasRef.value
  if (!canvas || !width.value) return
  const ctx = canvas.getContext('2d')
  if (!ctx) return

  const dpr = window.devicePixelRatio || 1
  canvas.width = width.value * dpr
  canvas.height = height.value * dpr
  canvas.style.width = `${width.value}px`
  canvas.style.height = `${height.value}px`
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
  ctx.clearRect(0, 0, width.value, height.value)

  const p = palette()
  const font = getComputedStyle(document.documentElement).getPropertyValue('--ui').trim()
  ctx.font = `500 11px ${font || 'system-ui, sans-serif'}`
  ctx.textBaseline = 'middle'

  const q = query.value.trim()
  for (const r of rects.value) {
    const x = r.x0 * width.value
    const w = Math.max(1, (r.x1 - r.x0) * width.value - 1)
    const y = r.depth * (ROW_HEIGHT + ROW_GAP)
    // A search dims what does not match rather than filtering it out, so hits
    // keep the context of the stack they sit in.
    const dimmed = !!q && !frameMatches(r.node, q)

    ctx.fillStyle = fillFor(r.node, p, dimmed)
    roundRect(ctx, x, y, w, ROW_HEIGHT, RADIUS)
    ctx.fill()

    if (hovered.value?.key === r.key) {
      ctx.strokeStyle = p.stroke
      ctx.lineWidth = 1.5
      roundRect(ctx, x + 0.75, y + 0.75, w - 1.5, ROW_HEIGHT - 1.5, RADIUS)
      ctx.stroke()
    }

    if (w >= MIN_LABEL_PX) {
      ctx.save()
      ctx.beginPath()
      ctx.rect(x + 5, y, w - 10, ROW_HEIGHT)
      ctx.clip()
      ctx.globalAlpha = dimmed ? 0.4 : 1
      ctx.fillStyle = isLibraryFrame(r.node) ? p.labelMuted : p.label
      ctx.fillText(shortName(r.node.function), x + 6, y + ROW_HEIGHT / 2 + 0.5)
      ctx.restore()
    }
  }
}

function rectAt(px: number, py: number): FlameRect | null {
  const depth = Math.floor(py / (ROW_HEIGHT + ROW_GAP))
  if (depth < 0 || depth >= rows.value) return null
  const frac = px / width.value
  for (const r of rects.value) {
    if (r.depth === depth && frac >= r.x0 && frac < r.x1) return r
  }
  return null
}

function onMove(e: MouseEvent) {
  const canvas = canvasRef.value
  if (!canvas) return
  const box = canvas.getBoundingClientRect()
  // Viewport coordinates: the tooltip is fixed and teleported to the body, so
  // no scroll container can clip it or be stretched by it.
  pointer.value = { x: e.clientX, y: e.clientY }
  const hit = rectAt(e.clientX - box.left, e.clientY - box.top)
  if (hit?.key !== hovered.value?.key) {
    hovered.value = hit
    draw()
  }
}

function onLeave() {
  if (hovered.value) {
    hovered.value = null
    draw()
  }
}

function onClick() {
  const hit = hovered.value
  // Zooming into a leaf fills the view with one box and shows nothing new.
  if (hit && hit.node.children?.length) zoomStack.value.push(hit.node)
}

function resetZoom() {
  zoomStack.value = []
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && zoomStack.value.length) {
    e.preventDefault()
    zoomStack.value.pop()
  }
}

const TIP_W = 280
const TIP_H = 108

const tipStyle = computed(() => {
  const { left, top } = clampTooltip(
    pointer.value.x, pointer.value.y, TIP_W, TIP_H, window.innerWidth, window.innerHeight,
  )
  return { left: `${left}px`, top: `${top}px` }
})

let observer: ResizeObserver | null = null

onMounted(() => {
  if (wrapRef.value) {
    observer = new ResizeObserver(entries => {
      width.value = Math.floor(entries[0].contentRect.width)
      draw()
    })
    observer.observe(wrapRef.value)
    width.value = Math.floor(wrapRef.value.clientWidth)
  }
  window.addEventListener('keydown', onKey)
  draw()
})

onUnmounted(() => {
  observer?.disconnect()
  window.removeEventListener('keydown', onKey)
})

watch([rects, query, height], draw)
watch(() => props.graph, resetZoom)
</script>

<template>
  <div class="flame">
    <div class="flame__bar">
      <Icon name="search" :size="12" class="flame__bar-icon" />
      <input
        v-model="query"
        class="flame__input"
        type="search"
        placeholder="Find a frame"
        aria-label="Find a frame"
      />
      <span v-if="query.trim()" class="flame__count">{{ matchCount }} matched</span>

      <span
        v-if="graph.idle_samples"
        class="flame__stat"
        v-tooltip="'Samples where the thread had an empty stack, so no work was attributed'"
      >{{ graph.idle_samples.toLocaleString() }} idle</span>
      <span
        v-if="!graph.sample_interval_ns"
        class="flame__stat flame__stat--warn"
        v-tooltip="'Too few samples to measure the sampling period, so durations are not shown'"
      >no timings</span>

      <span class="flame__legend">
        <span class="flame__swatch flame__swatch--app" />app
        <span class="flame__swatch flame__swatch--lib" />library
      </span>

      <button
        v-if="zoomStack.length"
        class="span-tree-btn"
        type="button"
        v-tooltip="'Reset zoom (Esc)'"
        @click="resetZoom"
      ><Icon name="maximize-2" :size="10" /></button>
    </div>

    <div v-if="zoomStack.length" class="flame__crumbs">
      <button class="flame__crumb" type="button" @click="resetZoom">All</button>
      <template v-for="(n, i) in zoomStack" :key="i">
        <Icon name="chevron-right" :size="10" class="flame__crumb-sep" />
        <button
          class="flame__crumb"
          type="button"
          @click="zoomStack = zoomStack.slice(0, i + 1)"
        >{{ shortName(n.function) }}</button>
      </template>
    </div>

    <div ref="wrapRef" class="flame__plot">
      <canvas
        ref="canvasRef"
        class="flame__canvas"
        @mousemove="onMove"
        @mouseleave="onLeave"
        @click="onClick"
      />
    </div>

    <!-- Teleported and fixed: inside the plot it would stretch the panel and
         raise scrollbars whenever the cursor neared an edge. -->
    <Teleport to="body">
      <div v-if="hovered" class="flame-tip" :style="tipStyle">
        <div class="flame-tip__fn">{{ hovered.node.function }}</div>
        <div v-if="hovered.node.module" class="flame-tip__sub">{{ hovered.node.module }}</div>
        <div v-if="hovered.node.filename" class="flame-tip__sub">
          {{ hovered.node.filename }}<template v-if="hovered.node.lineno">:{{ hovered.node.lineno }}</template>
        </div>
        <div class="flame-tip__stats">
          <span><em>total</em> {{ msLabel(hovered.node.total_samples) }} · {{ pctOf(hovered.node.total_samples) }}</span>
          <span><em>self</em> {{ msLabel(hovered.node.self_samples) }}</span>
        </div>
        <div v-if="hovered.node.children?.length" class="flame-tip__hint">Click to zoom</div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
/* Frame fills are built in canvas from these, mirroring the app's theme
   cascade so the palette tracks light and dark like everything else. */
.flame {
  --flame-l: 0.62;
  --flame-c: 0.13;
  --flame-muted: oklch(0.32 0.014 285);
  --flame-muted-dim: oklch(0.32 0.014 285 / 0.3);
  --flame-label: oklch(0.13 0.02 285);
  --flame-label-muted: oklch(0.80 0.02 285);
  --flame-stroke: oklch(0.97 0.008 285);
  --flame-dim-alpha: 0.18;
}

:global([data-theme='light']) .flame,
:global(:root:not([data-theme='dark']):not([data-theme='light'])) .flame {
  --flame-l: 0.72;
  --flame-c: 0.12;
  --flame-muted: oklch(0.88 0.008 285);
  --flame-muted-dim: oklch(0.88 0.008 285 / 0.4);
  --flame-label: oklch(0.22 0.02 285);
  --flame-label-muted: oklch(0.45 0.02 285);
  --flame-stroke: oklch(0.28 0.02 285);
  --flame-dim-alpha: 0.22;
}

@media (prefers-color-scheme: dark) {
  :global(:root:not([data-theme='light']):not([data-theme='dark'])) .flame {
    --flame-l: 0.62;
    --flame-c: 0.13;
    --flame-muted: oklch(0.32 0.014 285);
    --flame-muted-dim: oklch(0.32 0.014 285 / 0.3);
    --flame-label: oklch(0.13 0.02 285);
    --flame-label-muted: oklch(0.80 0.02 285);
    --flame-stroke: oklch(0.97 0.008 285);
    --flame-dim-alpha: 0.18;
  }
}

/* Mirrors .trace-search on the waterfall above it. */
.flame__bar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  border: 1px solid var(--border);
  border-radius: 6px 6px 0 0;
  border-bottom-color: var(--border-soft);
  background: var(--bg);
}

.flame__bar-icon {
  color: var(--text-3);
  flex-shrink: 0;
}

.flame__input {
  flex: 1;
  min-width: 80px;
  background: transparent;
  border: none;
  outline: none;
  font-size: var(--text-sm);
  font-family: var(--ui);
  color: var(--text-1);
}

.flame__input::placeholder { color: var(--text-3); }
.flame__input::-webkit-search-cancel-button { display: none; }

.flame__count {
  font-size: var(--text-xs);
  color: var(--text-3);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.flame__stat {
  font-size: var(--text-xs);
  color: var(--text-3);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.flame__stat--warn { color: var(--warning); }

.flame__legend {
  display: flex;
  align-items: center;
  gap: 5px;
  padding-left: 12px;
  border-left: 1px solid var(--border);
  font-size: var(--text-xs);
  color: var(--text-3);
  flex-shrink: 0;
}

.flame__swatch {
  width: 8px;
  height: 8px;
  border-radius: 2px;
  display: inline-block;
}

.flame__swatch--app { background: oklch(var(--flame-l) var(--flame-c) 285); }
.flame__swatch--lib { background: var(--flame-muted); }
.flame__swatch--lib + * { margin-left: 0; }

.flame__crumbs {
  display: flex;
  align-items: center;
  gap: 3px;
  padding: 5px 12px;
  border-left: 1px solid var(--border);
  border-right: 1px solid var(--border);
  border-bottom: 1px solid var(--border-soft);
  background: var(--surface);
  font-size: var(--text-xs);
  overflow-x: auto;
  white-space: nowrap;
}

.flame__crumb {
  background: none;
  border: none;
  padding: 1px 3px;
  border-radius: 3px;
  color: var(--text-3);
  cursor: pointer;
  font-size: var(--text-xs);
  font-family: var(--mono);
}

.flame__crumb:hover { color: var(--text-1); background: var(--surface-2); }
.flame__crumb:last-of-type { color: var(--text-1); }
.flame__crumb-sep { color: var(--text-3); flex-shrink: 0; }

.flame__plot {
  padding: 10px;
  border: 1px solid var(--border);
  border-top: none;
  border-radius: 0 0 6px 6px;
  background: var(--surface);
  overflow: hidden;
}

.flame__canvas {
  display: block;
  cursor: pointer;
}
</style>

<style>
/* Unscoped: the tooltip is teleported to the body. Matches .v-tooltip so it
   reads as the same object as every other tooltip in the app. */
.flame-tip {
  position: fixed;
  z-index: 9999;
  pointer-events: none;
  width: 280px;
  padding: 7px 10px;
  border-radius: 5px;
  background: var(--tooltip-bg, oklch(0.18 0.01 250));
  color: var(--tooltip-fg, oklch(0.96 0 0));
  font-family: var(--ui);
  font-size: 11px;
  line-height: 1.5;
  box-shadow: 0 2px 8px oklch(0 0 0 / 0.35), 0 0 0 1px oklch(1 0 0 / 0.06);
}

.flame-tip__fn {
  font-family: var(--mono);
  word-break: break-all;
  margin-bottom: 2px;
}

.flame-tip__sub {
  color: oklch(0.96 0 0 / 0.55);
  word-break: break-all;
}

.flame-tip__stats {
  display: flex;
  gap: 12px;
  margin-top: 5px;
  padding-top: 5px;
  border-top: 1px solid oklch(1 0 0 / 0.1);
  font-variant-numeric: tabular-nums;
}

.flame-tip__stats em {
  font-style: normal;
  color: oklch(0.96 0 0 / 0.5);
  margin-right: 3px;
}

.flame-tip__hint {
  margin-top: 4px;
  color: oklch(0.96 0 0 / 0.45);
}
</style>
