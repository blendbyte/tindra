import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ApiError, apiFetch } from '../client'

function makeResponse(
  status: number,
  body: unknown = null,
  contentType = 'application/json',
): Response {
  const bodyStr = body !== null ? JSON.stringify(body) : ''
  return new Response(bodyStr, {
    status,
    headers: { 'Content-Type': contentType },
  })
}

describe('ApiError', () => {
  it('carries status and message', () => {
    const err = new ApiError(404, 'not found')
    expect(err.status).toBe(404)
    expect(err.message).toBe('not found')
    expect(err).toBeInstanceOf(Error)
  })
})

describe('apiFetch', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })

  it('returns parsed JSON on 200', async () => {
    vi.mocked(fetch).mockResolvedValue(makeResponse(200, { ok: true }))
    const result = await apiFetch<{ ok: boolean }>('/api/test')
    expect(result).toEqual({ ok: true })
  })

  it('returns undefined on 204', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response(null, { status: 204 }))
    const result = await apiFetch('/api/test')
    expect(result).toBeUndefined()
  })

  it('returns undefined for non-JSON content-type', async () => {
    vi.mocked(fetch).mockResolvedValue(makeResponse(200, 'plain text', 'text/plain'))
    const result = await apiFetch('/api/test')
    expect(result).toBeUndefined()
  })

  it('throws ApiError on 4xx', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response('not found', { status: 404 }))
    await expect(apiFetch('/api/missing')).rejects.toBeInstanceOf(ApiError)
  })

  it('throws ApiError with correct status', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response('forbidden', { status: 403 }))
    await expect(apiFetch('/api/secret')).rejects.toMatchObject({ status: 403 })
  })

  it('throws ApiError on 5xx', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response('server error', { status: 500 }))
    await expect(apiFetch('/api/boom')).rejects.toBeInstanceOf(ApiError)
  })

  it('sends credentials and Content-Type by default', async () => {
    vi.mocked(fetch).mockResolvedValue(makeResponse(200, {}))
    await apiFetch('/api/test')
    expect(fetch).toHaveBeenCalledWith(
      '/api/test',
      expect.objectContaining({
        credentials: 'include',
        headers: expect.objectContaining({ 'Content-Type': 'application/json' }),
      }),
    )
  })

  it('merges caller headers with defaults', async () => {
    vi.mocked(fetch).mockResolvedValue(makeResponse(200, {}))
    await apiFetch('/api/test', { headers: { 'X-Custom': 'yes' } })
    expect(fetch).toHaveBeenCalledWith(
      '/api/test',
      expect.objectContaining({
        headers: expect.objectContaining({
          'Content-Type': 'application/json',
          'X-Custom': 'yes',
        }),
      }),
    )
  })

  it('redirects to /login on 401 from a non-auth endpoint', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response('unauthorized', { status: 401 }))
    const assign = vi.fn()
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { pathname: '/issues', href: '' },
    })
    Object.defineProperty(window.location, 'href', {
      set: assign,
      get: () => '',
    })
    await apiFetch('/api/issues')
    expect(assign).toHaveBeenCalledWith('/login')
  })

  it('throws ApiError on 401 from auth endpoints instead of redirecting', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response('bad credentials', { status: 401 }))
    await expect(apiFetch('/api/auth/login')).rejects.toBeInstanceOf(ApiError)
  })

  it('does not redirect on 401 when already on a public page', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response('unauthorized', { status: 401 }))
    const assign = vi.fn()
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { pathname: '/login', href: '' },
    })
    Object.defineProperty(window.location, 'href', {
      set: assign,
      get: () => '',
    })
    const result = await apiFetch('/api/issues')
    expect(assign).not.toHaveBeenCalled()
    expect(result).toBeUndefined()
  })

  it('does not redirect on 401 when on an invite page', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response('unauthorized', { status: 401 }))
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { pathname: '/invite/abc', href: '' },
    })
    const result = await apiFetch('/api/issues')
    expect(result).toBeUndefined()
  })

  it('returns undefined when response has no content-type header', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response('body', { status: 200, headers: {} }))
    const result = await apiFetch('/api/test')
    expect(result).toBeUndefined()
  })
})
