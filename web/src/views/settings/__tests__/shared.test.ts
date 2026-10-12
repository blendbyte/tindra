import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, h, ref } from 'vue'

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(),
  useQueryClient: vi.fn(),
}))

vi.mock('@/api/client', () => ({ apiFetch: vi.fn() }))
vi.mock('@/stores/auth', () => ({ useAuthStore: vi.fn() }))

import { apiFetch } from '@/api/client'
import type { Project, ServerSettings } from '@/api/types'
import {
  formatBytes, useFormatDate, useMonthlyEvents, usePermissions, useProjectByID, useProjects,
  useRowMenu, useServerSettings, useUsers,
} from '../shared'
import { adminUser, mockQueries, queryOptions } from './harness'

// Runs a composable inside a mounted component so lifecycle hooks fire.
function withSetup<T>(composable: () => T) {
  let result!: T
  const wrapper = mount(defineComponent({
    setup() {
      result = composable()
      return () => h('div')
    },
  }), { attachTo: document.body })
  return { result, wrapper }
}

const project = (id: string, event_count: number) => ({ id, name: id.toUpperCase(), event_count }) as Project

beforeEach(() => {
  mockQueries()
})

describe('formatBytes', () => {
  it('formats KB, MB and GB', () => {
    expect(formatBytes(512)).toBe('0.5 KB')
    expect(formatBytes(2 * 1024 * 1024)).toBe('2.0 MB')
    expect(formatBytes(3 * 1024 * 1024 * 1024)).toBe('3.00 GB')
  })
})

describe('usePermissions', () => {
  it('reads the manage permissions of the current user', () => {
    mockQueries({ me: { ...adminUser, permissions: { manage_projects: true, manage_users: false, manage_alerts: true, manage_issues: false } } })
    const { result } = withSetup(usePermissions)
    expect(result.canManageProjects.value).toBe(true)
    expect(result.canManageUsers.value).toBe(false)
    expect(result.canManageAlerts.value).toBe(true)
  })

  it('denies everything while the user is unknown', () => {
    mockQueries({ me: undefined })
    const { result } = withSetup(usePermissions)
    expect(result.canManageProjects.value).toBe(false)
    expect(result.canManageUsers.value).toBe(false)
    expect(result.canManageAlerts.value).toBe(false)
  })
})

describe('shared queries', () => {
  it('default to empty lists until data arrives', () => {
    mockQueries({ projects: undefined, users: undefined })
    const { result } = withSetup(() => ({ ...useProjects(), ...useUsers(), ...useServerSettings() }))
    expect(result.projects.value).toEqual([])
    expect(result.users.value).toEqual([])
    expect(result.settings.value).toBeUndefined()
  })

  it('use the existing query keys and endpoints', async () => {
    withSetup(() => [useProjects(), useUsers(), useServerSettings()])
    const signal = new AbortController().signal
    expect(queryOptions('projects').queryKey).toEqual(['projects', 'usage'])
    for (const [name, url] of [['projects', '/api/projects'], ['users', '/api/users'], ['settings', '/api/settings']] as const) {
      vi.mocked(apiFetch).mockClear()
      await queryOptions(name).queryFn({ signal })
      expect(apiFetch).toHaveBeenCalledWith(url, { signal })
    }
  })
})

describe('useProjectByID', () => {
  it('indexes projects by id and follows changes', () => {
    const projects = ref([project('p1', 0)])
    const byID = useProjectByID(projects)
    expect(byID.value.get('p1')?.name).toBe('P1')
    projects.value = [project('p2', 0)]
    expect(byID.value.has('p1')).toBe(false)
    expect(byID.value.get('p2')?.name).toBe('P2')
  })
})

describe('useMonthlyEvents', () => {
  it('sums events and compares them with the event limit', () => {
    const projects = ref([project('p1', 300), project('p2', 500)])
    const settings = ref<ServerSettings | undefined>({ event_limit: 1000 } as ServerSettings)
    const usage = useMonthlyEvents(projects, settings)
    expect(usage.totalMonthlyEvents.value).toBe(800)
    expect(usage.globalEventPct.value).toBe(80)
    expect(usage.instanceNearLimit.value).toBe(true)
    expect(usage.instanceAtLimit.value).toBe(false)

    projects.value = [project('p1', 1500)]
    expect(usage.globalEventPct.value).toBe(100)
    expect(usage.instanceAtLimit.value).toBe(true)
  })

  it('treats a missing or zero limit as unlimited', () => {
    const projects = ref([project('p1', 5000)])
    const settings = ref<ServerSettings | undefined>(undefined)
    const usage = useMonthlyEvents(projects, settings)
    expect(usage.globalEventPct.value).toBe(0)
    expect(usage.instanceNearLimit.value).toBe(false)
    expect(usage.instanceAtLimit.value).toBe(false)
    settings.value = { event_limit: 0 } as ServerSettings
    expect(usage.instanceAtLimit.value).toBe(false)
  })
})

describe('useRowMenu', () => {
  it('toggles one menu at a time without letting the click bubble', () => {
    const { result } = withSetup(useRowMenu)
    const event = new MouseEvent('click')
    const stop = vi.spyOn(event, 'stopPropagation')
    result.toggleUserMenu('a', event)
    expect(stop).toHaveBeenCalled()
    expect(result.openMenuId.value).toBe('a')
    result.toggleUserMenu('b', new MouseEvent('click'))
    expect(result.openMenuId.value).toBe('b')
    result.toggleUserMenu('b', new MouseEvent('click'))
    expect(result.openMenuId.value).toBeNull()
  })

  it('closes on any document click and stops listening once unmounted', () => {
    const { result, wrapper } = withSetup(useRowMenu)
    result.toggleUserMenu('a', new MouseEvent('click'))
    document.dispatchEvent(new MouseEvent('click'))
    expect(result.openMenuId.value).toBeNull()

    result.toggleUserMenu('a', new MouseEvent('click'))
    wrapper.unmount()
    document.dispatchEvent(new MouseEvent('click'))
    expect(result.openMenuId.value).toBe('a')
  })
})

describe('useFormatDate', () => {
  it("formats dates in the signed-in user's timezone", () => {
    mockQueries({ me: { ...adminUser, timezone: 'Pacific/Kiritimati' } })
    const { result: inKiritimati } = withSetup(useFormatDate)
    mockQueries({ me: { ...adminUser, timezone: 'Pacific/Pago_Pago' } })
    const { result: inPagoPago } = withSetup(useFormatDate)
    const iso = '2024-06-15T12:00:00Z'
    expect(inKiritimati(iso)).toBe(new Date(iso).toLocaleDateString(undefined, { timeZone: 'Pacific/Kiritimati' }))
    // UTC+14 and UTC-11 land on different calendar days at noon UTC.
    expect(inKiritimati(iso)).not.toBe(inPagoPago(iso))
  })
})
