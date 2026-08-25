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

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func issuesHandler() http.Handler {
	return api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
}

func authCookie() *http.Cookie {
	return &http.Cookie{Name: "tindra_session", Value: testSession.Token}
}

func truncateIssues(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE issues CASCADE"); err != nil {
		t.Fatalf("truncate issues: %v", err)
	}
}

func seedIssue(t *testing.T, fingerprint, title string) *storage.Issue {
	t.Helper()
	iss, _, _, err := storage.UpsertIssue(context.Background(), testPool, testProject.ID, fingerprint, title, "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	return iss
}

func TestListIssues_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/issues", nil)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestListIssues_empty(t *testing.T) {
	truncateIssues(t)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/issues", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Issues []json.RawMessage `json:"issues"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Issues) != 0 {
		t.Errorf("expected 0 issues, got %d", len(resp.Issues))
	}
}

func TestListIssues_withData(t *testing.T) {
	truncateIssues(t)

	seedIssue(t, "fp-a", "Error A")
	seedIssue(t, "fp-b", "Error B")

	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/issues", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Issues []*storage.Issue `json:"issues"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Issues) != 2 {
		t.Errorf("expected 2 issues, got %d", len(resp.Issues))
	}
}

func TestListIssues_unknownProject(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/projects/no-such-project/issues", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestGetIssue(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-get", "Get Issue")

	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/issues/"+iss.ID, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got storage.Issue
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != iss.ID {
		t.Errorf("ID mismatch: got %q", got.ID)
	}
}

func TestUpdateIssue_resolve(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-upd", "Update Issue")

	body := bytes.NewBufferString(`{"status":"resolved"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/projects/test-project/issues/"+iss.ID, body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got storage.Issue
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "resolved" {
		t.Errorf("status: got %q, want resolved", got.Status)
	}
}

func TestUpdateIssue_invalidStatus(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-inv", "Invalid Status")

	body := bytes.NewBufferString(`{"status":"nonsense"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/projects/test-project/issues/"+iss.ID, body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- handleListPerfEvents ---

func TestListPerfEvents_empty(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-perf-empty", "Perf Empty")

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/perf-events", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var events []json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&events); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 perf events, got %d", len(events))
	}
}

func TestListPerfEvents_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/issues/00000000-0000-0000-0000-000000000000/perf-events", nil)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}
