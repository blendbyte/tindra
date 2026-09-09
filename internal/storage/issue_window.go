package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type issueWindowQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Only event predicates belong in this CTE. Issue lifecycle state stays outside
// so an occurrence after the requested interval cannot hide a historical issue.
func windowIssueCTE(filter IssueFilter, bounds TimeRange) (string, []any) {
	args := []any{bounds.From, bounds.To}
	where := "timestamp >= $1 AND timestamp < $2 AND issue_id IS NOT NULL"
	if len(filter.ProjectIDs) > 0 {
		args = append(args, filter.ProjectIDs)
		where += fmt.Sprintf(" AND project_id = ANY($%d::uuid[])", len(args))
	}
	if filter.Environment != "" {
		args = append(args, filter.Environment)
		where += fmt.Sprintf(" AND environment = $%d", len(args))
	}
	if filter.UserIdentity != "" {
		args = append(args, filter.UserIdentity)
		where += fmt.Sprintf(" AND app_user_identity_hash(user_identity) = app_user_identity_hash($%[1]d::text) AND user_identity = $%[1]d", len(args))
	}
	origin := "$1::timestamptz"
	if bounds.AllTime {
		origin = "(SELECT COALESCE(min(timestamp),$2::timestamptz - INTERVAL '1 second') FROM matching)"
	}
	return `WITH matching AS MATERIALIZED (
 SELECT issue_id, timestamp, environment, COALESCE(payload->'user'->>'id',payload->'user'->>'username',payload->'user'->>'email',payload->'user'->>'ip_address') AS identity
 FROM events WHERE ` + where + `), scoped AS (
 SELECT issue_id, count(*) AS window_count, count(DISTINCT identity) AS window_users,
 min(timestamp) AS window_first, max(timestamp) AS window_last,
 CASE WHEN count(DISTINCT environment)=1 THEN max(environment) END AS window_environment
 FROM matching GROUP BY issue_id
 ), histogram AS (
 SELECT issue_id, floor(EXTRACT(EPOCH FROM (timestamp-` + origin + `)) / EXTRACT(EPOCH FROM ($2::timestamptz-` + origin + `))*24)::int AS bucket, count(*) AS n
 FROM matching GROUP BY issue_id,bucket
 ), histograms AS (
 SELECT issue_id,jsonb_object_agg(bucket,n) AS buckets FROM histogram GROUP BY issue_id
 ) `, args
}
func windowIssueFilters(q string, args []any, filter IssueFilter) (string, []any) {
	filter.Environment = ""
	filter.UserIdentity = ""
	return addCommonFilters(q, args, filter)
}
func countWindowIssues(ctx context.Context, pool issueWindowQuerier, filter IssueFilter, bounds TimeRange) (int, error) {
	cte, args := windowIssueCTE(filter, bounds)
	q, args := windowIssueFilters("SELECT count(*) FROM issues i JOIN scoped s ON s.issue_id=i.id WHERE TRUE", args, filter)
	var count int
	err := pool.QueryRow(ctx, cte+q, userQueryArgs(filter.UserIdentity, args)...).Scan(&count)
	return count, err
}
func listWindowIssues(ctx context.Context, pool issueWindowQuerier, filter IssueFilter, bounds TimeRange) ([]*Issue, error) {
	cte, args := windowIssueCTE(filter, bounds)
	// Histograms contain 24 equal-duration buckets for precisely the same interval.
	q := `SELECT i.id,i.project_id,i.fingerprint,i.title,i.level,i.kind,
 s.window_first,s.window_last,s.window_count,i.status,i.assignee_id,s.window_environment,
 i.ignore_until,i.ignore_count_limit,i.ignore_count,s.window_users,
 ARRAY(SELECT COALESCE((h.buckets->>bucket::text)::int,0) FROM generate_series(0,23) bucket ORDER BY bucket)
 FROM issues i JOIN scoped s ON s.issue_id=i.id JOIN histograms h ON h.issue_id=i.id WHERE TRUE`
	q, args = windowIssueFilters(q, args, filter)
	if filter.CursorTime != nil && filter.CursorID != nil {
		args = append(args, *filter.CursorTime, *filter.CursorID)
		q += fmt.Sprintf(" AND (s.window_last,i.id) < ($%d,$%d::uuid)", len(args)-1, len(args))
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 10000 {
		limit = 10000
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY s.window_last DESC,i.id DESC LIMIT $%d", len(args))
	rows, err := pool.Query(ctx, cte+q, userQueryArgs(filter.UserIdentity, args)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []*Issue{}
	for rows.Next() {
		issue := new(Issue)
		err = rows.Scan(&issue.ID, &issue.ProjectID, &issue.Fingerprint, &issue.Title, &issue.Level, &issue.Kind,
			&issue.FirstSeen, &issue.LastSeen, &issue.EventCount, &issue.Status, &issue.AssigneeID, &issue.Environment,
			&issue.IgnoreUntil, &issue.IgnoreCountLimit, &issue.IgnoreCount, &issue.UserCount, &issue.Sparkline)
		if err != nil {
			return nil, err
		}
		if filter.Environment != "" {
			env := filter.Environment
			issue.Environment = &env
		}
		result = append(result, issue)
	}
	return result, rows.Err()
}
