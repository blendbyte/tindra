package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

// seedLog inserts a log row directly and returns the generated ID.
func seedLog(t *testing.T, projectID, level, body string, ts time.Time, env string) string {
	t.Helper()
	var id string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO logs (project_id, timestamp, received_at, level, body, environment)
		VALUES ($1, $2, $2, $3, $4, $5)
		RETURNING id
	`, projectID, ts, level, body, nullableString(env)).Scan(&id)
	if err != nil {
		t.Fatalf("seed log: %v", err)
	}
	return id
}

// nullableString converts an empty string to nil so Postgres stores NULL.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func setupProjectForLogs(t *testing.T) *storage.Project {
	t.Helper()
	truncateProjects(t)
	p, err := storage.CreateProject(context.Background(), testPool, "logs-proj", "Logs Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p
}

// TestListLogs_empty verifies an empty result set is returned when there are no logs.
func TestListLogs_empty(t *testing.T) {
	p := setupProjectForLogs(t)

	logs, hasMore, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("expected 0 logs, got %d", len(logs))
	}
	if hasMore {
		t.Error("expected hasMore=false for empty set")
	}
}

// TestListLogs_returnsAll verifies all seeded logs come back.
func TestListLogs_returnsAll(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()

	seedLog(t, p.ID, "info", "first", now, "")
	seedLog(t, p.ID, "warn", "second", now.Add(time.Second), "")
	seedLog(t, p.ID, "error", "third", now.Add(2*time.Second), "")

	logs, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 3 {
		t.Fatalf("expected 3 logs, got %d", len(logs))
	}
	// newest first
	if logs[0].Body != "third" {
		t.Errorf("expected 'third' first, got %q", logs[0].Body)
	}
}

// TestListLogs_levelFilter verifies the level filter.
func TestListLogs_levelFilter(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()

	seedLog(t, p.ID, "info", "info msg", now, "")
	seedLog(t, p.ID, "error", "error msg", now.Add(time.Second), "")

	logs, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Level:      "error",
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].Level != "error" {
		t.Errorf("expected level=error, got %q", logs[0].Level)
	}
}

// TestListLogs_warnLevelFilter verifies that a "warning" filter also matches
// rows stored with the log protocol's "warn" spelling, and vice versa.
func TestListLogs_warnLevelFilter(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()

	seedLog(t, p.ID, "warn", "legacy warn row", now, "")
	seedLog(t, p.ID, "warning", "normalized warning row", now.Add(time.Second), "")
	seedLog(t, p.ID, "info", "info msg", now.Add(2*time.Second), "")

	for _, level := range []string{"warning", "warn"} {
		logs, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
			ProjectIDs: []string{p.ID},
			Level:      level,
			Limit:      50,
		})
		if err != nil {
			t.Fatalf("level %q: unexpected error: %v", level, err)
		}
		if len(logs) != 2 {
			t.Fatalf("level %q: expected both warn spellings, got %d", level, len(logs))
		}
		for _, l := range logs {
			if l.Level != "warn" && l.Level != "warning" {
				t.Errorf("level %q: unexpected row level %q", level, l.Level)
			}
		}
	}
}

// TestListLogs_environmentFilter verifies environment scoping.
func TestListLogs_environmentFilter(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()

	seedLog(t, p.ID, "info", "prod msg", now, "production")
	seedLog(t, p.ID, "info", "staging msg", now.Add(time.Second), "staging")

	got, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs:  []string{p.ID},
		Environment: "production",
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 production log, got %d", len(got))
	}
	if got[0].Body != "prod msg" {
		t.Errorf("expected 'prod msg', got %q", got[0].Body)
	}

	none, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs:  []string{p.ID},
		Environment: "unknown",
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 logs for unknown environment, got %d", len(none))
	}
}

// TestListLogs_searchFilter verifies the ILIKE body search.
func TestListLogs_searchFilter(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()

	seedLog(t, p.ID, "info", "connection refused", now, "")
	seedLog(t, p.ID, "warn", "disk space low", now.Add(time.Second), "")

	got, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Search:     "connection",
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 match, got %d", len(got))
	}
	if got[0].Body != "connection refused" {
		t.Errorf("expected 'connection refused', got %q", got[0].Body)
	}
}

// TestListLogs_pagination verifies hasMore and cursor-based keyset pagination.
func TestListLogs_pagination(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()

	for i := range 5 {
		seedLog(t, p.ID, "info", "paged", now.Add(time.Duration(i)*time.Second), "")
	}

	first, hasMore, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Limit:      3,
	})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("expected 3, got %d", len(first))
	}
	if !hasMore {
		t.Error("expected hasMore=true on page 1")
	}

	last := first[len(first)-1]
	second, hasMore2, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Limit:      3,
		CursorTime: &last.Timestamp,
		CursorID:   &last.ID,
	})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(second) != 2 {
		t.Errorf("expected 2 on page 2, got %d", len(second))
	}
	if hasMore2 {
		t.Error("expected hasMore=false on final page")
	}
}

// TestListLogs_limitClamping verifies limit=0 is clamped to the default.
func TestListLogs_limitClamping(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()
	seedLog(t, p.ID, "info", "any log", now, "")

	logs, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Limit:      0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) == 0 {
		t.Error("expected logs even with Limit=0 (should clamp to 100)")
	}
}

// TestListLogs_noProjectFilter verifies that omitting ProjectIDs returns logs from all projects.
func TestListLogs_noProjectFilter(t *testing.T) {
	truncateProjects(t)
	now := time.Now().UTC()

	p1, _ := storage.CreateProject(context.Background(), testPool, "logs-all-p1", "P1")
	p2, _ := storage.CreateProject(context.Background(), testPool, "logs-all-p2", "P2")

	seedLog(t, p1.ID, "info", "from p1", now, "")
	seedLog(t, p2.ID, "info", "from p2", now.Add(time.Second), "")

	// ProjectIDs=nil means "all projects"
	logs, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{Limit: 50})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) < 2 {
		t.Errorf("expected at least 2 logs across projects, got %d", len(logs))
	}
}

// TestDeleteLogsOlderThan_deletesOldRows verifies old rows are removed and recent ones are kept.
func TestDeleteLogsOlderThan_deletesOldRows(t *testing.T) {
	p := setupProjectForLogs(t)

	old := time.Now().UTC().Add(-48 * time.Hour)
	recent := time.Now().UTC()

	// Insert one old log and one recent log using received_at explicitly.
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO logs (project_id, timestamp, received_at, level, body)
		VALUES ($1, $2, $2, 'info', 'old log')
	`, p.ID, old); err != nil {
		t.Fatalf("insert old log: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO logs (project_id, timestamp, received_at, level, body)
		VALUES ($1, $2, $2, 'info', 'recent log')
	`, p.ID, recent); err != nil {
		t.Fatalf("insert recent log: %v", err)
	}

	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	deleted, err := storage.DeleteLogsOlderThan(context.Background(), testPool, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted row, got %d", deleted)
	}

	logs, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log remaining, got %d", len(logs))
	}
	if logs[0].Body != "recent log" {
		t.Errorf("expected 'recent log' to survive, got %q", logs[0].Body)
	}
}

// TestDeleteLogsOlderThan_nothingToDelete verifies zero is returned when no rows match.
func TestDeleteLogsOlderThan_nothingToDelete(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()
	seedLog(t, p.ID, "info", "fresh log", now, "")

	// Cutoff is 30 days ago - the fresh log should not be touched.
	cutoff := now.Add(-30 * 24 * time.Hour)
	deleted, err := storage.DeleteLogsOlderThan(context.Background(), testPool, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted rows, got %d", deleted)
	}
}

// seedTracedLog inserts a log carrying a trace_id and returns its ID.
func seedTracedLog(t *testing.T, projectID, body, traceID string, ts time.Time) string {
	t.Helper()
	var id string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO logs (project_id, timestamp, received_at, level, body, trace_id)
		VALUES ($1, $2, $2, 'info', $3, $4)
		RETURNING id
	`, projectID, ts, body, traceID).Scan(&id)
	if err != nil {
		t.Fatalf("seed traced log: %v", err)
	}
	return id
}

// seedTxForTrace inserts a transaction on the given trace and returns its ID.
func seedTxForTrace(t *testing.T, projectID, traceID string, receivedAt time.Time) string {
	t.Helper()
	var id string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO transactions
			(project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, received_at, trace_id)
		VALUES ($1, '/api/traced', 'http.server', 'ok', 200, $2, $2, $2, $3)
		RETURNING id
	`, projectID, receivedAt, traceID).Scan(&id)
	if err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
	return id
}

// TestListLogs_resolvesTransactionID verifies a log is joined to the transaction
// sharing its trace_id so the UI can deep-link the row to its trace.
func TestListLogs_resolvesTransactionID(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()

	txID := seedTxForTrace(t, p.ID, "trace-join-1", now)
	seedTracedLog(t, p.ID, "traced log", "trace-join-1", now)

	logs, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].TransactionID == nil {
		t.Fatal("expected TransactionID to be resolved, got nil")
	}
	if *logs[0].TransactionID != txID {
		t.Errorf("TransactionID: got %q, want %q", *logs[0].TransactionID, txID)
	}
}

// TestListLogs_transactionIDNilWithoutTrace verifies logs without a trace_id, and
// traced logs whose transaction was never ingested, both come back unlinked
// rather than joining to an arbitrary row.
func TestListLogs_transactionIDNilWithoutTrace(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()

	// A transaction exists, but on an unrelated trace.
	seedTxForTrace(t, p.ID, "trace-other", now)
	seedLog(t, p.ID, "info", "untraced log", now, "")
	seedTracedLog(t, p.ID, "orphan traced log", "trace-missing", now.Add(time.Second))

	logs, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logs))
	}
	for _, l := range logs {
		if l.TransactionID != nil {
			t.Errorf("log %q: expected nil TransactionID, got %q", l.Body, *l.TransactionID)
		}
	}
}

// TestListLogs_resolvesMostRecentTransaction verifies that when a trace has more
// than one transaction the newest one wins, matching GetTransactionByTraceID.
func TestListLogs_resolvesMostRecentTransaction(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()

	seedTxForTrace(t, p.ID, "trace-dup", now.Add(-time.Hour))
	newest := seedTxForTrace(t, p.ID, "trace-dup", now)
	seedTracedLog(t, p.ID, "dup traced log", "trace-dup", now)

	logs, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].TransactionID == nil || *logs[0].TransactionID != newest {
		t.Errorf("TransactionID: got %v, want %q", logs[0].TransactionID, newest)
	}
}

// TestListLogs_filtersStillApplyWithJoin guards the column qualification added
// alongside the transaction join: an unqualified filter column would make the
// query ambiguous and fail outright.
func TestListLogs_filtersStillApplyWithJoin(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()

	seedTxForTrace(t, p.ID, "trace-filter", now)
	seedTracedLog(t, p.ID, "keep me", "trace-filter", now)
	seedLog(t, p.ID, "error", "drop me", now.Add(-time.Minute), "staging")

	logs, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Level:      "info",
		Search:     "keep",
		TraceID:    "trace-filter",
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].Body != "keep me" {
		t.Errorf("Body: got %q, want %q", logs[0].Body, "keep me")
	}
}

// TestListLogs_cursorPaginationWithJoin exercises the qualified cursor predicate
// so keyset pagination keeps working now that logs join to transactions.
func TestListLogs_cursorPaginationWithJoin(t *testing.T) {
	p := setupProjectForLogs(t)
	base := time.Now().UTC().Truncate(time.Millisecond)

	seedLog(t, p.ID, "info", "older", base.Add(-2*time.Minute), "")
	seedLog(t, p.ID, "info", "newer", base, "")

	first, hasMore, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Limit:      1,
	})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if !hasMore {
		t.Fatal("expected hasMore=true on the first page")
	}
	if len(first) != 1 || first[0].Body != "newer" {
		t.Fatalf("first page: got %+v, want [newer]", first)
	}

	cursorTime := first[0].Timestamp
	second, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		CursorTime: &cursorTime,
		CursorID:   &first[0].ID,
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second) != 1 || second[0].Body != "older" {
		t.Errorf("second page: got %+v, want [older]", second)
	}
}

func TestCountLogs_windowAndLevels(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()

	seedLog(t, p.ID, "error", "stripe failed", now, "production")
	seedLog(t, p.ID, "fatal", "process dying", now, "production")
	seedLog(t, p.ID, "warning", "retry", now, "production")
	seedLog(t, p.ID, "error", "old error", now.Add(-2*time.Hour), "production")
	seedLog(t, p.ID, "error", "staging error", now, "staging")

	count, err := storage.CountLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs:  []string{p.ID},
		Levels:      []string{"fatal", "error"},
		Environment: "production",
		WindowMins:  5,
	})
	if err != nil {
		t.Fatalf("CountLogs: %v", err)
	}
	if count != 2 {
		t.Errorf("CountLogs: got %d, want 2 (error+fatal in window, production)", count)
	}
}

func TestCountLogs_searchEscapesLikeMeta(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()
	seedLog(t, p.ID, "error", "100% done", now, "")
	seedLog(t, p.ID, "error", "hello", now, "")

	count, err := storage.CountLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Search:     "%",
		WindowMins: 5,
	})
	if err != nil {
		t.Fatalf("CountLogs: %v", err)
	}
	if count != 1 {
		t.Errorf("literal %% search: got %d, want 1", count)
	}
}

func TestLogsReachThreshold_shortCircuits(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()
	for range 20 {
		seedLog(t, p.ID, "error", "burst", now, "")
	}

	ok, err := storage.LogsReachThreshold(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Levels:     []string{"error"},
		WindowMins: 5,
	}, 10)
	if err != nil {
		t.Fatalf("LogsReachThreshold: %v", err)
	}
	if !ok {
		t.Error("expected threshold met")
	}

	ok, err = storage.LogsReachThreshold(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Levels:     []string{"error"},
		WindowMins: 5,
	}, 50)
	if err != nil {
		t.Fatalf("LogsReachThreshold under: %v", err)
	}
	if ok {
		t.Error("expected threshold not met")
	}
}

func TestLogsReachThreshold_zeroThreshold(t *testing.T) {
	ok, err := storage.LogsReachThreshold(context.Background(), testPool, storage.LogFilter{}, 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Error("threshold 0 should not be reached")
	}
}

func TestCountLogs_warnOnlyLevel(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()
	seedLog(t, p.ID, "warn", "legacy", now, "")
	seedLog(t, p.ID, "info", "skip", now, "")
	count, err := storage.CountLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Levels:     []string{"warn"},
		WindowMins: 5,
	})
	if err != nil {
		t.Fatalf("CountLogs: %v", err)
	}
	if count != 1 {
		t.Errorf("warn-only: got %d", count)
	}
}

func TestCountLogs_queryError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := storage.CountLogs(ctx, testPool, storage.LogFilter{
		ProjectIDs: []string{"not-a-uuid"},
		WindowMins: 5,
	})
	if err == nil {
		t.Fatal("expected count error")
	}
}

func TestLogsReachThreshold_queryError(t *testing.T) {
	_, err := storage.LogsReachThreshold(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{"not-a-uuid"},
		WindowMins: 5,
	}, 1)
	if err == nil {
		t.Fatal("expected reach-threshold error")
	}
}

func TestLogsReachThreshold_warnSpelling(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()
	seedLog(t, p.ID, "warn", "legacy", now, "")
	seedLog(t, p.ID, "warning", "normalized", now, "")

	ok, err := storage.LogsReachThreshold(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Levels:     []string{"warning"},
		WindowMins: 5,
	}, 2)
	if err != nil {
		t.Fatalf("LogsReachThreshold: %v", err)
	}
	if !ok {
		t.Error("expected both warn spellings to count toward warning+")
	}
}

func TestListLogs_searchEscapesLikeMeta(t *testing.T) {
	p := setupProjectForLogs(t)
	now := time.Now().UTC()
	seedLog(t, p.ID, "info", "100% done", now, "")
	seedLog(t, p.ID, "info", "hello", now, "")

	logs, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs: []string{p.ID},
		Search:     "%",
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("ListLogs: %v", err)
	}
	if len(logs) != 1 || logs[0].Body != "100% done" {
		t.Errorf("literal %% search: got %+v", logs)
	}
}
