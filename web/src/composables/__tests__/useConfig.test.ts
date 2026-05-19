import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'

vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiFetch: vi.fn(),
}))

import { useQuery } from '@tanstack/vue-query'

describe('useConfig', () => {
  beforeEach(() => {
    vi.mocked(useQuery).mockReturnValue({ data: ref(undefined) } as any)
  })

  describe('dsnFor', () => {
    it('builds a Sentry-compatible DSN from public_url', async () => {
      vi.mocked(useQuery).mockReturnValue({
        data: ref({ public_url: 'https://tindra.example.com' }),
      } as any)
      const { useConfig } = await import('../useConfig')
      const { dsnFor } = useConfig()
      expect(dsnFor('abc123', '42')).toBe('https://abc123@tindra.example.com/42')
    })

    it('strips trailing slash from public_url', async () => {
      vi.mocked(useQuery).mockReturnValue({
        data: ref({ public_url: 'https://tindra.example.com/' }),
      } as any)
      const { useConfig } = await import('../useConfig')
      const { dsnFor } = useConfig()
      expect(dsnFor('abc123', '42')).toBe('https://abc123@tindra.example.com/42')
    })

    it('falls back to window.location.origin when public_url is empty', async () => {
      vi.mocked(useQuery).mockReturnValue({
        data: ref({ public_url: '' }),
      } as any)
      const { useConfig } = await import('../useConfig')
      const { dsnFor } = useConfig()
      const expectedHost = new URL(window.location.origin).host
      expect(dsnFor('mykey', '7')).toBe(`http://mykey@${expectedHost}/7`)
    })

    it('falls back gracefully on an unparseable base URL', async () => {
      vi.mocked(useQuery).mockReturnValue({
        data: ref({ public_url: 'not-a-url' }),
      } as any)
      const { useConfig } = await import('../useConfig')
      const { dsnFor } = useConfig()
      expect(dsnFor('mykey', '7')).toBe('not-a-url/7')
    })
  })

  describe('baseUrl', () => {
    it('strips trailing slash', async () => {
      vi.mocked(useQuery).mockReturnValue({
        data: ref({ public_url: 'https://example.com/' }),
      } as any)
      const { useConfig } = await import('../useConfig')
      const { baseUrl } = useConfig()
      expect(baseUrl.value).toBe('https://example.com')
    })

    it('returns empty string when data is not yet loaded', async () => {
      vi.mocked(useQuery).mockReturnValue({ data: ref(undefined) } as any)
      const { useConfig } = await import('../useConfig')
      const { baseUrl } = useConfig()
      expect(baseUrl.value).toBe('')
    })
  })
})
