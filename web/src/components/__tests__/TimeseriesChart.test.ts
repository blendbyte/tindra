import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import type { ChartSeries } from '@/composables/useChartInteraction'

vi.mock('@/composables/useChartInteraction', () => ({
  useChartInteraction: vi.fn(() => ({
    mouseX: { value: 0 },
    mouseY: { value: 0 },
    hovered: { value: null },
    handleMouseMove: vi.fn(),
    handleMouseLeave: vi.fn(),
  })),
  PAD_LEFT: 44,
}))

vi.mock('./ChartTooltip.vue', () => ({
  default: { template: '<div />', props: ['visible', 'mouseX', 'mouseY', 'time', 'lines'] },
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: vi.fn(),
}))

import TimeseriesChart from '../TimeseriesChart.vue'
import { useAuthStore } from '@/stores/auth'

beforeEach(() => {
  vi.mocked(useAuthStore).mockReset()
  vi.mocked(useAuthStore).mockReturnValue({ user: { timezone: 'UTC' }, setUser: vi.fn() } as any)
})

const times = ['2024-01-01T00:00:00Z', '2024-01-01T01:00:00Z', '2024-01-01T02:00:00Z']

const barSeries: ChartSeries = {
  id: 'requests',
  label: 'Requests',
  type: 'bar',
  values: [10, 20, 15],
}

const lineSeries: ChartSeries = {
  id: 'p50',
  label: 'P50',
  type: 'line',
  values: [5, 8, 6],
}

describe('TimeseriesChart', () => {
  it('mounts without errors', () => {
    const wrapper = mount(TimeseriesChart, {
      props: { times, series: [barSeries], bucketSize: 'hour' },
    })
    expect(wrapper.find('svg').exists()).toBe(true)
  })

  it('applies the height style to the SVG', () => {
    const wrapper = mount(TimeseriesChart, {
      props: { times, series: [barSeries], bucketSize: 'hour', height: 200 },
    })
    expect(wrapper.find('svg').attributes('style')).toContain('200px')
  })

  it('defaults height to 120px', () => {
    const wrapper = mount(TimeseriesChart, {
      props: { times, series: [barSeries], bucketSize: 'hour' },
    })
    expect(wrapper.find('svg').attributes('style')).toContain('120px')
  })

  it('renders grid lines', () => {
    const wrapper = mount(TimeseriesChart, {
      props: { times, series: [barSeries], bucketSize: 'hour', gridLines: 3 },
    })
    const gridLines = wrapper.find('.chart__grid').findAll('line')
    expect(gridLines.length).toBe(4)
  })

  it('renders y-axis labels', () => {
    const wrapper = mount(TimeseriesChart, {
      props: { times, series: [barSeries], bucketSize: 'hour' },
    })
    expect(wrapper.find('.chart__ylabels').exists()).toBe(true)
    const labels = wrapper.find('.chart__ylabels').findAll('text')
    expect(labels.length).toBeGreaterThan(0)
  })

  it('renders x-axis labels', () => {
    const wrapper = mount(TimeseriesChart, {
      props: { times, series: [barSeries], bucketSize: 'hour' },
    })
    expect(wrapper.find('.chart__xlabels').exists()).toBe(true)
  })

  it('renders bars for bar series', () => {
    const wrapper = mount(TimeseriesChart, {
      props: { times, series: [barSeries], bucketSize: 'hour' },
    })
    const rects = wrapper.findAll('rect')
    expect(rects.length).toBe(times.length)
  })

  it('renders a polyline for line series', () => {
    const wrapper = mount(TimeseriesChart, {
      props: { times, series: [lineSeries], bucketSize: 'hour' },
    })
    expect(wrapper.find('.chart__line').exists()).toBe(true)
  })

  it('renders both bar and line series together', () => {
    const wrapper = mount(TimeseriesChart, {
      props: { times, series: [barSeries, lineSeries], bucketSize: 'hour' },
    })
    expect(wrapper.findAll('rect')).toHaveLength(times.length)
    expect(wrapper.find('.chart__line').exists()).toBe(true)
  })

  it('uses custom formatValue for y-axis labels', () => {
    const formatValue = (v: number) => `${v}ms`
    const wrapper = mount(TimeseriesChart, {
      props: { times, series: [barSeries], bucketSize: 'hour', formatValue },
    })
    const labels = wrapper.find('.chart__ylabels').findAll('text')
    expect(labels.some(l => l.text().endsWith('ms'))).toBe(true)
  })

  it('renders with an empty times array', () => {
    const wrapper = mount(TimeseriesChart, {
      props: { times: [], series: [{ ...barSeries, values: [] }], bucketSize: 'hour' },
    })
    expect(wrapper.find('svg').exists()).toBe(true)
    expect(wrapper.find('.chart__xlabels').findAll('text')).toHaveLength(0)
  })

  it('renders a dashed polyline for dashed series', () => {
    const dashedSeries: ChartSeries = { ...lineSeries, dashed: true }
    const wrapper = mount(TimeseriesChart, {
      props: { times, series: [dashedSeries], bucketSize: 'hour' },
    })
    const polyline = wrapper.find('.chart__line')
    expect(polyline.attributes('stroke-dasharray')).toBeDefined()
  })

  it('renders a dimmed polyline for dimmed series', () => {
    const dimmedSeries: ChartSeries = { ...lineSeries, dimmed: true }
    const wrapper = mount(TimeseriesChart, {
      props: { times, series: [dimmedSeries], bucketSize: 'hour' },
    })
    const polyline = wrapper.find('.chart__line')
    expect(polyline.attributes('opacity')).toBe('0.4')
  })

  it('formats large values with M suffix in y-axis', () => {
    const bigSeries: ChartSeries = {
      ...barSeries,
      values: [1_500_000, 2_000_000, 1_800_000],
    }
    const wrapper = mount(TimeseriesChart, {
      props: { times, series: [bigSeries], bucketSize: 'day' },
    })
    const labels = wrapper.find('.chart__ylabels').findAll('text')
    expect(labels.some(l => l.text().includes('M'))).toBe(true)
  })

  it('formats values >= 1000 with k suffix in y-axis', () => {
    const kSeries: ChartSeries = {
      ...barSeries,
      values: [1000, 2000, 1500],
    }
    const wrapper = mount(TimeseriesChart, {
      props: { times, series: [kSeries], bucketSize: 'hour' },
    })
    const labels = wrapper.find('.chart__ylabels').findAll('text')
    expect(labels.some(l => l.text().includes('k'))).toBe(true)
  })
})
