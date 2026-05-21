import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

const pushMock = vi.fn()

vi.mock('vue-router', () => ({
  useRoute: vi.fn(() => ({ params: { id: 'rel-123' } })),
  useRouter: vi.fn(() => ({ push: pushMock })),
  RouterLink: { template: '<a><slot /></a>' },
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
}))

vi.mock('@/stores/projects', () => ({
  useProjectsStore: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

vi.mock('@/utils/formatters', () => ({
  formatDuration: vi.fn((n: number) => `${n}ms`),
  formatRel: vi.fn(() => '2m ago'),
}))

import ReleaseDetailView from '../ReleaseDetailView.vue'
import { useQuery } from '@tanstack/vue-query'
import { useProjectsStore } from '@/stores/projects'

const stubs = {
  RouterLink: { template: '<a><slot /></a>' },
  Icon: { template: '<span />' },
}

const baseRelease = {
  id: 'rel-123',
  version: 'v1.2.3',
  project_id: 'proj-1',
  deployed_at: '2024-01-01T00:00:00Z',
  tx_count: 50,
  tx_p50: 120,
  tx_p95: 450,
  tx_error_rate: 0,
  new_issues: 0,
  regressed_issues: 0,
}

function setupMocks(
  release: unknown = baseRelease,
  isLoading = false,
  isError = false,
  issues: unknown[] = [],
  transactions: unknown[] = [],
) {
  vi.mocked(useProjectsStore).mockReturnValue({
    projects: [{ id: 'proj-1', name: 'App' }],
    selectedIds: [],
  } as any)

  vi.mocked(useQuery)
    .mockReturnValueOnce({ data: ref(release), isError: ref(isError), isLoading: ref(isLoading) } as any)
    .mockReturnValueOnce({ data: ref(issues) } as any)
    .mockReturnValueOnce({ data: ref(transactions) } as any)
}

beforeEach(() => {
  vi.mocked(useQuery).mockReset()
  vi.mocked(useProjectsStore).mockReset()
  pushMock.mockReset()
})

describe('ReleaseDetailView', () => {
  describe('error state', () => {
    it('shows "Release not found" when loading fails', () => {
      setupMocks(undefined, false, true)
      const wrapper = mount(ReleaseDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Release not found')
    })

    it('shows back to releases link on error', () => {
      setupMocks(undefined, false, true)
      const wrapper = mount(ReleaseDetailView, { global: { stubs } })
      expect(wrapper.find('.detail-breadcrumb__back').exists()).toBe(true)
    })
  })

  describe('loading skeleton', () => {
    it('renders skeleton state while loading', () => {
      setupMocks(undefined, true, false)
      const wrapper = mount(ReleaseDetailView, { global: { stubs } })
      expect(wrapper.find('.skel').exists()).toBe(true)
    })
  })

  describe('loaded release', () => {
    it('shows the version in the breadcrumb', () => {
      setupMocks()
      const wrapper = mount(ReleaseDetailView, { global: { stubs } })
      expect(wrapper.find('.detail-breadcrumb__title').text()).toBe('v1.2.3')
    })

    it('shows transaction stats', () => {
      setupMocks()
      const wrapper = mount(ReleaseDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('Transactions')
      expect(wrapper.text()).toContain('P50')
    })

    it('renders the Transactions and Issues tabs', () => {
      setupMocks()
      const wrapper = mount(ReleaseDetailView, { global: { stubs } })
      const tabs = wrapper.findAll('.optab')
      expect(tabs.some(t => t.text().includes('Transactions'))).toBe(true)
      expect(tabs.some(t => t.text().includes('Issues'))).toBe(true)
    })

    it('shows transactions tab content by default', () => {
      setupMocks(baseRelease, false, false, [], [])
      const wrapper = mount(ReleaseDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('No transactions recorded for this release')
    })

    it('switches to issues tab on click', async () => {
      setupMocks()
      const wrapper = mount(ReleaseDetailView, { global: { stubs } })
      const issuesTab = wrapper.findAll('.optab').find(t => t.text().includes('Issues'))!
      await issuesTab.trigger('click')
      expect(wrapper.text()).toContain('No issues recorded for this release')
    })

    it('renders transactions when present', () => {
      const tx = { transaction: '/api/users', op: 'http.server', sample_count: 10, p50: 100, p95: 300, error_rate: 0 }
      setupMocks(baseRelease, false, false, [], [tx])
      const wrapper = mount(ReleaseDetailView, { global: { stubs } })
      expect(wrapper.text()).toContain('/api/users')
    })

    it('renders new issues when present', async () => {
      const issue = { id: 'iss-1', title: 'TypeError: foo', level: 'error', category: 'new', event_count: 3, last_seen: '2024-01-01' }
      setupMocks(baseRelease, false, false, [issue], [])
      const wrapper = mount(ReleaseDetailView, { global: { stubs } })
      const issuesTab = wrapper.findAll('.optab').find(t => t.text().includes('Issues'))!
      await issuesTab.trigger('click')
      expect(wrapper.find('.rel-category-badge--new').exists()).toBe(true)
      expect(wrapper.text()).toContain('TypeError: foo')
    })

    it('back link navigates to /releases', async () => {
      setupMocks()
      const wrapper = mount(ReleaseDetailView, { global: { stubs } })
      await wrapper.find('.detail-breadcrumb__back').trigger('click')
      expect(pushMock).toHaveBeenCalledWith('/releases')
    })

    it('renders regressed issues section when present', async () => {
      const issue = { id: 'iss-r1', title: 'Regressed error', level: 'error', category: 'regressed', event_count: 2, last_seen: '2024-01-01' }
      setupMocks(baseRelease, false, false, [issue], [])
      const wrapper = mount(ReleaseDetailView, { global: { stubs } })
      const issuesTab = wrapper.findAll('.optab').find(t => t.text().includes('Issues'))!
      await issuesTab.trigger('click')
      expect(wrapper.find('.rel-category-badge--regressed').exists()).toBe(true)
      expect(wrapper.text()).toContain('Regressed error')
    })

    it('navigates to issue when regressed issue row is clicked', async () => {
      const issue = { id: 'iss-r2', title: 'Regressed crash', level: 'error', category: 'regressed', event_count: 1, last_seen: '2024-01-01' }
      setupMocks(baseRelease, false, false, [issue], [])
      const wrapper = mount(ReleaseDetailView, { global: { stubs } })
      const issuesTab = wrapper.findAll('.optab').find(t => t.text().includes('Issues'))!
      await issuesTab.trigger('click')
      const row = wrapper.findAll('.rel-issue-row').find(r => r.text().includes('Regressed crash'))!
      await row.trigger('click')
      expect(pushMock).toHaveBeenCalledWith('/issues/iss-r2')
    })

    it('renders ongoing issues section when present', async () => {
      const issue = { id: 'iss-o1', title: 'Ongoing warning', level: 'warning', category: 'ongoing', event_count: 5, last_seen: '2024-01-01' }
      setupMocks(baseRelease, false, false, [issue], [])
      const wrapper = mount(ReleaseDetailView, { global: { stubs } })
      const issuesTab = wrapper.findAll('.optab').find(t => t.text().includes('Issues'))!
      await issuesTab.trigger('click')
      expect(wrapper.find('.rel-category-badge--ongoing').exists()).toBe(true)
      expect(wrapper.text()).toContain('Ongoing warning')
    })

    it('navigates to issue when ongoing issue row is clicked', async () => {
      const issue = { id: 'iss-o2', title: 'Ongoing slow query', level: 'warning', category: 'ongoing', event_count: 3, last_seen: '2024-01-01' }
      setupMocks(baseRelease, false, false, [issue], [])
      const wrapper = mount(ReleaseDetailView, { global: { stubs } })
      const issuesTab = wrapper.findAll('.optab').find(t => t.text().includes('Issues'))!
      await issuesTab.trigger('click')
      const row = wrapper.findAll('.rel-issue-row').find(r => r.text().includes('Ongoing slow query'))!
      await row.trigger('click')
      expect(pushMock).toHaveBeenCalledWith('/issues/iss-o2')
    })
  })
})
