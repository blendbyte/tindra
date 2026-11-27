import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'

const { showToast, confirm } = vi.hoisted(() => ({ showToast: vi.fn(), confirm: vi.fn() }))

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: vi.fn(),
  useQueryClient: vi.fn(),
}))

vi.mock('@/composables/useConfirm', () => ({
  useConfirm: vi.fn(() => ({ confirm })),
}))

vi.mock('@/composables/useToast', () => ({
  useToast: vi.fn(() => ({ show: showToast })),
}))

vi.mock('@/api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/client')>()),
  apiFetch: vi.fn(),
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: vi.fn() }))

import ProjectTeamsPanel from '../ProjectTeamsPanel.vue'
import { apiFetch } from '@/api/client'
import { useQueryClient } from '@tanstack/vue-query'
import { mockQueries, queryOptions, recordMutations, stubs, type RecordedMutation } from './harness'

const ops = { id: 'g-1', name: 'Ops', member_ids: [], created_at: '2024-01-01T00:00:00Z' }
const dev = { id: 'g-2', name: 'Dev', member_ids: [], created_at: '2024-01-01T00:00:00Z' }
const loose = { team_ids: [] }
const inOps = { team_ids: ['g-1'] }

let mutations: RecordedMutation[]
let qc: { invalidateQueries: ReturnType<typeof vi.fn> }

beforeEach(() => {
  showToast.mockReset()
  confirm.mockReset()
  vi.mocked(apiFetch).mockReset()
  qc = { invalidateQueries: vi.fn() }
  vi.mocked(useQueryClient).mockReturnValue(qc as any)
  mutations = recordMutations()
})

function mountPanel(data: Parameters<typeof mockQueries>[0] = {}) {
  mockQueries({ teams: [ops, dev], ...data })
  return mount(ProjectTeamsPanel, { props: { projectId: 'p-1' }, global: { stubs } })
}

const boxes = (wrapper: ReturnType<typeof mountPanel>) =>
  wrapper.findAll('.rule__project-check input').map(b => (b.element as HTMLInputElement).checked)

describe('ProjectTeamsPanel', () => {
  it('reads the project teams from the API', async () => {
    mountPanel({ 'project-teams': loose })
    expect(queryOptions('project-teams').queryKey).toEqual(['project-teams', 'p-1'])
    vi.mocked(apiFetch).mockResolvedValue(loose)
    await queryOptions('project-teams').queryFn({ signal: undefined })
    expect(apiFetch).toHaveBeenCalledWith('/api/projects/p-1/teams', { signal: undefined })
  })

  it('offers every team as a checkbox, so a project can belong to several', () => {
    const wrapper = mountPanel({ 'project-teams': loose })
    expect(wrapper.findAll('.rule__project-check').map(l => l.text())).toEqual(['Ops', 'Dev'])
    expect(wrapper.find('select').exists()).toBe(false)
  })

  it('says who sees the project', () => {
    const text = mountPanel({ 'project-teams': loose }).text()
    expect(text).toContain('Members of these teams see this project')
    expect(text).toContain('everyone with access to all projects')
  })

  it('hints at the Teams tab when there is no team yet', () => {
    expect(mountPanel({ 'project-teams': loose, teams: [] }).text()).toContain('No teams yet')
  })

  describe('showing the current teams', () => {
    it('ticks nothing for a project without a team', () => {
      expect(boxes(mountPanel({ 'project-teams': loose }))).toEqual([false, false])
    })

    it('ticks the project teams', () => {
      expect(boxes(mountPanel({ 'project-teams': { team_ids: ['g-1', 'g-2'] } }))).toEqual([true, true])
    })

    it('follows the teams when they arrive after mounting', async () => {
      const current = ref<unknown>(undefined)
      const wrapper = mountPanel({ 'project-teams': current as any })
      expect(wrapper.find('form button[type="submit"]').attributes('disabled')).toBeDefined()
      current.value = inOps
      await flushPromises()
      expect(boxes(wrapper)).toEqual([true, false])
      expect(wrapper.find('form button[type="submit"]').attributes('disabled')).toBeUndefined()
    })
  })

  describe('saving', () => {
    it('runs the mutation when the form is submitted', async () => {
      const wrapper = mountPanel({ 'project-teams': inOps })
      await wrapper.find('form').trigger('submit')
      expect(mutations[0].mutate).toHaveBeenCalledTimes(1)
    })

    describe('taking the project out of a team', () => {
      const effect = {
        removed_recipients: [{ user_id: 'u-1', user_name: 'Anna', rule_id: 'r-1', rule_name: 'Prod alerts' }],
        paused_rules: [],
      }

      async function untick() {
        const wrapper = mountPanel({ 'project-teams': inOps })
        await wrapper.findAll('.rule__project-check input')[0].setValue(false)
        await wrapper.find('form').trigger('submit')
        await flushPromises()
      }

      it('previews the change and asks when alert recipients would lose access', async () => {
        vi.mocked(apiFetch).mockResolvedValue(effect)
        confirm.mockResolvedValue(true)
        await untick()
        expect(apiFetch).toHaveBeenCalledWith('/api/projects/p-1/teams?dry_run=true', {
          method: 'PUT', body: JSON.stringify({ team_ids: [] }),
        })
        expect(confirm).toHaveBeenCalledWith(expect.objectContaining({
          message: 'This removes Anna from 1 alert rule.',
        }))
        expect(mutations[0].mutate).toHaveBeenCalledTimes(1)
      })

      it('saves nothing when the admin cancels', async () => {
        vi.mocked(apiFetch).mockResolvedValue(effect)
        confirm.mockResolvedValue(false)
        await untick()
        expect(mutations[0].mutate).not.toHaveBeenCalled()
      })

      it('saves without asking when no alert rule is affected', async () => {
        vi.mocked(apiFetch).mockResolvedValue({ removed_recipients: [], paused_rules: [] })
        await untick()
        expect(confirm).not.toHaveBeenCalled()
        expect(mutations[0].mutate).toHaveBeenCalledTimes(1)
      })

      it('does not preview when only adding teams', async () => {
        const wrapper = mountPanel({ 'project-teams': inOps })
        await wrapper.findAll('.rule__project-check input')[1].setValue(true)
        await wrapper.find('form').trigger('submit')
        await flushPromises()
        expect(apiFetch).not.toHaveBeenCalled()
        expect(mutations[0].mutate).toHaveBeenCalledTimes(1)
      })
    })

    it('puts the chosen teams', async () => {
      const wrapper = mountPanel({ 'project-teams': inOps })
      await wrapper.findAll('.rule__project-check input')[1].setValue(true)
      vi.mocked(apiFetch).mockResolvedValue({ team_ids: ['g-1', 'g-2'] })
      await mutations[0].opts.mutationFn()
      expect(apiFetch).toHaveBeenCalledWith('/api/projects/p-1/teams', {
        method: 'PUT',
        body: JSON.stringify({ team_ids: ['g-1', 'g-2'] }),
      })
    })

    it('sends no team once every box is cleared', async () => {
      const wrapper = mountPanel({ 'project-teams': inOps })
      await wrapper.findAll('.rule__project-check input')[0].setValue(false)
      await mutations[0].opts.mutationFn()
      expect(JSON.parse(vi.mocked(apiFetch).mock.calls[0][1]!.body as string)).toEqual({ team_ids: [] })
    })

    it('refreshes, confirms and closes once saved', () => {
      const wrapper = mountPanel({ 'project-teams': inOps })
      mutations[0].opts.onSuccess()
      expect(qc.invalidateQueries.mock.calls.map(([a]) => a.queryKey)).toEqual([['project-teams'], ['projects']])
      expect(showToast).toHaveBeenCalledWith('Project teams saved', 'success')
      expect(wrapper.emitted('close')).toHaveLength(1)
    })

    it('shows an error and stays open when saving fails', async () => {
      const wrapper = mountPanel({ 'project-teams': inOps })
      mutations[0].opts.onError(new Error('boom'))
      await flushPromises()
      expect(wrapper.text()).toContain('Failed to save the project teams')
      expect(wrapper.emitted('close')).toBeUndefined()
    })

    it('closes without saving on Cancel', async () => {
      const wrapper = mountPanel({ 'project-teams': inOps })
      await wrapper.find('button[type="button"]').trigger('click')
      expect(wrapper.emitted('close')).toHaveLength(1)
      expect(mutations[0].mutate).not.toHaveBeenCalled()
    })
  })
})
