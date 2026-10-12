<script setup lang="ts">
import { useConfirm } from '@/composables/useConfirm'
import ModalDialog from './ModalDialog.vue'

const { pending, answer } = useConfirm()
</script>

<template>
  <Teleport to="body">
    <ModalDialog v-if="pending" aria-labelledby="confirm-dialog-title" @close="answer(false)">
      <div class="shortcuts-overlay" @mousedown.self.prevent="answer(false)">
        <div class="confirm-dialog">
          <div id="confirm-dialog-title" class="confirm-dialog__title">{{ pending.title }}</div>
          <p v-if="pending.message" class="confirm-dialog__message">{{ pending.message }}</p>
          <div class="confirm-dialog__actions">
            <button class="btn btn--ghost" autofocus @click="answer(false)">Cancel</button>
            <button class="btn" :class="pending.danger ? 'btn--danger' : 'btn--primary'" @click="answer(true)">
              {{ pending.confirmLabel ?? 'Confirm' }}
            </button>
          </div>
        </div>
      </div>
    </ModalDialog>
  </Teleport>
</template>
