import type { Directive } from 'vue'

// Keeps the active tab of a horizontally scrolling tab bar in view, so on a
// phone the selected tab is never hidden past the edge. The binding value is
// the selector of the active tab inside the bar.
function reveal(el: HTMLElement, selector: string) {
  if (el.scrollWidth <= el.clientWidth) return
  const active = el.querySelector<HTMLElement>(selector)
  if (!active) return
  const target = active.offsetLeft - (el.clientWidth - active.offsetWidth) / 2
  el.scrollLeft = Math.max(0, Math.min(target, el.scrollWidth - el.clientWidth))
}

export const vActiveInView: Directive<HTMLElement, string> = {
  mounted: (el, binding) => reveal(el, binding.value),
  updated: (el, binding) => reveal(el, binding.value),
}
