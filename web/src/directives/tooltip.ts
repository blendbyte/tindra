import type { ObjectDirective } from 'vue'

let el: HTMLElement | null = null

function getEl(): HTMLElement {
  if (!el) {
    el = document.createElement('div')
    el.className = 'v-tooltip'
    el.setAttribute('role', 'tooltip')
    el.setAttribute('aria-hidden', 'true')
    document.body.appendChild(el)
  }
  return el
}

function position(trigger: HTMLElement) {
  const t = getEl()
  const tr = trigger.getBoundingClientRect()
  const tw = t.offsetWidth
  const th = t.offsetHeight
  const gap = 7

  let left = tr.left + tr.width / 2 - tw / 2
  left = Math.max(8, Math.min(left, window.innerWidth - tw - 8))

  let top: number
  let placement: string
  if (tr.top - th - gap < 8) {
    top = tr.bottom + gap
    placement = 'bottom'
  } else {
    top = tr.top - th - gap
    placement = 'top'
  }

  t.style.left = `${left}px`
  t.style.top = `${top}px`
  t.dataset.placement = placement
}

function show(trigger: HTMLElement, text: string) {
  if (!text) return
  const t = getEl()
  t.textContent = text
  t.classList.add('v-tooltip--visible')
  position(trigger)
}

function hide() {
  getEl().classList.remove('v-tooltip--visible')
}

// WeakMap keeps handler refs without polluting element properties
const map = new WeakMap<HTMLElement, { enter: () => void; leave: () => void }>()

export const vTooltip: ObjectDirective<HTMLElement, string | undefined> = {
  mounted(trigger, binding) {
    const enter = () => { if (binding.value) show(trigger, binding.value) }
    const leave = () => hide()
    map.set(trigger, { enter, leave })
    trigger.addEventListener('mouseenter', enter)
    trigger.addEventListener('mouseleave', leave)
    trigger.addEventListener('focus', enter)
    trigger.addEventListener('blur', leave)
    document.addEventListener('scroll', hide, { passive: true, capture: true })
  },
  updated(trigger, binding) {
    // keep enter closure up-to-date when text changes
    const prev = map.get(trigger)
    if (!prev) return
    const enter = () => { if (binding.value) show(trigger, binding.value) }
    trigger.removeEventListener('mouseenter', prev.enter)
    trigger.removeEventListener('focus', prev.enter)
    trigger.addEventListener('mouseenter', enter)
    trigger.addEventListener('focus', enter)
    map.set(trigger, { enter, leave: prev.leave })
  },
  unmounted(trigger) {
    const h = map.get(trigger)
    if (!h) return
    trigger.removeEventListener('mouseenter', h.enter)
    trigger.removeEventListener('mouseleave', h.leave)
    trigger.removeEventListener('focus', h.enter)
    trigger.removeEventListener('blur', h.leave)
    map.delete(trigger)
    hide()
  },
}
