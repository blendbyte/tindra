import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'

vi.mock('vue-router', () => ({
  useRoute: vi.fn(),
}))

import PerformanceSubnav from '../PerformanceSubnav.vue'
import { useRoute } from 'vue-router'

function mountAt(path: string) {
  vi.mocked(useRoute).mockReturnValue({ path } as any)
  return mount(PerformanceSubnav, {
    global: {
      stubs: {
        RouterLink: { template: '<a><slot /></a>' },
      },
    },
  })
}

beforeEach(() => {
  vi.mocked(useRoute).mockReset()
})

describe('PerformanceSubnav', () => {
  it('renders five navigation links', () => {
    const wrapper = mountAt('/performance/transactions')
    expect(wrapper.findAll('.perf-subnav__link')).toHaveLength(5)
  })

  it('renders Transactions, Queries, Caches, Jobs and Browser links', () => {
    const wrapper = mountAt('/performance/transactions')
    const texts = wrapper.findAll('.perf-subnav__link').map((l) => l.text())
    expect(texts).toEqual(['Transactions', 'Queries', 'Caches', 'Jobs', 'Browser'])
  })

  describe('active link', () => {
    it('marks Transactions active on /performance/transactions', () => {
      const wrapper = mountAt('/performance/transactions')
      const links = wrapper.findAll('.perf-subnav__link')
      expect(links[0].classes()).toContain('perf-subnav__link--active')
      expect(links[1].classes()).not.toContain('perf-subnav__link--active')
    })

    it('marks Transactions active on the legacy /transactions path', () => {
      const wrapper = mountAt('/transactions')
      expect(wrapper.findAll('.perf-subnav__link')[0].classes()).toContain('perf-subnav__link--active')
    })

    it('marks Queries active on /performance/queries', () => {
      const wrapper = mountAt('/performance/queries')
      const links = wrapper.findAll('.perf-subnav__link')
      expect(links[1].classes()).toContain('perf-subnav__link--active')
      expect(links[0].classes()).not.toContain('perf-subnav__link--active')
    })

    it('marks Caches active on /performance/caches', () => {
      const wrapper = mountAt('/performance/caches')
      const links = wrapper.findAll('.perf-subnav__link')
      expect(links[2].classes()).toContain('perf-subnav__link--active')
    })

    it('marks Jobs active on /performance/jobs', () => {
      const wrapper = mountAt('/performance/jobs')
      const links = wrapper.findAll('.perf-subnav__link')
      expect(links[3].classes()).toContain('perf-subnav__link--active')
    })

    it('marks Browser active on /performance/browser', () => {
      const wrapper = mountAt('/performance/browser')
      const links = wrapper.findAll('.perf-subnav__link')
      expect(links[4].classes()).toContain('perf-subnav__link--active')
    })

    it('has no active link when on an unrelated path', () => {
      const wrapper = mountAt('/issues')
      const active = wrapper.findAll('.perf-subnav__link--active')
      expect(active).toHaveLength(0)
    })
  })
})
