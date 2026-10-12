import { vi } from 'vitest'
import { isRef, ref, toValue, type Ref } from 'vue'
import { useQuery, useMutation } from '@tanstack/vue-query'
import { useAuthStore } from '@/stores/auth'

// Test helpers shared by the settings tab tests. Every test file still declares
// its own vi.mock calls (they are hoisted per file); this module only drives the
// mocked useQuery / useMutation and the auth store.

export const stubs = {
  RouterLink: { template: '<a><slot /></a>' },
  Icon: { template: '<span />' },
  Sparkline: { template: '<span />' },
}

export const adminUser = {
  id: 'user-1',
  name: 'Admin',
  email: 'admin@example.com',
  mfa_enabled: false,
  weekly_digest: true,
  timezone: 'UTC',
  permissions: {
    manage_projects: true,
    manage_users: true,
    manage_alerts: true,
    manage_issues: true,
  },
}

// First queryKey segment of every query the settings screens run.
export type QueryName =
  | 'me' | 'tokens' | 'projects' | 'users' | 'settings' | 'invites' | 'audit'
  | 'instance-health' | 'quota' | 'alert-rules' | 'alert-firings' | 'logs-count'

const listQueries: QueryName[] = ['tokens', 'projects', 'users', 'invites', 'audit', 'alert-rules', 'alert-firings']

export function queryName(options: any): QueryName {
  return toValue(options.queryKey)[0] as QueryName
}

// Mocks useQuery by queryKey instead of call order. Lists default to [], `me`
// defaults to the admin user and everything else to undefined. Pass a ref to
// change a query's data after mounting.
export function mockQueries(data: Partial<Record<QueryName, unknown>> = {}) {
  vi.mocked(useAuthStore).mockReturnValue({ user: data.me ?? adminUser, setUser: vi.fn() } as any)
  const refs = new Map<QueryName, Ref>()
  vi.mocked(useQuery).mockImplementation(((options: any) => {
    const name = queryName(options)
    if (!refs.has(name)) {
      const value = name in data ? data[name] : name === 'me' ? adminUser : listQueries.includes(name) ? [] : undefined
      refs.set(name, isRef(value) ? value : ref(value))
    }
    return { data: refs.get(name) }
  }) as any)
}

// The options a query was created with, for asserting keys, URLs or `enabled`.
export function queryOptions(name: QueryName): any {
  const call = vi.mocked(useQuery).mock.calls.find(([options]) => queryName(options) === name)
  if (!call) throw new Error(`no ${name} query`)
  return call[0]
}

export function isEnabled(options: any): boolean {
  if (options.enabled === undefined) return true
  return !!toValue(options.enabled)
}

export type RecordedMutation = { opts: any; mutate: ReturnType<typeof vi.fn>; isPending: Ref<boolean> }

// Records every useMutation call in order with its options and mutate spy. A tab
// creates its mutations in a fixed order, so tests index into the result with
// the per-tab constants they define.
export function recordMutations() {
  const recorded: RecordedMutation[] = []
  vi.mocked(useMutation).mockImplementation(((opts: any) => {
    const entry = { opts, mutate: vi.fn(), isPending: ref(false) }
    recorded.push(entry)
    return { mutate: entry.mutate, isPending: entry.isPending }
  }) as any)
  return recorded
}

// Makes every mutate call run mutationFn and then onSuccess or onError, like
// vue-query does, so a click goes through the real request and callbacks.
export function runMutations() {
  vi.mocked(useMutation).mockImplementation(((opts: any) => ({
    isPending: ref(false),
    mutate: async (vars: unknown) => {
      try { opts.onSuccess?.(await opts.mutationFn(vars), vars) }
      catch (error) { opts.onError?.(error, vars) }
    },
  })) as any)
}
