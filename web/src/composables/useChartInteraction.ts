import { ref } from 'vue'

export interface ChartSeries {
  id: string
  label: string
  type: 'bar' | 'line'
  values: number[]
  color?: string
  dashed?: boolean
  dimmed?: boolean
}

export const PAD_LEFT = 44

export function useChartInteraction() {
  const mouseX = ref(0)
  const mouseY = ref(0)
  const hovered = ref<number | null>(null)

  function handleMouseMove(e: MouseEvent, n: number, isBar: boolean, cw: number) {
    mouseX.value = e.clientX
    mouseY.value = e.clientY
    if (n === 0 || cw === 0) { hovered.value = null; return }
    const rect = (e.currentTarget as SVGSVGElement).getBoundingClientRect()
    const relX = e.clientX - rect.left - PAD_LEFT
    const idx = isBar
      ? Math.floor(relX * n / cw)
      : Math.round((relX / cw) * (n - 1))
    hovered.value = Math.max(0, Math.min(n - 1, idx))
  }

  function handleMouseLeave() {
    hovered.value = null
  }

  return { mouseX, mouseY, hovered, handleMouseMove, handleMouseLeave }
}
