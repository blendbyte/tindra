package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

func seedIssueFull(t *testing.T, fingerprint, title string) *storage.Issue {
	t.Helper()
	iss, _, _, err := storage.UpsertIssue(context.Background(), testPool, testProject.ID, fingerprint, title, "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("seed issue %s: %v", fingerprint, err)
	}
	// Insert a real event row so UnmergeFingerprints can count events per fingerprint.
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO events (project_id, timestamp, payload, fingerprint, issue_id)
		VALUES ($1, NOW(), '{"level":"error"}'::jsonb, $2, $3)
	`, testProject.ID, fingerprint, iss.ID); err != nil {
		t.Fatalf("seed event for %s: %v", fingerprint, err)
	}
	return iss
}

func TestMergeIssues_basic(t *testing.T) {
	truncateIssues(t)

	a := seedIssueFull(t, "fp-merge-a", "Error A")
	b := seedIssueFull(t, "fp-merge-b", "Error B")
	c := seedIssueFull(t, "fp-merge-c", "Error C")

	body, _ := json.Marshal(map[string]any{
		"issue_ids": []string{a.ID, b.ID, c.ID},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/issues/merge", bytes.NewBuffer(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var merged storage.Issue
	if err := json.NewDecoder(rec.Body).Decode(&merged); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if merged.ID != a.ID {
		t.Errorf("primary should be the first ID, got %s", merged.ID)
	}
	// event_count should be 3 (1 per seed call)
	if merged.EventCount != 3 {
		t.Errorf("expected event_count=3, got %d", merged.EventCount)
	}

	// b and c should no longer exist
	for _, id := range []string{b.ID, c.ID} {
		iss, _ := storage.GetIssue(context.Background(), testPool, id)
		if iss != nil {
			t.Errorf("merged issue %s should be deleted", id)
		}
	}
}

func TestGetIssueFingerprints_afterMerge(t *testing.T) {
	truncateIssues(t)

	a := seedIssueFull(t, "fp-fps-a", "Error A")
	b := seedIssueFull(t, "fp-fps-b", "Error B")

	// Merge b into a.
	_, err := storage.MergeIssues(context.Background(), testPool, a.ID, []string{b.ID})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/projects/test-project/issues/%s/fingerprints", a.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Fingerprints []string `json:"fingerprints"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Fingerprints) != 2 {
		t.Errorf("expected 2 fingerprints after merge, got %d: %v", len(resp.Fingerprints), resp.Fingerprints)
	}
}

func TestUnmergeIssue(t *testing.T) {
	truncateIssues(t)

	a := seedIssueFull(t, "fp-unm-a", "Error A")
	b := seedIssueFull(t, "fp-unm-b", "Error B")

	// Merge b into a first.
	merged, err := storage.MergeIssues(context.Background(), testPool, a.ID, []string{b.ID})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.EventCount != 2 {
		t.Fatalf("expected event_count=2 after merge, got %d", merged.EventCount)
	}

	// Now unmerge fp-unm-b back out.
	body, _ := json.Marshal(map[string]any{
		"fingerprints": []string{"fp-unm-b"},
	})
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/projects/test-project/issues/%s/unmerge", a.ID),
		bytes.NewBuffer(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Issues []*storage.Issue `json:"issues"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Issues) != 1 {
		t.Fatalf("expected 1 new issue, got %d", len(resp.Issues))
	}

	// Original issue should have event_count=1 now.
	original, _ := storage.GetIssue(context.Background(), testPool, a.ID)
	if original == nil {
		t.Fatal("original issue should still exist")
	}
	if original.EventCount != 1 {
		t.Errorf("expected original event_count=1, got %d", original.EventCount)
	}
}

func TestUnmergeIssue_cannotRemoveAll(t *testing.T) {
	truncateIssues(t)

	a := seedIssueFull(t, "fp-only-a", "Error A")

	// Try to unmerge the only fingerprint - should fail.
	body, _ := json.Marshal(map[string]any{
		"fingerprints": []string{"fp-only-a"},
	})
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/projects/test-project/issues/%s/unmerge", a.ID),
		bytes.NewBuffer(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMergeIssues_tooFewIDs(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"issue_ids": []string{"only-one"}})
	req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/issues/merge", bytes.NewBuffer(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMergeIssues_wrongProject(t *testing.T) {
	truncateIssues(t)

	// Create an issue in a different project.
	other, err := storage.CreateProject(context.Background(), testPool, "other-merge-proj", "Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	foreign, _, _, _ := storage.UpsertIssue(context.Background(), testPool, other.ID, "fp-foreign", "Foreign", "error", "error", "", "", time.Now())

	own := seedIssueFull(t, "fp-own-merge", "Own")

	body, _ := json.Marshal(map[string]any{"issue_ids": []string{own.ID, foreign.ID}})
	req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/issues/merge", bytes.NewBuffer(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for cross-project merge, got %d", rec.Code)
	}
}
