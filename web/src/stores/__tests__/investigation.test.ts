import { expect, it, vi } from 'vitest'
import { useInvestigationStore } from '../investigation'

it('preserves fixed bounds and existing local filters in drilldown links', () => {
  const state = useInvestigationStore()
  state.absolute = { from: '2026-01-01T00:00:00Z', to: '2026-01-02T00:00:00Z' }
  const link = new URL(state.link('/logs?level=error#record'), 'http://test')
  expect(link.searchParams.get('range')).toBe('custom')
  expect(link.searchParams.get('from')).toBe(state.absolute.from)
  expect(link.searchParams.get('to')).toBe(state.absolute.to)
  expect(link.searchParams.get('level')).toBe('error')
  expect(link.hash).toBe('#record')
})
it('continues with defaults when legacy preference storage is unavailable', () => {
  const get = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
  expect(useInvestigationStore().range).toBe('24h')
  get.mockRestore()
})

it('restores tab state while rejecting malformed project entries', () => {
  sessionStorage.setItem('tindra:investigation', JSON.stringify({
    projectIds: ['11111111-1111-4111-8111-111111111111', null, 42],
    environment: 'preview', range: '7d', paused: true,
  }))
  const state = useInvestigationStore()
  expect(state.projectIds).toEqual(['11111111-1111-4111-8111-111111111111'])
  expect(state.environment).toBe('preview')
  expect(state.range).toBe('7d')
  expect(state.paused).toBe(true)
})
