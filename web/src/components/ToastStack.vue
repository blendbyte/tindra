<script setup lang="ts">
import { useToast } from '@/composables/useToast'
import Icon from './Icon.vue'

const { toasts, dismiss } = useToast()

const icons = {
  success: 'check-circle',
  error: 'alert-circle',
  info: 'info',
} as const
</script>

<template>
  <div class="toast-stack" aria-live="polite" aria-atomic="false">
    <div
      v-for="t in toasts"
      :key="t.id"
      class="toast"
      :class="[`toast--${t.type}`, { 'toast--out': t.leaving }]"
      role="status"
    >
      <Icon :name="icons[t.type]" :size="15" class="toast__icon" aria-hidden="true" />
      <span class="toast__msg">{{ t.message }}</span>
      <button v-if="t.onUndo" class="toast__undo" @click="() => { t.onUndo!(); dismiss(t.id) }">
        Undo
      </button>
      <button class="toast__close" aria-label="Dismiss" @click="dismiss(t.id)">
        <Icon name="x" :size="11" />
      </button>
    </div>
  </div>
</template>
