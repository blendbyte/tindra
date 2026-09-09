package digest

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DayStat struct {
	Date  time.Time
	Count int64
}

type ProjectStat struct {
	ProjectID    string
	ProjectName  string
	Errors       int64
	Transactions int64
}

type IssuesSummary struct {
	New       int
	Regressed int
	Ongoing   int
}

type TopIssue struct {
	IssueID     string
	Title       string
	ProjectName string
	Count       int64
	Status      string
}

type TopTransaction struct {
	Transaction string
	ProjectName string
	Count       int64
	P50Ms       int64
	P95Ms       int64
	P50PrevMs   int64
}

func issuesSummary(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, from, to time.Time) (IssuesSummary, error) {
	var s IssuesSummary
	err := pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE first_seen >= $2 AND first_seen < $3)           AS new_count,
			COUNT(*) FILTER (WHERE status = 'regressed')                            AS regressed_count,
			COUNT(*) FILTER (WHERE status = 'open' AND first_seen < $2)            AS ongoing_count
		FROM issues
		WHERE project_id = ANY($1)
		  AND status IN ('open', 'regressed')
	`, projectIDs, from, to).Scan(&s.New, &s.Regressed, &s.Ongoing)
	if err != nil {
		return s, fmt.Errorf("issues summary: %w", err)
	}
	return s, nil
}

func topIssues(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, from, to time.Time, limit int) ([]TopIssue, error) {
	rows, err := pool.Query(ctx, `
		SELECT i.id, i.title, p.name, COUNT(e.id) AS cnt, i.status
		FROM events e
		JOIN issues i ON i.id = e.issue_id
		JOIN projects p ON p.id = i.project_id
		WHERE e.project_id = ANY($1)
		  AND e.received_at >= $2 AND e.received_at < $3
		  AND e.issue_id IS NOT NULL
		GROUP BY i.id, i.title, p.name, i.status
		ORDER BY cnt DESC
		LIMIT $4
	`, projectIDs, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("top issues: %w", err)
	}
	defer rows.Close()
	var issues []TopIssue
	for rows.Next() {
		var issue TopIssue
		if err := rows.Scan(&issue.IssueID, &issue.Title, &issue.ProjectName, &issue.Count, &issue.Status); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}

func topTransactions(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, from, to time.Time, limit int) ([]TopTransaction, error) {
	prevFrom := from.AddDate(0, 0, -7)
	rows, err := pool.Query(ctx, `
		WITH cur AS MATERIALIZED (
			SELECT t.transaction, p.name AS project_name,
			       COUNT(*) AS cnt,
			       PERCENTILE_CONT(0.5)  WITHIN GROUP (ORDER BY t.duration_ms)::bigint AS p50,
			       PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY t.duration_ms)::bigint AS p95
			FROM transactions t
			JOIN projects p ON p.id = t.project_id
			WHERE t.project_id = ANY($1)
			  AND t.start_timestamp >= $2 AND t.start_timestamp < $3
			GROUP BY t.transaction, p.name
			ORDER BY cnt DESC
			LIMIT $4
		),
		prev AS (
			SELECT t.transaction,
			       PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY t.duration_ms)::bigint AS p50
			FROM transactions t
			WHERE t.project_id = ANY($1)
			  AND t.start_timestamp >= $5 AND t.start_timestamp < $2
			  AND t.transaction IN (SELECT transaction FROM cur)
			GROUP BY t.transaction
		)
		SELECT cur.transaction, cur.project_name, cur.cnt, cur.p50, cur.p95,
		       COALESCE(prev.p50, 0) AS p50_prev
		FROM cur
		LEFT JOIN prev ON prev.transaction = cur.transaction
	`, projectIDs, from, to, limit, prevFrom)
	if err != nil {
		return nil, fmt.Errorf("top transactions: %w", err)
	}
	defer rows.Close()
	var txns []TopTransaction
	for rows.Next() {
		var tx TopTransaction
		if err := rows.Scan(&tx.Transaction, &tx.ProjectName, &tx.Count, &tx.P50Ms, &tx.P95Ms, &tx.P50PrevMs); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		txns = append(txns, tx)
	}
	return txns, rows.Err()
}
