import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('@/api/client', () => {
  class ApiError extends Error {
    constructor(public status: number, message: string) { super(message) }
  }
  return { apiFetch: vi.fn(), ApiError }
})

import { apiFetch, ApiError } from '@/api/client'
import { isShortId, findIssueByShortId } from '../shortId'

describe('isShortId', () => {
  it('accepts a prefix and an issue number', () => {
    for (const s of ['BACKEND-42', 'backend-42', ' MY-APP-7 ', 'APP-2-5', 'BACKEND2-1']) {
      expect(isShortId(s), s).toBe(true)
    }
  })

  it('rejects other text, a zero or padded number, and UUIDs', () => {
    for (const s of ['BACKEND', 'BACKEND-0', 'BACKEND-042', '-42', 'back end-4', '', '550e8400-e29b-41d4-a716-446655440000']) {
      expect(isShortId(s), s).toBe(false)
    }
  })
})

describe('findIssueByShortId', () => {
  beforeEach(() => { vi.mocked(apiFetch).mockClear() })

  it('loads the issue a short ID names', async () => {
    vi.mocked(apiFetch).mockResolvedValue({ id: 'iss-1', short_id: 'BACKEND-42' })
    expect(await findIssueByShortId(' BACKEND-42 ')).toEqual({ id: 'iss-1', short_id: 'BACKEND-42' })
    expect(vi.mocked(apiFetch).mock.calls[0][0]).toBe('/api/issues/BACKEND-42')
  })

  it('returns null for an unknown short ID and skips other text', async () => {
    vi.mocked(apiFetch).mockImplementation(async () => { throw new ApiError(404, 'not found') })
    expect(await findIssueByShortId('BACKEND-999')).toBeNull()
    vi.mocked(apiFetch).mockClear()
    expect(await findIssueByShortId('timeout error')).toBeNull()
    expect(apiFetch).not.toHaveBeenCalled()
  })

  it('passes other failures on', async () => {
    vi.mocked(apiFetch).mockImplementation(async () => { throw new ApiError(500, 'boom') })
    await expect(findIssueByShortId('BACKEND-42')).rejects.toThrow('boom')
  })
})
