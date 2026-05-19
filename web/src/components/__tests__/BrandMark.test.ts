import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import BrandMark from '../BrandMark.vue'

describe('BrandMark', () => {
  it('renders an SVG element', () => {
    const wrapper = mount(BrandMark)
    expect(wrapper.find('svg').exists()).toBe(true)
  })

  it('defaults to size 18', () => {
    const wrapper = mount(BrandMark)
    const svg = wrapper.find('svg')
    expect(svg.attributes('width')).toBe('18')
    expect(svg.attributes('height')).toBe('18')
  })

  it('applies a custom size prop', () => {
    const wrapper = mount(BrandMark, { props: { size: 32 } })
    const svg = wrapper.find('svg')
    expect(svg.attributes('width')).toBe('32')
    expect(svg.attributes('height')).toBe('32')
  })

  it('sets aria-hidden to true', () => {
    const wrapper = mount(BrandMark)
    expect(wrapper.find('svg').attributes('aria-hidden')).toBe('true')
  })

  it('renders the expected viewBox', () => {
    const wrapper = mount(BrandMark)
    expect(wrapper.find('svg').attributes('viewBox')).toBe('5 1 130 122')
  })

  it('renders gradient defs', () => {
    const wrapper = mount(BrandMark)
    expect(wrapper.find('defs').exists()).toBe(true)
    expect(wrapper.find('#bm-hull').exists()).toBe(true)
    expect(wrapper.find('#bm-top').exists()).toBe(true)
  })

  it('renders multiple polygon shapes', () => {
    const wrapper = mount(BrandMark)
    const polygons = wrapper.findAll('polygon')
    expect(polygons.length).toBeGreaterThan(0)
  })
})
