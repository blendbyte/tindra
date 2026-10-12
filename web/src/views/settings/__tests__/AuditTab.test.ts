import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: vi.fn(), replace: vi.fn() })),
  useRoute: vi.fn(() => ({ params: { tab: 'audit' }, query: {} })),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(),
  useQueryClient: vi.fn(() => ({ invalidateQueries: vi.fn(), setQueryData: vi.fn() })),
}))

vi.mock('@/api/client', () => ({ apiFetch: vi.fn() }))
vi.mock('@/stores/auth', () => ({ useAuthStore: vi.fn() }))
vi.mock('@/utils/formatters', () => ({ formatRel: vi.fn(() => '2m ago') }))

import AuditTab from '../AuditTab.vue'
import { useMutation } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import { mockQueries, queryOptions, stubs } from './harness'

beforeEach(() => {
  vi.mocked(useMutation).mockImplementation(() => ({ mutate: vi.fn(), isPending: ref(false) } as any))
  mockQueries()
})

function mountTab() {
  return mount(AuditTab, { global: { stubs } })
}

const makeAuditRow = (event_type: string) => ({
  id: `a-${event_type}`,
  event_type,
  actor_id: 'u1',
  actor_email: 'admin@example.com',
  ip: '127.0.0.1',
  created_at: '2024-01-01T00:00:00Z',
  metadata: {},
})

describe('AuditTab', () => {
  describe('direct render', () => {
    it('renders audit log empty state when no events', () => {
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('No audit events yet')
    })

    it('renders audit chip filters', () => {
      const wrapper = mountTab()
      expect(wrapper.find('.audit-chip').exists()).toBe(true)
    })

    it('renders audit rows when events exist', () => {
      mockQueries({ audit: [{ ...makeAuditRow('user.login'), id: 'a1' }] })
      const wrapper = mountTab()
      expect(wrapper.find('.audit-row').exists()).toBe(true)
    })
  })

  describe('filter chips', () => {
    it('renders multiple audit filter chips', () => {
      const wrapper = mountTab()
      expect(wrapper.findAll('.audit-chip').length).toBeGreaterThan(1)
    })

    it('activates audit chip when clicked', async () => {
      const wrapper = mountTab()
      const authChip = wrapper.findAll('.audit-chip').find(c => c.text().includes('auth'))!
      await authChip.trigger('click')
      expect(authChip.classes()).toContain('audit-chip--active')
    })
  })

  describe('search', () => {
    it('debounces the search and URL-encodes it', async () => {
      const wrapper = mountTab()
      const options = queryOptions('audit')
      vi.useFakeTimers()
      try {
        await wrapper.find('input[placeholder="Search audit log..."]').setValue('a&b #50% +x')
        expect(options.queryKey.value[1]).not.toContain('q=a')
        await vi.advanceTimersByTimeAsync(300)
      } finally {
        vi.useRealTimers()
      }
      vi.mocked(apiFetch).mockClear()
      await options.queryFn({ signal: new AbortController().signal })
      const url = new URL(vi.mocked(apiFetch).mock.calls[0][0] as string, 'http://localhost')
      expect(url.searchParams.get('q')).toBe('a&b #50% +x')
    })

    it('sends the selected kind in the query key and URL', async () => {
      const wrapper = mountTab()
      const options = queryOptions('audit')
      await wrapper.findAll('.audit-chip').find(c => c.text() === 'token')!.trigger('click')
      expect(options.queryKey.value).toEqual(['audit', 'kind=token&q='])
    })
  })

  describe('all action kinds', () => {
    it.each(['auth.login', 'alert.fired', 'issue.created', 'release.published', 'token.created', 'project.deleted'])(
      'renders audit row with %s event type',
      (eventType) => {
        mockQueries({ audit: [makeAuditRow(eventType)] })
        const wrapper = mountTab()
        expect(wrapper.find('.audit-row').exists()).toBe(true)
        expect(wrapper.find('.audit-row__kind').text()).toBe(eventType.split('.')[0])
      },
    )
  })

  describe('interactions', () => {
    it('renders audit tab with log entries', () => {
      const auditRows = [
        { id: 'a1', event_type: 'auth.login', actor_email: 'admin@example.com', ip: '127.0.0.1', created_at: '2024-01-01T00:00:00Z' },
        { id: 'a2', event_type: 'project.create', actor_email: 'admin@example.com', ip: '127.0.0.1', created_at: '2024-01-02T00:00:00Z' },
        { id: 'a3', event_type: 'token.create', actor_email: 'admin@example.com', ip: '127.0.0.1', created_at: '2024-01-03T00:00:00Z' },
        { id: 'a4', event_type: 'alert.create', actor_email: 'admin@example.com', ip: '127.0.0.1', created_at: '2024-01-04T00:00:00Z' },
        { id: 'a5', event_type: 'issue.resolve', actor_email: null, ip: '127.0.0.1', created_at: '2024-01-05T00:00:00Z' },
        { id: 'a6', event_type: 'release.create', actor_email: 'admin@example.com', ip: '127.0.0.1', created_at: '2024-01-06T00:00:00Z' },
        { id: 'a7', event_type: 'other.thing', actor_email: 'admin@example.com', ip: '127.0.0.1', created_at: '2024-01-07T00:00:00Z' },
      ]
      mockQueries({ audit: auditRows })
      const wrapper = mountTab()
      expect(wrapper.find('.audit-list').exists()).toBe(true)
      const rows = wrapper.findAll('.audit-row')
      expect(rows.length).toBe(7)
      expect(rows[4].find('.audit-row__actor').text()).toBe('system')
      expect(rows[6].find('.audit-row__kind').classes()).toContain('audit-row__kind--other')
    })

    it('clicking audit kind chip changes filter', async () => {
      const wrapper = mountTab()
      const chips = wrapper.findAll('.audit-chip')
      expect(chips.length).toBeGreaterThan(1)
      const authChip = chips.find(c => c.text() === 'auth')!
      await authChip.trigger('click')
      expect(authChip.classes()).toContain('audit-chip--active')
    })

    it('typing in audit search input updates auditSearch', async () => {
      const wrapper = mountTab()
      const searchInput = wrapper.find('input[placeholder="Search audit log..."]')
      await searchInput.setValue('login')
      expect((searchInput.element as HTMLInputElement).value).toBe('login')
    })

    it('renders empty state when auditLog is empty', () => {
      const wrapper = mountTab()
      expect(wrapper.find('.settings-empty').exists()).toBe(true)
    })
  })
})
