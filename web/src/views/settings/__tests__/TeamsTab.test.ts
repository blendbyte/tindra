import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const { showToast, confirm } = vi.hoisted(() => ({ showToast: vi.fn(), confirm: vi.fn() }))

vi.mock('vue-router', () => ({
  useRouter: vi.fn(() => ({ push: vi.fn(), replace: vi.fn() })),
  useRoute: vi.fn(() => ({ params: { tab: 'teams' }, query: {} })),
}))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(),
  useQueryClient: vi.fn(),
}))

vi.mock('@/composables/useToast', () => ({
  useToast: vi.fn(() => ({ show: showToast })),
}))

vi.mock('@/composables/useConfirm', () => ({
  useConfirm: vi.fn(() => ({ confirm })),
}))

vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  apiFetch: vi.fn(),
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: vi.fn() }))

import TeamsTab from '../TeamsTab.vue'
import { ApiError, apiFetch } from '@/api/client'
import { useQueryClient } from '@tanstack/vue-query'
import { adminUser, isEnabled, mockQueries, queryOptions, recordMutations, stubs, type RecordedMutation } from './harness'

// Order of the useMutation calls inside TeamsTab.
const M = { createTeam: 0, saveTeam: 1, deleteTeam: 2 } as const

const ops = { id: 'g-1', name: 'Ops', member_ids: ['user-2'], created_at: '2024-01-01T00:00:00Z' }
const bob = {
  id: 'user-2', email: 'bob@example.com', name: 'Bob', has_password: true, mfa_enabled: false,
  weekly_digest: false, timezone: 'UTC', created_at: '2024-01-01T00:00:00Z',
  permissions: { manage_projects: false, manage_users: false, manage_alerts: false, manage_issues: false },
}

let mutations: RecordedMutation[]
let qc: { invalidateQueries: ReturnType<typeof vi.fn> }

beforeEach(() => {
  showToast.mockReset()
  confirm.mockReset()
  vi.mocked(apiFetch).mockReset()
  qc = { invalidateQueries: vi.fn() }
  vi.mocked(useQueryClient).mockReturnValue(qc as any)
  mutations = recordMutations()
  mockQueries({ teams: [ops], users: [adminUser, bob] })
})

function mountTab() {
  return mount(TeamsTab, { global: { stubs } })
}

function invalidatedKeys(): string[] {
  return qc.invalidateQueries.mock.calls.map(([arg]) => arg.queryKey[0])
}

describe('TeamsTab', () => {
  describe('permissions', () => {
    const noUsersPerm = { ...adminUser, permissions: { ...adminUser.permissions, manage_users: false } }

    it('explains the missing permission and hides the create form', () => {
      mockQueries({ me: noUsersPerm, teams: [ops] })
      const wrapper = mountTab()
      expect(wrapper.text()).toContain("You don't have permission to manage teams")
      expect(wrapper.find('form').exists()).toBe(false)
    })

    it('does not fetch teams or users without the permission', () => {
      mockQueries({ me: noUsersPerm })
      mountTab()
      expect(isEnabled(queryOptions('teams'))).toBe(false)
      expect(isEnabled(queryOptions('users'))).toBe(false)
    })

    it('fetches teams from /api/teams', async () => {
      mountTab()
      vi.mocked(apiFetch).mockResolvedValue([])
      await queryOptions('teams').queryFn({ signal: undefined })
      expect(apiFetch).toHaveBeenCalledWith('/api/teams', { signal: undefined })
    })
  })

  describe('list', () => {
    it('shows each team with its member count', () => {
      mockQueries({ teams: [ops, { ...ops, id: 'g-2', name: 'Dev', member_ids: [] }], users: [adminUser, bob] })
      const text = mountTab().text()
      expect(text).toContain('Ops')
      expect(text).toContain('1 member')
      expect(text).toContain('Dev')
      expect(text).toContain('0 members')
    })

    it('shows an empty state when there are no teams', () => {
      mockQueries({ teams: [], users: [adminUser] })
      expect(mountTab().text()).toContain('No teams yet')
    })
  })

  describe('create', () => {
    it('keeps the button disabled while the name is blank', async () => {
      const wrapper = mountTab()
      const button = wrapper.find('form button[type="submit"]')
      expect(button.attributes('disabled')).toBeDefined()
      await wrapper.find('input[aria-label="New team name"]').setValue('   ')
      expect(button.attributes('disabled')).toBeDefined()
    })

    it('creates a team with the trimmed name', async () => {
      const wrapper = mountTab()
      await wrapper.find('input[aria-label="New team name"]').setValue('  Backend team  ')
      await wrapper.find('form').trigger('submit')
      expect(mutations[M.createTeam].mutate).toHaveBeenCalledWith('Backend team')
    })

    it('posts the name to /api/teams', async () => {
      mountTab()
      vi.mocked(apiFetch).mockResolvedValue(ops)
      await mutations[M.createTeam].opts.mutationFn('Ops')
      expect(apiFetch).toHaveBeenCalledWith('/api/teams', { method: 'POST', body: JSON.stringify({ name: 'Ops' }) })
    })

    it('refreshes the lists, opens the new team and says so', async () => {
      const wrapper = mountTab()
      mutations[M.createTeam].opts.onSuccess(ops)
      await flushPromises()
      expect(invalidatedKeys()).toEqual(expect.arrayContaining(['teams', 'project-teams', 'projects', 'users']))
      expect(showToast).toHaveBeenCalledWith('Team Ops created', 'success')
      expect(wrapper.find('input[id="team-name-g-1"]').exists()).toBe(true)
    })

    it('reports a name that is already taken', async () => {
      const wrapper = mountTab()
      mutations[M.createTeam].opts.onError(new ApiError(409, 'team name already taken'))
      await flushPromises()
      expect(wrapper.text()).toContain('A team with this name already exists')
    })

    it('reports any other failure', async () => {
      const wrapper = mountTab()
      mutations[M.createTeam].opts.onError(new ApiError(500, 'boom'))
      await flushPromises()
      expect(wrapper.text()).toContain('Failed to create team')
    })
  })

  describe('edit', () => {
    async function openOps() {
      const wrapper = mountTab()
      await wrapper.find('.proj-card__head').trigger('click')
      return wrapper
    }

    it('lists every user as a member checkbox, ticking the current members', async () => {
      const wrapper = await openOps()
      const boxes = wrapper.findAll('.rule__project-check input')
      expect(boxes).toHaveLength(2)
      expect(wrapper.findAll('.rule__project-check').map(l => l.text())).toEqual(['Admin', 'Bob'])
      expect((boxes[0].element as HTMLInputElement).checked).toBe(false)
      expect((boxes[1].element as HTMLInputElement).checked).toBe(true)
    })

    it('saves the new name and members', async () => {
      const wrapper = await openOps()
      await wrapper.find('#team-name-g-1').setValue('Platform')
      await wrapper.findAll('.rule__project-check input')[0].setValue(true)
      await wrapper.find('.proj-card__body form').trigger('submit')
      expect(mutations[M.saveTeam].mutate).toHaveBeenCalledWith({
        team: ops, name: 'Platform', memberIds: ['user-2', 'user-1'],
      })
    })

    describe('removing members', () => {
      const effect = {
        removed_recipients: [
          { user_id: 'user-2', user_name: 'Bob', rule_id: 'r-1', rule_name: 'Prod alerts' },
          { user_id: 'user-2', user_name: 'Bob', rule_id: 'r-2', rule_name: 'Staging alerts' },
        ],
        paused_rules: [{ id: 'r-2', name: 'Staging alerts' }],
      }

      async function untickBob() {
        const wrapper = await openOps()
        await wrapper.findAll('.rule__project-check input')[1].setValue(false)
        await wrapper.find('.proj-card__body form').trigger('submit')
        await flushPromises()
      }

      it('previews the change and says what happens to the alert rules', async () => {
        vi.mocked(apiFetch).mockResolvedValue(effect)
        confirm.mockResolvedValue(true)
        await untickBob()
        expect(apiFetch).toHaveBeenCalledWith('/api/teams/g-1/members?dry_run=true', {
          method: 'PUT', body: JSON.stringify({ user_ids: [] }),
        })
        expect(confirm).toHaveBeenCalledWith(expect.objectContaining({
          message: 'This removes Bob from 2 alert rules. 1 push rule will be paused (Staging alerts) because nobody would be left to notify.',
        }))
        expect(mutations[M.saveTeam].mutate).toHaveBeenCalledWith({ team: ops, name: 'Ops', memberIds: [] })
      })

      it('saves nothing when the admin cancels', async () => {
        vi.mocked(apiFetch).mockResolvedValue(effect)
        confirm.mockResolvedValue(false)
        await untickBob()
        expect(mutations[M.saveTeam].mutate).not.toHaveBeenCalled()
      })

      it('saves without asking when no alert rule is affected', async () => {
        vi.mocked(apiFetch).mockResolvedValue({ removed_recipients: [], paused_rules: [] })
        await untickBob()
        expect(confirm).not.toHaveBeenCalled()
        expect(mutations[M.saveTeam].mutate).toHaveBeenCalled()
      })

      it('does not save, and says so, when the effect cannot be checked', async () => {
        vi.mocked(apiFetch).mockRejectedValue(new ApiError(500, 'boom'))
        await untickBob()
        expect(showToast).toHaveBeenCalledWith('Could not check the effect on alert rules. Try again.', 'error')
        expect(mutations[M.saveTeam].mutate).not.toHaveBeenCalled()
      })

      it('does not preview when only adding members', async () => {
        const wrapper = await openOps()
        await wrapper.findAll('.rule__project-check input')[0].setValue(true)
        await wrapper.find('.proj-card__body form').trigger('submit')
        await flushPromises()
        expect(apiFetch).not.toHaveBeenCalled()
        expect(mutations[M.saveTeam].mutate).toHaveBeenCalled()
      })
    })

    it('does not save a blank name', async () => {
      const wrapper = await openOps()
      await wrapper.find('#team-name-g-1').setValue('  ')
      await wrapper.find('.proj-card__body form').trigger('submit')
      expect(mutations[M.saveTeam].mutate).not.toHaveBeenCalled()
    })

    it('renames only when the name changed, then replaces the members', async () => {
      mountTab()
      vi.mocked(apiFetch).mockResolvedValue(ops)
      await mutations[M.saveTeam].opts.mutationFn({ team: ops, name: 'Ops', memberIds: ['user-1'] })
      expect(apiFetch).toHaveBeenCalledTimes(1)
      expect(apiFetch).toHaveBeenCalledWith('/api/teams/g-1/members', {
        method: 'PUT', body: JSON.stringify({ user_ids: ['user-1'] }),
      })

      vi.mocked(apiFetch).mockClear()
      await mutations[M.saveTeam].opts.mutationFn({ team: ops, name: 'Platform', memberIds: [] })
      expect(vi.mocked(apiFetch).mock.calls.map(([path, init]) => `${init?.method} ${path}`)).toEqual([
        'PATCH /api/teams/g-1',
        'PUT /api/teams/g-1/members',
      ])
    })

    it('closes the editor and refreshes once saved', async () => {
      const wrapper = await openOps()
      mutations[M.saveTeam].opts.onSuccess(ops)
      await flushPromises()
      expect(wrapper.find('#team-name-g-1').exists()).toBe(false)
      expect(invalidatedKeys()).toContain('teams')
      expect(showToast).toHaveBeenCalledWith('Team Ops saved', 'success')
    })

    it('reports a rename to a name already taken', async () => {
      const wrapper = await openOps()
      mutations[M.saveTeam].opts.onError(new ApiError(409, 'team name already taken'))
      await flushPromises()
      expect(wrapper.text()).toContain('A team with this name already exists')
    })
  })

  describe('delete', () => {
    async function clickDelete() {
      const wrapper = mountTab()
      await wrapper.find('.proj-card__head').trigger('click')
      const button = wrapper.findAll('button').find(b => b.text().includes('Delete'))!
      await button.trigger('click')
      await flushPromises()
      return wrapper
    }

    it('asks for confirmation and deletes when confirmed', async () => {
      confirm.mockResolvedValue(true)
      await clickDelete()
      expect(confirm).toHaveBeenCalledWith(expect.objectContaining({ title: 'Delete Ops?', danger: true }))
      expect(mutations[M.deleteTeam].mutate).toHaveBeenCalledWith(ops)
    })

    it('adds what the deletion does to alert rules to the confirmation', async () => {
      vi.mocked(apiFetch).mockResolvedValue({
        removed_recipients: [{ user_id: 'user-2', user_name: 'Bob', rule_id: 'r-1', rule_name: 'Prod alerts' }],
        paused_rules: [],
      })
      confirm.mockResolvedValue(true)
      await clickDelete()
      expect(apiFetch).toHaveBeenCalledWith('/api/teams/g-1?dry_run=true', { method: 'DELETE' })
      expect(confirm).toHaveBeenCalledWith(expect.objectContaining({
        message: expect.stringMatching(/Accounts are not affected\. This removes Bob from 1 alert rule\.$/),
      }))
    })

    it('does nothing when the confirmation is declined', async () => {
      confirm.mockResolvedValue(false)
      await clickDelete()
      expect(mutations[M.deleteTeam].mutate).not.toHaveBeenCalled()
    })

    it('sends DELETE to the team', async () => {
      mountTab()
      vi.mocked(apiFetch).mockResolvedValue(undefined)
      await mutations[M.deleteTeam].opts.mutationFn(ops)
      expect(apiFetch).toHaveBeenCalledWith('/api/teams/g-1', { method: 'DELETE' })
    })

    it('refreshes and confirms once deleted, and toasts a failure', async () => {
      mountTab()
      mutations[M.deleteTeam].opts.onSuccess(undefined, ops)
      expect(invalidatedKeys()).toContain('teams')
      expect(showToast).toHaveBeenCalledWith('Team Ops deleted', 'success')
      mutations[M.deleteTeam].opts.onError(new ApiError(500, 'boom'))
      expect(showToast).toHaveBeenCalledWith('Failed to delete team', 'error')
    })
  })
})
