import { ref } from 'vue'

export interface ConfirmOptions {
  title: string
  message?: string
  confirmLabel?: string
  // danger styles the confirm button for actions that delete data.
  danger?: boolean
}

interface PendingConfirm extends ConfirmOptions {
  resolve: (ok: boolean) => void
}

// One dialog for the whole app, rendered by ConfirmDialog in App.vue.
const pending = ref<PendingConfirm | null>(null)

// useConfirm replaces window.confirm with an in-app dialog that matches the
// theme, traps focus and works with screen readers.
export function useConfirm() {
  function confirm(options: ConfirmOptions): Promise<boolean> {
    // A newer question replaces an unanswered one, which counts as cancelled.
    pending.value?.resolve(false)
    return new Promise((resolve) => {
      pending.value = { ...options, resolve }
    })
  }

  function answer(ok: boolean) {
    const current = pending.value
    pending.value = null
    current?.resolve(ok)
  }

  return { pending, confirm, answer }
}
