package storage_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func setupProjectAndEvent(t *testing.T) (*storage.Project, string) {
	t.Helper()
	truncateProjects(t) // cascades to events and issues

	project, err := storage.CreateProject(context.Background(), testPool, "test-issues", "Test Issues")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	payload := json.RawMessage(`{"level":"error","message":"test error","timestamp":"2024-01-01T00:00:00Z"}`)
	var eventID string
	err = testPool.QueryRow(context.Background(), `
		INSERT INTO events (project_id, timestamp, payload) VALUES ($1, $2, $3) RETURNING id
	`, project.ID, time.Now(), payload).Scan(&eventID)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}

	return project, eventID
}

func TestUpsertIssue_creates(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	ts := time.Now().UTC()
	issue, isNew, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-abc", "Some error", "error", "error", "", "", ts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isNew {
		t.Error("expected isNew=true on first insert")
	}
	if issue.ID == "" {
		t.Error("expected non-empty ID")
	}
	if issue.Status != "open" {
		t.Errorf("status: got %q, want %q", issue.Status, "open")
	}
	if issue.EventCount != 1 {
		t.Errorf("event_count: got %d, want 1", issue.EventCount)
	}
}

func TestUpsertIssue_updates(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	ts := time.Now().UTC()
	first, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-update", "Title", "error", "error", "", "", ts)
	second, isNew, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-update", "Title", "error", "error", "", "", ts.Add(time.Second))
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if isNew {
		t.Error("expected isNew=false on second upsert")
	}
	if second.ID != first.ID {
		t.Error("expected same issue ID")
	}
	if second.EventCount != 2 {
		t.Errorf("event_count: got %d, want 2", second.EventCount)
	}
}

func TestLinkEventToIssue(t *testing.T) {
	project, eventID := setupProjectAndEvent(t)
	ctx := context.Background()

	issue, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-link", "Title", "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	if err := storage.LinkEventToIssue(ctx, testPool, eventID, issue.ID, "fp-link"); err != nil {
		t.Fatalf("link event: %v", err)
	}

	var issueID, fingerprint string
	err = testPool.QueryRow(context.Background(),
		"SELECT issue_id::text, fingerprint FROM events WHERE id = $1", eventID,
	).Scan(&issueID, &fingerprint)
	if err != nil {
		t.Fatalf("query event: %v", err)
	}
	if issueID != issue.ID {
		t.Errorf("issue_id: got %q, want %q", issueID, issue.ID)
	}
	if fingerprint != "fp-link" {
		t.Errorf("fingerprint: got %q, want %q", fingerprint, "fp-link")
	}
}

func TestListIssues(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	ts := time.Now().UTC()
	for i, fp := range []string{"fp-1", "fp-2", "fp-3"} {
		if _, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, fp, "Title", "error", "error", "", "", ts.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("upsert issue: %v", err)
		}
	}

	issues, err := storage.ListIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{project.ID}, Limit: 50})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 3 {
		t.Fatalf("expected 3 issues, got %d", len(issues))
	}
	// Most recent first
	if issues[0].Fingerprint != "fp-3" {
		t.Errorf("expected fp-3 first, got %q", issues[0].Fingerprint)
	}
}

func TestListIssues_filterByStatus(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	ts := time.Now().UTC()
	iss, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-status", "Title", "error", "error", "", "", ts)
	_, _ = storage.UpdateIssueStatus(ctx, testPool, project.ID, iss.ID, "resolved", nil)
	storage.UpsertIssue(ctx, testPool, project.ID, "fp-open", "Title 2", "error", "error", "", "", ts)

	// Create a regressed issue: upsert, resolve, then upsert again (triggers regressed).
	regIss, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-regressed", "Title 3", "error", "error", "", "", ts)
	_, _ = storage.UpdateIssueStatus(ctx, testPool, project.ID, regIss.ID, "resolved", nil)
	storage.UpsertIssue(ctx, testPool, project.ID, "fp-regressed", "Title 3", "error", "error", "", "", ts.Add(time.Second))

	// "open" filter must include regressed issues.
	open, _ := storage.ListIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{project.ID}, Status: "open", Limit: 50})
	if len(open) != 2 {
		t.Errorf("expected 2 open+regressed issues, got %d", len(open))
	}

	resolved, _ := storage.ListIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{project.ID}, Status: "resolved", Limit: 50})
	if len(resolved) != 1 {
		t.Errorf("expected 1 resolved issue, got %d", len(resolved))
	}
}

func TestGetIssueMetadataIndependentOfEvents(t *testing.T) {
	p, _ := setupProjectAndEvent(t)
	ctx := context.Background()
	issue, _, _, err := storage.UpsertIssue(ctx, testPool, p.ID, "metadata", "Metadata", "error", "error", "", "v1", time.Now())
	require.NoError(t, err)
	full, err := storage.GetIssue(ctx, testPool, issue.ID)
	require.NoError(t, err)
	lock, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer lock.Rollback(ctx)
	_, err = lock.Exec(ctx, `LOCK TABLE events, releases IN ACCESS EXCLUSIVE MODE`)
	require.NoError(t, err)
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	metadata, err := storage.GetIssueMetadata(readCtx, testPool, issue.ID)
	require.NoError(t, err)
	require.Equal(t, &storage.IssueMetadata{ID: full.ID, ProjectID: full.ProjectID, FirstSeen: full.FirstSeen, Status: full.Status}, metadata)
	missing, err := storage.GetIssueMetadata(readCtx, testPool, "00000000-0000-0000-0000-000000000000")
	require.NoError(t, err)
	require.Nil(t, missing)
}

func TestGetIssue(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	created, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-get", "Get me", "warning", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := storage.GetIssue(ctx, testPool, created.ID)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID mismatch: got %q", got.ID)
	}
	if got.Title != "Get me" {
		t.Errorf("title: got %q", got.Title)
	}
}

func TestUpdateIssueStatus(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	created, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-upd", "Update me", "error", "error", "", "", time.Now())

	updated, err := storage.UpdateIssueStatus(ctx, testPool, project.ID, created.ID, "resolved", nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Status != "resolved" {
		t.Errorf("status: got %q", updated.Status)
	}
}

func TestUpdateIssueStatus_invalid(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	created, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-inv", "Title", "error", "error", "", "", time.Now())
	_, err := storage.UpdateIssueStatus(ctx, testPool, project.ID, created.ID, "badstatus", nil)
	if err == nil {
		t.Error("expected error for invalid status")
	}
}

func TestUpdateIssueStatus_notFound(t *testing.T) {
	got, err := storage.UpdateIssueStatus(context.Background(), testPool, "00000000-0000-0000-0000-000000000000", "00000000-0000-0000-0000-000000000000", "resolved", nil)
	if err != nil {
		t.Fatalf("unexpected error for not-found update: %v", err)
	}
	if got != nil {
		t.Error("expected nil issue when ID not found")
	}
}

func TestListIssues_filterByLevel(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	ts := time.Now().UTC()
	storage.UpsertIssue(ctx, testPool, project.ID, "fp-err", "Error", "error", "error", "", "", ts)
	storage.UpsertIssue(ctx, testPool, project.ID, "fp-warn", "Warning", "warning", "error", "", "", ts)

	errors, _ := storage.ListIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{project.ID}, Level: "error", Limit: 50})
	if len(errors) != 1 || errors[0].Level != "error" {
		t.Errorf("expected 1 error-level issue, got %d", len(errors))
	}

	warnings, _ := storage.ListIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{project.ID}, Level: "warning", Limit: 50})
	if len(warnings) != 1 || warnings[0].Level != "warning" {
		t.Errorf("expected 1 warning-level issue, got %d", len(warnings))
	}
}

func TestListIssues_filterByLevels(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	ts := time.Now().UTC()
	storage.UpsertIssue(ctx, testPool, project.ID, "fp-levels-fatal", "Fatal", "fatal", "error", "", "", ts)
	storage.UpsertIssue(ctx, testPool, project.ID, "fp-levels-error", "Error", "error", "error", "", "", ts)
	storage.UpsertIssue(ctx, testPool, project.ID, "fp-levels-info", "Info", "info", "error", "", "", ts)

	// Levels filter: fatal and error, not info
	got, err := storage.ListIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{project.ID},
		Levels: []string{"fatal", "error"},
		Limit:  50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 issues (fatal+error), got %d", len(got))
	}
	for _, iss := range got {
		if iss.Level != "fatal" && iss.Level != "error" {
			t.Errorf("unexpected level %q in Levels-filtered result", iss.Level)
		}
	}
}

func TestListIssues_cursor(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	base := time.Now().UTC()
	for i, fp := range []string{"fp-a", "fp-b", "fp-c", "fp-d"} {
		storage.UpsertIssue(ctx, testPool, project.ID, fp, "Title", "error", "error", "", "", base.Add(time.Duration(i)*time.Second))
	}

	// Get all issues (newest first)
	all, _ := storage.ListIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{project.ID}, Limit: 50})
	if len(all) != 4 {
		t.Fatalf("expected 4 issues, got %d", len(all))
	}

	// Use the second issue as a cursor - should return issues 3 and 4
	cursor := all[1]
	paged, err := storage.ListIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{project.ID},
		Limit:      50,
		CursorTime: &cursor.LastSeen,
		CursorID:   &cursor.ID,
	})
	if err != nil {
		t.Fatalf("cursor query: %v", err)
	}
	if len(paged) != 2 {
		t.Errorf("expected 2 issues after cursor, got %d", len(paged))
	}
}

func TestGetIssueFingerprints(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	iss, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-1", "Title", "error", "error", "", "", time.Now())
	// Add a second fingerprint to the same issue via the join table directly
	testPool.Exec(ctx, `
		INSERT INTO issue_fingerprints (project_id, fingerprint, issue_id)
		VALUES ($1, 'fp-2', $2)
	`, project.ID, iss.ID)

	fps, err := storage.GetIssueFingerprints(ctx, testPool, iss.ID)
	if err != nil {
		t.Fatalf("get fingerprints: %v", err)
	}
	if len(fps) != 2 {
		t.Errorf("expected 2 fingerprints, got %d: %v", len(fps), fps)
	}
}

func seedIssueWithEvent(t *testing.T, projectID, fp, title string) *storage.Issue {
	t.Helper()
	ctx := context.Background()
	iss, _, _, err := storage.UpsertIssue(ctx, testPool, projectID, fp, title, "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("upsert issue %s: %v", fp, err)
	}
	testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, NOW(), NOW(), '{"level":"error","message":"test"}'::jsonb, $2, $3)
	`, projectID, fp, iss.ID)
	// Update event_count to reflect the actual event
	testPool.Exec(ctx, `UPDATE issues SET event_count = 1 WHERE id = $1`, iss.ID)
	return iss
}

func TestMergeIssues_basic(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	primary := seedIssueWithEvent(t, project.ID, "fp-primary", "Primary")
	merge1 := seedIssueWithEvent(t, project.ID, "fp-merge-1", "Merge1")
	merge2 := seedIssueWithEvent(t, project.ID, "fp-merge-2", "Merge2")

	merged, err := storage.MergeIssues(ctx, testPool, primary.ID, []string{merge1.ID, merge2.ID})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.ID != primary.ID {
		t.Error("expected primary issue ID")
	}
	// Primary should now have event_count = 3 (1 from each)
	if merged.EventCount != 3 {
		t.Errorf("event_count: got %d, want 3", merged.EventCount)
	}

	// Merged issues should be gone
	for _, id := range []string{merge1.ID, merge2.ID} {
		iss, _ := storage.GetIssue(ctx, testPool, id)
		if iss != nil {
			t.Errorf("expected issue %s to be deleted", id)
		}
	}
}

func TestMergeIssues_emptyMergeList(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	primary := seedIssueWithEvent(t, project.ID, "fp-nomerge", "Primary")

	merged, err := storage.MergeIssues(ctx, testPool, primary.ID, []string{})
	if err != nil {
		t.Fatalf("merge empty: %v", err)
	}
	if merged.ID != primary.ID {
		t.Error("expected primary issue returned unchanged")
	}
}

func TestUnmergeFingerprints_basic(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	// Build a merged issue with 2 fingerprints
	primary := seedIssueWithEvent(t, project.ID, "fp-um-1", "Primary")
	secondary := seedIssueWithEvent(t, project.ID, "fp-um-2", "Secondary")
	storage.MergeIssues(ctx, testPool, primary.ID, []string{secondary.ID})

	// Unmerge fp-um-2 back out
	newIssues, err := storage.UnmergeFingerprints(ctx, testPool, primary.ID, []string{"fp-um-2"})
	if err != nil {
		t.Fatalf("unmerge: %v", err)
	}
	if len(newIssues) != 1 {
		t.Fatalf("expected 1 new issue, got %d", len(newIssues))
	}

	// Primary should still exist and have 1 fingerprint
	fps, _ := storage.GetIssueFingerprints(ctx, testPool, primary.ID)
	if len(fps) != 1 || fps[0] != "fp-um-1" {
		t.Errorf("expected fp-um-1 to remain, got %v", fps)
	}
}

func TestUnmergeFingerprints_cannotRemoveAll(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	iss := seedIssueWithEvent(t, project.ID, "fp-only", "Only")

	_, err := storage.UnmergeFingerprints(ctx, testPool, iss.ID, []string{"fp-only"})
	if err == nil {
		t.Error("expected error when trying to remove all fingerprints")
	}
}

func TestUnmergeFingerprints_noEvents(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	// Primary issue with one event-backed fingerprint.
	primary := seedIssueWithEvent(t, project.ID, "fp-noev-primary", "Primary")

	// Directly insert a second fingerprint mapping with NO associated events.
	// This exercises the pgx.ErrNoRows fallback path in UnmergeFingerprints.
	testPool.Exec(ctx, `
		INSERT INTO issue_fingerprints (project_id, fingerprint, issue_id)
		VALUES ($1, 'fp-noev-orphan', $2)
	`, project.ID, primary.ID)

	newIssues, err := storage.UnmergeFingerprints(ctx, testPool, primary.ID, []string{"fp-noev-orphan"})
	if err != nil {
		t.Fatalf("unmerge: %v", err)
	}
	if len(newIssues) != 1 {
		t.Fatalf("expected 1 new issue, got %d", len(newIssues))
	}
	if newIssues[0].Fingerprint != "fp-noev-orphan" {
		t.Errorf("fingerprint: got %q", newIssues[0].Fingerprint)
	}
	// Without events there is nothing to count, rather than an invented 1.
	if newIssues[0].EventCount != 0 {
		t.Errorf("event count: got %d, want 0", newIssues[0].EventCount)
	}
}

// Merging used to delete the merged issues' tags, N+1 samples, comments and
// history through ON DELETE CASCADE. They now move to the primary.
func TestMergeIssues_keepsRelatedRows(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()
	primary := seedIssueWithEvent(t, project.ID, "fp-keep-primary", "Primary")
	merged := seedIssueWithEvent(t, project.ID, "fp-keep-merged", "Merged")

	user, err := storage.CreateUser(ctx, testPool, "merge-keep@example.com", "password1234")
	require.NoError(t, err)
	var eventID, txID string
	require.NoError(t, testPool.QueryRow(ctx, `SELECT id FROM events WHERE issue_id = $1`, merged.ID).Scan(&eventID))
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
		VALUES ($1, '/n1', 'http.server', 'ok', 10, NOW(), NOW()) RETURNING id`, project.ID).Scan(&txID))
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO event_tags (event_id, issue_id, project_id, key, value) VALUES ($1, $2, $3, 'browser', 'Firefox')`, []any{eventID, merged.ID, project.ID}},
		{`INSERT INTO perf_events (issue_id, transaction_id, span_count, total_ms) VALUES ($1, $2, 6, 60)`, []any{merged.ID, txID}},
		{`INSERT INTO issue_comments (issue_id, user_id, body) VALUES ($1, $2, 'keep me')`, []any{merged.ID, user.ID}},
		{`INSERT INTO issue_history (issue_id, event_type) VALUES ($1, 'created')`, []any{merged.ID}},
	} {
		_, err := testPool.Exec(ctx, q.sql, q.args...)
		require.NoError(t, err)
	}

	_, err = storage.MergeIssues(ctx, testPool, primary.ID, []string{merged.ID})
	require.NoError(t, err)

	for _, table := range []string{"event_tags", "perf_events", "issue_comments", "issue_history"} {
		var n int
		require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE issue_id = $1`, primary.ID).Scan(&n))
		require.Equal(t, 1, n, "%s rows must move to the primary", table)
	}
}

func TestUnmergeFingerprints_validatesAndMovesTags(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()
	primary := seedIssueWithEvent(t, project.ID, "fp-umv-1", "Primary")
	secondary := seedIssueWithEvent(t, project.ID, "fp-umv-2", "Secondary")
	_, err := testPool.Exec(ctx, `UPDATE events SET payload = '{"level":"warning","release":"2.0","message":{"formatted":"Disk 91% full","message":"Disk %s full"}}' WHERE issue_id = $1`, secondary.ID)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `UPDATE issues SET environment = 'production' WHERE id = $1`, primary.ID)
	require.NoError(t, err)
	var eventID string
	require.NoError(t, testPool.QueryRow(ctx, `SELECT id FROM events WHERE issue_id = $1`, secondary.ID).Scan(&eventID))
	_, err = storage.MergeIssues(ctx, testPool, primary.ID, []string{secondary.ID})
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `INSERT INTO event_tags (event_id, issue_id, project_id, key, value) VALUES ($1, $2, $3, 'os', 'Linux')`, eventID, primary.ID, project.ID)
	require.NoError(t, err)

	_, err = storage.UnmergeFingerprints(ctx, testPool, primary.ID, []string{"not-on-this-issue"})
	require.ErrorIs(t, err, storage.ErrUnknownFingerprint)

	// A duplicated fingerprint counts once instead of fabricating an issue.
	created, err := storage.UnmergeFingerprints(ctx, testPool, primary.ID, []string{"fp-umv-2", "fp-umv-2"})
	require.NoError(t, err)
	require.Len(t, created, 1)
	split := created[0]
	require.Equal(t, "Disk 91% full", split.Title, "object messages produce a readable title")
	require.Equal(t, "error", split.Kind)
	require.NotNil(t, split.Environment)
	require.Equal(t, "production", *split.Environment)
	require.NotNil(t, split.FirstRelease)
	require.Equal(t, "2.0", *split.FirstRelease)

	var tagIssue string
	require.NoError(t, testPool.QueryRow(ctx, `SELECT issue_id FROM event_tags WHERE event_id = $1`, eventID).Scan(&tagIssue))
	require.Equal(t, split.ID, tagIssue, "tags move with their events")

	var primaryCount int64
	require.NoError(t, testPool.QueryRow(ctx, `SELECT event_count FROM issues WHERE id = $1`, primary.ID).Scan(&primaryCount))
	require.EqualValues(t, 1, primaryCount)
}

// --- BulkUpdateIssueStatus ---

func TestBulkUpdateIssueStatus_basic(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	iss1 := seedIssueWithEvent(t, project.ID, "bulk-fp-1", "Bulk One")
	iss2 := seedIssueWithEvent(t, project.ID, "bulk-fp-2", "Bulk Two")

	updated, err := storage.BulkUpdateIssueStatus(ctx, testPool, []string{iss1.ID, iss2.ID}, "resolved", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updated) != 2 {
		t.Errorf("expected 2 rows updated, got %d", len(updated))
	}

	// Verify status in DB.
	var status string
	testPool.QueryRow(ctx, `SELECT status FROM issues WHERE id = $1`, iss1.ID).Scan(&status)
	if status != "resolved" {
		t.Errorf("iss1 status: got %q, want resolved", status)
	}
}

func TestBulkUpdateIssueStatus_projectIDsFilter(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()

	p1, _ := storage.CreateProject(ctx, testPool, "bulk-p1", "P1")
	p2, _ := storage.CreateProject(ctx, testPool, "bulk-p2", "P2")

	iss1 := seedIssueWithEvent(t, p1.ID, "bulk-scope-fp-1", "P1 Issue")
	iss2 := seedIssueWithEvent(t, p2.ID, "bulk-scope-fp-2", "P2 Issue")

	// Update both IDs, but restrict to p1 only.
	updated, err := storage.BulkUpdateIssueStatus(ctx, testPool, []string{iss1.ID, iss2.ID}, "resolved", nil, []string{p1.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updated) != 1 || updated[0] != iss1.ID {
		t.Errorf("expected only p1 issue %s updated, got %v", iss1.ID, updated)
	}

	var s1, s2 string
	testPool.QueryRow(ctx, `SELECT status FROM issues WHERE id = $1`, iss1.ID).Scan(&s1)
	testPool.QueryRow(ctx, `SELECT status FROM issues WHERE id = $1`, iss2.ID).Scan(&s2)
	if s1 != "resolved" {
		t.Errorf("p1 issue status: got %q, want resolved", s1)
	}
	if s2 == "resolved" {
		t.Errorf("p2 issue should not have been updated, but status is resolved")
	}
}

func TestBulkUpdateIssueStatus_invalidStatus(t *testing.T) {
	ctx := context.Background()
	_, err := storage.BulkUpdateIssueStatus(ctx, testPool, []string{"00000000-0000-0000-0000-000000000000"}, "nonexistent", nil, nil)
	if err == nil {
		t.Error("expected error for invalid status")
	}
}

// --- AutoResolvePerformanceIssues ---

func TestAutoResolvePerformanceIssues_resolvesStale(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	stale := time.Now().Add(-15 * 24 * time.Hour)
	iss, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-stale-perf", "Slow query", "performance", "n1_query", "", "", stale)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	resolved, err := storage.AutoResolvePerformanceIssues(ctx, testPool, 14*24*time.Hour)
	if err != nil {
		t.Fatalf("auto-resolve: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved, got %d", len(resolved))
	}
	if resolved[0].ID != iss.ID {
		t.Errorf("wrong issue: got %s, want %s", resolved[0].ID, iss.ID)
	}

	var status string
	testPool.QueryRow(ctx, `SELECT status FROM issues WHERE id = $1`, iss.ID).Scan(&status)
	if status != "resolved" {
		t.Errorf("status in DB: got %q, want resolved", status)
	}
}

func TestAutoResolvePerformanceIssues_skipsErrorKind(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	stale := time.Now().Add(-15 * 24 * time.Hour)
	_, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-stale-error", "JS error", "error", "error", "", "", stale)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	resolved, err := storage.AutoResolvePerformanceIssues(ctx, testPool, 14*24*time.Hour)
	if err != nil {
		t.Fatalf("auto-resolve: %v", err)
	}
	if len(resolved) != 0 {
		t.Errorf("expected 0 resolved, got %d (error issues must not be auto-resolved)", len(resolved))
	}
}

func TestAutoResolvePerformanceIssues_skipsRecentPerf(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	recent := time.Now().Add(-5 * 24 * time.Hour)
	_, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-recent-perf", "Slow query", "performance", "n1_query", "", "", recent)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	resolved, err := storage.AutoResolvePerformanceIssues(ctx, testPool, 14*24*time.Hour)
	if err != nil {
		t.Fatalf("auto-resolve: %v", err)
	}
	if len(resolved) != 0 {
		t.Errorf("expected 0 resolved, got %d (issue seen 5 days ago must not be auto-resolved)", len(resolved))
	}
}

func TestAutoResolvePerformanceIssues_skipsAlreadyResolved(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	stale := time.Now().Add(-15 * 24 * time.Hour)
	iss, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-already-res", "Slow query", "performance", "n1_query", "", "", stale)
	storage.UpdateIssueStatus(ctx, testPool, project.ID, iss.ID, "resolved", nil)

	resolved, err := storage.AutoResolvePerformanceIssues(ctx, testPool, 14*24*time.Hour)
	if err != nil {
		t.Fatalf("auto-resolve: %v", err)
	}
	if len(resolved) != 0 {
		t.Errorf("expected 0 resolved (already resolved), got %d", len(resolved))
	}
}

func TestAutoResolvePerformanceIssues_includesRegressed(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	stale := time.Now().Add(-15 * 24 * time.Hour)
	iss, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-regressed-perf", "Slow query", "performance", "n1_query", "", "", stale)
	testPool.Exec(ctx, `UPDATE issues SET status = 'regressed' WHERE id = $1`, iss.ID)

	resolved, err := storage.AutoResolvePerformanceIssues(ctx, testPool, 14*24*time.Hour)
	if err != nil {
		t.Fatalf("auto-resolve: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved (regressed perf issues qualify), got %d", len(resolved))
	}
	if resolved[0].ID != iss.ID {
		t.Errorf("wrong issue resolved")
	}
}

// --- user_count ---

func insertEventWithPayload(t *testing.T, projectID, issueID string, payload string) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO events (project_id, timestamp, received_at, payload, issue_id)
		VALUES ($1, NOW(), NOW(), $2::jsonb, $3::uuid)
	`, projectID, payload, issueID)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
}

func TestUserCount_byID(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	iss, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-uc-id", "UC", "error", "error", "", "", time.Now())
	insertEventWithPayload(t, project.ID, iss.ID, `{"level":"error","user":{"id":"u1"}}`)
	insertEventWithPayload(t, project.ID, iss.ID, `{"level":"error","user":{"id":"u1"}}`)
	insertEventWithPayload(t, project.ID, iss.ID, `{"level":"error","user":{"id":"u2"}}`)

	got, err := storage.GetIssue(ctx, testPool, iss.ID)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if got.UserCount != 2 {
		t.Errorf("user_count: got %d, want 2", got.UserCount)
	}
}

func TestUserCount_byEmailFallback(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	iss, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-uc-email", "UC email", "error", "error", "", "", time.Now())
	// No user.id — only email
	insertEventWithPayload(t, project.ID, iss.ID, `{"level":"error","user":{"email":"alice@example.com"}}`)
	insertEventWithPayload(t, project.ID, iss.ID, `{"level":"error","user":{"email":"alice@example.com"}}`)
	insertEventWithPayload(t, project.ID, iss.ID, `{"level":"error","user":{"email":"bob@example.com"}}`)

	got, err := storage.GetIssue(ctx, testPool, iss.ID)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if got.UserCount != 2 {
		t.Errorf("user_count: got %d, want 2 (email fallback)", got.UserCount)
	}
}

func TestUserCount_byUsernameFallback(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	iss, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-uc-uname", "UC username", "error", "error", "", "", time.Now())
	insertEventWithPayload(t, project.ID, iss.ID, `{"level":"error","user":{"username":"alice"}}`)
	insertEventWithPayload(t, project.ID, iss.ID, `{"level":"error","user":{"username":"bob"}}`)

	got, err := storage.GetIssue(ctx, testPool, iss.ID)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if got.UserCount != 2 {
		t.Errorf("user_count: got %d, want 2 (username fallback)", got.UserCount)
	}
}

func TestUserCount_noUserField(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	iss, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-uc-none", "UC none", "error", "error", "", "", time.Now())
	insertEventWithPayload(t, project.ID, iss.ID, `{"level":"error","message":"no user"}`)

	got, err := storage.GetIssue(ctx, testPool, iss.ID)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if got.UserCount != 0 {
		t.Errorf("user_count: got %d, want 0", got.UserCount)
	}
}

func TestCountAllIssues(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	ts := time.Now().UTC()
	for _, fp := range []string{"fp-ca1", "fp-ca2", "fp-ca3"} {
		storage.UpsertIssue(ctx, testPool, project.ID, fp, "Title", "error", "error", "", "", ts)
	}

	n, err := storage.CountAllIssues(ctx, testPool, storage.IssueFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n < 3 {
		t.Errorf("expected >= 3, got %d", n)
	}
}

func TestCountAllIssues_withFilter(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	ts := time.Now().UTC()
	storage.UpsertIssue(ctx, testPool, project.ID, "fp-caf1", "Title", "error", "error", "prod", "", ts)
	storage.UpsertIssue(ctx, testPool, project.ID, "fp-caf2", "Title", "warning", "error", "staging", "", ts)

	n, err := storage.CountAllIssues(ctx, testPool, storage.IssueFilter{Level: "error"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n < 1 {
		t.Errorf("expected >= 1 error-level issue, got %d", n)
	}
}

func TestListAllIssues(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	ts := time.Now().UTC()
	for _, fp := range []string{"fp-la1", "fp-la2"} {
		storage.UpsertIssue(ctx, testPool, project.ID, fp, "Title", "error", "error", "", "", ts)
	}

	issues, err := storage.ListAllIssues(ctx, testPool, storage.IssueFilter{Limit: 50})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) < 2 {
		t.Errorf("expected >= 2 issues, got %d", len(issues))
	}
}

func TestListAllIssues_filterByProject(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	ts := time.Now().UTC()
	storage.UpsertIssue(ctx, testPool, project.ID, "fp-laf1", "Title", "error", "error", "", "", ts)

	issues, err := storage.ListAllIssues(ctx, testPool, storage.IssueFilter{
		ProjectIDs: []string{project.ID},
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, iss := range issues {
		if iss.ProjectID != project.ID {
			t.Errorf("unexpected project_id: got %q", iss.ProjectID)
		}
	}
}

func TestListAllIssues_cursorPagination(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	ts := time.Now().UTC()
	for i, fp := range []string{"fp-cur1", "fp-cur2", "fp-cur3"} {
		storage.UpsertIssue(ctx, testPool, project.ID, fp, "Cursor Issue", "error", "error", "", "", ts.Add(time.Duration(-i)*time.Second))
	}

	first, err := storage.ListAllIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{project.ID}, Limit: 2})
	if err != nil || len(first) == 0 {
		t.Fatalf("first page: err=%v len=%d", err, len(first))
	}

	last := first[len(first)-1]
	second, err := storage.ListAllIssues(ctx, testPool, storage.IssueFilter{
		ProjectIDs: []string{project.ID},
		Limit:      10,
		CursorTime: &last.LastSeen,
		CursorID:   &last.ID,
	})
	if err != nil {
		t.Fatalf("cursor page: %v", err)
	}
	_ = second
}

func TestExpireIgnoredIssues(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	ts := time.Now().UTC()
	issue, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-expire", "Title", "error", "error", "", "", ts)

	// Set status=ignored with an already-expired ignore_until
	testPool.Exec(ctx, `
		UPDATE issues SET status = 'ignored', ignore_until = NOW() - interval '1 minute'
		WHERE id = $1
	`, issue.ID)

	n, err := storage.ExpireIgnoredIssues(ctx, testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n < 1 {
		t.Errorf("expected >= 1 expired, got %d", n)
	}

	got, _ := storage.GetIssue(ctx, testPool, issue.ID)
	if got == nil {
		t.Fatal("expected non-nil issue")
	}
	// The snooze ends quietly: open, not regressed, until the error recurs.
	if got.Status != "open" {
		t.Errorf("status: got %q, want open", got.Status)
	}
}

// After a snooze ends, the next event is a regression: status regressed,
// regressed_at set, and reported so regression alerts fire.
func TestExpiredSnoozeRegressesOnRecurrence(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()
	issue, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-snooze-recur", "Title", "error", "error", "", "", time.Now())
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `UPDATE issues SET status = 'ignored', ignore_until = NOW() - interval '1 minute' WHERE id = $1`, issue.ID)
	require.NoError(t, err)
	_, err = storage.ExpireIgnoredIssues(ctx, testPool)
	require.NoError(t, err)

	recurred, created, regressed, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-snooze-recur", "Title", "error", "error", "", "", time.Now())
	require.NoError(t, err)
	require.False(t, created)
	require.True(t, regressed, "the first event after a snooze ends is a regression")
	require.Equal(t, "regressed", recurred.Status)
	var regressedAt *time.Time
	var snoozeEnded *time.Time
	require.NoError(t, testPool.QueryRow(ctx, `SELECT regressed_at, snooze_ended_at FROM issues WHERE id = $1`, issue.ID).Scan(&regressedAt, &snoozeEnded))
	require.NotNil(t, regressedAt, "regression alerts key off regressed_at")
	require.Nil(t, snoozeEnded, "the marker is cleared once used")

	_, _, regressedAgain, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-snooze-recur", "Title", "error", "error", "", "", time.Now())
	require.NoError(t, err)
	require.False(t, regressedAgain, "later events do not regress again")
}

// A manual status change after a snooze ended forgets it.
func TestExpiredSnoozeForgottenOnManualStatusChange(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()
	issue, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-snooze-manual", "Title", "error", "error", "", "", time.Now())
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `UPDATE issues SET status = 'ignored', ignore_until = NOW() - interval '1 minute' WHERE id = $1`, issue.ID)
	require.NoError(t, err)
	_, err = storage.ExpireIgnoredIssues(ctx, testPool)
	require.NoError(t, err)
	_, err = storage.UpdateIssueStatus(ctx, testPool, project.ID, issue.ID, "open", nil)
	require.NoError(t, err)

	_, _, regressed, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-snooze-manual", "Title", "error", "error", "", "", time.Now())
	require.NoError(t, err)
	require.False(t, regressed)
}

func TestExpireIgnoredIssues_noneToExpire(t *testing.T) {
	n, err := storage.ExpireIgnoredIssues(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n < 0 {
		t.Errorf("expected >= 0, got %d", n)
	}
}

func TestUpdateIssueAssignee(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()
	truncateUsers(t)

	u, _ := storage.CreateUser(ctx, testPool, "assignee@example.com", "password1234")
	issue, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-assign", "Title", "error", "error", "", "", time.Now())

	updated, err := storage.UpdateIssueAssignee(ctx, testPool, issue.ID, &u.ID)
	if err != nil {
		t.Fatalf("update assignee: %v", err)
	}
	if updated == nil {
		t.Fatal("expected non-nil result")
	}
	if updated.AssigneeID == nil || *updated.AssigneeID != u.ID {
		t.Errorf("assignee_id: got %v, want %q", updated.AssigneeID, u.ID)
	}

	// Unassign
	updated2, err := storage.UpdateIssueAssignee(ctx, testPool, issue.ID, nil)
	if err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if updated2.AssigneeID != nil {
		t.Errorf("expected nil assignee_id after unassign")
	}
}

func TestUpdateIssueAssignee_notFound(t *testing.T) {
	got, err := storage.UpdateIssueAssignee(context.Background(), testPool, "00000000-0000-0000-0000-000000000000", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for unknown issue, got %+v", got)
	}
}

func TestGetIssueSparklines(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	issue, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-spark", "Title", "error", "error", "", "", time.Now())

	// Insert an event today
	testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, NOW(), NOW(), '{"level":"error"}'::jsonb, 'fp-spark', $2)
	`, project.ID, issue.ID)

	sparklines, err := storage.GetIssueSparklines(ctx, testPool, []string{issue.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	counts, ok := sparklines[issue.ID]
	if !ok {
		t.Fatalf("expected sparkline for issue %q", issue.ID)
	}
	if len(counts) != 14 {
		t.Errorf("expected 14 buckets, got %d", len(counts))
	}
	total := 0
	for _, c := range counts {
		total += c
	}
	if total < 1 {
		t.Error("expected at least one event in sparkline")
	}
}

func TestGetIssueSparklines_empty(t *testing.T) {
	sparklines, err := storage.GetIssueSparklines(context.Background(), testPool, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sparklines) != 0 {
		t.Errorf("expected empty map, got %v", sparklines)
	}
}

func TestCountAllIssues_filterByLevels(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	ts := time.Now().UTC()
	storage.UpsertIssue(ctx, testPool, project.ID, "fp-caf-fatal", "Fatal", "fatal", "error", "", "", ts)
	storage.UpsertIssue(ctx, testPool, project.ID, "fp-caf-warning", "Warning", "warning", "error", "", "", ts)
	storage.UpsertIssue(ctx, testPool, project.ID, "fp-caf-info", "Info", "info", "error", "", "", ts)

	n, err := storage.CountAllIssues(ctx, testPool, storage.IssueFilter{
		Levels:     []string{"fatal", "warning"},
		ProjectIDs: []string{project.ID},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 issues (fatal+warning), got %d", n)
	}
}

// TestCountAllIssues_allCommonFilters exercises every branch in addCommonFilters by
// passing a filter with all optional fields populated.
func TestCountAllIssues_allCommonFilters(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	ts := time.Now().UTC()
	storage.UpsertIssue(ctx, testPool, project.ID, "fp-acf1", "Filter Title", "error", "error", "production", "", ts)

	sinceLast := ts.Add(-time.Hour)
	sinceReg := ts.Add(-time.Hour)

	// Status="resolved" hits the default switch case in addCommonFilters
	n, err := storage.CountAllIssues(ctx, testPool, storage.IssueFilter{
		Status:         "resolved",
		Level:          "error",
		Kind:           "error",
		Environment:    "production",
		Title:          "Filter",
		ProjectIDs:     []string{project.ID},
		SinceLast:      &sinceLast,
		SinceRegressed: &sinceReg,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = n // result may be 0 since issue is open, not resolved — we just need the branches covered
}

func TestCountAllIssues_tagFilters(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	ts := time.Now().UTC()
	storage.UpsertIssue(ctx, testPool, project.ID, "fp-tagf1", "Tag Issue", "error", "error", "", "", ts)

	// TagKey without TagValue (key-only tag filter branch)
	n1, err := storage.CountAllIssues(ctx, testPool, storage.IssueFilter{
		TagKey: "browser",
	})
	if err != nil {
		t.Fatalf("tag key filter: %v", err)
	}

	// TagKey + TagValue (key+value tag filter branch)
	n2, err := storage.CountAllIssues(ctx, testPool, storage.IssueFilter{
		TagKey:   "browser",
		TagValue: "Chrome",
	})
	if err != nil {
		t.Fatalf("tag key+value filter: %v", err)
	}
	_ = n1
	_ = n2
}

func TestListAllIssues_statusAndAssigneeFilters(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	ts := time.Now().UTC()
	storage.UpsertIssue(ctx, testPool, project.ID, "fp-saf1", "Assignee Issue", "error", "error", "", "", ts)

	// Status default case (not "open" or "")
	issues, err := storage.ListAllIssues(ctx, testPool, storage.IssueFilter{
		Status: "resolved",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("status filter: %v", err)
	}

	// AssigneeID filter (issues without that assignee returns empty)
	issues2, err := storage.ListAllIssues(ctx, testPool, storage.IssueFilter{
		AssigneeID: "00000000-0000-0000-0000-000000000001",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("assignee filter: %v", err)
	}
	_ = issues
	_ = issues2
}

// ListIssues used to ignore most filter fields; it now applies all of them.
func TestListIssues_appliesAllFilters(t *testing.T) {
	ctx := context.Background()
	truncateProjects(t)
	a, err := storage.CreateProject(ctx, testPool, "list-filter-a", "A")
	require.NoError(t, err)
	b, err := storage.CreateProject(ctx, testPool, "list-filter-b", "B")
	require.NoError(t, err)
	c, err := storage.CreateProject(ctx, testPool, "list-filter-c", "C")
	require.NoError(t, err)
	old := time.Now().Add(-2 * time.Hour)
	for _, tc := range []struct{ project, fp, env string }{
		{a.ID, "lf-a-prod", "production"}, {b.ID, "lf-b-prod", "production"},
		{b.ID, "lf-b-staging", "staging"}, {c.ID, "lf-c-prod", "production"},
	} {
		_, _, _, err := storage.UpsertIssue(ctx, testPool, tc.project, tc.fp, "Issue "+tc.fp, "error", "error", tc.env, "", time.Now())
		require.NoError(t, err)
	}
	oldIssue, _, _, err := storage.UpsertIssue(ctx, testPool, a.ID, "lf-a-old", "Old issue", "error", "error", "production", "", old)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `UPDATE issues SET created_at = $2 WHERE id = $1`, oldIssue.ID, old)
	require.NoError(t, err)

	got, err := storage.ListIssues(ctx, testPool, storage.IssueFilter{
		ProjectIDs:   []string{a.ID, b.ID},
		Environment:  "production",
		CreatedSince: new(time.Now().Add(-time.Hour)),
		Limit:        50,
	})
	require.NoError(t, err)
	fps := []string{}
	for _, iss := range got {
		fps = append(fps, iss.Fingerprint)
	}
	require.ElementsMatch(t, []string{"lf-a-prod", "lf-b-prod"}, fps)
}

// "%" and "_" in a title search are literal, not wildcards.
func TestListIssues_titleSearchEscapesWildcards(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()
	for _, title := range []string{"100% CPU", "user_id missing", "plain title"} {
		_, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-like-"+title, title, "error", "error", "", "", time.Now())
		require.NoError(t, err)
	}
	search := func(q string) []string {
		got, err := storage.ListIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{project.ID}, Title: q, Limit: 50})
		require.NoError(t, err)
		titles := []string{}
		for _, iss := range got {
			titles = append(titles, iss.Title)
		}
		return titles
	}
	require.Equal(t, []string{"100% CPU"}, search("%"))
	require.Equal(t, []string{"user_id missing"}, search("_"))
}

// Per-project open counts include regressed issues, matching every other
// "open" in the app (the dashboard total, the issue list filter).
func TestGetProjectIssueCounts_includesRegressed(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()
	for _, status := range []string{"open", "regressed", "resolved"} {
		iss, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-count-"+status, status, "error", "error", "", "", time.Now())
		require.NoError(t, err)
		_, err = testPool.Exec(ctx, `UPDATE issues SET status = $2 WHERE id = $1`, iss.ID, status)
		require.NoError(t, err)
	}
	counts, err := storage.GetProjectIssueCounts(ctx, testPool, []string{project.ID})
	require.NoError(t, err)
	require.Len(t, counts, 1)
	var openOrRegressed int64
	require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE project_id = $1 AND status IN ('open','regressed')`, project.ID).Scan(&openOrRegressed))
	require.Equal(t, openOrRegressed, counts[0].OpenIssues)
}

// Concurrent events on a resolved issue report the regression exactly once.
// Transaction A plays the first event: it regresses the issue and holds the
// row lock. A second upsert started meanwhile must wait, then see the row as
// already regressed rather than the "resolved" of its statement snapshot.
func TestUpsertIssue_concurrentRegressionReportedOnce(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()
	iss, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-concurrent-regress", "Title", "error", "error", "", "", time.Now())
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `UPDATE issues SET status = 'resolved' WHERE id = $1`, iss.ID)
	require.NoError(t, err)

	first, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer first.Rollback(ctx) //nolint:errcheck
	_, err = first.Exec(ctx, `UPDATE issues SET status = 'regressed', regressed_at = NOW() WHERE id = $1`, iss.ID)
	require.NoError(t, err)

	regressed := make(chan bool, 1)
	go func() {
		_, _, r, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-concurrent-regress", "Title", "error", "error", "", "", time.Now())
		if err != nil {
			t.Errorf("upsert: %v", err)
		}
		regressed <- r
	}()
	require.Eventually(t, func() bool {
		var waiting int
		testPool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting)
		return waiting > 0
	}, 5*time.Second, 10*time.Millisecond, "second upsert should be waiting on the row lock")
	require.NoError(t, first.Commit(ctx))

	require.False(t, <-regressed, "the regression was already reported by the first event")
}
