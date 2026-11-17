import { apiFetch, ApiError } from '@/api/client'
import type { Issue } from '@/api/types'

// A short issue ID is a project prefix and an issue number, such as
// BACKEND-42 or MY-APP-7. It mirrors storage.ParseShortID on the server.
const SHORT_ID = /^[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*-[1-9][0-9]{0,17}$/
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

// isShortId reports whether s looks like a short ID. A UUID's last group can
// match the pattern, so UUIDs are ruled out first.
export function isShortId(s: string): boolean {
  const v = s.trim()
  return !UUID.test(v) && SHORT_ID.test(v)
}

// issueRef is how an issue's address names it: by its short ID, or by its
// UUID when it was loaded without one, which the issue page then swaps out.
export function issueRef(issue: { id: string; short_id?: string }): string {
  return issue.short_id || issue.id
}

// findIssueByShortId loads the issue a short ID names, following merges, or
// returns null when no issue has it.
export async function findIssueByShortId(s: string, signal?: AbortSignal): Promise<Issue | null> {
  if (!isShortId(s)) return null
  try {
    return await apiFetch<Issue>(`/api/issues/${encodeURIComponent(s.trim())}`, { signal })
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return null
    throw e
  }
}
