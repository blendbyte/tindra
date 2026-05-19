import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import Sparkline from '../Sparkline.vue'

describe('Sparkline', () => {
  describe('rendering', () => {
    it('renders nothing for empty data', () => {
      const wrapper = mount(Sparkline, { props: { data: [] } })
      expect(wrapper.find('svg').exists()).toBe(false)
    })

    it('renders an SVG for non-empty data', () => {
      const wrapper = mount(Sparkline, { props: { data: [1, 2, 3] } })
      expect(wrapper.find('svg').exists()).toBe(true)
    })

    it('renders an SVG for a single data point', () => {
      const wrapper = mount(Sparkline, { props: { data: [5] } })
      expect(wrapper.find('svg').exists()).toBe(true)
    })
  })

  describe('dimensions', () => {
    it('defaults to 36×14', () => {
      const wrapper = mount(Sparkline, { props: { data: [1, 2] } })
      const svg = wrapper.find('svg')
      expect(svg.attributes('width')).toBe('36')
      expect(svg.attributes('height')).toBe('14')
    })

    it('applies custom width and height props', () => {
      const wrapper = mount(Sparkline, { props: { data: [1, 2], width: 100, height: 30 } })
      const svg = wrapper.find('svg')
      expect(svg.attributes('width')).toBe('100')
      expect(svg.attributes('height')).toBe('30')
    })

    it('sets a matching viewBox', () => {
      const wrapper = mount(Sparkline, { props: { data: [1, 2], width: 60, height: 20 } })
      expect(wrapper.find('svg').attributes('viewBox')).toBe('0 0 60 20')
    })
  })

  describe('polyline point calculation', () => {
    it('generates correct points for an ascending series', () => {
      // data: [0, 5, 10], w=36, h=14
      // max=10, stepX=18
      // (0, 13.5) (18, 7.0) (36, 0.5)
      const wrapper = mount(Sparkline, { props: { data: [0, 5, 10], width: 36, height: 14 } })
      expect(wrapper.find('polyline').attributes('points')).toBe('0.0,13.5 18.0,7.0 36.0,0.5')
    })

    it('clamps max to 1 to avoid division by zero on all-zero data', () => {
      // All-zero data: max clamped to 1, y = 14 - (0/1)*13 - 0.5 = 13.5 for all points
      const wrapper = mount(Sparkline, { props: { data: [0, 0, 0], width: 36, height: 14 } })
      expect(wrapper.find('polyline').attributes('points')).toBe('0.0,13.5 18.0,13.5 36.0,13.5')
    })

    it('pins the peak value to the top of the chart', () => {
      // For a two-point series [0, max], the last point lands at y=0.5
      const wrapper = mount(Sparkline, { props: { data: [0, 10], width: 36, height: 14 } })
      const points = wrapper.find('polyline').attributes('points') ?? ''
      expect(points.split(' ')[1]).toBe('36.0,0.5')
    })

    it('uses the color prop as the SVG color style', () => {
      const wrapper = mount(Sparkline, { props: { data: [1, 2], color: '#ff0000' } })
      expect(wrapper.find('svg').attributes('style')).toContain('#ff0000')
    })
  })
})
