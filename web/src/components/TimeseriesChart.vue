<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { useTimezone } from '@/composables/useTimezone'
import { useChartInteraction, PAD_LEFT } from '@/composables/useChartInteraction'
import type { ChartSeries } from '@/composables/useChartInteraction'
import ChartTooltip from './ChartTooltip.vue'

const props = withDefaults(defineProps<{
  times: string[]
  series: ChartSeries[]
  bucketSize: '5min' | 'hour' | 'day' | 'week'
  height?: number
  gridLines?: number
  formatValue?: (v: number) => string
  formatTime?: (iso: string) => string
}>(), {
  height: 120,
  gridLines: 3,
})

const tz = useTimezone()

const PAD = { top: 8, right: 8, bottom: 28, left: PAD_LEFT }

const svgRef = ref<SVGSVGElement | null>(null)
const svgWidth = ref(600)

let ro: ResizeObserver | null = null

onMounted(() => {
  if (!svgRef.value) return
  const w = svgRef.value.clientWidth
  if (w > 0) svgWidth.value = w
  ro = new ResizeObserver(entries => {
    const ew = entries[0]?.contentRect.width
    if (ew > 0) svgWidth.value = ew
  })
  ro.observe(svgRef.value)
})

onUnmounted(() => ro?.disconnect())

const CW = computed(() => svgWidth.value - PAD.left - PAD.right)
const CH = computed(() => props.height - PAD.top - PAD.bottom)

const n = computed(() => props.times.length)
const hasBar = computed(() => props.series.some(s => s.type === 'bar'))
const lineSeries = computed(() => props.series.filter(s => s.type === 'line'))

const { mouseX, mouseY, hovered, handleMouseMove, handleMouseLeave } = useChartInteraction()

const maxVal = computed(() => {
  const vals = props.series.flatMap(s => s.values)
  return Math.max(...vals, 1) * 1.15
})

function xPos(i: number): number {
  if (n.value <= 1) return PAD.left + CW.value / 2
  return PAD.left + (i / (n.value - 1)) * CW.value
}

function barCenterX(i: number): number {
  return PAD.left + CW.value * (i + 0.5) / n.value
}

function activeX(i: number): number {
  return hasBar.value ? barCenterX(i) : xPos(i)
}

function yPos(v: number): number {
  return PAD.top + CH.value - (v / maxVal.value) * CH.value
}

const yTicks = computed(() => {
  const count = props.gridLines
  const step = maxVal.value / count
  return Array.from({ length: count + 1 }, (_, k) => {
    const v = step * k
    return { y: yPos(v), label: fmtAxis(v) }
  })
})

function fmtAxis(v: number): string {
  if (props.formatValue) return props.formatValue(v)
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`
  if (v >= 1000) return `${(v / 1000).toFixed(1)}k`
  return String(Math.round(v))
}

function fmtValue(v: number): string {
  if (props.formatValue) return props.formatValue(v)
  return v.toLocaleString()
}

function fmtAxisTime(iso: string): string {
  const d = new Date(iso)
  if (props.bucketSize === 'week' || props.bucketSize === 'day') {
    return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: tz.value })
  }
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false, timeZone: tz.value })
}

function fmtTime(iso: string): string {
  if (props.formatTime) return props.formatTime(iso)
  return fmtAxisTime(iso)
}

const xLabels = computed(() => {
  if (n.value === 0) return []
  const step = Math.max(1, Math.ceil(n.value / 4))
  const indices: number[] = []
  for (let i = 0; i < n.value; i += step) indices.push(i)
  if (indices[indices.length - 1] !== n.value - 1) indices.push(n.value - 1)
  return indices.map(i => ({ x: activeX(i), label: fmtAxisTime(props.times[i]) }))
})

const barData = computed(() => {
  const bw = n.value > 1 ? Math.max(2, CW.value / n.value - 2) : CW.value * 0.5
  return props.series
    .filter(s => s.type === 'bar')
    .map(series => ({
      series,
      bars: series.values.map((v, i) => {
        const barH = (v / maxVal.value) * CH.value
        return {
          x: PAD.left + (i / n.value) * CW.value + 1,
          y: PAD.top + CH.value - barH,
          w: bw,
          h: barH,
          i,
        }
      }),
    }))
})

function polyline(series: ChartSeries): string {
  return series.values.map((v, i) => `${xPos(i)},${yPos(v)}`).join(' ')
}

const tooltipData = computed(() => {
  if (hovered.value === null || n.value === 0) return null
  const i = hovered.value
  return {
    time: fmtTime(props.times[i]),
    lines: props.series.map(s => ({
      label: s.label,
      value: fmtValue(s.values[i] ?? 0),
    })),
  }
})
</script>

<template>
  <svg
    ref="svgRef"
    class="chart-svg"
    :style="{ height: height + 'px' }"
    @mousemove="handleMouseMove($event, n, hasBar, CW)"
    @mouseleave="handleMouseLeave"
  >
    <g class="chart__grid">
      <line
        v-for="tick in yTicks"
        :key="tick.label"
        :x1="PAD.left"
        :x2="svgWidth - PAD.right"
        :y1="tick.y"
        :y2="tick.y"
      />
    </g>

    <g class="chart__ylabels">
      <text
        v-for="tick in yTicks"
        :key="tick.label"
        :x="PAD.left - 6"
        :y="tick.y + 4"
        text-anchor="end"
      >{{ tick.label }}</text>
    </g>

    <g v-for="bd in barData" :key="bd.series.id">
      <rect
        v-for="bar in bd.bars"
        :key="bar.i"
        :x="bar.x"
        :y="bar.y"
        :width="bar.w"
        :height="bar.h"
        :fill="bd.series.color ?? 'var(--accent)'"
        :opacity="hovered === bar.i ? 1 : 0.6"
        class="chart__bar"
      />
    </g>

    <g v-for="s in lineSeries" :key="s.id">
      <polyline
        :points="polyline(s)"
        :stroke="s.color ?? 'var(--accent)'"
        :stroke-dasharray="s.dashed ? '3 2' : undefined"
        :opacity="s.dimmed ? 0.4 : 1"
        class="chart__line"
      />
      <circle
        v-if="hovered !== null"
        :cx="xPos(hovered)"
        :cy="yPos(s.values[hovered] ?? 0)"
        r="3"
        :fill="s.color ?? 'var(--accent)'"
        :opacity="s.dimmed ? 0.5 : 1"
        class="chart__dot"
      />
    </g>

    <line
      v-if="hovered !== null"
      :x1="activeX(hovered)"
      :x2="activeX(hovered)"
      :y1="PAD.top"
      :y2="PAD.top + CH"
      class="chart__crosshair"
    />

    <g class="chart__xlabels">
      <text
        v-for="lbl in xLabels"
        :key="lbl.label"
        :x="lbl.x"
        :y="height - 6"
        text-anchor="middle"
      >{{ lbl.label }}</text>
    </g>
  </svg>

  <ChartTooltip
    :visible="hovered !== null && tooltipData !== null"
    :mouse-x="mouseX"
    :mouse-y="mouseY"
    :time="tooltipData?.time ?? ''"
    :lines="tooltipData?.lines ?? []"
  />
</template>
