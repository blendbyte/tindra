package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func setupProjectForTxns(t *testing.T) *storage.Project {
	t.Helper()
	truncateProjects(t)
	p, err := storage.CreateProject(context.Background(), testPool, "txn-test", "Txn Test")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p
}

func seedTransaction(t *testing.T, projectID, name string, durationMs int, start time.Time) *storage.Transaction {
	t.Helper()
	end := start.Add(time.Duration(durationMs) * time.Millisecond)
	var tx storage.Transaction
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO transactions
			(project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
		VALUES ($1, $2, 'http.server', 'ok', $3, $4, $5)
		RETURNING id, project_id, COALESCE(trace_id,''), COALESCE(span_id,''), transaction,
		          op, status, duration_ms, start_timestamp, timestamp, received_at,
		          COALESCE(environment,''), COALESCE(release,''), COALESCE(platform,'')
	`, projectID, name, durationMs, start, end).Scan(
		&tx.ID, &tx.ProjectID, &tx.TraceID, &tx.SpanID, &tx.Transaction,
		&tx.Op, &tx.Status, &tx.DurationMs, &tx.StartTimestamp, &tx.Timestamp, &tx.ReceivedAt,
		&tx.Environment, &tx.Release, &tx.Platform,
	)
	if err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
	return &tx
}

func TestListTransactions_empty(t *testing.T) {
	p := setupProjectForTxns(t)

	txns, err := storage.ListTransactions(context.Background(), testPool, p.ID, storage.TransactionFilter{Limit: 50})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) != 0 {
		t.Errorf("expected 0 transactions, got %d", len(txns))
	}
}

func TestListTransactions_returnsAll(t *testing.T) {
	p := setupProjectForTxns(t)
	now := time.Now().UTC()

	seedTransaction(t, p.ID, "/api/a", 10, now)
	seedTransaction(t, p.ID, "/api/b", 20, now.Add(time.Second))
	seedTransaction(t, p.ID, "/api/c", 30, now.Add(2*time.Second))

	txns, err := storage.ListTransactions(context.Background(), testPool, p.ID, storage.TransactionFilter{Limit: 50})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) != 3 {
		t.Fatalf("expected 3, got %d", len(txns))
	}
	// newest first
	if txns[0].Transaction != "/api/c" {
		t.Errorf("expected /api/c first, got %q", txns[0].Transaction)
	}
}

func TestListTransactions_pagination(t *testing.T) {
	p := setupProjectForTxns(t)
	now := time.Now().UTC()

	for i := range 5 {
		seedTransaction(t, p.ID, "/api/page", 10, now.Add(time.Duration(i)*time.Second))
	}

	first, err := storage.ListTransactions(context.Background(), testPool, p.ID, storage.TransactionFilter{Limit: 3})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("expected 3, got %d", len(first))
	}

	last := first[len(first)-1]
	second, err := storage.ListTransactions(context.Background(), testPool, p.ID, storage.TransactionFilter{
		Limit:      3,
		CursorTime: &last.StartTimestamp,
		CursorID:   &last.ID,
	})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(second) != 2 {
		t.Errorf("expected 2 on page 2, got %d", len(second))
	}
}

func TestGetTransaction_found(t *testing.T) {
	p := setupProjectForTxns(t)
	created := seedTransaction(t, p.ID, "/api/get", 55, time.Now().UTC())

	got, err := storage.GetTransaction(context.Background(), testPool, created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.NotNil(t, got, "expected transaction, got nil")
	if got.ID != created.ID {
		t.Errorf("ID: got %q, want %q", got.ID, created.ID)
	}
	if got.DurationMs != 55 {
		t.Errorf("duration_ms: got %d, want 55", got.DurationMs)
	}
}

func TestGetTransaction_notFound(t *testing.T) {
	got, err := storage.GetTransaction(context.Background(), testPool, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestGetSpansForTransaction(t *testing.T) {
	p := setupProjectForTxns(t)
	tx := seedTransaction(t, p.ID, "/api/spans", 100, time.Now().UTC())

	// Insert two spans
	for i, spanID := range []string{"span-1", "span-2"} {
		start := tx.StartTimestamp.Add(time.Duration(i*10) * time.Millisecond)
		end := start.Add(10 * time.Millisecond)
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO spans (transaction_id, span_id, op, start_timestamp, timestamp, duration_ms, status, project_id)
			SELECT $1, $2, 'db.query', $3, $4, 10, 'ok', project_id FROM transactions WHERE id = $1
		`, tx.ID, spanID, start, end); err != nil {
			t.Fatalf("insert span: %v", err)
		}
	}

	spans, err := storage.GetSpansForTransaction(context.Background(), testPool, tx.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}
	if spans[0].SpanID != "span-1" {
		t.Errorf("expected span-1 first, got %q", spans[0].SpanID)
	}
}

func TestGetSpansForTransaction_empty(t *testing.T) {
	p := setupProjectForTxns(t)
	tx := seedTransaction(t, p.ID, "/api/nospans", 5, time.Now().UTC())

	spans, err := storage.GetSpansForTransaction(context.Background(), testPool, tx.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spans) != 0 {
		t.Errorf("expected 0 spans, got %d", len(spans))
	}
}

func TestGetSpansForTransaction_dataField(t *testing.T) {
	p := setupProjectForTxns(t)
	tx := seedTransaction(t, p.ID, "/api/spandata", 100, time.Now().UTC())
	start := tx.StartTimestamp.Add(5 * time.Millisecond)
	end := start.Add(10 * time.Millisecond)

	// Span with JSONB data
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO spans (transaction_id, span_id, op, start_timestamp, timestamp, duration_ms, status, data, project_id)
		SELECT $1, 'with-data', 'db.query', $2, $3, 10, 'ok', '{"db.system":"postgresql","rows":42}', project_id
		FROM transactions WHERE id = $1
	`, tx.ID, start, end); err != nil {
		t.Fatalf("insert span with data: %v", err)
	}

	// Span with NULL data — COALESCE should return '{}'
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO spans (transaction_id, span_id, op, start_timestamp, timestamp, duration_ms, status, project_id)
		SELECT $1, 'null-data', 'http.client', $2, $3, 10, 'ok', project_id FROM transactions WHERE id = $1
	`, tx.ID, start, end); err != nil {
		t.Fatalf("insert span without data: %v", err)
	}

	spans, err := storage.GetSpansForTransaction(context.Background(), testPool, tx.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}

	byID := make(map[string]*storage.Span, 2)
	for _, s := range spans {
		byID[s.SpanID] = s
	}

	withData := byID["with-data"]
	require.NotNil(t, withData, "span with-data not found")
	if len(withData.Data) == 0 || string(withData.Data) == "null" {
		t.Errorf("expected non-empty Data, got %q", withData.Data)
	}

	nullData := byID["null-data"]
	require.NotNil(t, nullData, "span null-data not found")
	if string(nullData.Data) != "{}" {
		t.Errorf("expected '{}' for null data via COALESCE, got %q", nullData.Data)
	}
}

func TestGetTransactionPercentiles_noData(t *testing.T) {
	p := setupProjectForTxns(t)

	stats, err := storage.GetTransactionPercentiles(context.Background(), testPool, p.ID, 24)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.SampleCount != 0 {
		t.Errorf("expected 0 samples, got %d", stats.SampleCount)
	}
	if stats.P50 != 0 || stats.P99 != 0 {
		t.Errorf("expected zero percentiles with no data")
	}
}

func TestListTransactions_statusFilter(t *testing.T) {
	p := setupProjectForTxns(t)
	now := time.Now().UTC()
	seedTransaction(t, p.ID, "/api/ok", 10, now)

	txns, err := storage.ListTransactions(context.Background(), testPool, p.ID, storage.TransactionFilter{
		Status: "ok",
		Limit:  50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) == 0 {
		t.Error("expected transactions with status=ok")
	}
	for _, tx := range txns {
		if tx.Status != "ok" {
			t.Errorf("unexpected status: %q", tx.Status)
		}
	}
}

func TestListTransactions_environmentFilter(t *testing.T) {
	p := setupProjectForTxns(t)
	now := time.Now().UTC()

	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, environment)
		VALUES ($1, '/api/env', 'http.server', 'ok', 10, $2, $3, 'production')
	`, p.ID, now, now.Add(10*time.Millisecond)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := storage.ListTransactions(context.Background(), testPool, p.ID, storage.TransactionFilter{
		Environment: "production",
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) == 0 {
		t.Error("expected 1 transaction for production environment")
	}

	none, err := storage.ListTransactions(context.Background(), testPool, p.ID, storage.TransactionFilter{
		Environment: "staging",
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 transactions for staging, got %d", len(none))
	}
}

func TestListTransactions_limitClamping(t *testing.T) {
	p := setupProjectForTxns(t)
	seedTransaction(t, p.ID, "/api/clamp", 10, time.Now().UTC())

	// limit=0 → clamped to 50 internally
	txns, err := storage.ListTransactions(context.Background(), testPool, p.ID, storage.TransactionFilter{Limit: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) == 0 {
		t.Error("expected transactions even with Limit=0 (should clamp to 50)")
	}
}

func TestGetTransactionPercentiles_withData(t *testing.T) {
	p := setupProjectForTxns(t)
	now := time.Now().UTC()

	durations := []int{10, 20, 50, 100, 200}
	for i, d := range durations {
		seedTransaction(t, p.ID, "/api/perf", d, now.Add(time.Duration(i)*time.Second))
	}

	stats, err := storage.GetTransactionPercentiles(context.Background(), testPool, p.ID, 24)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.SampleCount != 5 {
		t.Errorf("expected 5 samples, got %d", stats.SampleCount)
	}
	if stats.P50 <= 0 {
		t.Errorf("expected positive P50, got %f", stats.P50)
	}
	if stats.P99 < stats.P50 {
		t.Errorf("P99 (%f) should be >= P50 (%f)", stats.P99, stats.P50)
	}
}

func TestListAllTransactions_empty(t *testing.T) {
	setupProjectForTxns(t) // truncates projects cascade → transactions

	txns, err := storage.ListAllTransactions(context.Background(), testPool, storage.TransactionFilter{Limit: 50})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) != 0 {
		t.Errorf("expected 0, got %d", len(txns))
	}
}

func TestListAllTransactions_acrossProjects(t *testing.T) {
	truncateProjects(t)
	now := time.Now().UTC()

	p1, _ := storage.CreateProject(context.Background(), testPool, "all-txn-p1", "P1")
	p2, _ := storage.CreateProject(context.Background(), testPool, "all-txn-p2", "P2")

	seedTransaction(t, p1.ID, "/api/from-p1", 10, now)
	seedTransaction(t, p2.ID, "/api/from-p2", 20, now.Add(time.Second))

	txns, err := storage.ListAllTransactions(context.Background(), testPool, storage.TransactionFilter{Limit: 50})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) != 2 {
		t.Fatalf("expected 2 transactions across projects, got %d", len(txns))
	}
	seen := map[string]bool{}
	for _, tx := range txns {
		seen[tx.ProjectID] = true
	}
	if !seen[p1.ID] || !seen[p2.ID] {
		t.Error("expected transactions from both projects")
	}
}

func TestListAllTransactions_statusFilter(t *testing.T) {
	p := setupProjectForTxns(t)
	now := time.Now().UTC()

	seedTransaction(t, p.ID, "/api/ok1", 10, now)
	seedTransaction(t, p.ID, "/api/ok2", 20, now.Add(time.Second))

	txns, err := storage.ListAllTransactions(context.Background(), testPool, storage.TransactionFilter{
		Status: "ok",
		Limit:  50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) < 2 {
		t.Fatalf("expected at least 2 with status=ok, got %d", len(txns))
	}
	for _, tx := range txns {
		if tx.Status != "ok" {
			t.Errorf("unexpected status %q in all-txns result", tx.Status)
		}
	}
}

func TestListAllTransactions_limitClamping(t *testing.T) {
	p := setupProjectForTxns(t)
	seedTransaction(t, p.ID, "/api/clamp2", 10, time.Now().UTC())

	txns, err := storage.ListAllTransactions(context.Background(), testPool, storage.TransactionFilter{Limit: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) == 0 {
		t.Error("expected results even with Limit=0 (should clamp to 50)")
	}
}

func TestGetTransactionByTraceID_found(t *testing.T) {
	p := setupProjectForTxns(t)
	start := time.Now().UTC().Truncate(time.Millisecond)
	const traceID = "trace-test-abc123"

	var tx storage.Transaction
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO transactions
			(project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, trace_id)
		VALUES ($1, '/api/traced', 'http.server', 'ok', 200, $2, $3, $4)
		RETURNING id, project_id, COALESCE(trace_id,''), COALESCE(span_id,''), transaction,
		          op, status, duration_ms, start_timestamp, timestamp, received_at,
		          COALESCE(environment,''), COALESCE(release,''), COALESCE(platform,'')
	`, p.ID, start, start.Add(200*time.Millisecond), traceID).Scan(
		&tx.ID, &tx.ProjectID, &tx.TraceID, &tx.SpanID, &tx.Transaction,
		&tx.Op, &tx.Status, &tx.DurationMs, &tx.StartTimestamp, &tx.Timestamp, &tx.ReceivedAt,
		&tx.Environment, &tx.Release, &tx.Platform,
	)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := storage.GetTransactionByTraceID(context.Background(), testPool, traceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.NotNil(t, got, "expected transaction, got nil")
	if got.ID != tx.ID {
		t.Errorf("ID: got %q, want %q", got.ID, tx.ID)
	}
	if got.TraceID != traceID {
		t.Errorf("TraceID: got %q, want %q", got.TraceID, traceID)
	}
}

func TestGetTransactionByTraceID_notFound(t *testing.T) {
	setupProjectForTxns(t) // clears transactions via CASCADE

	got, err := storage.GetTransactionByTraceID(context.Background(), testPool, "nonexistent-trace-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got transaction %q", got.ID)
	}
}

func TestGetTransactionTimeseries_empty(t *testing.T) {
	p := setupProjectForTxns(t)

	ts, err := storage.GetTransactionTimeseries(context.Background(), testPool, []string{p.ID}, 24, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts == nil {
		t.Fatal("expected non-nil timeseries")
	}
	if ts.BucketSize != "hour" {
		t.Errorf("bucket_size: got %q, want hour", ts.BucketSize)
	}
}

func TestGetTransactionTimeseries_withData(t *testing.T) {
	p := setupProjectForTxns(t)
	now := time.Now().UTC()
	seedTransaction(t, p.ID, "/api/ts", 100, now)
	seedTransaction(t, p.ID, "/api/ts", 200, now.Add(-30*time.Minute))

	ts, err := storage.GetTransactionTimeseries(context.Background(), testPool, []string{p.ID}, 24, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ts.Buckets) == 0 {
		t.Error("expected non-empty buckets")
	}
	total := int64(0)
	for _, b := range ts.Buckets {
		total += b.Count
	}
	if total < 2 {
		t.Errorf("expected at least 2 total events, got %d", total)
	}
}

func TestGetTransactionTimeseries_bucketSizes(t *testing.T) {
	p := setupProjectForTxns(t)

	// <= 1 hour: 5min buckets
	ts1, err := storage.GetTransactionTimeseries(context.Background(), testPool, []string{p.ID}, 1, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts1.BucketSize != "5min" {
		t.Errorf("1h bucket_size: got %q, want 5min", ts1.BucketSize)
	}

	// > 168 hours: day buckets
	ts2, err := storage.GetTransactionTimeseries(context.Background(), testPool, []string{p.ID}, 200, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts2.BucketSize != "day" {
		t.Errorf("200h bucket_size: got %q, want day", ts2.BucketSize)
	}
}

func TestListTransactionSummaries_empty(t *testing.T) {
	p := setupProjectForTxns(t)

	summaries, err := storage.ListTransactionSummaries(context.Background(), testPool, []string{p.ID}, 24, 0, "", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summaries) != 0 {
		t.Errorf("expected empty, got %d", len(summaries))
	}
}

func TestListTransactionSummaries_withData(t *testing.T) {
	p := setupProjectForTxns(t)
	now := time.Now().UTC()
	seedTransaction(t, p.ID, "/api/summary", 100, now.Add(-10*time.Minute))
	seedTransaction(t, p.ID, "/api/summary", 200, now.Add(-5*time.Minute))
	seedTransaction(t, p.ID, "/api/other", 50, now.Add(-3*time.Minute))

	summaries, err := storage.ListTransactionSummaries(context.Background(), testPool, []string{p.ID}, 24, 0, "", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summaries) == 0 {
		t.Error("expected non-empty summaries")
	}
	found := false
	for _, s := range summaries {
		if s.Transaction == "/api/summary" {
			found = true
			if s.SampleCount != 2 {
				t.Errorf("/api/summary sample_count: got %d, want 2", s.SampleCount)
			}
		}
	}
	if !found {
		t.Error("expected /api/summary in summaries")
	}
}

func TestGetErrorsForTrace_empty(t *testing.T) {
	errs, err := storage.GetErrorsForTrace(context.Background(), testPool, "nonexistent-trace-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errs) != 0 {
		t.Errorf("expected empty, got %d", len(errs))
	}
}

func TestGetErrorsForTrace_withErrors(t *testing.T) {
	p := setupProjectForTxns(t)
	ctx := context.Background()
	traceID := "test-trace-12345"

	issue, _, _, err := storage.UpsertIssue(ctx, testPool, p.ID, "fp-trace-err", "Trace Error", "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id, trace_id)
		VALUES ($1, NOW(), NOW(), '{"level":"error"}'::jsonb, 'fp-trace-err', $2, $3)
	`, p.ID, issue.ID, traceID)

	errs, err := storage.GetErrorsForTrace(ctx, testPool, traceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errs) != 1 {
		t.Errorf("expected 1 error, got %d", len(errs))
	}
	if errs[0].IssueID != issue.ID {
		t.Errorf("issue_id: got %q, want %q", errs[0].IssueID, issue.ID)
	}
	if errs[0].Title != "Trace Error" {
		t.Errorf("title: got %q", errs[0].Title)
	}
}

func TestListTransactionSummaries_withFilters(t *testing.T) {
	p := setupProjectForTxns(t)
	now := time.Now().UTC()

	// Transaction with specific op + environment + release
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, environment, release)
		VALUES ($1, '/api/filtered', 'http.server', 'ok', 100, $2, $3, 'production', 'v1.0.0')
	`, p.ID, now.Add(-5*time.Minute), now.Add(-5*time.Minute).Add(100*time.Millisecond))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Filter by op - should find the transaction
	got, err := storage.ListTransactionSummaries(context.Background(), testPool, []string{p.ID}, 24, 0, "", "", "http.server", "")
	if err != nil {
		t.Fatalf("op filter: %v", err)
	}
	var found bool
	for _, s := range got {
		if s.Transaction == "/api/filtered" {
			found = true
		}
	}
	if !found {
		t.Error("expected /api/filtered in summaries with op filter")
	}

	// Filter by name
	byName, err := storage.ListTransactionSummaries(context.Background(), testPool, []string{p.ID}, 24, 0, "", "/api/filtered", "", "")
	if err != nil {
		t.Fatalf("name filter: %v", err)
	}
	if len(byName) == 0 {
		t.Error("expected results when filtering by name /api/filtered")
	}

	// Filter by environment
	byEnv, err := storage.ListTransactionSummaries(context.Background(), testPool, []string{p.ID}, 24, 0, "production", "", "", "")
	if err != nil {
		t.Fatalf("env filter: %v", err)
	}
	if len(byEnv) == 0 {
		t.Error("expected results when filtering by environment=production")
	}

	// Filter by release
	byRel, err := storage.ListTransactionSummaries(context.Background(), testPool, []string{p.ID}, 24, 0, "", "", "", "v1.0.0")
	if err != nil {
		t.Fatalf("release filter: %v", err)
	}
	if len(byRel) == 0 {
		t.Error("expected results when filtering by release=v1.0.0")
	}

	// Test with offsetHours > 0 (the offset window should not include recent transactions)
	withOffset, err := storage.ListTransactionSummaries(context.Background(), testPool, []string{p.ID}, 24, 1, "", "", "", "")
	if err != nil {
		t.Fatalf("offset: %v", err)
	}
	_ = withOffset // just verify it doesn't error
}

func TestGetTransactionTimeseries_withFilters(t *testing.T) {
	p := setupProjectForTxns(t)
	now := time.Now().UTC()

	// Insert transaction with environment and op
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, environment)
		VALUES ($1, '/api/ts-filt', 'http.server', 'ok', 50, $2, $3, 'production')
	`, p.ID, now.Add(-30*time.Minute), now.Add(-30*time.Minute).Add(50*time.Millisecond))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Filter by env
	ts1, err := storage.GetTransactionTimeseries(context.Background(), testPool, []string{p.ID}, 24, "production", "", "")
	if err != nil {
		t.Fatalf("env filter: %v", err)
	}
	if ts1 == nil {
		t.Fatal("expected non-nil timeseries")
	}

	// Filter by name
	ts2, err := storage.GetTransactionTimeseries(context.Background(), testPool, []string{p.ID}, 24, "", "/api/ts-filt", "")
	if err != nil {
		t.Fatalf("name filter: %v", err)
	}
	if len(ts2.Buckets) == 0 {
		t.Error("expected at least one bucket when filtering by matching name")
	}

	// Filter by op
	ts3, err := storage.GetTransactionTimeseries(context.Background(), testPool, []string{p.ID}, 24, "", "", "http.server")
	if err != nil {
		t.Fatalf("op filter: %v", err)
	}
	if ts3 == nil {
		t.Fatal("expected non-nil timeseries with op filter")
	}
}

func TestListAllTransactions_withNameAndOpFilter(t *testing.T) {
	p := setupProjectForTxns(t)
	now := time.Now().UTC()

	_, err := testPool.Exec(context.Background(), `
		INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, environment)
		VALUES ($1, '/api/named', 'grpc', 'ok', 30, $2, $3, 'staging')
	`, p.ID, now, now.Add(30*time.Millisecond))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Filter by name
	byName, err := storage.ListAllTransactions(context.Background(), testPool, storage.TransactionFilter{
		Name:  "/api/named",
		Limit: 50,
	})
	if err != nil {
		t.Fatalf("name filter: %v", err)
	}
	if len(byName) == 0 {
		t.Error("expected results when filtering by name")
	}
	for _, tx := range byName {
		if tx.Transaction != "/api/named" {
			t.Errorf("unexpected transaction name: %q", tx.Transaction)
		}
	}

	// Filter by op
	byOp, err := storage.ListAllTransactions(context.Background(), testPool, storage.TransactionFilter{
		Op:    "grpc",
		Limit: 50,
	})
	if err != nil {
		t.Fatalf("op filter: %v", err)
	}
	if len(byOp) == 0 {
		t.Error("expected results when filtering by op=grpc")
	}

	// Filter by environment
	byEnv, err := storage.ListAllTransactions(context.Background(), testPool, storage.TransactionFilter{
		Environment: "staging",
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("env filter: %v", err)
	}
	if len(byEnv) == 0 {
		t.Error("expected results when filtering by environment=staging")
	}

	// Filter by projectIDs
	byProject, err := storage.ListAllTransactions(context.Background(), testPool, storage.TransactionFilter{
		ProjectIDs: []string{p.ID},
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("projectIDs filter: %v", err)
	}
	if len(byProject) == 0 {
		t.Error("expected results when filtering by projectID")
	}
}

func TestListAllTransactions_pagination(t *testing.T) {
	p := setupProjectForTxns(t)
	now := time.Now().UTC()

	for i := range 5 {
		seedTransaction(t, p.ID, "/api/allpage", 10, now.Add(time.Duration(i)*time.Second))
	}

	first, err := storage.ListAllTransactions(context.Background(), testPool, storage.TransactionFilter{
		ProjectIDs: []string{p.ID},
		Limit:      3,
	})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("expected 3, got %d", len(first))
	}

	last := first[len(first)-1]
	second, err := storage.ListAllTransactions(context.Background(), testPool, storage.TransactionFilter{
		ProjectIDs: []string{p.ID},
		Limit:      3,
		CursorTime: &last.StartTimestamp,
		CursorID:   &last.ID,
	})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(second) != 2 {
		t.Errorf("expected 2 on page 2, got %d", len(second))
	}
}
