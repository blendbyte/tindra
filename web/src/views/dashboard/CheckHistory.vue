<script setup lang="ts">
import { computed } from 'vue'
import type { CheckState } from './briefing'

const props = defineProps<{ history: CheckState[] }>()

const SLOTS = 20
// Newest on the right; missing checks stay empty on the left.
const slots = computed(() => {
  const recent = props.history.slice(-SLOTS)
  return [...Array(SLOTS - recent.length).fill('empty'), ...recent] as string[]
})
</script>

<template>
  <span class="check-history" role="img" :aria-label="`Last ${history.length} checks`">
    <i v-for="(s, i) in slots" :key="i" :class="`check-history__tick--${s}`" />
  </span>
</template>

<style scoped>
.check-history { display: flex; gap: 2px; width: 100%; height: 18px; }
.check-history i { flex: 1; height: 100%; border-radius: 1px; background: var(--border-soft); }
.check-history .check-history__tick--ok { background: oklch(from var(--success) l c h / 0.55); }
.check-history .check-history__tick--bad { background: var(--danger); }
.check-history .check-history__tick--pending { background: var(--warning); }
</style>
