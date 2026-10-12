<script setup lang="ts">
import { vClickable } from '@/directives/clickable'
import { ref } from 'vue'
import Icon from '@/components/Icon.vue'
import { loadCollapsed, saveCollapsed } from './collapsedSections'

// IssueSection is a collapsible issue detail section. Its head toggles the
// body by click or keyboard, and the state is remembered per sectionKey.
const props = defineProps<{ sectionKey: string; title: string }>()

const collapsed = ref(loadCollapsed()[props.sectionKey] ?? false)

function toggle() {
  collapsed.value = !collapsed.value
  saveCollapsed(props.sectionKey, collapsed.value)
}
</script>

<template>
  <div class="section">
    <div class="section__head" v-clickable :aria-expanded="!collapsed" @click="toggle">
      <Icon name="chevron-right" :size="12" class="section__chevron" :class="{ 'section__chevron--open': !collapsed }" />
      <h2 class="section__title">{{ title }}</h2>
      <slot name="head" />
    </div>
    <slot v-if="!collapsed" />
  </div>
</template>
