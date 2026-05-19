<script setup lang="ts">
defineProps<{
  visible: boolean
  mouseX: number
  mouseY: number
  time: string
  lines: { label: string; value: string }[]
}>()
</script>

<template>
  <Teleport to="body">
    <Transition name="chart-tip">
      <div
        v-if="visible"
        class="chart-tip"
        :style="{ left: mouseX + 'px', top: mouseY + 'px' }"
      >
        <span class="chart-tip__time">{{ time }}</span>
        <div v-for="line in lines" :key="line.label" class="chart-tip__row">
          <span class="chart-tip__label">{{ line.label }}</span>
          <span class="chart-tip__value">{{ line.value }}</span>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
