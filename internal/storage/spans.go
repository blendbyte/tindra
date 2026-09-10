package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type SpanSummary struct {
	Op          string   `json:"op"`
	Description string   `json:"description"`
	SampleCount int64    `json:"sample_count"`
	Rate        float64  `json:"rate"`
	P50         float64  `json:"p50"`
	P95         float64  `json:"p95"`
	TotalMs     int64    `json:"total_ms"`
	TimePct     float64  `json:"time_pct"`
	ErrorRate   float64  `json:"error_rate"`
	MissRate    *float64 `json:"miss_rate,omitempty"`
}

type SpanBucket struct {
	Time  time.Time `json:"time"`
	Count int64     `json:"count"`
}

type SpanTimeseries struct {
	Buckets    []SpanBucket `json:"buckets"`
	BucketSize string       `json:"bucket_size"`
}

func spanOpFilter(category string) string {
	switch category {
	case "db":
		return "s.op LIKE 'db%'"
	case "cache":
		return "s.op LIKE 'cache%'"
	case "job":
		return "(s.op LIKE 'task%' OR s.op LIKE 'queue%' OR s.op LIKE 'celery%' OR s.op LIKE 'job%')"
	}
	return "TRUE"
}

func GetSpanSummaries(ctx context.Context, pool *pgxpool.Pool, category string, projectIDs []string, hours int, env, release string, userIdentity ...string) ([]*SpanSummary, error) {
	if hours <= 0 || hours > 2160 {
		hours = 24
	}

	window := ResolveTimeRange(ctx, hours, 0)
	args := []any{window.From, window.To}
	where := fmt.Sprintf(`WHERE %s AND s.start_timestamp >= $1 AND s.start_timestamp < $2`, spanOpFilter(category))
	if len(projectIDs) > 0 {
		args = append(args, projectIDs)
		where += " AND s.project_id = ANY($3::uuid[])"
	}

	if env != "" {
		args = append(args, env)
		where += fmt.Sprintf(" AND s.environment = $%d", len(args))
	}
	if release != "" {
		args = append(args, release)
		where += fmt.Sprintf(" AND s.release = $%d", len(args))
	}
	if len(userIdentity) > 0 && userIdentity[0] != "" {
		args = append(args, userIdentity[0])
		where += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM transactions parent WHERE parent.id = s.transaction_id AND parent.project_id = s.project_id AND parent.user_identity = $%d)", len(args))
	}

	missRateExpr := `NULL`
	if category == "cache" {
		missRateExpr = `
			CASE
				WHEN COUNT(*) FILTER (WHERE s.data->>'cache.hit' IS NOT NULL) > 0
				THEN ROUND(
					COUNT(*) FILTER (WHERE s.data->>'cache.hit' = 'false') * 100.0
					/ NULLIF(COUNT(*) FILTER (WHERE s.data->>'cache.hit' IS NOT NULL), 0),
					1)
				ELSE NULL
			END`
	}

	q := fmt.Sprintf(`
		SELECT
			s.op,
			COALESCE(s.description, '') AS description,
			COUNT(*) AS sample_count,
			ROUND(COUNT(*)::numeric / GREATEST(EXTRACT(EPOCH FROM ($2::timestamptz - $1::timestamptz)) / 60.0, 1), 2) AS rate,
			COALESCE(PERCENTILE_CONT(0.5)  WITHIN GROUP (ORDER BY s.duration_ms), 0) AS p50,
			COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY s.duration_ms), 0) AS p95,
			SUM(s.duration_ms) AS total_ms,
			COALESCE(ROUND(
				SUM(s.duration_ms) * 100.0 / NULLIF(SUM(SUM(s.duration_ms)) OVER (), 0),
				1
			), 0) AS time_pct,
			ROUND(
				COUNT(*) FILTER (WHERE s.status IN ('internal_error', 'unavailable', 'data_loss', 'unknown_error', 'deadline_exceeded')) * 100.0
				/ NULLIF(COUNT(*), 0),
				2
			) AS error_rate,
			%s AS miss_rate
		FROM spans s
		%s
		GROUP BY s.op, s.description
		ORDER BY total_ms DESC
		LIMIT 200
	`, missRateExpr, where)

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []*SpanSummary
	for rows.Next() {
		var s SpanSummary
		if err := rows.Scan(
			&s.Op, &s.Description, &s.SampleCount, &s.Rate,
			&s.P50, &s.P95, &s.TotalMs, &s.TimePct, &s.ErrorRate, &s.MissRate,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, &s)
	}
	if out == nil {
		out = []*SpanSummary{}
	}
	return out, rows.Err()
}

func GetSpanTimeseries(ctx context.Context, pool *pgxpool.Pool, category string, projectIDs []string, hours int, env, release string, userIdentity ...string) (*SpanTimeseries, error) {
	if hours <= 0 || hours > 2160 {
		hours = 24
	}

	var bucketExpr, bucketSize string
	switch {
	case hours <= 1:
		bucketExpr = "date_trunc('minute', s.start_timestamp) - (EXTRACT(minute FROM s.start_timestamp)::int % 5) * INTERVAL '1 minute'"
		bucketSize = "5min"
	case hours <= 48:
		bucketExpr = "date_trunc('hour', s.start_timestamp)"
		bucketSize = "hour"
	default:
		bucketExpr = "date_trunc('day', s.start_timestamp)"
		bucketSize = "day"
	}

	window := ResolveTimeRange(ctx, hours, 0)
	args := []any{window.From, window.To}
	where := fmt.Sprintf(`WHERE %s AND s.start_timestamp >= $1 AND s.start_timestamp < $2`, spanOpFilter(category))
	if len(projectIDs) > 0 {
		args = append(args, projectIDs)
		where += " AND s.project_id = ANY($3::uuid[])"
	}

	if env != "" {
		args = append(args, env)
		where += fmt.Sprintf(" AND s.environment = $%d", len(args))
	}
	if release != "" {
		args = append(args, release)
		where += fmt.Sprintf(" AND s.release = $%d", len(args))
	}
	if len(userIdentity) > 0 && userIdentity[0] != "" {
		args = append(args, userIdentity[0])
		where += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM transactions parent WHERE parent.id = s.transaction_id AND parent.project_id = s.project_id AND parent.user_identity = $%d)", len(args))
	}

	q := fmt.Sprintf(`
		SELECT
			%s AS bucket,
			COUNT(*) AS count
		FROM spans s
		%s
		GROUP BY bucket
		ORDER BY bucket ASC
	`, bucketExpr, where)

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	ts := &SpanTimeseries{BucketSize: bucketSize, Buckets: []SpanBucket{}}
	for rows.Next() {
		var b SpanBucket
		if err := rows.Scan(&b.Time, &b.Count); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		ts.Buckets = append(ts.Buckets, b)
	}
	return ts, rows.Err()
}

type SpanSample struct {
	SpanID          string    `json:"span_id"`
	TransactionID   string    `json:"transaction_id"`
	Op              string    `json:"op"`
	Description     string    `json:"description"`
	DurationMs      int       `json:"duration_ms"`
	Status          string    `json:"status"`
	StartTimestamp  time.Time `json:"start_timestamp"`
	TransactionName string    `json:"transaction_name"`
	TraceID         string    `json:"trace_id"`
}

func GetSpanSamples(ctx context.Context, pool *pgxpool.Pool, op, description string, projectIDs []string, hours int, env, release string, userIdentity ...string) ([]*SpanSample, error) {
	if hours <= 0 || hours > 2160 {
		hours = 24
	}
	if projectIDs == nil {
		projectIDs = []string{}
	}

	window := ResolveTimeRange(ctx, hours, 0)
	args := []any{window.From, projectIDs, op, description, window.To}
	where := `
		WHERE s.op = $3
		  AND COALESCE(s.description, '') = $4
		  AND s.start_timestamp >= $1 AND s.start_timestamp < $5
		  AND (CARDINALITY($2::uuid[]) = 0 OR s.project_id = ANY($2::uuid[]))`

	if env != "" {
		args = append(args, env)
		where += fmt.Sprintf(" AND s.environment = $%d", len(args))
	}
	if release != "" {
		args = append(args, release)
		where += fmt.Sprintf(" AND s.release = $%d", len(args))
	}
	if len(userIdentity) > 0 && userIdentity[0] != "" {
		args = append(args, userIdentity[0])
		where += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM transactions parent WHERE parent.id = s.transaction_id AND parent.project_id = s.project_id AND parent.user_identity = $%d)", len(args))
	}

	rows, err := pool.Query(ctx, `
		SELECT s.span_id, s.transaction_id, s.op, COALESCE(s.description, ''),
		       s.duration_ms, s.status, s.start_timestamp,
		       t.transaction, COALESCE(t.trace_id, '')
		FROM spans s
		JOIN transactions t ON t.id = s.transaction_id
		`+where+`
		ORDER BY s.start_timestamp DESC
		LIMIT 50
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []*SpanSample
	for rows.Next() {
		var s SpanSample
		if err := rows.Scan(
			&s.SpanID, &s.TransactionID, &s.Op, &s.Description,
			&s.DurationMs, &s.Status, &s.StartTimestamp,
			&s.TransactionName, &s.TraceID,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, &s)
	}
	if out == nil {
		out = []*SpanSample{}
	}
	return out, rows.Err()
}
