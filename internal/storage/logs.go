package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Log struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"project_id"`
	Timestamp   time.Time       `json:"timestamp"`
	ReceivedAt  time.Time       `json:"received_at"`
	Level       string          `json:"level"`
	Body        string          `json:"body"`
	TraceID     *string         `json:"trace_id,omitempty"`
	SpanID      *string         `json:"span_id,omitempty"`
	Environment *string         `json:"environment,omitempty"`
	Release     *string         `json:"release,omitempty"`
	Attributes  json.RawMessage `json:"attributes"`
}

type LogFilter struct {
	ProjectIDs  []string
	Level       string
	Environment string
	Search      string
	TraceID     string
	CursorTime  *time.Time
	CursorID    *string
	Limit       int
}

func ListLogs(ctx context.Context, pool *pgxpool.Pool, filter LogFilter) ([]*Log, bool, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	args := []any{}
	where := "TRUE"

	if len(filter.ProjectIDs) > 0 {
		args = append(args, filter.ProjectIDs)
		where += fmt.Sprintf(" AND project_id = ANY($%d::uuid[])", len(args))
	}
	if filter.Level != "" {
		// The log protocol spells this level "warn" while the rest of the app
		// uses "warning". Ingest normalizes to "warning", so match both to keep
		// rows stored before that normalization reachable.
		if filter.Level == "warn" || filter.Level == "warning" {
			where += " AND level IN ('warn', 'warning')"
		} else {
			args = append(args, filter.Level)
			where += fmt.Sprintf(" AND level = $%d", len(args))
		}
	}
	if filter.Environment != "" {
		args = append(args, filter.Environment)
		where += fmt.Sprintf(" AND environment = $%d", len(args))
	}
	if filter.TraceID != "" {
		args = append(args, filter.TraceID)
		where += fmt.Sprintf(" AND trace_id = $%d", len(args))
	}
	if filter.Search != "" {
		args = append(args, "%"+filter.Search+"%")
		where += fmt.Sprintf(" AND body ILIKE $%d", len(args))
	}
	if filter.CursorTime != nil && filter.CursorID != nil {
		n := len(args) + 1
		args = append(args, *filter.CursorTime, *filter.CursorID)
		where += fmt.Sprintf(
			" AND (timestamp < $%d OR (timestamp = $%d AND id < $%d::uuid))",
			n, n, n+1,
		)
	}

	// Fetch one extra row to determine has_more.
	args = append(args, limit+1)
	q := fmt.Sprintf(`
		SELECT id, project_id, timestamp, received_at, level, body,
		       trace_id, span_id, environment, release, attributes
		FROM logs
		WHERE %s
		ORDER BY timestamp DESC, id DESC
		LIMIT $%d
	`, where, len(args))

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, false, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var logs []*Log
	for rows.Next() {
		var l Log
		if err := rows.Scan(
			&l.ID, &l.ProjectID, &l.Timestamp, &l.ReceivedAt,
			&l.Level, &l.Body,
			&l.TraceID, &l.SpanID, &l.Environment, &l.Release,
			&l.Attributes,
		); err != nil {
			return nil, false, fmt.Errorf("scan: %w", err)
		}
		logs = append(logs, &l)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(logs) > limit
	if hasMore {
		logs = logs[:limit]
	}
	return logs, hasMore, nil
}

func DeleteLogsOlderThan(ctx context.Context, pool *pgxpool.Pool, cutoff time.Time) (int64, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM logs WHERE received_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete logs: %w", err)
	}
	return tag.RowsAffected(), nil
}
