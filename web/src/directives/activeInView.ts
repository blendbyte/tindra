import type { Directive } from 'vue'

// Keeps the active tab of a horizontally scrolling tab bar in view, so on a
// phone the selected tab is never hidden past the edge. The binding value is
// the selector of the active tab inside the bar. It scrolls only when the
// active tab changes, so other re-renders never undo a hand scroll.
const revealed = new WeakMap<HTMLElement, HTMLElement>()

function reveal(el: HTMLElement, selector: string) {
  const active = el.querySelector<HTMLElement>(selector)
  if (!active || revealed.get(el) === active) return
  revealed.set(el, active)
  if (el.scrollWidth <= el.clientWidth) return
  // Measured against the bar itself: offsetLeft would count from whichever
  // ancestor is positioned, not necessarily the bar.
  const tab = active.getBoundingClientRect()
  const left = tab.left - el.getBoundingClientRect().left + el.scrollLeft
  const target = left - (el.clientWidth - tab.width) / 2
  el.scrollLeft = Math.max(0, Math.min(target, el.scrollWidth - el.clientWidth))
}

export const vActiveInView: Directive<HTMLElement, string> = {
  mounted: (el, binding) => reveal(el, binding.value),
  updated: (el, binding) => reveal(el, binding.value),
}
