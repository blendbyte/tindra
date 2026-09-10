<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'

const emit = defineEmits<{ close: []; keydown: [event: KeyboardEvent] }>()
const dialog = ref<HTMLDialogElement | null>(null)
let returnFocus: HTMLElement | null = null

onMounted(() => {
  returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  dialog.value?.showModal()
})

onBeforeUnmount(() => {
  dialog.value?.close()
  if (returnFocus?.isConnected) returnFocus.focus()
})

function onKey(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    emit('close')
    return
  }
  if (event.key === 'Tab') {
    const controls = [...dialog.value!.querySelectorAll<HTMLElement>(
      'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href], [tabindex]',
    )].filter(el => el.tabIndex >= 0 && !el.matches(':disabled') &&
      !el.closest('[hidden], [inert]') && !['hidden', 'collapse'].includes(getComputedStyle(el).visibility) &&
      el.getClientRects().length > 0)
    const current = controls.indexOf(document.activeElement as HTMLElement)
    const next = event.shiftKey
      ? (current <= 0 ? controls.length - 1 : current - 1)
      : (current + 1) % controls.length
    event.preventDefault()
    const target = controls[next] ?? dialog.value
    target?.focus()
    return
  }
  emit('keydown', event)
}
</script>

<template>
  <dialog ref="dialog" class="modal-root" role="dialog" aria-modal="true" tabindex="-1"
    @cancel.prevent="emit('close')" @keydown.stop="onKey">
    <slot />
  </dialog>
</template>

<style scoped>
.modal-root {
  margin: 0;
  padding: 0;
  border: 0;
  width: 100vw;
  height: 100dvh;
  max-width: none;
  max-height: none;
  background: transparent;
  color: inherit;
}
.modal-root::backdrop { background: transparent; }
</style>
