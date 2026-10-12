import type { ObjectDirective } from 'vue'

// Enter and Space activate a native button; give clickable elements the same.
function onKeydown(event: KeyboardEvent) {
  const el = event.currentTarget as HTMLElement
  // Keys typed into a control inside the element belong to that control.
  if (event.target !== el) return
  if (event.key !== 'Enter' && event.key !== ' ') return
  event.preventDefault()
  el.click()
}

// v-clickable makes an element with a click handler usable from the keyboard:
// it becomes focusable, Enter or Space clicks it, and screen readers hear a
// button. Table rows keep their row role so the table still reads as a table.
// Use a real <button> where the markup allows it; this is for rows and cards
// whose layout does not.
export const vClickable: ObjectDirective<HTMLElement> = {
  mounted(el) {
    if (el.tagName !== 'TR' && !el.hasAttribute('role')) el.setAttribute('role', 'button')
    if (!el.hasAttribute('tabindex')) el.tabIndex = 0
    el.addEventListener('keydown', onKeydown)
  },
  beforeUnmount(el) {
    el.removeEventListener('keydown', onKeydown)
  },
}
