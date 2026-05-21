package issues_test

import (
	"context"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/issues"
	"github.com/blendbyte/tindra/internal/storage"
)

func truncateForN1(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, "TRUNCATE perf_events CASCADE"); err != nil {
		t.Fatalf("truncate perf_events: %v", err)
	}
	if _, err := testPool.Exec(ctx, "TRUNCATE issues CASCADE"); err != nil {
		t.Fatalf("truncate issues: %v", err)
	}
	if _, err := testPool.Exec(ctx, "TRUNCATE transactions CASCADE"); err != nil {
		t.Fatalf("truncate transactions: %v", err)
	}
}

func insertTestTransaction(t *testing.T, name string) string {
	t.Helper()
	var id string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
		VALUES ($1, $2, 'http.server', 'ok', 150, NOW(), NOW())
		RETURNING id
	`, testProject.ID, name).Scan(&id)
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	return id
}

func dbSpans(description string, count int, durationMs int) []ingest.BufferedSpan {
	spans := make([]ingest.BufferedSpan, count)
	for i := range spans {
		spans[i] = ingest.BufferedSpan{
			Op:          "db.query",
			Description: description,
			DurationMs:  durationMs,
		}
	}
	return spans
}

func TestN1Detector_createsIssueForRepeatedDbSpans(t *testing.T) {
	truncateForN1(t)

	txID := insertTestTransaction(t, "GET /api/users")
	detector := issues.NewN1Detector(testPool)

	// 8 spans × 10ms = 80ms — above both count and duration thresholds.
	tx := ingest.BufferedTransaction{
		ProjectID:   testProject.ID,
		Transaction: "GET /api/users",
		Spans:       dbSpans("SELECT * FROM users WHERE id = ?", 8, 10),
	}
	detector.ProcessBatch(context.Background(), testPool, []ingest.BufferedTransaction{tx}, []string{txID})

	list, err := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(list))
	}
	iss := list[0]
	if iss.Kind != "n1_query" {
		t.Errorf("kind: got %q, want n1_query", iss.Kind)
	}
	if iss.Level != "performance" {
		t.Errorf("level: got %q, want performance", iss.Level)
	}
	if iss.EventCount != 1 {
		t.Errorf("event_count: got %d, want 1", iss.EventCount)
	}
}

func TestN1Detector_recordsPerfEvent(t *testing.T) {
	truncateForN1(t)

	txID := insertTestTransaction(t, "GET /api/orders")
	detector := issues.NewN1Detector(testPool)

	// 7 spans × 12ms = 84ms.
	tx := ingest.BufferedTransaction{
		ProjectID:   testProject.ID,
		Transaction: "GET /api/orders",
		Spans:       dbSpans("SELECT * FROM order_items WHERE order_id = ?", 7, 12),
	}
	detector.ProcessBatch(context.Background(), testPool, []ingest.BufferedTransaction{tx}, []string{txID})

	list, _ := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 10})
	if len(list) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(list))
	}

	perfEvs, err := storage.ListPerfEvents(context.Background(), testPool, list[0].ID, 10)
	if err != nil {
		t.Fatalf("list perf events: %v", err)
	}
	if len(perfEvs) != 1 {
		t.Fatalf("expected 1 perf event, got %d", len(perfEvs))
	}
	pe := perfEvs[0]
	if pe.SpanCount != 7 {
		t.Errorf("span_count: got %d, want 7", pe.SpanCount)
	}
	if pe.TotalMs != 7*12 {
		t.Errorf("total_ms: got %d, want %d", pe.TotalMs, 7*12)
	}
	if pe.TransactionID != txID {
		t.Errorf("transaction_id: got %q, want %q", pe.TransactionID, txID)
	}
	if pe.Transaction != "GET /api/orders" {
		t.Errorf("transaction name: got %q", pe.Transaction)
	}
}

func TestN1Detector_belowCountThreshold_noIssue(t *testing.T) {
	truncateForN1(t)

	txID := insertTestTransaction(t, "GET /api/products")
	detector := issues.NewN1Detector(testPool)

	// 4 spans — one below the count threshold of 5.
	tx := ingest.BufferedTransaction{
		ProjectID:   testProject.ID,
		Transaction: "GET /api/products",
		Spans:       dbSpans("SELECT * FROM products WHERE id = ?", 4, 20),
	}
	detector.ProcessBatch(context.Background(), testPool, []ingest.BufferedTransaction{tx}, []string{txID})

	list, _ := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 10})
	if len(list) != 0 {
		t.Errorf("expected 0 issues for 4 repeated spans, got %d", len(list))
	}
}

func TestN1Detector_belowDurationThreshold_noIssue(t *testing.T) {
	truncateForN1(t)

	txID := insertTestTransaction(t, "GET /api/fast")
	detector := issues.NewN1Detector(testPool)

	// 6 spans × 5ms = 30ms — count passes but total duration is below 50ms.
	tx := ingest.BufferedTransaction{
		ProjectID:   testProject.ID,
		Transaction: "GET /api/fast",
		Spans:       dbSpans("SELECT * FROM cache_entries WHERE key = ?", 6, 5),
	}
	detector.ProcessBatch(context.Background(), testPool, []ingest.BufferedTransaction{tx}, []string{txID})

	list, _ := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 10})
	if len(list) != 0 {
		t.Errorf("expected 0 issues for fast repeated queries (total < 50ms), got %d", len(list))
	}
}

func TestN1Detector_normalizesQueryLiterals(t *testing.T) {
	truncateForN1(t)

	txID := insertTestTransaction(t, "GET /api/posts")
	detector := issues.NewN1Detector(testPool)

	// Each span has a different literal value — a real N+1 from an ORM that
	// embeds values in query text. They should normalize to the same key.
	spans := []ingest.BufferedSpan{
		{Op: "db.query", Description: `select * from "posts" where "id" = 1`, DurationMs: 12},
		{Op: "db.query", Description: `select * from "posts" where "id" = 2`, DurationMs: 11},
		{Op: "db.query", Description: `select * from "posts" where "id" = 3`, DurationMs: 13},
		{Op: "db.query", Description: `select * from "posts" where "id" = 4`, DurationMs: 12},
		{Op: "db.query", Description: `select * from "posts" where "id" = 5`, DurationMs: 14},
		{Op: "db.query", Description: `select * from "posts" where "id" = 6`, DurationMs: 11},
	}
	tx := ingest.BufferedTransaction{
		ProjectID:   testProject.ID,
		Transaction: "GET /api/posts",
		Spans:       spans,
	}
	detector.ProcessBatch(context.Background(), testPool, []ingest.BufferedTransaction{tx}, []string{txID})

	list, _ := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 10})
	if len(list) != 1 {
		t.Fatalf("expected 1 issue (queries normalized), got %d", len(list))
	}
	if list[0].Kind != "n1_query" {
		t.Errorf("kind: got %q", list[0].Kind)
	}
}

func TestN1Detector_normalizesStringLiterals(t *testing.T) {
	truncateForN1(t)

	txID := insertTestTransaction(t, "GET /api/users/by-name")
	detector := issues.NewN1Detector(testPool)

	spans := []ingest.BufferedSpan{
		{Op: "db.query", Description: `SELECT * FROM users WHERE email = 'alice@example.com'`, DurationMs: 15},
		{Op: "db.query", Description: `SELECT * FROM users WHERE email = 'bob@example.com'`, DurationMs: 14},
		{Op: "db.query", Description: `SELECT * FROM users WHERE email = 'carol@example.com'`, DurationMs: 13},
		{Op: "db.query", Description: `SELECT * FROM users WHERE email = 'dan@example.com'`, DurationMs: 12},
		{Op: "db.query", Description: `SELECT * FROM users WHERE email = 'eve@example.com'`, DurationMs: 16},
	}
	tx := ingest.BufferedTransaction{
		ProjectID:   testProject.ID,
		Transaction: "GET /api/users/by-name",
		Spans:       spans,
	}
	detector.ProcessBatch(context.Background(), testPool, []ingest.BufferedTransaction{tx}, []string{txID})

	list, _ := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 10})
	if len(list) != 1 {
		t.Fatalf("expected 1 issue (string literals normalized), got %d", len(list))
	}
}

func TestN1Detector_ignoresNonDbOps(t *testing.T) {
	truncateForN1(t)

	txID := insertTestTransaction(t, "GET /api/feed")
	detector := issues.NewN1Detector(testPool)

	spans := make([]ingest.BufferedSpan, 10)
	for i := range spans {
		spans[i] = ingest.BufferedSpan{
			Op:          "http.client",
			Description: "GET https://api.example.com/items",
			DurationMs:  20,
		}
	}
	tx := ingest.BufferedTransaction{
		ProjectID:   testProject.ID,
		Transaction: "GET /api/feed",
		Spans:       spans,
	}
	detector.ProcessBatch(context.Background(), testPool, []ingest.BufferedTransaction{tx}, []string{txID})

	list, _ := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 10})
	if len(list) != 0 {
		t.Errorf("expected 0 issues for non-db ops, got %d", len(list))
	}
}

func TestN1Detector_ignoresEmptyDescription(t *testing.T) {
	truncateForN1(t)

	txID := insertTestTransaction(t, "GET /api/misc")
	detector := issues.NewN1Detector(testPool)

	spans := make([]ingest.BufferedSpan, 8)
	for i := range spans {
		spans[i] = ingest.BufferedSpan{Op: "db.query", Description: "", DurationMs: 20}
	}
	tx := ingest.BufferedTransaction{
		ProjectID:   testProject.ID,
		Transaction: "GET /api/misc",
		Spans:       spans,
	}
	detector.ProcessBatch(context.Background(), testPool, []ingest.BufferedTransaction{tx}, []string{txID})

	list, _ := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 10})
	if len(list) != 0 {
		t.Errorf("expected 0 issues for empty descriptions, got %d", len(list))
	}
}

func TestN1Detector_groupsSameQueryAcrossTransactions(t *testing.T) {
	truncateForN1(t)

	txID1 := insertTestTransaction(t, "GET /api/dashboard")
	txID2 := insertTestTransaction(t, "GET /api/dashboard")
	detector := issues.NewN1Detector(testPool)

	// 6 spans × 10ms = 60ms per transaction.
	spans := dbSpans("SELECT * FROM widgets WHERE user_id = ?", 6, 10)
	txs := []ingest.BufferedTransaction{
		{ProjectID: testProject.ID, Transaction: "GET /api/dashboard", Spans: spans},
		{ProjectID: testProject.ID, Transaction: "GET /api/dashboard", Spans: spans},
	}
	detector.ProcessBatch(context.Background(), testPool, txs, []string{txID1, txID2})

	list, _ := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 10})
	if len(list) != 1 {
		t.Fatalf("expected 1 issue for same query in two transactions, got %d", len(list))
	}
	if list[0].EventCount != 2 {
		t.Errorf("event_count: got %d, want 2", list[0].EventCount)
	}

	perfEvs, _ := storage.ListPerfEvents(context.Background(), testPool, list[0].ID, 10)
	if len(perfEvs) != 2 {
		t.Errorf("expected 2 perf events, got %d", len(perfEvs))
	}
}

func TestN1Detector_differentQueriesSeparateIssues(t *testing.T) {
	truncateForN1(t)

	txID := insertTestTransaction(t, "GET /api/reports")
	detector := issues.NewN1Detector(testPool)

	// Both groups need to clear the 50ms total threshold.
	// users: 6 × 10ms = 60ms, roles: 7 × 10ms = 70ms.
	mixed := append(
		dbSpans("SELECT * FROM users WHERE id = ?", 6, 10),
		dbSpans("SELECT * FROM roles WHERE id = ?", 7, 10)...,
	)
	tx := ingest.BufferedTransaction{
		ProjectID:   testProject.ID,
		Transaction: "GET /api/reports",
		Spans:       mixed,
	}
	detector.ProcessBatch(context.Background(), testPool, []ingest.BufferedTransaction{tx}, []string{txID})

	list, _ := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 10})
	if len(list) != 2 {
		t.Errorf("expected 2 issues for two distinct N+1 queries, got %d", len(list))
	}
}

func TestN1Detector_skipsEmptyTxID(t *testing.T) {
	truncateForN1(t)

	detector := issues.NewN1Detector(testPool)

	tx := ingest.BufferedTransaction{
		ProjectID:   testProject.ID,
		Transaction: "GET /api/skip",
		Spans:       dbSpans("SELECT * FROM tags WHERE id = ?", 8, 10),
	}
	// Empty txID should be skipped entirely - no panic, no issue.
	detector.ProcessBatch(context.Background(), testPool, []ingest.BufferedTransaction{tx}, []string{""})

	list, _ := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 10})
	if len(list) != 0 {
		t.Errorf("expected 0 issues when txID is empty, got %d", len(list))
	}
}

func TestN1Detector_kindFilterReturnsOnlyPerfIssues(t *testing.T) {
	truncateForN1(t)

	// Create one error issue and one N+1 issue.
	_, _, _, err := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-err-n1", "Some Error", "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("upsert error issue: %v", err)
	}

	txID := insertTestTransaction(t, "GET /api/mix")
	detector := issues.NewN1Detector(testPool)
	// 5 spans × 12ms = 60ms — clears both thresholds.
	tx := ingest.BufferedTransaction{
		ProjectID:   testProject.ID,
		Transaction: "GET /api/mix",
		Spans:       dbSpans("SELECT * FROM items WHERE id = ?", 5, 12),
	}
	detector.ProcessBatch(context.Background(), testPool, []ingest.BufferedTransaction{tx}, []string{txID})

	all, _ := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 10})
	if len(all) != 2 {
		t.Fatalf("expected 2 total issues, got %d", len(all))
	}

	perfOnly, _ := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 10, Kind: "n1_query"})
	if len(perfOnly) != 1 {
		t.Errorf("kind filter: expected 1 n1_query issue, got %d", len(perfOnly))
	}
	if perfOnly[0].Kind != "n1_query" {
		t.Errorf("kind: got %q", perfOnly[0].Kind)
	}

	errOnly, _ := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 10, Kind: "error"})
	if len(errOnly) != 1 {
		t.Errorf("kind filter: expected 1 error issue, got %d", len(errOnly))
	}
}
