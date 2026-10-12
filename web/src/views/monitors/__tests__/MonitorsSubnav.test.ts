import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'

vi.mock('vue-router', () => ({
  RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' },
}))

import MonitorsSubnav from '../MonitorsSubnav.vue'

describe('MonitorsSubnav', () => {
  it('links both monitor pages', () => {
    const wrapper = mount(MonitorsSubnav, { props: { active: 'cron' } })
    const tabs = wrapper.findAll('.mon-subnav__tab')
    expect(tabs.map(t => [t.text(), t.attributes('href')])).toEqual([
      ['Cron monitors', '/monitors/cron'],
      ['Uptime monitors', '/monitors/uptime'],
    ])
  })

  it.each([
    ['cron', 'Cron monitors'],
    ['uptime', 'Uptime monitors'],
  ] as const)('marks only the %s tab active', (active, label) => {
    const wrapper = mount(MonitorsSubnav, { props: { active } })
    const activeTabs = wrapper.findAll('.mon-subnav__tab--active')
    expect(activeTabs).toHaveLength(1)
    expect(activeTabs[0]!.text()).toBe(label)
  })

  it('renders page actions in the filterbar after the spacer', () => {
    const wrapper = mount(MonitorsSubnav, {
      props: { active: 'cron' },
      slots: { default: '<button class="btn btn--primary">New monitor</button>' },
    })
    const filterbar = wrapper.find('.filterbar')
    expect(filterbar.find('.filterbar__spacer + .btn--primary').text()).toBe('New monitor')
  })

  it('places the filterbar right after the tab bar so it sticks below it', () => {
    const wrapper = mount(MonitorsSubnav, { props: { active: 'uptime' }, attachTo: document.body })
    expect(document.querySelector('.mon-subnav + .filterbar')).not.toBeNull()
    wrapper.unmount()
  })
})
