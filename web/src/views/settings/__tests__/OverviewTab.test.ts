import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: vi.fn(), replace: vi.fn() })),
  useRoute: vi.fn(() => ({ params: { tab: 'overview' }, query: {} })),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(),
  useQueryClient: vi.fn(() => ({ invalidateQueries: vi.fn(), setQueryData: vi.fn() })),
}))

vi.mock('@/api/client', () => ({ apiFetch: vi.fn() }))
vi.mock('@/stores/auth', () => ({ useAuthStore: vi.fn() }))
vi.mock('@/utils/formatters', () => ({ formatRel: vi.fn(() => '2m ago') }))

import OverviewTab from '../OverviewTab.vue'
import { useMutation } from '@tanstack/vue-query'
import { adminUser, isEnabled, mockQueries, queryOptions, stubs } from './harness'

beforeEach(() => {
  vi.mocked(useMutation).mockImplementation(() => ({ mutate: vi.fn(), isPending: ref(false) } as any))
  mockQueries()
})

function mountTab() {
  return mount(OverviewTab, { global: { stubs } })
}

const daysAgo = (days: number) => new Date(Date.now() - days * 24 * 60 * 60 * 1000).toISOString()

function usageRow(wrapper: ReturnType<typeof mountTab>, name: string) {
  const row = wrapper.findAll('.overview-usage-row').find(r => r.find('.overview-usage-row__name').text() === name)
  if (!row) throw new Error(`no usage row ${name}`)
  return row
}

function volRow(wrapper: ReturnType<typeof mountTab>, name: string) {
  const row = wrapper.findAll('.overview-vol-row').find(r => r.find('.overview-vol-row__name').exists() && r.find('.overview-vol-row__name').text() === name)
  if (!row) throw new Error(`no volume row ${name}`)
  return row
}

function chip(wrapper: ReturnType<typeof mountTab>, label: string) {
  return wrapper.findAll('.overview-chip').find(c => c.find('.overview-chip__label').text() === label)!.find('.overview-chip__value').text()
}

describe('OverviewTab', () => {
  describe('with health data', () => {
    const healthData = {
      events_total: 1_500_000,
      events_24h: 2500,
      tx_total: 500_000,
      tx_24h: 1000,
      logs_total: 3_000_000,
      logs_24h: 5000,
      oldest_event_at: '2024-01-01T00:00:00Z',
      oldest_tx_at: '2024-02-01T00:00:00Z',
      oldest_log_at: null,
      retention_days: 90,
      db_size_bytes: 1_073_741_824,
      db_path: '/data/tindra.db',
    }

    it('renders health data on overview tab', () => {
      mockQueries({ 'instance-health': healthData })
      const wrapper = mountTab()
      // formatNum is called for health data: 1.5M should appear
      expect(wrapper.text()).toContain('1.5M')
    })

    it('renders data volume rows without an expires column', () => {
      mockQueries({ 'instance-health': healthData })
      const wrapper = mountTab()
      expect(wrapper.find('.overview-vol-row').exists()).toBe(true)
      expect(wrapper.find('.overview-vol-row__expires').exists()).toBe(false)
      expect(wrapper.find('.overview-vol-row--head').text()).not.toContain('Expires')
      // head row and each data row carry five cells
      expect(wrapper.find('.overview-vol-row--head').findAll('span')).toHaveLength(5)
      expect(wrapper.findAll('.overview-vol-row')[1]!.findAll('span')).toHaveLength(5)
    })

    it('renders usage section with settings data', () => {
      const settings = { event_limit: 1_000_000, project_limit: 10, user_limit: 5, billing_url: null }
      mockQueries({ settings })
      const wrapper = mountTab()
      expect(wrapper.find('.overview-usage-row').exists()).toBe(true)
    })
  })

  describe('update badge', () => {
    const base = { version: 'v1.0.0', commit: 'abc123', project_limit: 0, event_limit: 0, user_limit: 0 }
    const withUpdate = {
      ...base,
      update_available: true,
      latest_version: 'v2.0.0',
      release_url: 'https://github.com/blendbyte/tindra/releases/v2.0.0',
    }

    it('shows update badge when update is available', () => {
      mockQueries({ settings: withUpdate })
      const wrapper = mountTab()
      const badge = wrapper.find('.update-avail')
      expect(badge.exists()).toBe(true)
      expect(badge.text()).toContain('v2.0.0')
    })

    it('badge links to release_url', () => {
      mockQueries({ settings: withUpdate })
      const wrapper = mountTab()
      expect(wrapper.find('.update-avail').attributes('href')).toBe(
        'https://github.com/blendbyte/tindra/releases/v2.0.0',
      )
    })

    it('hides update badge when update_available is false', () => {
      mockQueries({ settings: { ...base, update_available: false } })
      const wrapper = mountTab()
      expect(wrapper.find('.update-avail').exists()).toBe(false)
    })

    it('hides update badge when settings are not yet loaded', () => {
      const wrapper = mountTab()
      expect(wrapper.find('.update-avail').exists()).toBe(false)
    })
  })

  describe('usage computed values', () => {
    const settings = { event_limit: 100000, project_limit: 0, user_limit: 0, version: 'v1', commit: '', update_available: false }

    it('shows instanceNearLimit warning when usage >= 80%', () => {
      const projs = [{ id: 'p1', name: 'App', slug: 'app', public_key: 'k', event_count: 85000, event_limit: 0, passthrough_dsn: null }]
      mockQueries({ projects: projs, settings })
      const wrapper = mountTab()
      const fill = usageRow(wrapper, 'Events / month').find('.overview-bar__fill')
      expect(fill.classes()).toContain('overview-bar__fill--warn')
      expect(fill.attributes('style')).toContain('width: 85%')
    })

    it('shows instanceAtLimit when events >= limit', () => {
      const projs = [{ id: 'p1', name: 'App', slug: 'app', public_key: 'k', event_count: 100000, event_limit: 0, passthrough_dsn: null }]
      mockQueries({ projects: projs, settings })
      const wrapper = mountTab()
      const fill = usageRow(wrapper, 'Events / month').find('.overview-bar__fill')
      expect(fill.classes()).toContain('overview-bar__fill--danger')
      expect(fill.attributes('style')).toContain('width: 100%')
    })
  })

  describe('health data and format helpers', () => {
    const settings = { version: 'v1.0.0', commit: 'abc', project_limit: 0, event_limit: 0, user_limit: 0, update_available: false }

    it('renders overview tab with health data', () => {
      mockQueries({
        settings,
        'instance-health': {
          db_ok: true, db_latency_ms: 2,
          events_total: 1500, events_24h: 100,
          tx_total: 500, tx_24h: 50,
          logs_total: 200, logs_24h: 10,
          oldest_event_at: daysAgo(5),
          oldest_tx_at: daysAgo(5),
          oldest_log_at: null,
          retention_days: 30,
          db_size_bytes: 1024 * 1024 * 5,
        },
      })
      const wrapper = mountTab()
      const errors = volRow(wrapper, 'Errors')
      expect(errors.text()).toContain('1.5K')
      expect(errors.text()).toContain('+100')
      expect(errors.find('.overview-vol-row__age').text()).toBe('5d ago')
      expect(volRow(wrapper, 'Logs').find('.overview-vol-row__age').text()).toBe('–')
      expect(chip(wrapper, 'Retention')).toBe('30 days')
      expect(chip(wrapper, 'Database')).toBe('5.0 MB')
    })

    it('renders with large values (millions range for formatNum)', () => {
      mockQueries({
        settings,
        'instance-health': {
          db_ok: true, db_latency_ms: 5,
          events_total: 5000000, events_24h: 10000,
          tx_total: 2000000, tx_24h: 5000,
          logs_total: 1000000, logs_24h: 2000,
          oldest_event_at: '2024-01-01T00:00:00Z',
          oldest_tx_at: '2024-01-01T00:00:00Z',
          oldest_log_at: null,
          retention_days: 90,
          db_size_bytes: 2 * 1024 * 1024 * 1024,
        },
      })
      const wrapper = mountTab()
      expect(volRow(wrapper, 'Errors').text()).toContain('5.0M')
      expect(volRow(wrapper, 'Errors').text()).toContain('+10.0K')
      expect(volRow(wrapper, 'Traces').text()).toContain('2.0M')
      expect(volRow(wrapper, 'Logs').text()).toContain('1.0M')
    })

    it('renders with events within expiry window (danger color path)', () => {
      // The expiry column is gone; the row now shows the age of the oldest data.
      mockQueries({
        settings,
        'instance-health': {
          db_ok: true, db_latency_ms: 1,
          events_total: 100, events_24h: 10,
          tx_total: 50, tx_24h: 5,
          logs_total: 0, logs_24h: 0,
          oldest_event_at: daysAgo(29),
          oldest_tx_at: daysAgo(15),
          oldest_log_at: null,
          retention_days: 30,
          db_size_bytes: 512 * 1024,
        },
      })
      const wrapper = mountTab()
      expect(volRow(wrapper, 'Errors').find('.overview-vol-row__age').text()).toBe('29d ago')
      expect(volRow(wrapper, 'Traces').find('.overview-vol-row__age').text()).toBe('15d ago')
    })
  })

  describe('format helpers coverage', () => {
    const settings = { version: 'v1', commit: 'abc', project_limit: 5, event_limit: 0, user_limit: 3, update_available: false }

    it('shows user limit bar when user_limit > 0', () => {
      const users = [{ id: 'u1' }, { id: 'u2' }]
      mockQueries({ users, settings: { ...settings, user_limit: 5 } })
      const wrapper = mountTab()
      const row = usageRow(wrapper, 'Users')
      expect(row.find('.overview-usage-row__count').text().replace(/\s+/g, '')).toBe('2/5')
      expect(row.find('.overview-bar').classes()).not.toContain('overview-bar--unlimited')
      expect(row.find('.overview-bar__fill').attributes('style')).toContain('width: 40%')
    })

    it('shows project limit reached state', () => {
      const projs = [
        { id: 'p1', name: 'App1', slug: 'app1', public_key: 'k1', event_count: 0, event_limit: 0, passthrough_dsn: null },
        { id: 'p2', name: 'App2', slug: 'app2', public_key: 'k2', event_count: 0, event_limit: 0, passthrough_dsn: null },
      ]
      mockQueries({ projects: projs, settings: { ...settings, project_limit: 2, user_limit: 0 } })
      const wrapper = mountTab()
      const row = usageRow(wrapper, 'Projects')
      expect(row.find('.overview-usage-row__count').text().replace(/\s+/g, '')).toBe('2/2')
      expect(row.find('.overview-bar__fill').attributes('style')).toContain('width: 100%')
    })
  })

  describe('health data format helpers branch coverage', () => {
    const settings = { version: 'v1.0.0', commit: 'abc', project_limit: 0, event_limit: 0, user_limit: 0, update_available: false }

    it('renders with retention_days=0 (Forever path in expiresLabel)', () => {
      mockQueries({
        settings,
        'instance-health': {
          events_total: 0, events_24h: 0,
          tx_total: 0, tx_24h: 0,
          logs_total: 0, logs_24h: 0,
          oldest_event_at: null,
          oldest_tx_at: null,
          oldest_log_at: null,
          retention_days: 0,
          db_size_bytes: 512,
        },
      })
      const wrapper = mountTab()
      expect(chip(wrapper, 'Retention')).toBe('Forever')
      expect(chip(wrapper, 'Database')).toBe('0.5 KB')
      expect(volRow(wrapper, 'Errors').find('.overview-vol-row__age').text()).toBe('–')
    })

    it('renders with expiry > 30 days away (success color in expiresColor)', () => {
      mockQueries({
        settings,
        'instance-health': {
          events_total: 100, events_24h: 10,
          tx_total: 50, tx_24h: 5,
          logs_total: 0, logs_24h: 0,
          oldest_event_at: daysAgo(1),
          oldest_tx_at: daysAgo(1),
          oldest_log_at: null,
          retention_days: 90,
          db_size_bytes: 500 * 1024,
        },
      })
      const wrapper = mountTab()
      expect(chip(wrapper, 'Retention')).toBe('90 days')
      expect(chip(wrapper, 'Database')).toBe('500.0 KB')
      expect(volRow(wrapper, 'Errors').find('.overview-vol-row__age').text()).toBe('1d ago')
    })

    it('renders with oldest_event today (dataAgeDays today path)', () => {
      mockQueries({
        settings,
        'instance-health': {
          events_total: 1, events_24h: 1,
          tx_total: 0, tx_24h: 0,
          logs_total: 0, logs_24h: 0,
          oldest_event_at: new Date().toISOString(),
          oldest_tx_at: null,
          oldest_log_at: null,
          retention_days: 30,
          db_size_bytes: 1024 * 1024 * 512,
        },
      })
      const wrapper = mountTab()
      expect(volRow(wrapper, 'Errors').find('.overview-vol-row__age').text()).toBe('today')
      expect(chip(wrapper, 'Database')).toBe('512.0 MB')
    })
  })

  describe('health data with db_size_bytes (formatBytes coverage)', () => {
    const settings = { version: 'v1.0.0', commit: 'abc', project_limit: 0, event_limit: 0, user_limit: 0, update_available: false }

    it('formatBytes shows KB for small db_size_bytes', () => {
      mockQueries({
        settings,
        'instance-health': {
          db_ok: true, db_latency_ms: 2, db_size_bytes: 512 * 1024,
          events_total: 0, events_24h: 0, tx_total: 0, tx_24h: 0, logs_total: 0, logs_24h: 0,
          oldest_event_at: null, oldest_tx_at: null, oldest_log_at: null, retention_days: 30,
        },
      })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('KB')
      expect(chip(wrapper, 'Database')).toBe('512.0 KB')
    })

    it('formatBytes shows MB for medium db_size_bytes', () => {
      mockQueries({
        settings,
        'instance-health': {
          db_ok: true, db_latency_ms: 5, db_size_bytes: 256 * 1024 * 1024,
          events_total: 1000, events_24h: 50, tx_total: 500, tx_24h: 25, logs_total: 0, logs_24h: 0,
          oldest_event_at: '2024-01-01T00:00:00Z', oldest_tx_at: null, oldest_log_at: null, retention_days: 30,
        },
      })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('MB')
      expect(chip(wrapper, 'Database')).toBe('256.0 MB')
    })

    it('formatBytes shows GB for large db_size_bytes', () => {
      mockQueries({
        settings,
        'instance-health': {
          db_ok: true, db_latency_ms: 10, db_size_bytes: 2 * 1024 * 1024 * 1024,
          events_total: 5000000, events_24h: 1000, tx_total: 2000000, tx_24h: 500, logs_total: 0, logs_24h: 0,
          oldest_event_at: '2024-01-01T00:00:00Z', oldest_tx_at: null, oldest_log_at: null, retention_days: 90,
        },
      })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain('GB')
      expect(chip(wrapper, 'Database')).toBe('2.00 GB')
    })
  })

  describe('logs and transactions usage rows', () => {
    const baseSettings = {
      event_limit: 0, project_limit: 0, user_limit: 0, billing_url: null,
      update_available: false, version: 'v1.0.0', commit: 'abc',
      log_row_limit: 0, tx_row_limit: 0,
    }

    const baseHealth = {
      events_total: 1_000, events_24h: 10,
      tx_total: 500, tx_24h: 5,
      logs_total: 2_000, logs_24h: 20,
      oldest_event_at: null, oldest_tx_at: null, oldest_log_at: null,
      retention_days: 90, db_size_bytes: 1024,
    }

    function mountWith(settings: unknown, health: unknown = undefined) {
      mockQueries({ settings, 'instance-health': health })
      return mountTab()
    }

    it('renders "Logs stored" label in overview', () => {
      const wrapper = mountWith(baseSettings)
      expect(wrapper.text()).toContain('Logs stored')
    })

    it('renders "Transactions stored" label in overview', () => {
      const wrapper = mountWith(baseSettings)
      expect(wrapper.text()).toContain('Transactions stored')
    })

    it('shows ∞ for logs when log_row_limit is 0', () => {
      const wrapper = mountWith({ ...baseSettings, log_row_limit: 0 })
      expect(usageRow(wrapper, 'Logs stored').find('.overview-limit__unlimited').exists()).toBe(true)
    })

    it('shows ∞ for transactions when tx_row_limit is 0', () => {
      const wrapper = mountWith({ ...baseSettings, tx_row_limit: 0 })
      expect(usageRow(wrapper, 'Transactions stored').find('.overview-limit__unlimited').exists()).toBe(true)
    })

    it('adds overview-bar--unlimited class to log bar when log_row_limit is 0', () => {
      const wrapper = mountWith({ ...baseSettings, log_row_limit: 0 })
      expect(usageRow(wrapper, 'Logs stored').find('.overview-bar').classes()).toContain('overview-bar--unlimited')
    })

    it('adds overview-bar--unlimited class to tx bar when tx_row_limit is 0', () => {
      const wrapper = mountWith({ ...baseSettings, tx_row_limit: 0 })
      expect(usageRow(wrapper, 'Transactions stored').find('.overview-bar').classes()).toContain('overview-bar--unlimited')
    })

    it('shows log count from health data in logs row', () => {
      const wrapper = mountWith(baseSettings, { ...baseHealth, logs_total: 3_000_000 })
      expect(wrapper.text()).toContain('3.0M')
      expect(usageRow(wrapper, 'Logs stored').text()).toContain('3.0M')
    })

    it('shows fill bar for logs when log_row_limit > 0 and health data present', () => {
      const wrapper = mountWith(
        { ...baseSettings, log_row_limit: 5_000_000 },
        { ...baseHealth, logs_total: 2_500_000 },
      )
      const fill = usageRow(wrapper, 'Logs stored').find('.overview-bar__fill')
      expect(fill.exists()).toBe(true)
      expect(fill.attributes('style')).toContain('width: 50%')
    })

    it('does not show fill bar for logs when log_row_limit is 0', () => {
      const wrapper = mountWith({ ...baseSettings, log_row_limit: 0 }, baseHealth)
      expect(usageRow(wrapper, 'Logs stored').find('.overview-bar__fill').exists()).toBe(false)
    })

    it('adds warn class to log bar when usage >= 80%', () => {
      const wrapper = mountWith(
        { ...baseSettings, log_row_limit: 5_000_000 },
        { ...baseHealth, logs_total: 4_500_000 },
      )
      expect(usageRow(wrapper, 'Logs stored').find('.overview-bar__fill').classes()).toContain('overview-bar__fill--warn')
    })

    it('adds danger class to log bar when usage >= 100%', () => {
      const wrapper = mountWith(
        { ...baseSettings, log_row_limit: 5_000_000 },
        { ...baseHealth, logs_total: 5_000_000 },
      )
      expect(usageRow(wrapper, 'Logs stored').find('.overview-bar__fill').classes()).toContain('overview-bar__fill--danger')
    })

    it('shows fill bar for transactions when tx_row_limit > 0 and health data present', () => {
      const wrapper = mountWith(
        { ...baseSettings, tx_row_limit: 10_000_000 },
        { ...baseHealth, tx_total: 5_000_000 },
      )
      expect(usageRow(wrapper, 'Transactions stored').find('.overview-bar__fill').exists()).toBe(true)
    })

    it('does not show fill bar for transactions when tx_row_limit is 0', () => {
      const wrapper = mountWith({ ...baseSettings, tx_row_limit: 0 }, baseHealth)
      expect(usageRow(wrapper, 'Transactions stored').find('.overview-bar__fill').exists()).toBe(false)
    })

    it('adds danger class to tx bar when usage >= 100%', () => {
      const wrapper = mountWith(
        { ...baseSettings, tx_row_limit: 10_000_000 },
        { ...baseHealth, tx_total: 10_000_000 },
      )
      expect(usageRow(wrapper, 'Transactions stored').find('.overview-bar__fill').classes()).toContain('overview-bar__fill--danger')
    })
  })

  describe('instance-health query', () => {
    it('is enabled for a user with manage_projects', () => {
      mountTab()
      expect(isEnabled(queryOptions('instance-health'))).toBe(true)
    })

    it('is disabled for a user without manage_projects', () => {
      mockQueries({ me: { ...adminUser, permissions: { ...adminUser.permissions, manage_projects: false } } })
      mountTab()
      expect(isEnabled(queryOptions('instance-health'))).toBe(false)
    })
  })
})

describe('event limit bar', () => {
  const settings = { event_limit: 100000, project_limit: 0, user_limit: 0, version: 'v1', commit: '', update_available: false }

  it('renders overview tab with event_limit > 0 and events (covers globalEventPct bar render)', () => {
    mockQueries({ projects: [{ id: 'p1', name: 'App', slug: 'app', public_key: 'k1', event_count: 50000, event_limit: 0, passthrough_dsn: null }], settings })
    const wrapper = mountTab()
    expect(wrapper.find('.overview-bar__fill').attributes('style')).toContain('width: 50%')
  })
})
