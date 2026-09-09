package digest

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Former queries retained for parity and performance comparisons.
func dailyErrorCounts(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, from, to time.Time) ([]DayStat, error) {
	rows, err := pool.Query(ctx, `
		SELECT date_trunc('day', received_at AT TIME ZONE 'UTC') AS day, COUNT(*) AS cnt
		FROM events
		WHERE project_id = ANY($1)
		  AND received_at >= $2 AND received_at < $3
		GROUP BY day
		ORDER BY day ASC
	`, projectIDs, from, to)
	if err != nil {
		return nil, fmt.Errorf("daily error counts: %w", err)
	}
	defer rows.Close()
	var stats []DayStat
	for rows.Next() {
		var s DayStat
		if err := rows.Scan(&s.Date, &s.Count); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}

func dailyTxCounts(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, from, to time.Time) ([]DayStat, error) {
	rows, err := pool.Query(ctx, `
		SELECT date_trunc('day', start_timestamp AT TIME ZONE 'UTC') AS day, COUNT(*) AS cnt
		FROM transactions
		WHERE project_id = ANY($1)
		  AND start_timestamp >= $2 AND start_timestamp < $3
		GROUP BY day
		ORDER BY day ASC
	`, projectIDs, from, to)
	if err != nil {
		return nil, fmt.Errorf("daily tx counts: %w", err)
	}
	defer rows.Close()
	var stats []DayStat
	for rows.Next() {
		var s DayStat
		if err := rows.Scan(&s.Date, &s.Count); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}

func projectBreakdown(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, from, to time.Time) ([]ProjectStat, error) {
	rows, err := pool.Query(ctx, `
		SELECT p.id, p.name,
		       COALESCE(e.cnt, 0)  AS errors,
		       COALESCE(t.cnt, 0)  AS transactions
		FROM projects p
		LEFT JOIN (
			SELECT project_id, COUNT(*) AS cnt
			FROM events
			WHERE project_id = ANY($1) AND received_at >= $2 AND received_at < $3
			GROUP BY project_id
		) e ON e.project_id = p.id
		LEFT JOIN (
			SELECT project_id, COUNT(*) AS cnt
			FROM transactions
			WHERE project_id = ANY($1) AND start_timestamp >= $2 AND start_timestamp < $3
			GROUP BY project_id
		) t ON t.project_id = p.id
		WHERE p.id = ANY($1)
		ORDER BY errors DESC, transactions DESC
	`, projectIDs, from, to)
	if err != nil {
		return nil, fmt.Errorf("project breakdown: %w", err)
	}
	defer rows.Close()
	var stats []ProjectStat
	for rows.Next() {
		var s ProjectStat
		if err := rows.Scan(&s.ProjectID, &s.ProjectName, &s.Errors, &s.Transactions); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}
