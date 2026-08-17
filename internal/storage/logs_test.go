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
