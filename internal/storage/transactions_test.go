package storage_test

import (
	"context"
	"testing"
	"time"

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
	if got == nil {
		t.Fatal("expected transaction, got nil")
	}
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
			INSERT INTO spans (transaction_id, span_id, op, start_timestamp, timestamp, duration_ms, status)
			VALUES ($1, $2, 'db.query', $3, $4, 10, 'ok')
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
		INSERT INTO spans (transaction_id, span_id, op, start_timestamp, timestamp, duration_ms, status, data)
		VALUES ($1, 'with-data', 'db.query', $2, $3, 10, 'ok', '{"db.system":"postgresql","rows":42}')
	`, tx.ID, start, end); err != nil {
		t.Fatalf("insert span with data: %v", err)
	}

	// Span with NULL data — COALESCE should return '{}'
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO spans (transaction_id, span_id, op, start_timestamp, timestamp, duration_ms, status)
		VALUES ($1, 'null-data', 'http.client', $2, $3, 10, 'ok')
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
	if withData == nil {
		t.Fatal("span with-data not found")
	}
	if len(withData.Data) == 0 || string(withData.Data) == "null" {
		t.Errorf("expected non-empty Data, got %q", withData.Data)
	}

	nullData := byID["null-data"]
	if nullData == nil {
		t.Fatal("span null-data not found")
	}
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
	if got == nil {
		t.Fatal("expected transaction, got nil")
	}
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
