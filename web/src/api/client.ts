export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message)
  }
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...init?.headers },
  })
  if (!res.ok) {
    if (res.status === 401 && !path.includes('/api/auth/')) {
      const here = window.location.pathname
      const isPublic =
        here === '/login' || here.startsWith('/invite/') || here.startsWith('/reset-password/')
      if (!isPublic) {
        window.location.href = '/login'
        return undefined as T
      }
    }
    const text = await res.text().catch(() => res.statusText)
    throw new ApiError(res.status, text)
  }
  if (res.status === 204) return undefined as T
  const ct = res.headers.get('content-type') ?? ''
  if (!ct.includes('application/json')) return undefined as T
  return res.json() as Promise<T>
}
