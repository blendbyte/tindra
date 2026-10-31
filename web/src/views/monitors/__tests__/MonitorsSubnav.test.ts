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

  it('puts page actions in the tab row instead of a second bar', () => {
    const wrapper = mount(MonitorsSubnav, {
      props: { active: 'cron' },
      slots: { default: '<button class="btn btn--primary">New monitor</button>' },
    })
    expect(wrapper.find('.mon-subnav .mon-subnav__actions .btn--primary').text()).toBe('New monitor')
    expect(wrapper.find('.filterbar').exists()).toBe(false)
  })
})
