<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import type { FlameGraph, FlameNode } from '@/api/types'
import {
  layoutFlame,
  maxDepth,
  samplesToMs,
  frameColor,
  frameMatches,
  type FlameRect,
} from '@/utils/flamegraph'
import Icon from '@/components/Icon.vue'

const props = defineProps<{ graph: FlameGraph }>()

const ROW_HEIGHT = 18
const MIN_LABEL_PX = 34

const canvasRef = ref<HTMLCanvasElement | null>(null)
const wrapRef = ref<HTMLElement | null>(null)
const width = ref(0)
const query = ref('')
const hovered = ref<FlameRect | null>(null)
const pointer = ref({ x: 0, y: 0 })

// Zoom is a stack so Escape and the breadcrumb can walk back out one level at
// a time rather than jumping straight to the top.
const zoomStack = ref<FlameNode[]>([])
const focus = computed(() => zoomStack.value[zoomStack.value.length - 1] ?? null)

const rects = computed(() =>
  focus.value
    ? layoutFlame(focus.value, { drawRoot: true })
    : layoutFlame(props.graph.root)
)

const rows = computed(() => maxDepth(rects.value) + 1)
const height = computed(() => Math.max(ROW_HEIGHT, rows.value * ROW_HEIGHT))

function msLabel(samples: number): string {
  const ms = samplesToMs(samples, props.graph.sample_interval_ns)
  if (ms === null) return `${samples} samples`
  return ms >= 1 ? `${ms.toFixed(ms >= 10 ? 0 : 1)}ms` : `${(ms * 1000).toFixed(0)}µs`
}

function pctOf(samples: number): string {
  const total = props.graph.sample_count || 1
  return `${((samples / total) * 100).toFixed(1)}%`
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
  ctx.font = '11px ui-sans-serif, system-ui, sans-serif'
  ctx.textBaseline = 'middle'

  const q = query.value.trim()
  for (const r of rects.value) {
    const x = r.x0 * width.value
    const w = Math.max(1, (r.x1 - r.x0) * width.value)
    const y = r.depth * ROW_HEIGHT
    // A search dims everything that does not match, so the hits stay legible
    // in place instead of being pulled out of context.
    const dimmed = !!q && !frameMatches(r.node, q)

    ctx.fillStyle = frameColor(r.node.function, r.node.in_app, dimmed)
    ctx.fillRect(x, y, w - 1, ROW_HEIGHT - 1)

    if (hovered.value?.key === r.key) {
      ctx.strokeStyle = 'rgba(255,255,255,0.85)'
      ctx.lineWidth = 1
      ctx.strokeRect(x + 0.5, y + 0.5, w - 2, ROW_HEIGHT - 2)
    }

    if (w >= MIN_LABEL_PX) {
      ctx.save()
      ctx.beginPath()
      ctx.rect(x + 3, y, w - 7, ROW_HEIGHT)
      ctx.clip()
      ctx.fillStyle = dimmed ? 'rgba(0,0,0,0.35)' : 'rgba(0,0,0,0.82)'
      ctx.fillText(r.node.function, x + 4, y + ROW_HEIGHT / 2)
      ctx.restore()
    }
  }
}

function rectAt(px: number, py: number): FlameRect | null {
  const depth = Math.floor(py / ROW_HEIGHT)
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
  pointer.value = { x: e.clientX - box.left, y: e.clientY - box.top }
  const hit = rectAt(pointer.value.x, pointer.value.y)
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
  // Zooming into a leaf would fill the view with a single box and show nothing
  // new, so only frames with callees are worth descending into.
  if (hit && hit.node.children?.length) zoomStack.value.push(hit.node)
}

function zoomOut() {
  zoomStack.value.pop()
}

function resetZoom() {
  zoomStack.value = []
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && zoomStack.value.length) {
    e.preventDefault()
    zoomOut()
  }
}

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
    <div class="flame__toolbar">
      <div class="flame__meta">
        <span>{{ graph.sample_count.toLocaleString() }} samples</span>
        <span v-if="graph.thread_name">{{ graph.thread_name }}</span>
        <span v-if="graph.idle_samples" v-tooltip="'Samples where the thread had an empty stack'">
          {{ graph.idle_samples.toLocaleString() }} idle
        </span>
        <span v-if="!graph.sample_interval_ns" v-tooltip="'Too few samples to measure the sampling period'">
          timings unavailable
        </span>
      </div>

      <div class="flame__controls">
        <input
          v-model="query"
          class="flame__search"
          type="search"
          placeholder="Find function"
          aria-label="Find function"
        />
        <button
          v-if="zoomStack.length"
          class="flame__btn"
          type="button"
          @click="resetZoom"
        >
          <Icon name="x" :size="11" /> Reset zoom
        </button>
      </div>
    </div>

    <div v-if="zoomStack.length" class="flame__crumbs">
      <button class="flame__crumb" type="button" @click="resetZoom">All</button>
      <template v-for="(n, i) in zoomStack" :key="i">
        <span class="flame__crumb-sep">/</span>
        <button
          class="flame__crumb"
          type="button"
          @click="zoomStack = zoomStack.slice(0, i + 1)"
        >{{ n.function }}</button>
      </template>
    </div>

    <div ref="wrapRef" class="flame__canvas-wrap">
      <canvas
        ref="canvasRef"
        class="flame__canvas"
        @mousemove="onMove"
        @mouseleave="onLeave"
        @click="onClick"
      />
      <div
        v-if="hovered"
        class="flame__tip"
        :style="{
          left: `${Math.min(pointer.x + 12, width - 260)}px`,
          top: `${pointer.y + 16}px`,
        }"
      >
        <div class="flame__tip-fn">{{ hovered.node.function }}</div>
        <div v-if="hovered.node.module" class="flame__tip-sub">{{ hovered.node.module }}</div>
        <div v-if="hovered.node.filename" class="flame__tip-sub">
          {{ hovered.node.filename }}<template v-if="hovered.node.lineno">:{{ hovered.node.lineno }}</template>
        </div>
        <div class="flame__tip-stats">
          <span>Total {{ msLabel(hovered.node.total_samples) }} ({{ pctOf(hovered.node.total_samples) }})</span>
          <span>Self {{ msLabel(hovered.node.self_samples) }}</span>
        </div>
      </div>
    </div>

    <p class="flame__hint">
      Click a frame to zoom in<template v-if="zoomStack.length">, Escape to go back</template>.
      Width is time on the call path; muted frames are outside your application.
    </p>
  </div>
</template>

<style scoped>
.flame {
  border: 1px solid var(--border);
  border-radius: 6px;
  overflow: hidden;
}

.flame__toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 7px 10px;
  border-bottom: 1px solid var(--border-soft);
  flex-wrap: wrap;
}

.flame__meta {
  display: flex;
  gap: 12px;
  font-size: var(--text-xs);
  color: var(--text-3);
}

.flame__controls {
  display: flex;
  align-items: center;
  gap: 6px;
}

.flame__search {
  font-size: var(--text-xs);
  padding: 3px 7px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--bg);
  color: var(--text-1);
  width: 150px;
}

.flame__btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: var(--text-xs);
  padding: 3px 7px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--surface);
  color: var(--text-2);
  cursor: pointer;
}

.flame__btn:hover {
  color: var(--text-1);
}

.flame__crumbs {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 5px 10px;
  border-bottom: 1px solid var(--border-soft);
  font-size: var(--text-xs);
  overflow-x: auto;
  white-space: nowrap;
}

.flame__crumb {
  background: none;
  border: none;
  padding: 0;
  color: var(--accent);
  cursor: pointer;
  font-size: var(--text-xs);
}

.flame__crumb-sep {
  color: var(--text-3);
}

.flame__canvas-wrap {
  position: relative;
  padding: 6px;
  overflow-x: hidden;
}

.flame__canvas {
  display: block;
  cursor: pointer;
}

.flame__tip {
  position: absolute;
  z-index: 10;
  pointer-events: none;
  max-width: 260px;
  padding: 6px 8px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--surface-2);
  box-shadow: 0 4px 12px rgb(0 0 0 / 0.18);
  font-size: var(--text-xs);
}

.flame__tip-fn {
  font-family: var(--mono);
  color: var(--text-1);
  word-break: break-all;
}

.flame__tip-sub {
  color: var(--text-3);
  word-break: break-all;
}

.flame__tip-stats {
  display: flex;
  gap: 10px;
  margin-top: 4px;
  color: var(--text-2);
}

.flame__hint {
  margin: 0;
  padding: 6px 10px;
  border-top: 1px solid var(--border-soft);
  font-size: var(--text-xs);
  color: var(--text-3);
}
</style>
