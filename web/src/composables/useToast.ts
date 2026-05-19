import { ref } from 'vue'

export type ToastType = 'success' | 'error' | 'info'

export interface Toast {
  id: number
  message: string
  type: ToastType
  leaving?: boolean
  onUndo?: () => void
}

const toasts = ref<Toast[]>([])
let nextId = 0

function detectType(message: string): ToastType {
  const lower = message.toLowerCase()
  if (/failed|error|try again|could not|incorrect|invalid/.test(lower)) return 'error'
  if (/copied|saved|created|sent|removed|revoked|changed|updated|enabled|disabled|uploaded|set/.test(lower)) return 'success'
  return 'info'
}

export function useToast() {
  function show(message: string, onUndoOrType?: (() => void) | ToastType, onUndo?: () => void) {
    const id = ++nextId
    let type: ToastType
    let undoFn: (() => void) | undefined

    if (typeof onUndoOrType === 'string') {
      type = onUndoOrType
      undoFn = onUndo
    } else {
      type = detectType(message)
      undoFn = onUndoOrType
    }

    toasts.value.push({ id, message, type, onUndo: undoFn })
    setTimeout(() => dismiss(id), 4000)
  }

  function dismiss(id: number) {
    const t = toasts.value.find((t) => t.id === id)
    if (!t) return
    t.leaving = true
    setTimeout(() => {
      toasts.value = toasts.value.filter((t) => t.id !== id)
    }, 180)
  }

  return { toasts, show, dismiss }
}
