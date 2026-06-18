package storage_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	pool, cleanup := testutil.SetupDB(ctx)

	testPool = pool

	code := m.Run()
	cleanup()
	os.Exit(code)
}

func truncateProjects(t *testing.T) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), "TRUNCATE projects CASCADE")
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func TestCreateProject(t *testing.T) {
	truncateProjects(t)

	p, err := storage.CreateProject(context.Background(), testPool, "my-app", "My App")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ID == "" {
		t.Error("expected non-empty ID")
	}
	if p.Slug != "my-app" {
		t.Errorf("slug: got %q, want %q", p.Slug, "my-app")
	}
	if p.Name != "My App" {
		t.Errorf("name: got %q, want %q", p.Name, "My App")
	}
	if len(p.PublicKey) != 32 {
		t.Errorf("public_key length: got %d, want 32", len(p.PublicKey))
	}
	if p.CreatedAt.IsZero() {
		t.Error("expected non-zero created_at")
	}
}

func TestCreateProject_duplicateSlug(t *testing.T) {
	truncateProjects(t)

	if _, err := storage.CreateProject(context.Background(), testPool, "dupe", "First"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := storage.CreateProject(context.Background(), testPool, "dupe", "Second")
	if err == nil {
		t.Error("expected error on duplicate slug")
	}
}

func TestListProjects(t *testing.T) {
	truncateProjects(t)

	for _, slug := range []string{"alpha", "beta", "gamma"} {
		if _, err := storage.CreateProject(context.Background(), testPool, slug, slug); err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
	}

	projects, err := storage.ListProjects(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(projects) != 3 {
		t.Fatalf("expected 3 projects, got %d", len(projects))
	}
	// ListProjects now returns alphabetically by name
	if projects[0].Slug != "alpha" {
		t.Errorf("expected alpha first (alphabetical), got %q", projects[0].Slug)
	}
	if projects[2].Slug != "gamma" {
		t.Errorf("expected gamma last (alphabetical), got %q", projects[2].Slug)
	}
}

func TestListProjects_statsFields(t *testing.T) {
	truncateProjects(t)

	p, err := storage.CreateProject(context.Background(), testPool, "statstest", "Stats Test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	projects, err := storage.ListProjects(context.Background(), testPool)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *storage.Project
	for _, pr := range projects {
		if pr.ID == p.ID {
			found = pr
			break
		}
	}
	if found == nil {
		t.Fatal("project not found in list")
	}
	if found.EventCount < 0 {
		t.Errorf("EventCount: got %d, want >= 0", found.EventCount)
	}
	if found.Events24h < 0 {
		t.Errorf("Events24h: got %d, want >= 0", found.Events24h)
	}
	if found.StorageBytes < 0 {
		t.Errorf("StorageBytes: got %d, want >= 0", found.StorageBytes)
	}
}

func TestListProjects_eventCountMonth(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()

	p, err := storage.CreateProject(ctx, testPool, "evcount-month", "Ev Count Month")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	payload := json.RawMessage(`{"level":"error"}`)
	for range 3 {
		testPool.Exec(ctx, `
			INSERT INTO events (project_id, timestamp, received_at, payload)
			VALUES ($1, NOW(), NOW(), $2)
		`, p.ID, payload)
	}

	projects, err := storage.ListProjects(ctx, testPool)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *storage.Project
	for _, pr := range projects {
		if pr.ID == p.ID {
			found = pr
			break
		}
	}
	if found == nil {
		t.Fatal("project not found in list")
	}
	if found.EventCount < 3 {
		t.Errorf("EventCount: got %d, want >= 3", found.EventCount)
	}
}

func TestListProjects_events24h(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()

	p, err := storage.CreateProject(ctx, testPool, "ev24h", "Ev 24h")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	payload := json.RawMessage(`{"level":"error"}`)
	// 2 events in the last hour
	for range 2 {
		testPool.Exec(ctx, `
			INSERT INTO events (project_id, timestamp, received_at, payload)
			VALUES ($1, NOW(), NOW(), $2)
		`, p.ID, payload)
	}
	// 1 event from 2 days ago (outside 24h window)
	testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload)
		VALUES ($1, NOW() - INTERVAL '2 days', NOW() - INTERVAL '2 days', $2)
	`, p.ID, payload)

	projects, err := storage.ListProjects(ctx, testPool)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *storage.Project
	for _, pr := range projects {
		if pr.ID == p.ID {
			found = pr
			break
		}
	}
	if found == nil {
		t.Fatal("project not found in list")
	}
	if found.Events24h != 2 {
		t.Errorf("Events24h: got %d, want 2", found.Events24h)
	}
	if found.EventCount < 3 {
		t.Errorf("EventCount (month): got %d, want >= 3 (includes old event if same month)", found.EventCount)
	}
}

func TestListProjects_storageBytesPositive(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()

	if _, err := storage.CreateProject(ctx, testPool, "storage-est", "Storage Est"); err != nil {
		t.Fatalf("create: %v", err)
	}

	projects, err := storage.ListProjects(ctx, testPool)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(projects) == 0 {
		t.Fatal("expected at least one project")
	}
	// storage_bytes is a proportional estimate: even a project with zero rows
	// gets 0 storage, so we only assert non-negative here.
	if projects[0].StorageBytes < 0 {
		t.Errorf("StorageBytes: got %d, want >= 0", projects[0].StorageBytes)
	}
}

func TestListProjects_alphabeticalOrder(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()

	for _, name := range []string{"Zebra", "Alpha", "Mango"} {
		slug := strings.ToLower(name)
		if _, err := storage.CreateProject(ctx, testPool, slug, name); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	projects, err := storage.ListProjects(ctx, testPool)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(projects) != 3 {
		t.Fatalf("expected 3, got %d", len(projects))
	}
	names := make([]string, len(projects))
	for i, p := range projects {
		names[i] = p.Name
	}
	want := []string{"Alpha", "Mango", "Zebra"}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("position %d: got %q, want %q (full list: %v)", i, names[i], w, names)
		}
	}
}

func TestListProjects_logCount(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()

	p, err := storage.CreateProject(ctx, testPool, "log-count", "Log Count")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for range 4 {
		testPool.Exec(ctx, `
			INSERT INTO logs (project_id, timestamp, received_at, level, body)
			VALUES ($1, NOW(), NOW(), 'info', 'test log')
		`, p.ID)
	}

	projects, err := storage.ListProjects(ctx, testPool)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *storage.Project
	for _, pr := range projects {
		if pr.ID == p.ID {
			found = pr
			break
		}
	}
	if found == nil {
		t.Fatal("project not found in list")
	}
	if found.LogCount != 4 {
		t.Errorf("LogCount: got %d, want 4", found.LogCount)
	}
}

func TestListProjects_txCount(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()

	p, err := storage.CreateProject(ctx, testPool, "tx-count", "Tx Count")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for range 3 {
		testPool.Exec(ctx, `
			INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
			VALUES ($1, '/tx', 'http.server', 'ok', 10, NOW(), NOW())
		`, p.ID)
	}

	projects, err := storage.ListProjects(ctx, testPool)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *storage.Project
	for _, pr := range projects {
		if pr.ID == p.ID {
			found = pr
			break
		}
	}
	if found == nil {
		t.Fatal("project not found in list")
	}
	if found.TxCount != 3 {
		t.Errorf("TxCount: got %d, want 3", found.TxCount)
	}
}

func TestListProjects_logAndTxCountZeroWhenEmpty(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()

	if _, err := storage.CreateProject(ctx, testPool, "empty-counts", "Empty Counts"); err != nil {
		t.Fatalf("create: %v", err)
	}

	projects, err := storage.ListProjects(ctx, testPool)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(projects) == 0 {
		t.Fatal("expected project in list")
	}
	p := projects[0]
	if p.LogCount != 0 {
		t.Errorf("LogCount: got %d, want 0", p.LogCount)
	}
	if p.TxCount != 0 {
		t.Errorf("TxCount: got %d, want 0", p.TxCount)
	}
}

func TestDeleteProject(t *testing.T) {
	truncateProjects(t)

	if _, err := storage.CreateProject(context.Background(), testPool, "to-delete", "To Delete"); err != nil {
		t.Fatalf("create: %v", err)
	}

	found, err := storage.DeleteProject(context.Background(), testPool, "to-delete")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Error("expected found=true")
	}

	found, err = storage.DeleteProject(context.Background(), testPool, "to-delete")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected found=false on second delete")
	}
}

func TestGetByPublicKey_found(t *testing.T) {
	truncateProjects(t)

	created, err := storage.CreateProject(context.Background(), testPool, "lookup-me", "Lookup Me")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	p, err := storage.GetByPublicKey(context.Background(), testPool, created.PublicKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.NotNil(t, p, "expected project, got nil")
	if p.ID != created.ID {
		t.Errorf("ID mismatch: got %q, want %q", p.ID, created.ID)
	}
}

func TestGetByPublicKey_notFound(t *testing.T) {
	p, err := storage.GetByPublicKey(context.Background(), testPool, "nonexistent-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Errorf("expected nil, got %+v", p)
	}
}

func TestGetProjectBySlug_found(t *testing.T) {
	truncateProjects(t)

	created, err := storage.CreateProject(context.Background(), testPool, "slug-lookup", "Slug Lookup")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	p, err := storage.GetProjectBySlug(context.Background(), testPool, "slug-lookup")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.NotNil(t, p, "expected project, got nil")
	if p.ID != created.ID {
		t.Errorf("ID mismatch: got %q, want %q", p.ID, created.ID)
	}
}

func TestGetProjectBySlug_notFound(t *testing.T) {
	p, err := storage.GetProjectBySlug(context.Background(), testPool, "does-not-exist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Errorf("expected nil, got %+v", p)
	}
}

func TestGetByIDAndPublicKey_found(t *testing.T) {
	truncateProjects(t)

	created, err := storage.CreateProject(context.Background(), testPool, "id-and-key", "ID And Key")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	p, err := storage.GetByIDAndPublicKey(context.Background(), testPool, created.ID, created.PublicKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.NotNil(t, p, "expected project, got nil")
	if p.ID != created.ID {
		t.Errorf("ID mismatch: got %q, want %q", p.ID, created.ID)
	}
}

func TestGetByIDAndPublicKey_wrongKey(t *testing.T) {
	truncateProjects(t)

	created, _ := storage.CreateProject(context.Background(), testPool, "id-wrong-key", "Test")

	p, err := storage.GetByIDAndPublicKey(context.Background(), testPool, created.ID, "wrong-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Error("expected nil for wrong key")
	}
}

func TestGetByIDAndPublicKey_wrongID(t *testing.T) {
	truncateProjects(t)

	created, _ := storage.CreateProject(context.Background(), testPool, "wrong-id", "Test")

	p, err := storage.GetByIDAndPublicKey(context.Background(), testPool, "00000000-0000-0000-0000-000000000000", created.PublicKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Error("expected nil for wrong ID")
	}
}

func TestUpdateProject_passthroughDSN(t *testing.T) {
	truncateProjects(t)

	p, err := storage.CreateProject(context.Background(), testPool, "pt-test", "PT Test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.PassthroughDSN != nil {
		t.Errorf("new project should have nil passthrough_dsn, got %q", *p.PassthroughDSN)
	}

	dsn := "https://abc123@sentry.io/456"
	updated, err := storage.UpdateProject(context.Background(), testPool, p.ID, p.Name, p.Slug, &dsn)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	require.NotNil(t, updated, "expected non-nil project")
	if updated.PassthroughDSN == nil || *updated.PassthroughDSN != dsn {
		t.Errorf("passthrough_dsn: got %v, want %q", updated.PassthroughDSN, dsn)
	}

	// Confirm it's persisted.
	fetched, err := storage.GetByPublicKey(context.Background(), testPool, p.PublicKey)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fetched.PassthroughDSN == nil || *fetched.PassthroughDSN != dsn {
		t.Errorf("persisted passthrough_dsn: got %v, want %q", fetched.PassthroughDSN, dsn)
	}
}

func TestUpdateProject_clearPassthroughDSN(t *testing.T) {
	truncateProjects(t)

	p, err := storage.CreateProject(context.Background(), testPool, "pt-clear", "PT Clear")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	dsn := "https://abc123@sentry.io/456"
	p, err = storage.UpdateProject(context.Background(), testPool, p.ID, p.Name, p.Slug, &dsn)
	if err != nil {
		t.Fatalf("set dsn: %v", err)
	}

	cleared, err := storage.UpdateProject(context.Background(), testPool, p.ID, p.Name, p.Slug, nil)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cleared.PassthroughDSN != nil {
		t.Errorf("expected nil passthrough_dsn after clearing, got %q", *cleared.PassthroughDSN)
	}
}

func TestCountProjects_zero(t *testing.T) {
	truncateProjects(t)

	n, err := storage.CountProjects(context.Background(), testPool)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 projects, got %d", n)
	}
}

func TestCountProjects_nonZero(t *testing.T) {
	truncateProjects(t)

	for _, slug := range []string{"count-a", "count-b", "count-c"} {
		if _, err := storage.CreateProject(context.Background(), testPool, slug, slug); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	n, err := storage.CountProjects(context.Background(), testPool)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 projects, got %d", n)
	}
}

func TestDeleteProjectByID_found(t *testing.T) {
	truncateProjects(t)

	p, err := storage.CreateProject(context.Background(), testPool, "del-by-id", "Del By ID")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	deleted, err := storage.DeleteProjectByID(context.Background(), testPool, p.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !deleted {
		t.Error("expected deleted=true")
	}

	// Second delete: not found
	deleted, err = storage.DeleteProjectByID(context.Background(), testPool, p.ID)
	if err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if deleted {
		t.Error("expected deleted=false on second delete")
	}
}

func TestDeleteProjectByID_notFound(t *testing.T) {
	deleted, err := storage.DeleteProjectByID(context.Background(), testPool, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted {
		t.Error("expected deleted=false for nonexistent ID")
	}
}

func TestCountProjectEvents_empty(t *testing.T) {
	truncateProjects(t)
	p, err := storage.CreateProject(context.Background(), testPool, "ev-count-empty", "Ev Count Empty")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	n, err := storage.CountProjectEvents(context.Background(), testPool, p.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
}

func TestCountProjectEvents_withEvents(t *testing.T) {
	p, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	payload := json.RawMessage(`{"level":"error"}`)
	testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload)
		VALUES ($1, NOW(), NOW(), $2)
	`, p.ID, payload)

	n, err := storage.CountProjectEvents(ctx, testPool, p.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n < 1 {
		t.Errorf("expected >= 1, got %d", n)
	}
}

func TestCountMonthlyEvents(t *testing.T) {
	n, err := storage.CountMonthlyEvents(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n < 0 {
		t.Errorf("expected >= 0, got %d", n)
	}
}

func TestCountLastMonthEvents(t *testing.T) {
	n, err := storage.CountLastMonthEvents(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n < 0 {
		t.Errorf("expected >= 0, got %d", n)
	}
}

func TestDailyEventVolume(t *testing.T) {
	p, _ := setupProjectAndEvent(t)

	counts, err := storage.DailyEventVolume(context.Background(), testPool, p.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(counts) != 30 {
		t.Errorf("expected 30 daily buckets, got %d", len(counts))
	}
}

func TestUpdateProjectScrubbing(t *testing.T) {
	p, _ := setupProjectAndEvent(t)

	fields := []string{"password", "token"}
	patterns := json.RawMessage(`[{"pattern": "secret_.*"}]`)

	updated, err := storage.UpdateProjectScrubbing(context.Background(), testPool, p.ID, fields, patterns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated == nil {
		t.Fatal("expected non-nil result")
	}
	if len(updated.ScrubFields) != 2 {
		t.Errorf("scrub_fields: got %v", updated.ScrubFields)
	}
}

func TestUpdateProjectScrubbing_notFound(t *testing.T) {
	got, err := storage.UpdateProjectScrubbing(context.Background(), testPool,
		"00000000-0000-0000-0000-000000000000", nil, json.RawMessage(`[]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for unknown project, got %+v", got)
	}
}

// ── GetProjectIssueCounts ────────────────────────────────────────────────────

func truncateIssues(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE issues CASCADE"); err != nil {
		t.Fatalf("truncate issues: %v", err)
	}
}

func seedOpenIssue(t *testing.T, projectID, fingerprint, title string) *storage.Issue {
	t.Helper()
	iss, _, _, err := storage.UpsertIssue(context.Background(), testPool, projectID, fingerprint, title, "error", "error", "", "", time.Now().UTC())
	if err != nil {
		t.Fatalf("seed issue %q: %v", title, err)
	}
	return iss
}

func TestGetProjectIssueCounts_emptyIDs(t *testing.T) {
	counts, err := storage.GetProjectIssueCounts(context.Background(), testPool, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("expected 0 results for empty ID list, got %d", len(counts))
	}
}

func TestGetProjectIssueCounts_noIssues(t *testing.T) {
	truncateProjects(t)
	p, err := storage.CreateProject(context.Background(), testPool, "issue-count-empty", "Issue Count Empty")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	counts, err := storage.GetProjectIssueCounts(context.Background(), testPool, []string{p.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(counts) != 1 {
		t.Fatalf("expected 1 result, got %d", len(counts))
	}
	if counts[0].OpenIssues != 0 {
		t.Errorf("expected 0 open issues, got %d", counts[0].OpenIssues)
	}
	if counts[0].ProjectID != p.ID {
		t.Errorf("project_id mismatch: got %q, want %q", counts[0].ProjectID, p.ID)
	}
}

func TestGetProjectIssueCounts_openIssues(t *testing.T) {
	truncateProjects(t)
	truncateIssues(t)
	p, err := storage.CreateProject(context.Background(), testPool, "issue-count-open", "Issue Count Open")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	seedOpenIssue(t, p.ID, "fp-cnt-1", "Error One")
	seedOpenIssue(t, p.ID, "fp-cnt-2", "Error Two")
	seedOpenIssue(t, p.ID, "fp-cnt-3", "Error Three")

	counts, err := storage.GetProjectIssueCounts(context.Background(), testPool, []string{p.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(counts) != 1 {
		t.Fatalf("expected 1 result, got %d", len(counts))
	}
	if counts[0].OpenIssues != 3 {
		t.Errorf("expected 3 open issues, got %d", counts[0].OpenIssues)
	}
}

func TestGetProjectIssueCounts_excludesResolvedIssues(t *testing.T) {
	truncateProjects(t)
	truncateIssues(t)
	p, err := storage.CreateProject(context.Background(), testPool, "issue-count-filter", "Issue Count Filter")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	seedOpenIssue(t, p.ID, "fp-filter-open", "Open Error")

	resolved := seedOpenIssue(t, p.ID, "fp-filter-resolved", "Resolved Error")
	if _, err := storage.UpdateIssueStatus(context.Background(), testPool, p.ID, resolved.ID, "resolved", nil); err != nil {
		t.Fatalf("resolve issue: %v", err)
	}

	counts, err := storage.GetProjectIssueCounts(context.Background(), testPool, []string{p.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(counts) != 1 {
		t.Fatalf("expected 1 result, got %d", len(counts))
	}
	if counts[0].OpenIssues != 1 {
		t.Errorf("expected 1 open issue (resolved excluded), got %d", counts[0].OpenIssues)
	}
}

func TestGetProjectIssueCounts_multipleProjects(t *testing.T) {
	truncateProjects(t)
	truncateIssues(t)
	ctx := context.Background()

	pa, _ := storage.CreateProject(ctx, testPool, "issue-count-multi-a", "A")
	pb, _ := storage.CreateProject(ctx, testPool, "issue-count-multi-b", "B")
	pc, _ := storage.CreateProject(ctx, testPool, "issue-count-multi-c", "C")

	seedOpenIssue(t, pa.ID, "fp-multi-a1", "A Error 1")
	seedOpenIssue(t, pa.ID, "fp-multi-a2", "A Error 2")
	seedOpenIssue(t, pb.ID, "fp-multi-b1", "B Error 1")
	// pc has no issues

	counts, err := storage.GetProjectIssueCounts(ctx, testPool, []string{pa.ID, pb.ID, pc.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(counts) != 3 {
		t.Fatalf("expected 3 results, got %d", len(counts))
	}

	byID := make(map[string]int64, 3)
	for _, c := range counts {
		byID[c.ProjectID] = c.OpenIssues
	}
	if byID[pa.ID] != 2 {
		t.Errorf("project A: expected 2, got %d", byID[pa.ID])
	}
	if byID[pb.ID] != 1 {
		t.Errorf("project B: expected 1, got %d", byID[pb.ID])
	}
	if byID[pc.ID] != 0 {
		t.Errorf("project C: expected 0, got %d", byID[pc.ID])
	}
}
