<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import Icon from './Icon.vue'

const props = defineProps<{
  label: string
  value: string
  options: string[]
}>()

const emit = defineEmits<{ change: [value: string] }>()

const open = ref(false)
const el = ref<HTMLElement | null>(null)

function onMouseDown(e: MouseEvent) {
  if (el.value && !el.value.contains(e.target as Node)) open.value = false
}
onMounted(() => document.addEventListener('mousedown', onMouseDown))
onUnmounted(() => document.removeEventListener('mousedown', onMouseDown))
</script>

<template>
  <div ref="el" class="nav__projects" style="position: relative">
    <button
      class="filterchip"
      :class="{ 'filterchip--active': value !== options[0] }"
      @click="open = !open"
    >
      <span class="filterchip__label">{{ label }}:</span>
      <span class="filterchip__value">{{ value }}</span>
      <Icon name="chevron-down" :size="11" />
    </button>
    <div
      v-if="open"
      class="popover"
      style="left: 0; right: auto; min-width: 160px"
    >
      <div class="popover__list">
        <div
          v-for="opt in options"
          :key="opt"
          class="popover__item"
          :class="{ 'popover__item--active': opt === value }"
          @click="emit('change', opt); open = false"
        >
          <span>{{ opt }}</span>
        </div>
      </div>
    </div>
  </div>
</template>
