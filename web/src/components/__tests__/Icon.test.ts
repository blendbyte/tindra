import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import Icon from '../Icon.vue'

const knownIcons = [
  'check', 'chevron-down', 'chevron-right', 'chevron-left', 'chevrons-left',
  'braces', 'chevrons-right', 'arrow-left', 'arrow-right', 'search', 'cog',
  'log-out', 'plus', 'more', 'x', 'copy', 'sun', 'moon', 'bell', 'package',
  'shield', 'shield-check', 'external', 'pause', 'play', 'users', 'send',
  'user', 'mail', 'tag', 'alert-circle', 'git-commit', 'corner-return',
  'pencil', 'trash', 'globe', 'github', 'more-horizontal', 'trash-2',
  'check-circle', 'info', 'key-round', 'shield-off', 'refresh-cw', 'zap',
  'clock', 'database', 'activity', 'download', 'credit-card', 'file-text',
  'alert-triangle', 'chevrons-down', 'chevrons-up', 'circle-dot',
  'git-commit-vertical', 'key', 'loader',
]

describe('Icon', () => {
  it('renders an SVG element', () => {
    const wrapper = mount(Icon, { props: { name: 'check' } })
    expect(wrapper.find('svg').exists()).toBe(true)
  })

  it('uses size 14 by default', () => {
    const wrapper = mount(Icon, { props: { name: 'check' } })
    const svg = wrapper.find('svg')
    expect(svg.attributes('width')).toBe('14')
    expect(svg.attributes('height')).toBe('14')
  })

  it('applies a custom size prop', () => {
    const wrapper = mount(Icon, { props: { name: 'check', size: 20 } })
    const svg = wrapper.find('svg')
    expect(svg.attributes('width')).toBe('20')
    expect(svg.attributes('height')).toBe('20')
  })

  it('has aria-hidden on the SVG', () => {
    const wrapper = mount(Icon, { props: { name: 'check' } })
    expect(wrapper.find('svg').attributes('aria-hidden')).toBe('true')
  })

  it('renders no child elements inside the SVG for an unknown icon name', () => {
    const wrapper = mount(Icon, { props: { name: 'this-icon-does-not-exist' } })
    expect(wrapper.find('svg').element.childElementCount).toBe(0)
  })

  it.each(knownIcons)('renders inner SVG content for icon "%s"', (name) => {
    const wrapper = mount(Icon, { props: { name } })
    expect(wrapper.find('svg').element.innerHTML.trim().length).toBeGreaterThan(0)
  })
})
