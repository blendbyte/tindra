import type { QueryClient } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import type { Comment, IssueHistoryEntry, TagSummary } from '@/api/types'

// Queries the issue sections run as soon as they know the issue ID. They are
// defined once here so the sections and the page's prefetch use the same keys.
export const issueSectionQueries = {
  comments: (issueId: string) => ({
    queryKey: ['issues', issueId, 'comments'],
    queryFn: ({ signal }: { signal: AbortSignal }) => apiFetch<Comment[]>(`/api/issues/${issueId}/comments`, { signal }),
  }),
  history: (issueId: string) => ({
    queryKey: ['issues', issueId, 'history'],
    queryFn: ({ signal }: { signal: AbortSignal }) => apiFetch<IssueHistoryEntry[]>(`/api/issues/${issueId}/history`, { signal }),
  }),
  tags: (issueId: string) => ({
    queryKey: ['issues', issueId, 'tags'],
    queryFn: ({ signal }: { signal: AbortSignal }) => apiFetch<TagSummary[]>(`/api/issues/${issueId}/tags`, { signal }),
  }),
}

// prefetchIssueSections starts the section requests alongside the issue
// request. The sections only mount once the issue has loaded, so without this
// they would wait a full round trip before asking for their own data.
export function prefetchIssueSections(qc: QueryClient, issueId: string) {
  void qc.prefetchQuery(issueSectionQueries.comments(issueId))
  void qc.prefetchQuery(issueSectionQueries.history(issueId))
  void qc.prefetchQuery(issueSectionQueries.tags(issueId))
}
