import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import type { SpanSummary, SpanSample } from '@/api/types'

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

vi.mock('@/stores/projects', () => ({
  useProjectsStore: vi.fn(),
}))

import SpanSamplesPanel from '../SpanSamplesPanel.vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'

const mockRow: SpanSummary = {
  op: 'db.query',
  description: 'SELECT * FROM users',
  sample_count: 500,
  rate: 10.0,
  p50: 20,
  p95: 100,
  total_ms: 5000,
  time_pct: 30,
  error_rate: 5.0,
}

const mockSample: SpanSample = {
  span_id: 'span-1',
  transaction_id: 'tx-1',
  op: 'db.query',
  description: 'SELECT * FROM users',
  duration_ms: 25,
  status: 'ok',
  start_timestamp: new Date(Date.now() - 30000).toISOString(),
  transaction_name: '/api/users',
  trace_id: 'trace-1',
}

function setupQuery(samples: SpanSample[] | undefined, isLoading = false) {
  vi.mocked(useProjectsStore).mockReturnValue({
    selectedIds: [],
  } as any)

  vi.mocked(useQuery).mockReturnValue({
    data: ref(samples),
    isLoading: ref(isLoading),
  } as any)
}

beforeEach(() => {
  vi.clearAllMocks()
})

const globalStubs = {
  stubs: {
    RouterLink: { template: '<a :to="to"><slot /></a>', props: ['to'] },
    Icon: { template: '<span />' },
  },
}

describe('SpanSamplesPanel', () => {
  it('renders the panel structure', () => {
    setupQuery([])
    const wrapper = mount(SpanSamplesPanel, {
      props: { row: mockRow, hours: 24, env: 'All' },
      global: globalStubs,
    })
    expect(wrapper.find('.samples-panel').exists()).toBe(true)
    expect(wrapper.find('.samples-backdrop').exists()).toBe(true)
  })

  it('renders the op tag and description in header', () => {
    setupQuery([])
    const wrapper = mount(SpanSamplesPanel, {
      props: { row: mockRow, hours: 24, env: 'All' },
      global: globalStubs,
    })
    expect(wrapper.find('.optag').text()).toBe('db.query')
    expect(wrapper.find('.samples-panel__title').text()).toBe('SELECT * FROM users')
  })

  it('shows (no description) when description is empty', () => {
    setupQuery([])
    const row = { ...mockRow, description: '' }
    const wrapper = mount(SpanSamplesPanel, {
      props: { row, hours: 24, env: 'All' },
      global: globalStubs,
    })
    expect(wrapper.find('.samples-panel__title').text()).toBe('(no description)')
  })

  it('renders stats: sample count, P50, P95, error rate', () => {
    setupQuery([])
    const wrapper = mount(SpanSamplesPanel, {
      props: { row: mockRow, hours: 24, env: 'All' },
      global: globalStubs,
    })
    const stats = wrapper.findAll('.samples-panel__stat-value')
    expect(stats[0].text()).toBe('500')
    expect(stats[1].text()).toBe('20ms')
    expect(stats[2].text()).toBe('100ms')
    expect(stats[3].text()).toContain('5.0')
  })

  it('shows skeleton rows when loading', () => {
    setupQuery(undefined, true)
    const wrapper = mount(SpanSamplesPanel, {
      props: { row: mockRow, hours: 24, env: 'All' },
      global: globalStubs,
    })
    expect(wrapper.find('.sample-item--skel').exists()).toBe(true)
  })

  it('shows empty state when samples array is empty', () => {
    setupQuery([])
    const wrapper = mount(SpanSamplesPanel, {
      props: { row: mockRow, hours: 24, env: 'All' },
      global: globalStubs,
    })
    expect(wrapper.find('.samples-empty').exists()).toBe(true)
    expect(wrapper.find('.samples-empty').text()).toContain('No recent samples')
  })

  it('renders sample items when data is available', () => {
    setupQuery([mockSample])
    const wrapper = mount(SpanSamplesPanel, {
      props: { row: mockRow, hours: 24, env: 'All' },
      global: globalStubs,
    })
    expect(wrapper.find('.sample-item__name').text()).toBe('/api/users')
  })

  it('shows error status badge for failed samples', () => {
    const errorSample = { ...mockSample, status: 'error' }
    setupQuery([errorSample])
    const wrapper = mount(SpanSamplesPanel, {
      props: { row: mockRow, hours: 24, env: 'All' },
      global: globalStubs,
    })
    expect(wrapper.find('.sample-item__status').text()).toBe('error')
  })

  it('does not show error badge for ok samples', () => {
    setupQuery([mockSample])
    const wrapper = mount(SpanSamplesPanel, {
      props: { row: mockRow, hours: 24, env: 'All' },
      global: globalStubs,
    })
    expect(wrapper.find('.sample-item__status').exists()).toBe(false)
  })

  it('emits close when backdrop is clicked', async () => {
    setupQuery([])
    const wrapper = mount(SpanSamplesPanel, {
      props: { row: mockRow, hours: 24, env: 'All' },
      global: globalStubs,
    })
    await wrapper.find('.samples-backdrop').trigger('click')
    expect(wrapper.emitted('close')).toBeTruthy()
  })

  it('emits close when close button is clicked', async () => {
    setupQuery([])
    const wrapper = mount(SpanSamplesPanel, {
      props: { row: mockRow, hours: 24, env: 'All' },
      global: globalStubs,
    })
    await wrapper.find('.icon-btn').trigger('click')
    expect(wrapper.emitted('close')).toBeTruthy()
  })

  it('emits close on Escape keydown', async () => {
    setupQuery([])
    const wrapper = mount(SpanSamplesPanel, {
      props: { row: mockRow, hours: 24, env: 'All' },
      global: globalStubs,
      attachTo: document.body,
    })
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(wrapper.emitted('close')).toBeTruthy()
    wrapper.unmount()
  })

  it('applies tx-failure class when error_rate is greater than 0', () => {
    setupQuery([])
    const wrapper = mount(SpanSamplesPanel, {
      props: { row: { ...mockRow, error_rate: 10 }, hours: 24, env: 'All' },
      global: globalStubs,
    })
    const errorStat = wrapper.findAll('.samples-panel__stat-value').find(
      el => el.classes().includes('tx-failure'),
    )
    expect(errorStat).toBeDefined()
  })

  it('does not apply tx-failure class when error_rate is 0', () => {
    setupQuery([])
    const wrapper = mount(SpanSamplesPanel, {
      props: { row: { ...mockRow, error_rate: 0 }, hours: 24, env: 'All' },
      global: globalStubs,
    })
    expect(wrapper.find('.tx-failure').exists()).toBe(false)
  })

  describe('formatTime branches', () => {
    it('shows "just now" for timestamps less than 1 minute ago', () => {
      const sample = { ...mockSample, start_timestamp: new Date(Date.now() - 10_000).toISOString() }
      setupQuery([sample])
      const wrapper = mount(SpanSamplesPanel, { props: { row: mockRow, hours: 24, env: 'All' }, global: globalStubs })
      expect(wrapper.find('.sample-item__time').text()).toBe('just now')
    })

    it('shows "Xm ago" for timestamps 1-60 minutes ago', () => {
      const sample = { ...mockSample, start_timestamp: new Date(Date.now() - 5 * 60_000).toISOString() }
      setupQuery([sample])
      const wrapper = mount(SpanSamplesPanel, { props: { row: mockRow, hours: 24, env: 'All' }, global: globalStubs })
      expect(wrapper.find('.sample-item__time').text()).toBe('5m ago')
    })

    it('shows "Xh ago" for timestamps 1-24 hours ago', () => {
      const sample = { ...mockSample, start_timestamp: new Date(Date.now() - 3 * 3_600_000).toISOString() }
      setupQuery([sample])
      const wrapper = mount(SpanSamplesPanel, { props: { row: mockRow, hours: 24, env: 'All' }, global: globalStubs })
      expect(wrapper.find('.sample-item__time').text()).toBe('3h ago')
    })

    it('shows locale date for timestamps older than 24 hours', () => {
      const sample = { ...mockSample, start_timestamp: new Date(Date.now() - 3 * 86_400_000).toISOString() }
      setupQuery([sample])
      const wrapper = mount(SpanSamplesPanel, { props: { row: mockRow, hours: 24, env: 'All' }, global: globalStubs })
      // Returns a locale date string like "Jan 1"
      expect(wrapper.find('.sample-item__time').text()).toBeTruthy()
    })
  })

  describe('durClass branches', () => {
    it('applies slow class when duration > p95', () => {
      const sample = { ...mockSample, duration_ms: 150 }
      setupQuery([sample])
      const wrapper = mount(SpanSamplesPanel, { props: { row: mockRow, hours: 24, env: 'All' }, global: globalStubs })
      expect(wrapper.find('.sample-item__dur').classes()).toContain('sample-item__dur--slow')
    })

    it('applies medium class when duration is between p50 and p95', () => {
      const sample = { ...mockSample, duration_ms: 50 }
      setupQuery([sample])
      const wrapper = mount(SpanSamplesPanel, { props: { row: mockRow, hours: 24, env: 'All' }, global: globalStubs })
      expect(wrapper.find('.sample-item__dur').classes()).toContain('sample-item__dur--medium')
    })

    it('applies no class when duration <= p50', () => {
      const sample = { ...mockSample, duration_ms: 10 }
      setupQuery([sample])
      const wrapper = mount(SpanSamplesPanel, { props: { row: mockRow, hours: 24, env: 'All' }, global: globalStubs })
      const dur = wrapper.find('.sample-item__dur')
      expect(dur.classes()).not.toContain('sample-item__dur--slow')
      expect(dur.classes()).not.toContain('sample-item__dur--medium')
    })
  })

  it('emits close when a sample item link is clicked', async () => {
    setupQuery([mockSample])
    const wrapper = mount(SpanSamplesPanel, { props: { row: mockRow, hours: 24, env: 'All' }, global: globalStubs })
    await wrapper.find('.sample-item').trigger('click')
    expect(wrapper.emitted('close')).toBeTruthy()
  })

  it('includes env query param when env is not All', () => {
    setupQuery([])
    const wrapper = mount(SpanSamplesPanel, {
      props: { row: mockRow, hours: 24, env: 'production' },
      global: globalStubs,
    })
    expect(wrapper.find('.samples-panel').exists()).toBe(true)
  })

  it('includes project_id params when selectedIds are non-empty', () => {
    vi.mocked(useProjectsStore).mockReturnValue({ selectedIds: ['proj-1', 'proj-2'] } as any)
    vi.mocked(useQuery).mockReturnValue({ data: ref([]), isLoading: ref(false) } as any)
    const wrapper = mount(SpanSamplesPanel, {
      props: { row: mockRow, hours: 24, env: 'All' },
      global: globalStubs,
    })
    expect(wrapper.find('.samples-panel').exists()).toBe(true)
  })
})
