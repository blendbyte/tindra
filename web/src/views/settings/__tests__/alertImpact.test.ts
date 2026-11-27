import { describe, it, expect, vi, beforeEach } from 'vitest'

const { showToast, confirm } = vi.hoisted(() => ({ showToast: vi.fn(), confirm: vi.fn() }))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(),
  useQueryClient: vi.fn(),
}))
vi.mock('@/composables/useToast', () => ({ useToast: vi.fn(() => ({ show: showToast })) }))
vi.mock('@/composables/useConfirm', () => ({ useConfirm: vi.fn(() => ({ confirm })) }))
vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  apiFetch: vi.fn(),
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: vi.fn() }))

import { ApiError, apiFetch } from '@/api/client'
import type { AlertImpact } from '@/api/types'
import { describeAlertImpact, previewAlertImpact, useAlertImpactConfirm } from '../shared'

const nothing: AlertImpact = { removed_recipients: [], paused_rules: [] }
const anna = (rule: string) => ({ user_id: 'u-1', user_name: 'Anna', rule_id: rule, rule_name: `Rule ${rule}` })
const ben = (rule: string) => ({ user_id: 'u-2', user_name: 'Ben', rule_id: rule, rule_name: `Rule ${rule}` })

beforeEach(() => {
  showToast.mockReset()
  confirm.mockReset()
  vi.mocked(apiFetch).mockReset()
})

describe('describeAlertImpact', () => {
  it('is empty when no alert rule is affected, or the server sent nothing', () => {
    expect(describeAlertImpact(nothing)).toBe('')
    expect(describeAlertImpact(undefined)).toBe('')
    expect(describeAlertImpact(null)).toBe('')
  })

  it('counts the rules each person comes off', () => {
    expect(describeAlertImpact({ removed_recipients: [anna('1'), anna('2'), ben('1')], paused_rules: [] }))
      .toBe('This removes Anna from 2 alert rules, Ben from 1 alert rule.')
  })

  it('names the push rules that are paused', () => {
    expect(describeAlertImpact({ removed_recipients: [anna('1')], paused_rules: [{ id: '1', name: 'Rule 1' }, { id: '2', name: 'Rule 2' }] }))
      .toBe('This removes Anna from 1 alert rule. 2 push rules will be paused (Rule 1, Rule 2) because nobody would be left to notify.')
  })

  it('does not break on a user without a name', () => {
    expect(describeAlertImpact({ removed_recipients: [{ ...anna('1'), user_name: '' }], paused_rules: [] }))
      .toBe('This removes A user from 1 alert rule.')
  })
})

describe('previewAlertImpact', () => {
  it('sends the change itself with dry_run=true', async () => {
    vi.mocked(apiFetch).mockResolvedValue(nothing)
    await previewAlertImpact('/api/teams/t-1/members', 'PUT', { user_ids: [] })
    expect(apiFetch).toHaveBeenCalledWith('/api/teams/t-1/members?dry_run=true', {
      method: 'PUT', body: JSON.stringify({ user_ids: [] }),
    })
  })

  it('sends no body for a deletion', async () => {
    vi.mocked(apiFetch).mockResolvedValue(nothing)
    await previewAlertImpact('/api/teams/t-1', 'DELETE')
    expect(apiFetch).toHaveBeenCalledWith('/api/teams/t-1?dry_run=true', { method: 'DELETE' })
  })
})

describe('useAlertImpactConfirm', () => {
  const run = (options: Record<string, unknown> = {}) =>
    useAlertImpactConfirm()('/api/teams/t-1', 'DELETE', undefined, { title: 'Delete?', ...options })

  it('goes ahead without a dialog when nothing is affected', async () => {
    vi.mocked(apiFetch).mockResolvedValue(nothing)
    expect(await run()).toBe(true)
    expect(confirm).not.toHaveBeenCalled()
  })

  it('asks with the effect when alert rules are affected, and returns the answer', async () => {
    vi.mocked(apiFetch).mockResolvedValue({ removed_recipients: [anna('1')], paused_rules: [] })
    confirm.mockResolvedValueOnce(true).mockResolvedValueOnce(false)
    expect(await run({ confirmLabel: 'Go' })).toBe(true)
    expect(confirm).toHaveBeenCalledWith({ title: 'Delete?', confirmLabel: 'Go', message: 'This removes Anna from 1 alert rule.' })
    expect(await run()).toBe(false)
  })

  it('keeps the caller message and asks anyway with alwaysAsk', async () => {
    vi.mocked(apiFetch).mockResolvedValue({ removed_recipients: [anna('1')], paused_rules: [] })
    confirm.mockResolvedValue(true)
    await run({ message: 'Members lose access.', alwaysAsk: true })
    expect(confirm).toHaveBeenCalledWith({ title: 'Delete?', message: 'Members lose access. This removes Anna from 1 alert rule.' })

    vi.mocked(apiFetch).mockResolvedValue(nothing)
    await run({ message: 'Members lose access.', alwaysAsk: true })
    expect(confirm).toHaveBeenLastCalledWith({ title: 'Delete?', message: 'Members lose access.' })
  })

  it('stops, and says so, when the effect cannot be checked', async () => {
    vi.mocked(apiFetch).mockRejectedValue(new ApiError(500, 'boom'))
    expect(await run()).toBe(false)
    expect(showToast).toHaveBeenCalledWith('Could not check the effect on alert rules. Try again.', 'error')
    expect(confirm).not.toHaveBeenCalled()
  })

  it('lets the save report a change the server refuses', async () => {
    for (const status of [400, 409]) {
      vi.mocked(apiFetch).mockRejectedValue(new ApiError(status, 'refused'))
      expect(await run()).toBe(true)
    }
    expect(showToast).not.toHaveBeenCalled()
    expect(confirm).not.toHaveBeenCalled()
  })
})
