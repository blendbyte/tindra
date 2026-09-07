package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	// TransactionID is the transaction that shares this log's trace_id, when one
	// exists. It lets the UI link a log line straight to its trace waterfall.
	TransactionID *string `json:"transaction_id,omitempty"`
}

type LogFilter struct {
	ProjectIDs []string
	Level      string
	// Levels, when non-empty, matches any of the given levels (at-or-above
	// queries). Takes precedence over Level. "warning" also matches "warn".
	Levels      []string
	Environment string
	Search      string
	TraceID     string
	// WindowMins, when > 0, restricts to timestamp in (NOW() - window, NOW() + 2m].
	WindowMins int
	CursorTime *time.Time
	CursorID   *string
	Limit      int
}

func ListLogs(ctx context.Context, pool *pgxpool.Pool, filter LogFilter) ([]*Log, bool, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	where, args := appendLogWhere(filter, "TRUE", nil)
	if filter.CursorTime != nil && filter.CursorID != nil {
		n := len(args) + 1
		args = append(args, *filter.CursorTime, *filter.CursorID)
		where += fmt.Sprintf(
			" AND (l.timestamp < $%d OR (l.timestamp = $%d AND l.id < $%d::uuid))",
			n, n, n+1,
		)
	}

	// Fetch one extra row to determine has_more.
	args = append(args, limit+1)
	// The lateral join resolves each log's trace to the most recent transaction
	// carrying the same trace_id, so the UI can deep-link to the trace.
	q := fmt.Sprintf(`
		SELECT l.id, l.project_id, l.timestamp, l.received_at, l.level, l.body,
		       l.trace_id, l.span_id, l.environment, l.release, l.attributes, tx.id
		FROM logs l
		LEFT JOIN LATERAL (
			SELECT t.id FROM transactions t
			WHERE l.trace_id IS NOT NULL AND t.trace_id = l.trace_id
			ORDER BY t.received_at DESC
			LIMIT 1
		) tx ON TRUE
		WHERE %s
		ORDER BY l.timestamp DESC, l.id DESC
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
			&l.Attributes, &l.TransactionID,
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

// CountLogs returns how many logs match filter. Used by the alert preview
// endpoint and by enrichPayload after a log_count rule fires.
func CountLogs(ctx context.Context, pool *pgxpool.Pool, filter LogFilter) (int, error) {
	where, args := appendLogWhere(filter, "TRUE", nil)
	q := fmt.Sprintf(`SELECT COUNT(*) FROM logs l WHERE %s`, where)
	var count int
	if err := pool.QueryRow(ctx, q, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count logs: %w", err)
	}
	return count, nil
}

// LogsReachThreshold reports whether at least threshold matching logs exist.
// The inner LIMIT stops the index scan once the bar is met, so the 15s
// evaluator tick never walks a hot window just to prove N was exceeded.
func LogsReachThreshold(ctx context.Context, pool *pgxpool.Pool, filter LogFilter, threshold int) (bool, error) {
	if threshold <= 0 {
		return false, nil
	}
	where, args := appendLogWhere(filter, "TRUE", nil)
	args = append(args, threshold)
	q := fmt.Sprintf(`
		SELECT COUNT(*) FROM (
			SELECT 1 FROM logs l WHERE %s LIMIT $%d
		) t
	`, where, len(args))
	var count int
	if err := pool.QueryRow(ctx, q, args...).Scan(&count); err != nil {
		return false, fmt.Errorf("logs reach threshold: %w", err)
	}
	return count >= threshold, nil
}

func DeleteLogsOlderThan(ctx context.Context, pool *pgxpool.Pool, cutoff time.Time) (int64, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM logs WHERE received_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete logs: %w", err)
	}
	return tag.RowsAffected(), nil
}

func appendLogWhere(filter LogFilter, where string, args []any) (string, []any) {
	if len(filter.ProjectIDs) > 0 {
		args = append(args, filter.ProjectIDs)
		where += fmt.Sprintf(" AND l.project_id = ANY($%d::uuid[])", len(args))
	}
	if len(filter.Levels) > 0 {
		args = append(args, expandWarnLevels(filter.Levels))
		where += fmt.Sprintf(" AND l.level = ANY($%d::text[])", len(args))
	} else if filter.Level != "" {
		// The log protocol spells this level "warn" while the rest of the app
		// uses "warning". Ingest normalizes to "warning", so match both to keep
		// rows stored before that normalization reachable.
		if filter.Level == "warn" || filter.Level == "warning" {
			where += " AND l.level IN ('warn', 'warning')"
		} else {
			args = append(args, filter.Level)
			where += fmt.Sprintf(" AND l.level = $%d", len(args))
		}
	}
	if filter.Environment != "" {
		args = append(args, filter.Environment)
		where += fmt.Sprintf(" AND l.environment = $%d", len(args))
	}
	if filter.TraceID != "" {
		args = append(args, filter.TraceID)
		where += fmt.Sprintf(" AND l.trace_id = $%d", len(args))
	}
	if filter.Search != "" {
		args = append(args, likeContains(filter.Search))
		where += fmt.Sprintf(" AND l.body ILIKE $%d ESCAPE E'\\\\'", len(args))
	}
	if filter.WindowMins > 0 {
		args = append(args, filter.WindowMins)
		// +2 minutes rejects far-future client clocks without dropping
		// slightly-skewed SDK timestamps.
		where += fmt.Sprintf(
			" AND l.timestamp > NOW() - make_interval(mins => $%d::int) AND l.timestamp <= NOW() + INTERVAL '2 minutes'",
			len(args),
		)
	}
	return where, args
}

func expandWarnLevels(levels []string) []string {
	hasWarn, hasWarning := false, false
	for _, l := range levels {
		if l == "warn" {
			hasWarn = true
		}
		if l == "warning" {
			hasWarning = true
		}
	}
	if !hasWarn && !hasWarning {
		return levels
	}
	out := make([]string, 0, len(levels)+1)
	out = append(out, levels...)
	if hasWarning && !hasWarn {
		out = append(out, "warn")
	}
	if hasWarn && !hasWarning {
		out = append(out, "warning")
	}
	return out
}

// likeContains wraps s as an ILIKE contains pattern and escapes LIKE
// metacharacters so a user search of "%" does not match every row.
func likeContains(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return "%" + s + "%"
}
