package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Transaction struct {
	ID             string          `json:"id"`
	ProjectID      string          `json:"project_id"`
	TraceID        string          `json:"trace_id,omitempty"`
	SpanID         string          `json:"span_id,omitempty"`
	Transaction    string          `json:"transaction"`
	Op             string          `json:"op"`
	Status         string          `json:"status"`
	DurationMs     int             `json:"duration_ms"`
	StartTimestamp time.Time       `json:"start_timestamp"`
	Timestamp      time.Time       `json:"timestamp"`
	ReceivedAt     time.Time       `json:"received_at"`
	Environment    string          `json:"environment,omitempty"`
	Release        string          `json:"release,omitempty"`
	Platform       string          `json:"platform,omitempty"`
	Measurements   json.RawMessage `json:"measurements,omitempty"`
}

type Span struct {
	ID             string          `json:"id"`
	TransactionID  string          `json:"transaction_id"`
	SpanID         string          `json:"span_id"`
	ParentSpanID   string          `json:"parent_span_id,omitempty"`
	Op             string          `json:"op"`
	Description    string          `json:"description,omitempty"`
	StartTimestamp time.Time       `json:"start_timestamp"`
	Timestamp      time.Time       `json:"timestamp"`
	DurationMs     int             `json:"duration_ms"`
	Status         string          `json:"status"`
	Data           json.RawMessage `json:"data,omitempty"`
}

// TraceError is a single error event that occurred within a trace, used to correlate
// errors with specific spans in the transaction waterfall.
type TraceError struct {
	EventID   string    `json:"event_id"`
	SpanID    string    `json:"span_id,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	IssueID   string    `json:"issue_id"`
	Title     string    `json:"title"`
	Level     string    `json:"level"`
	Status    string    `json:"status"`
}

type TransactionFilter struct {
	ProjectIDs   []string
	Op           string
	Ops          []string // when set, op = ANY(...); takes precedence over Op
	Status       string
	Environment  string
	Name         string
	UserIdentity string
	Since        *time.Time
	CursorTime   *time.Time
	CursorID     *string
	Limit        int
}

type TransactionPercentiles struct {
	P50         float64 `json:"p50"`
	P75         float64 `json:"p75"`
	P95         float64 `json:"p95"`
	P99         float64 `json:"p99"`
	SampleCount int64   `json:"sample_count"`
}

func ListTransactions(ctx context.Context, pool *pgxpool.Pool, projectID string, filter TransactionFilter) ([]*Transaction, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	args := []any{projectID}
	q := `SELECT id, project_id, COALESCE(trace_id,''), COALESCE(span_id,''), transaction,
	             op, status, duration_ms, start_timestamp, timestamp, received_at,
	             COALESCE(environment,''), COALESCE(release,''), COALESCE(platform,'')
	      FROM transactions WHERE project_id = $1`

	if filter.Op != "" {
		args = append(args, filter.Op)
		q += fmt.Sprintf(" AND op = $%d", len(args))
	}
	if filter.Status != "" {
		args = append(args, filter.Status)
		q += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if filter.Environment != "" {
		args = append(args, filter.Environment)
		q += fmt.Sprintf(" AND environment = $%d", len(args))
	}
	q, args = appendTxUserAndTime(q, args, filter)
	if filter.CursorTime != nil && filter.CursorID != nil {
		n := len(args) + 1
		args = append(args, *filter.CursorTime, *filter.CursorID)
		q += fmt.Sprintf(
			" AND (start_timestamp, id) < ($%d, $%d::uuid)",
			n, n+1,
		)
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY start_timestamp DESC, id DESC LIMIT $%d", len(args))

	rows, err := pool.Query(ctx, q, userQueryArgs(filter.UserIdentity, args)...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var txns []*Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(
			&t.ID, &t.ProjectID, &t.TraceID, &t.SpanID, &t.Transaction,
			&t.Op, &t.Status, &t.DurationMs, &t.StartTimestamp, &t.Timestamp, &t.ReceivedAt,
			&t.Environment, &t.Release, &t.Platform,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		txns = append(txns, &t)
	}
	return txns, rows.Err()
}

func appendTxUserAndTime(q string, args []any, filter TransactionFilter) (string, []any) {
	if len(filter.Ops) > 0 {
		args = append(args, filter.Ops)
		q += fmt.Sprintf(" AND op = ANY($%d::text[])", len(args))
	}
	if filter.UserIdentity != "" {
		args = append(args, filter.UserIdentity)
		q += fmt.Sprintf(" AND app_user_identity_hash(user_identity) = app_user_identity_hash($%[1]d::text) AND user_identity = $%[1]d", len(args))
	}
	if filter.Since != nil {
		args = append(args, *filter.Since)
		q += fmt.Sprintf(" AND start_timestamp >= $%d", len(args))
	}
	return q, args
}

func ListAllTransactions(ctx context.Context, pool *pgxpool.Pool, filter TransactionFilter) ([]*Transaction, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	args := []any{}
	q := `SELECT id, project_id, COALESCE(trace_id,''), COALESCE(span_id,''), transaction,
	             op, status, duration_ms, start_timestamp, timestamp, received_at,
	             COALESCE(environment,''), COALESCE(release,''), COALESCE(platform,''),
	             measurements
	      FROM transactions WHERE TRUE`

	if len(filter.ProjectIDs) > 0 {
		args = append(args, filter.ProjectIDs)
		q += fmt.Sprintf(" AND project_id = ANY($%d::uuid[])", len(args))
	}
	if len(filter.Ops) == 0 && filter.Op != "" {
		args = append(args, filter.Op)
		q += fmt.Sprintf(" AND op = $%d", len(args))
	}
	if filter.Status != "" {
		args = append(args, filter.Status)
		q += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if filter.Environment != "" {
		args = append(args, filter.Environment)
		q += fmt.Sprintf(" AND environment = $%d", len(args))
	}
	if filter.Name != "" {
		args = append(args, filter.Name)
		q += fmt.Sprintf(" AND transaction = $%d", len(args))
	}
	q, args = appendTxUserAndTime(q, args, filter)
	if filter.CursorTime != nil && filter.CursorID != nil {
		n := len(args) + 1
		args = append(args, *filter.CursorTime, *filter.CursorID)
		q += fmt.Sprintf(
			" AND (start_timestamp, id) < ($%d, $%d::uuid)",
			n, n+1,
		)
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY start_timestamp DESC, id DESC LIMIT $%d", len(args))

	rows, err := pool.Query(ctx, q, userQueryArgs(filter.UserIdentity, args)...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var txns []*Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(
			&t.ID, &t.ProjectID, &t.TraceID, &t.SpanID, &t.Transaction,
			&t.Op, &t.Status, &t.DurationMs, &t.StartTimestamp, &t.Timestamp, &t.ReceivedAt,
			&t.Environment, &t.Release, &t.Platform, &t.Measurements,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		txns = append(txns, &t)
	}
	return txns, rows.Err()
}

func GetTransaction(ctx context.Context, pool *pgxpool.Pool, id string) (*Transaction, error) {
	var t Transaction
	err := pool.QueryRow(ctx, `
		SELECT id, project_id, COALESCE(trace_id,''), COALESCE(span_id,''), transaction,
		       op, status, duration_ms, start_timestamp, timestamp, received_at,
		       COALESCE(environment,''), COALESCE(release,''), COALESCE(platform,'')
		FROM transactions WHERE id = $1
	`, id).Scan(
		&t.ID, &t.ProjectID, &t.TraceID, &t.SpanID, &t.Transaction,
		&t.Op, &t.Status, &t.DurationMs, &t.StartTimestamp, &t.Timestamp, &t.ReceivedAt,
		&t.Environment, &t.Release, &t.Platform,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return &t, nil
}

func GetSpansForTransaction(ctx context.Context, pool *pgxpool.Pool, transactionID string) ([]*Span, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, transaction_id, span_id, COALESCE(parent_span_id,''), op,
		       COALESCE(description,''), start_timestamp, timestamp, duration_ms, status,
		       COALESCE(data, '{}')
		FROM spans WHERE transaction_id = $1 ORDER BY start_timestamp ASC
	`, transactionID)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var spans []*Span
	for rows.Next() {
		var s Span
		if err := rows.Scan(
			&s.ID, &s.TransactionID, &s.SpanID, &s.ParentSpanID, &s.Op,
			&s.Description, &s.StartTimestamp, &s.Timestamp, &s.DurationMs, &s.Status,
			&s.Data,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		spans = append(spans, &s)
	}
	return spans, rows.Err()
}

type TransactionSummary struct {
	Transaction string  `json:"transaction"`
	Op          string  `json:"op"`
	ProjectID   string  `json:"project_id"`
	SampleCount int64   `json:"sample_count"`
	TPM         float64 `json:"tpm"`
	P50         float64 `json:"p50"`
	P95         float64 `json:"p95"`
	Apdex       float64 `json:"apdex"`
	FailureRate float64 `json:"failure_rate"`
	TimeSpentMs int64   `json:"time_spent_ms"`
}

type TxBucket struct {
	Time  time.Time `json:"time"`
	Count int64     `json:"count"`
	P50   float64   `json:"p50"`
	P95   float64   `json:"p95"`
}

type TxTimeseries struct {
	Buckets    []TxBucket `json:"buckets"`
	BucketSize string     `json:"bucket_size"`
}

func GetTransactionTimeseries(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, hours int, env string, name string, op string, userIdentity string) (*TxTimeseries, error) {
	q, args, bucketSize := transactionTimeseriesQuery(projectIDs, hours, env, name, op, userIdentity, false)

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	ts := &TxTimeseries{BucketSize: bucketSize, Buckets: []TxBucket{}}
	for rows.Next() {
		var b TxBucket
		if err := rows.Scan(&b.Time, &b.Count, &b.P50, &b.P95); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		ts.Buckets = append(ts.Buckets, b)
	}
	return ts, rows.Err()
}

type TxCountBucket struct {
	Time  time.Time `json:"time"`
	Count int64     `json:"count"`
}

type TxCountTimeseries struct {
	Buckets    []TxCountBucket `json:"buckets"`
	BucketSize string          `json:"bucket_size"`
}

func GetTransactionCounts(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, hours int, env, name, op, userIdentity string) (*TxCountTimeseries, error) {
	q, args, bucketSize := transactionTimeseriesQuery(projectIDs, hours, env, name, op, userIdentity, true)
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	result := &TxCountTimeseries{BucketSize: bucketSize, Buckets: []TxCountBucket{}}
	for rows.Next() {
		var b TxCountBucket
		if err := rows.Scan(&b.Time, &b.Count); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		result.Buckets = append(result.Buckets, b)
	}
	return result, rows.Err()
}

func transactionTimeseriesQuery(projectIDs []string, hours int, env, name, op, userIdentity string, countsOnly bool) (string, []any, string) {

	if hours <= 0 || hours > 720 {
		hours = 24
	}

	var bucketExpr, bucketSize string
	switch {
	case hours <= 1:
		bucketExpr = "date_trunc('minute', start_timestamp) - (EXTRACT(minute FROM start_timestamp)::int % 5) * INTERVAL '1 minute'"
		bucketSize = "5min"
	case hours <= 168:
		bucketExpr = "date_trunc('hour', start_timestamp)"
		bucketSize = "hour"
	default:
		bucketExpr = "date_trunc('day', start_timestamp)"
		bucketSize = "day"
	}

	args := []any{hours}
	where := `
		WHERE start_timestamp >= NOW() - ($1 * INTERVAL '1 hour')`
	if len(projectIDs) > 0 {
		args = append(args, projectIDs)
		where += " AND project_id = ANY($2::uuid[])"
	}
	if env != "" {
		args = append(args, env)
		where += fmt.Sprintf(" AND environment = $%d", len(args))
	}
	if name != "" {
		args = append(args, name)
		where += fmt.Sprintf(" AND transaction = $%d", len(args))
	}
	if op != "" {
		args = append(args, op)
		where += fmt.Sprintf(" AND op = $%d", len(args))
	}
	if userIdentity != "" {
		args = append(args, userIdentity)
		where += fmt.Sprintf(" AND app_user_identity_hash(user_identity) = app_user_identity_hash($%[1]d::text) AND user_identity = $%[1]d", len(args))
	}

	metrics := ""
	if !countsOnly {
		metrics = ", COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY duration_ms), 0) AS p50, COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY duration_ms), 0) AS p95"
	}
	q := fmt.Sprintf(`
		SELECT
			%s AS bucket,
			COUNT(*) AS count%s
		FROM transactions
		%s
		GROUP BY bucket
		ORDER BY bucket ASC
	`, bucketExpr, metrics, where)

	return q, userQueryArgs(userIdentity, args), bucketSize
}

func ListTransactionSummaries(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, hours int, offsetHours int, env string, name string, op string, release string, userIdentity string) ([]*TransactionSummary, error) {
	if hours <= 0 || hours > 720 {
		hours = 24
	}
	if offsetHours < 0 {
		offsetHours = 0
	}
	minutesInWindow := float64(hours) * 60.0
	now := time.Now().UTC()
	since := now.Add(-time.Duration(hours+offsetHours) * time.Hour)
	until := now.Add(-time.Duration(offsetHours) * time.Hour)

	args := []any{minutesInWindow, since, until}
	where := `
		WHERE start_timestamp >= $2
		  AND start_timestamp < $3`
	if len(projectIDs) > 0 {
		args = append(args, projectIDs)
		where += " AND project_id = ANY($4::uuid[])"
	}
	if env != "" {
		args = append(args, env)
		where += fmt.Sprintf(" AND environment = $%d", len(args))
	}
	if name != "" {
		args = append(args, name)
		where += fmt.Sprintf(" AND transaction = $%d", len(args))
	}
	if op != "" {
		args = append(args, op)
		where += fmt.Sprintf(" AND op = $%d", len(args))
	}
	if release != "" {
		args = append(args, release)
		where += fmt.Sprintf(" AND release = $%d", len(args))
	}
	if userIdentity != "" {
		args = append(args, userIdentity)
		where += fmt.Sprintf(" AND app_user_identity_hash(user_identity) = app_user_identity_hash($%[1]d::text) AND user_identity = $%[1]d", len(args))
	}

	rows, err := pool.Query(ctx, `
		SELECT
			transaction,
			op,
			project_id::text,
			COUNT(*) AS sample_count,
			COUNT(*)::float8 / $1 AS tpm,
			COALESCE(PERCENTILE_CONT(0.5)  WITHIN GROUP (ORDER BY duration_ms), 0) AS p50,
			COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY duration_ms), 0) AS p95,
			COALESCE(
				(
					COUNT(CASE WHEN duration_ms <= 500 THEN 1 END)::float8 +
					COUNT(CASE WHEN duration_ms > 500 AND duration_ms <= 2000 THEN 1 END)::float8 * 0.5
				) / NULLIF(COUNT(*), 0),
			0) AS apdex,
			COUNT(CASE WHEN status IN ('internal_error', 'unavailable', 'data_loss', 'unknown_error', 'deadline_exceeded') THEN 1 END)::float8 / COUNT(*) AS failure_rate,
			SUM(duration_ms)::bigint AS time_spent_ms
		FROM transactions
		`+where+`
		GROUP BY transaction, op, project_id
		ORDER BY time_spent_ms DESC
		LIMIT 1000
	`, userQueryArgs(userIdentity, args)...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []*TransactionSummary
	for rows.Next() {
		var s TransactionSummary
		if err := rows.Scan(
			&s.Transaction, &s.Op, &s.ProjectID,
			&s.SampleCount, &s.TPM, &s.P50, &s.P95, &s.Apdex, &s.FailureRate, &s.TimeSpentMs,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

func GetTransactionPercentiles(ctx context.Context, pool *pgxpool.Pool, projectID string, hours int) (*TransactionPercentiles, error) {
	var p TransactionPercentiles
	var p50, p75, p95, p99 *float64
	err := pool.QueryRow(ctx, `
		SELECT
			percentile_cont(0.50) WITHIN GROUP (ORDER BY duration_ms),
			percentile_cont(0.75) WITHIN GROUP (ORDER BY duration_ms),
			percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms),
			percentile_cont(0.99) WITHIN GROUP (ORDER BY duration_ms),
			COUNT(*)
		FROM transactions
		WHERE project_id = $1
		  AND start_timestamp >= NOW() - ($2 * INTERVAL '1 hour')
	`, projectID, hours).Scan(&p50, &p75, &p95, &p99, &p.SampleCount)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	if p50 != nil {
		p.P50 = *p50
	}
	if p75 != nil {
		p.P75 = *p75
	}
	if p95 != nil {
		p.P95 = *p95
	}
	if p99 != nil {
		p.P99 = *p99
	}
	return &p, nil
}

// GetTransactionByTraceID returns the most recent transaction for a given trace_id.
func GetTransactionByTraceID(ctx context.Context, pool *pgxpool.Pool, traceID string) (*Transaction, error) {
	var t Transaction
	err := pool.QueryRow(ctx, `
		SELECT id, project_id, COALESCE(trace_id,''), COALESCE(span_id,''), transaction,
		       op, status, duration_ms, start_timestamp, timestamp, received_at,
		       COALESCE(environment,''), COALESCE(release,''), COALESCE(platform,'')
		FROM transactions WHERE trace_id = $1
		ORDER BY received_at DESC LIMIT 1
	`, traceID).Scan(
		&t.ID, &t.ProjectID, &t.TraceID, &t.SpanID, &t.Transaction,
		&t.Op, &t.Status, &t.DurationMs, &t.StartTimestamp, &t.Timestamp, &t.ReceivedAt,
		&t.Environment, &t.Release, &t.Platform,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return &t, nil
}

// GetErrorsForTrace returns up to 50 error events that share a trace_id, joined to their
// parent issue for title and status. Used to correlate errors with spans in the waterfall.
func GetErrorsForTrace(ctx context.Context, pool *pgxpool.Pool, traceID string) ([]*TraceError, error) {
	rows, err := pool.Query(ctx, `
		SELECT e.id, COALESCE(e.span_id, ''), e.timestamp, i.id, i.title, COALESCE(e.level, 'error'), i.status
		FROM events e
		JOIN issues i ON i.id = e.issue_id
		WHERE e.trace_id = $1
		ORDER BY e.timestamp ASC
		LIMIT 50
	`, traceID)
	if err != nil {
		return nil, fmt.Errorf("trace errors: %w", err)
	}
	defer rows.Close()

	var out []*TraceError
	for rows.Next() {
		var e TraceError
		if err := rows.Scan(&e.EventID, &e.SpanID, &e.Timestamp, &e.IssueID, &e.Title, &e.Level, &e.Status); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
