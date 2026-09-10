<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import Icon from './Icon.vue'
import { usePopoverPosition } from '@/composables/usePopoverPosition'

const props = defineProps<{
  label: string
  value: string
  options: string[]
  icon?: string
}>()

const emit = defineEmits<{ change: [value: string] }>()

const open = ref(false)
const el = ref<HTMLElement | null>(null)
const menuStyle = usePopoverPosition(open, el, 160)

function onMouseDown(e: MouseEvent) {
  if (el.value && !el.value.contains(e.target as Node)) open.value = false
}
onMounted(() => document.addEventListener('mousedown', onMouseDown))
onUnmounted(() => document.removeEventListener('mousedown', onMouseDown))
</script>

<template>
  <div ref="el" class="filterchip-wrap" style="position: relative">
    <button
      class="filterchip"
      :class="{ 'filterchip--active': value !== options[0] }"
      :aria-label="`${label}: ${value}`"
      @click="open = !open"
      :aria-expanded="open"
      aria-haspopup="true"
      @keydown.esc="open = false"
    >
      <Icon v-if="icon" :name="icon" :size="13" />
      <span v-else class="filterchip__label">{{ label }}:</span>
      <span class="filterchip__value">{{ value }}</span>
      <Icon name="chevron-down" :size="11" />
    </button>
    <div
      v-if="open"
      class="popover"
      :style="menuStyle"
    >
      <div class="popover__list">
        <button
          v-for="opt in options"
          type="button"
          @keydown.esc="open = false"
          :key="opt"
          class="popover__item"
          :class="{ 'popover__item--active': opt === value }"
          @click="emit('change', opt); open = false"
        >
          <span>{{ opt }}</span>
        </button>
      </div>
    </div>
  </div>
</template>
