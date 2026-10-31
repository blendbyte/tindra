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

function layout(el: HTMLElement, { clientWidth, scrollWidth, tabWidth }: { clientWidth: number; scrollWidth: number; tabWidth: number }) {
  Object.defineProperty(el, 'clientWidth', { configurable: true, value: clientWidth })
  Object.defineProperty(el, 'scrollWidth', { configurable: true, value: scrollWidth })
  ;[...el.children].forEach((tab, i) => {
    Object.defineProperty(tab, 'offsetLeft', { configurable: true, value: i * tabWidth })
    Object.defineProperty(tab, 'offsetWidth', { configurable: true, value: tabWidth })
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

  it('leaves a bar that fits alone', async () => {
    const wrapper = bar(1)
    const el = wrapper.element as HTMLElement
    layout(el, { clientWidth: 600, scrollWidth: 500, tabWidth: 100 })
    await wrapper.setProps({ active: 5 })
    expect(el.scrollLeft).toBe(0)
    wrapper.unmount()
  })
})
