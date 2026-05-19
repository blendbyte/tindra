package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

func setupProjectAndIssueAndTx(t *testing.T) (*storage.Project, *storage.Issue, *storage.Transaction) {
	t.Helper()
	truncateProjects(t)

	p, err := storage.CreateProject(context.Background(), testPool, "perf-proj", "Perf Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	issue, _, _, err := storage.UpsertIssue(context.Background(), testPool, p.ID, "fp-perf", "Perf Issue", "performance", "info", "", "", time.Now().UTC())
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	tx := seedTransaction(t, p.ID, "/api/slow", 500, time.Now().UTC())
	return p, issue, tx
}

func TestInsertPerfEvent(t *testing.T) {
	_, issue, tx := setupProjectAndIssueAndTx(t)

	err := storage.InsertPerfEvent(context.Background(), testPool, issue.ID, tx.ID, 12, 480)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var count int
	err = testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM perf_events WHERE issue_id = $1 AND transaction_id = $2`,
		issue.ID, tx.ID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 perf_event, got %d", count)
	}
}

func TestInsertPerfEvent_storesValues(t *testing.T) {
	_, issue, tx := setupProjectAndIssueAndTx(t)

	err := storage.InsertPerfEvent(context.Background(), testPool, issue.ID, tx.ID, 7, 320)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var spanCount, totalMs int
	err = testPool.QueryRow(context.Background(),
		`SELECT span_count, total_ms FROM perf_events WHERE issue_id = $1 AND transaction_id = $2`,
		issue.ID, tx.ID,
	).Scan(&spanCount, &totalMs)
	if err != nil {
		t.Fatalf("query stored values: %v", err)
	}
	if spanCount != 7 {
		t.Errorf("span_count: got %d, want 7", spanCount)
	}
	if totalMs != 320 {
		t.Errorf("total_ms: got %d, want 320", totalMs)
	}
}

func TestListPerfEvents_returnsEvents(t *testing.T) {
	_, issue, tx := setupProjectAndIssueAndTx(t)

	if err := storage.InsertPerfEvent(context.Background(), testPool, issue.ID, tx.ID, 5, 100); err != nil {
		t.Fatalf("insert: %v", err)
	}

	events, err := storage.ListPerfEvents(context.Background(), testPool, issue.ID, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.ID == "" {
		t.Error("expected non-empty ID")
	}
	if e.IssueID != issue.ID {
		t.Errorf("issue_id: got %q, want %q", e.IssueID, issue.ID)
	}
	if e.TransactionID != tx.ID {
		t.Errorf("transaction_id: got %q, want %q", e.TransactionID, tx.ID)
	}
	if e.Transaction != "/api/slow" {
		t.Errorf("transaction: got %q, want /api/slow", e.Transaction)
	}
	if e.SpanCount != 5 {
		t.Errorf("span_count: got %d, want 5", e.SpanCount)
	}
	if e.TotalMs != 100 {
		t.Errorf("total_ms: got %d, want 100", e.TotalMs)
	}
	if e.CreatedAt.IsZero() {
		t.Error("expected non-zero created_at")
	}
}

func TestListPerfEvents_empty(t *testing.T) {
	_, issue, _ := setupProjectAndIssueAndTx(t)

	events, err := storage.ListPerfEvents(context.Background(), testPool, issue.ID, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestListPerfEvents_mostRecentFirst(t *testing.T) {
	p, issue, _ := setupProjectAndIssueAndTx(t)
	now := time.Now().UTC()

	for i, dur := range []int{100, 200, 300} {
		tx := seedTransaction(t, p.ID, "/api/slow", dur, now.Add(time.Duration(i)*time.Second))
		if err := storage.InsertPerfEvent(context.Background(), testPool, issue.ID, tx.ID, 3, dur); err != nil {
			t.Fatalf("insert perf event %d: %v", i, err)
		}
	}

	events, err := storage.ListPerfEvents(context.Background(), testPool, issue.ID, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	for i := 1; i < len(events); i++ {
		if events[i].CreatedAt.After(events[i-1].CreatedAt) {
			t.Errorf("events not ordered most-recent-first at index %d", i)
		}
	}
}

func TestListPerfEvents_respectsLimit(t *testing.T) {
	p, issue, _ := setupProjectAndIssueAndTx(t)
	now := time.Now().UTC()

	for i := range 5 {
		tx := seedTransaction(t, p.ID, "/api/limited", 10, now.Add(time.Duration(i)*time.Second))
		if err := storage.InsertPerfEvent(context.Background(), testPool, issue.ID, tx.ID, 2, 10); err != nil {
			t.Fatalf("insert perf event %d: %v", i, err)
		}
	}

	events, err := storage.ListPerfEvents(context.Background(), testPool, issue.ID, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("expected 3 events with limit=3, got %d", len(events))
	}
}

func TestListPerfEvents_zeroLimitClamped(t *testing.T) {
	p, issue, _ := setupProjectAndIssueAndTx(t)
	now := time.Now().UTC()

	for i := range 3 {
		tx := seedTransaction(t, p.ID, "/api/clamp", 10, now.Add(time.Duration(i)*time.Second))
		if err := storage.InsertPerfEvent(context.Background(), testPool, issue.ID, tx.ID, 1, 10); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	events, err := storage.ListPerfEvents(context.Background(), testPool, issue.ID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) == 0 {
		t.Error("expected events even with limit=0 (should clamp to 25)")
	}
}

func TestListPerfEvents_oversizeLimitClamped(t *testing.T) {
	p, issue, _ := setupProjectAndIssueAndTx(t)
	now := time.Now().UTC()

	for i := range 3 {
		tx := seedTransaction(t, p.ID, "/api/oversize", 10, now.Add(time.Duration(i)*time.Second))
		if err := storage.InsertPerfEvent(context.Background(), testPool, issue.ID, tx.ID, 1, 10); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	events, err := storage.ListPerfEvents(context.Background(), testPool, issue.ID, 999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("expected 3 events with oversize limit clamped to 25, got %d", len(events))
	}
}

func TestListPerfEvents_wrongIssueID(t *testing.T) {
	_, _, tx := setupProjectAndIssueAndTx(t)

	p2, err := storage.CreateProject(context.Background(), testPool, "perf-other", "Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	otherIssue, _, _, err := storage.UpsertIssue(context.Background(), testPool, p2.ID, "fp-other", "Other Issue", "error", "error", "", "", time.Now().UTC())
	if err != nil {
		t.Fatalf("create other issue: %v", err)
	}

	if err := storage.InsertPerfEvent(context.Background(), testPool, otherIssue.ID, tx.ID, 1, 50); err != nil {
		t.Fatalf("insert: %v", err)
	}

	events, err := storage.ListPerfEvents(context.Background(), testPool, "00000000-0000-0000-0000-000000000000", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown issue, got %d", len(events))
	}
}
