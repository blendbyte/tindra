import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { vActiveInView } from '../activeInView'

function bar(active: number) {
  const wrapper = mount({
    directives: { activeInView: vActiveInView },
    props: ['active'],
    template: `<nav v-active-in-view="'.on'"><a v-for="i in 5" :key="i" :class="{ on: i === active }">Tab {{ i }}</a></nav>`,
  }, { props: { active }, attachTo: document.body })
  return wrapper
}

// The bar sits 40px from the viewport edge and scrolls; each tab reports its
// position on screen, so it moves left as the bar scrolls.
function layout(el: HTMLElement, { clientWidth, scrollWidth, tabWidth }: { clientWidth: number; scrollWidth: number; tabWidth: number }) {
  const barLeft = 40
  Object.defineProperty(el, 'clientWidth', { configurable: true, value: clientWidth })
  Object.defineProperty(el, 'scrollWidth', { configurable: true, value: scrollWidth })
  el.getBoundingClientRect = () => ({ left: barLeft, width: clientWidth }) as DOMRect
  ;[...el.children].forEach((tab, i) => {
    ;(tab as HTMLElement).getBoundingClientRect = () => ({ left: barLeft + i * tabWidth - el.scrollLeft, width: tabWidth }) as DOMRect
  })
}

describe('vActiveInView', () => {
  it('scrolls an overflowing bar so the active tab is centred', async () => {
    const wrapper = bar(1)
    const el = wrapper.element as HTMLElement
    layout(el, { clientWidth: 200, scrollWidth: 500, tabWidth: 100 })
    await wrapper.setProps({ active: 4 })
    // Tab 4 starts at 300; centring a 100px tab in 200px puts scrollLeft at 250.
    expect(el.scrollLeft).toBe(250)
    wrapper.unmount()
  })

  it('measures from the current scroll position and leaves a hand scroll alone on re-render', async () => {
    const wrapper = bar(1)
    const el = wrapper.element as HTMLElement
    layout(el, { clientWidth: 200, scrollWidth: 500, tabWidth: 100 })
    el.scrollLeft = 120
    await wrapper.setProps({ active: 3 })
    // Tab 3 starts at 200 in the bar; centred, scrollLeft is 150 whatever it was.
    expect(el.scrollLeft).toBe(150)
    el.scrollLeft = 0
    await wrapper.setProps({ active: 3, extra: 1 })
    expect(el.scrollLeft).toBe(0)
    wrapper.unmount()
  })

  it('leaves a bar that fits alone', async () => {
    const wrapper = bar(1)
    const el = wrapper.element as HTMLElement
    layout(el, { clientWidth: 600, scrollWidth: 500, tabWidth: 100 })
    await wrapper.setProps({ active: 5 })
    expect(el.scrollLeft).toBe(0)
    wrapper.unmount()
  })
})
