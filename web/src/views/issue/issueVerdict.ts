import type { Issue } from '@/api/types'

// The issue page opens with one sentence a person on call can act on: what
// state the issue is in and since when, then who owns it. `lead` carries the
// state and takes its colour (amber for a regression, violet for new);
// `rest` is plain text.
export interface IssueVerdict {
  lead: string
  tone: 'regressed' | 'new' | 'quiet'
  rest: string
}

type Rel = (iso: string) => string

export function issueVerdict(
  issue: Pick<Issue, 'status' | 'first_seen' | 'last_seen' | 'first_release' | 'regressed_at' | 'release' | 'ignore_until' | 'ignore_count_limit' | 'ignore_count'>,
  opts: { newSince: string | null; owner: string | null; rel: Rel; span: Rel; date: (iso: string) => string },
): IssueVerdict {
  const owner = opts.owner ? `${opts.owner} is on it.` : 'Nobody is assigned yet.'
  switch (issue.status) {
    case 'regressed': {
      const at = issue.regressed_at ?? issue.last_seen
      const release = issue.release ? ` in ${issue.release}` : ''
      return { lead: `Regressed ${opts.rel(at)}${release}`, tone: 'regressed', rest: `after it was resolved. ${owner}` }
    }
    case 'resolved':
      return { lead: 'Resolved', tone: 'quiet', rest: `and last seen ${opts.rel(issue.last_seen)}.` }
    case 'ignored': {
      if (issue.ignore_until) return { lead: 'Ignored', tone: 'quiet', rest: `until ${opts.date(issue.ignore_until)}.` }
      if (issue.ignore_count_limit != null) {
        const left = Math.max(0, issue.ignore_count_limit - (issue.ignore_count ?? 0))
        return { lead: 'Ignored', tone: 'quiet', rest: `for ${left} more ${left === 1 ? 'event' : 'events'}.` }
      }
      return { lead: 'Ignored', tone: 'quiet', rest: 'until you change it.' }
    }
  }
  const isNew = opts.newSince !== null && Date.parse(issue.first_seen) >= Date.parse(opts.newSince)
  if (isNew) {
    const release = issue.first_release ? ` in ${issue.first_release}` : ''
    return { lead: `New ${opts.rel(issue.first_seen)}${release}.`, tone: 'new', rest: owner }
  }
  return { lead: `Ongoing for ${opts.span(issue.first_seen)}`, tone: 'quiet', rest: `and last seen ${opts.rel(issue.last_seen)}. ${owner}` }
}
