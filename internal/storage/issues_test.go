package storage_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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

	issues, err := storage.ListIssues(ctx, testPool, project.ID, storage.IssueFilter{Limit: 50})
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
	open, _ := storage.ListIssues(ctx, testPool, project.ID, storage.IssueFilter{Status: "open", Limit: 50})
	if len(open) != 2 {
		t.Errorf("expected 2 open+regressed issues, got %d", len(open))
	}

	resolved, _ := storage.ListIssues(ctx, testPool, project.ID, storage.IssueFilter{Status: "resolved", Limit: 50})
	if len(resolved) != 1 {
		t.Errorf("expected 1 resolved issue, got %d", len(resolved))
	}
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

	errors, _ := storage.ListIssues(ctx, testPool, project.ID, storage.IssueFilter{Level: "error", Limit: 50})
	if len(errors) != 1 || errors[0].Level != "error" {
		t.Errorf("expected 1 error-level issue, got %d", len(errors))
	}

	warnings, _ := storage.ListIssues(ctx, testPool, project.ID, storage.IssueFilter{Level: "warning", Limit: 50})
	if len(warnings) != 1 || warnings[0].Level != "warning" {
		t.Errorf("expected 1 warning-level issue, got %d", len(warnings))
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
	all, _ := storage.ListIssues(ctx, testPool, project.ID, storage.IssueFilter{Limit: 50})
	if len(all) != 4 {
		t.Fatalf("expected 4 issues, got %d", len(all))
	}

	// Use the second issue as a cursor - should return issues 3 and 4
	cursor := all[1]
	paged, err := storage.ListIssues(ctx, testPool, project.ID, storage.IssueFilter{
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
	// Fallback sets fingerprint as the title.
	if newIssues[0].Fingerprint != "fp-noev-orphan" {
		t.Errorf("fingerprint: got %q", newIssues[0].Fingerprint)
	}
}

// --- BulkUpdateIssueStatus ---

func TestBulkUpdateIssueStatus_basic(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	iss1 := seedIssueWithEvent(t, project.ID, "bulk-fp-1", "Bulk One")
	iss2 := seedIssueWithEvent(t, project.ID, "bulk-fp-2", "Bulk Two")

	n, err := storage.BulkUpdateIssueStatus(ctx, testPool, []string{iss1.ID, iss2.ID}, "resolved", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 rows updated, got %d", n)
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
	n, err := storage.BulkUpdateIssueStatus(ctx, testPool, []string{iss1.ID, iss2.ID}, "resolved", nil, []string{p1.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row updated (p1 only), got %d", n)
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
