<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    data: number[]
    color?: string
    width?: number
    height?: number
  }>(),
  { color: 'currentColor', width: 36, height: 14 },
)

const points = computed(() => {
  if (!props.data || props.data.length <= 1) return ''
  const max = Math.max(...props.data, 1)
  const stepX = props.width / (props.data.length - 1)
  return props.data
    .map((v, i) => {
      const x = i * stepX
      const y = props.height - (v / max) * (props.height - 1) - 0.5
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
})
</script>

<template>
  <svg
    v-if="data && data.length > 1"
    class="spark"
    :width="width"
    :height="height"
    :viewBox="`0 0 ${width} ${height}`"
    :style="{ color }"
  >
    <polyline
      :points="points"
      fill="none"
      stroke="currentColor"
      stroke-width="1.2"
      stroke-linejoin="round"
    />
  </svg>
</template>
