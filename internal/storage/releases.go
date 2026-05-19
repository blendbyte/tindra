package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Release struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id"`
	Version         string    `json:"version"`
	DeployedAt      time.Time `json:"deployed_at"`
	NewIssues       int       `json:"new_issues"`
	RegressedIssues int       `json:"regressed_issues"`
	TxCount         int64     `json:"tx_count"`
	TxP50           float64   `json:"tx_p50"`
	TxErrorRate     float64   `json:"tx_error_rate"`
	CreatedAt       time.Time `json:"created_at"`
}

// ReleaseIssue is a minimal issue summary used on the release detail page.
type ReleaseIssue struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	Level      string    `json:"level"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	EventCount int       `json:"event_count"`
	// Category is "new", "regressed", or "ongoing".
	Category string `json:"category"`
}

// ReleaseTxSummary is a per-transaction performance summary scoped to a release.
type ReleaseTxSummary struct {
	Transaction string  `json:"transaction"`
	Op          string  `json:"op"`
	SampleCount int64   `json:"sample_count"`
	P50         float64 `json:"p50"`
	P95         float64 `json:"p95"`
	ErrorRate   float64 `json:"error_rate"`
}

type ReleaseFilter struct {
	ProjectIDs []string
	CursorTime *time.Time
	CursorID   *string
	Limit      int
}

// releaseSelectSQL is the common SELECT + LEFT JOIN block for release queries.
const releaseSelectSQL = `
	SELECT
		r.id, r.project_id, r.version, r.deployed_at, r.created_at,
		(SELECT COUNT(*) FROM issues
		 WHERE project_id = r.project_id AND first_release = r.version)                     AS new_issues,
		(SELECT COUNT(DISTINCT i.id) FROM issues i
		 JOIN events e ON e.issue_id = i.id
		 WHERE i.status = 'regressed'
		   AND e.project_id = r.project_id AND e.release = r.version)                       AS regressed_issues,
		COUNT(t.id)                                                                          AS tx_count,
		COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY t.duration_ms), 0)             AS tx_p50,
		COALESCE(ROUND(
			COUNT(t.id) FILTER (WHERE t.status != 'ok') * 100.0 / NULLIF(COUNT(t.id), 0), 1
		), 0)                                                                                AS tx_error_rate
	FROM releases r
	LEFT JOIN transactions t ON t.project_id = r.project_id AND t.release = r.version`

func scanRelease(row pgx.Row, r *Release) error {
	return row.Scan(
		&r.ID, &r.ProjectID, &r.Version, &r.DeployedAt, &r.CreatedAt,
		&r.NewIssues, &r.RegressedIssues, &r.TxCount, &r.TxP50, &r.TxErrorRate,
	)
}

func CountReleases(ctx context.Context, pool *pgxpool.Pool, filter ReleaseFilter) (int, error) {
	q := `SELECT COUNT(*) FROM releases r WHERE TRUE`
	args := []any{}
	if len(filter.ProjectIDs) > 0 {
		args = append(args, filter.ProjectIDs)
		q += fmt.Sprintf(" AND r.project_id = ANY($%d::uuid[])", len(args))
	}
	var n int
	if err := pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count releases: %w", err)
	}
	return n, nil
}

func ListReleases(ctx context.Context, pool *pgxpool.Pool, filter ReleaseFilter) ([]*Release, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	args := []any{}
	where := " WHERE TRUE"
	if len(filter.ProjectIDs) > 0 {
		args = append(args, filter.ProjectIDs)
		where += fmt.Sprintf(" AND r.project_id = ANY($%d::uuid[])", len(args))
	}
	if filter.CursorTime != nil && filter.CursorID != nil {
		n := len(args) + 1
		args = append(args, *filter.CursorTime, *filter.CursorID)
		where += fmt.Sprintf(
			" AND (r.deployed_at < $%d OR (r.deployed_at = $%d AND r.id < $%d::uuid))",
			n, n, n+1,
		)
	}
	args = append(args, limit)

	q := releaseSelectSQL + where +
		" GROUP BY r.id, r.project_id, r.version, r.deployed_at, r.created_at" +
		fmt.Sprintf(" ORDER BY r.deployed_at DESC, r.id DESC LIMIT $%d", len(args))

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var releases []*Release
	for rows.Next() {
		var r Release
		if err := rows.Scan(
			&r.ID, &r.ProjectID, &r.Version, &r.DeployedAt, &r.CreatedAt,
			&r.NewIssues, &r.RegressedIssues, &r.TxCount, &r.TxP50, &r.TxErrorRate,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		releases = append(releases, &r)
	}
	return releases, rows.Err()
}

func GetRelease(ctx context.Context, pool *pgxpool.Pool, id string) (*Release, error) {
	var r Release
	err := scanRelease(pool.QueryRow(ctx, releaseSelectSQL+`
		WHERE r.id = $1
		GROUP BY r.id, r.project_id, r.version, r.deployed_at, r.created_at
	`, id), &r)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return &r, nil
}

func GetReleaseTransactions(ctx context.Context, pool *pgxpool.Pool, releaseID string) ([]*ReleaseTxSummary, error) {
	var projectID, version string
	err := pool.QueryRow(ctx, `SELECT project_id, version FROM releases WHERE id = $1`, releaseID).
		Scan(&projectID, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve release: %w", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT
			transaction,
			COALESCE(op, '') AS op,
			COUNT(*) AS sample_count,
			COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY duration_ms), 0) AS p50,
			COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY duration_ms), 0) AS p95,
			ROUND(COUNT(*) FILTER (WHERE status != 'ok') * 100.0 / NULLIF(COUNT(*), 0), 2) AS error_rate
		FROM transactions
		WHERE project_id = $1 AND release = $2
		GROUP BY transaction, op
		ORDER BY COUNT(*) DESC
		LIMIT 50
	`, projectID, version)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []*ReleaseTxSummary
	for rows.Next() {
		var s ReleaseTxSummary
		if err := rows.Scan(&s.Transaction, &s.Op, &s.SampleCount, &s.P50, &s.P95, &s.ErrorRate); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

func GetReleaseIssues(ctx context.Context, pool *pgxpool.Pool, releaseID string) ([]*ReleaseIssue, error) {
	var projectID, version string
	err := pool.QueryRow(ctx, `SELECT project_id, version FROM releases WHERE id = $1`, releaseID).
		Scan(&projectID, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve release: %w", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT
			i.id, i.title, i.status, i.level, i.first_seen, i.last_seen, i.event_count,
			CASE
				WHEN i.first_release = $2 THEN 'new'
				WHEN i.status = 'regressed' THEN 'regressed'
				ELSE 'ongoing'
			END AS category
		FROM issues i
		WHERE i.id IN (
			SELECT DISTINCT issue_id FROM events
			WHERE project_id = $1 AND release = $2 AND issue_id IS NOT NULL
		)
		ORDER BY
			CASE
				WHEN i.first_release = $2 THEN 0
				WHEN i.status = 'regressed' THEN 1
				ELSE 2
			END,
			i.last_seen DESC
		LIMIT 100
	`, projectID, version)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var issues []*ReleaseIssue
	for rows.Next() {
		var ri ReleaseIssue
		if err := rows.Scan(&ri.ID, &ri.Title, &ri.Status, &ri.Level, &ri.FirstSeen, &ri.LastSeen, &ri.EventCount, &ri.Category); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		issues = append(issues, &ri)
	}
	return issues, rows.Err()
}
