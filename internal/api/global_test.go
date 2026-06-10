package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

// --- handleListProjects ---

func TestListProjects_success(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var projects []*storage.Project
	if err := json.NewDecoder(rec.Body).Decode(&projects); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(projects) == 0 {
		t.Error("expected at least one project (testProject)")
	}
}

func TestListProjects_includesStatsFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var projects []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&projects); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(projects) == 0 {
		t.Fatal("expected at least one project")
	}
	p := projects[0]
	for _, key := range []string{"event_count", "events_24h", "storage_bytes"} {
		if _, ok := p[key]; !ok {
			t.Errorf("expected key %q in project response", key)
		}
	}
}

func TestListProjects_sortedAlphabetically(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var projects []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&projects); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i := 1; i < len(projects); i++ {
		a := projects[i-1]["name"].(string)
		b := projects[i]["name"].(string)
		if a > b {
			t.Errorf("not sorted: %q > %q at positions %d,%d", a, b, i-1, i)
		}
	}
}

func TestListProjects_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleCreateProject ---

func TestCreateProject_success(t *testing.T) {
	body := bytes.NewBufferString(`{"name":"New Project","slug":"new-project-test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var p storage.Project
	if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Slug != "new-project-test" {
		t.Errorf("slug: got %q", p.Slug)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", p.ID)
	})
}

func TestCreateProject_missingFields(t *testing.T) {
	body := bytes.NewBufferString(`{"name":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name/slug, got %d", rec.Code)
	}
}

func TestCreateProject_invalidSlug(t *testing.T) {
	body := bytes.NewBufferString(`{"name":"X","slug":"Invalid Slug!!"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid slug, got %d", rec.Code)
	}
}

func TestCreateProject_duplicateSlug(t *testing.T) {
	// testProject slug is "test-project" - creating it again should 409.
	body := bytes.NewBufferString(`{"name":"Dup","slug":"test-project"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 for duplicate slug, got %d", rec.Code)
	}
}

func TestCreateProject_projectLimitReached(t *testing.T) {
	// projectLimit=1; testProject already exists → 429.
	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "", 0, 1, 0, 0, 0, 0, nil, false, nil)
	body := bytes.NewBufferString(`{"name":"Limited","slug":"limited-proj"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 for project limit, got %d", rec.Code)
	}
}

func TestCreateProject_forbidden(t *testing.T) {
	roCookie := makeReadOnlyUser(t, "ro-create-project@example.com")
	body := bytes.NewBufferString(`{"name":"Forbidden","slug":"forbidden-proj"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

// --- handleGetSettings ---

func TestGetSettings_success(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["version"]; !ok {
		t.Error("expected 'version' key in settings response")
	}
}

func TestGetSettings_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestGetSettings_updateAvailable(t *testing.T) {
	prev := api.AppVersion
	api.AppVersion = "v1.0.0"
	defer func() { api.AppVersion = prev }()

	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, nil)
	api.SetLatestVersionForTest(h, "v9.9.9", "https://example.com/releases/v9.9.9")

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		UpdateAvailable bool   `json:"update_available"`
		LatestVersion   string `json:"latest_version"`
		ReleaseURL      string `json:"release_url"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.UpdateAvailable {
		t.Error("expected update_available: true when latest > current")
	}
	if resp.LatestVersion != "v9.9.9" {
		t.Errorf("latest_version: got %q, want v9.9.9", resp.LatestVersion)
	}
	if resp.ReleaseURL == "" {
		t.Error("expected non-empty release_url")
	}
}

// --- handleListAllIssues ---

func TestListAllIssues_success(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["issues"]; !ok {
		t.Error("expected 'issues' key in response")
	}
}

func TestListAllIssues_withFilters(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues?status=open&level=error", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

// --- handleGetIssueGlobal ---

func TestGetIssueGlobal_found(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-global-get", "Global Get", "error", "error", "", "", time.Now().UTC())

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got storage.Issue
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != iss.ID {
		t.Errorf("ID: got %q, want %q", got.ID, iss.ID)
	}
}

func TestGetIssueGlobal_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/issues/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- handleGetIssueHistory ---

func TestGetIssueHistory_success(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-hist-api", "History API", "error", "error", "", "", time.Now().UTC())

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/history", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var entries []*storage.IssueHistoryEntry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if entries == nil {
		t.Error("expected non-nil slice")
	}
}

// --- handleGetIssueTags ---

func TestGetIssueTags_success(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-tags-api", "Tags API", "error", "error", "", "", time.Now().UTC())

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/tags", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var tags []storage.TagSummary
	if err := json.NewDecoder(rec.Body).Decode(&tags); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if tags == nil {
		t.Error("expected non-nil slice")
	}
}

// --- handleBulkUpdateIssues ---

func TestBulkUpdateIssues_success(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-bulk", "Bulk Issue", "error", "error", "", "", time.Now().UTC())

	body, _ := json.Marshal(map[string]any{
		"ids":    []string{iss.ID},
		"status": "resolved",
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/bulk", bytes.NewBuffer(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["updated"] == nil {
		t.Error("expected 'updated' in response")
	}
}

func TestBulkUpdateIssues_emptyIDs(t *testing.T) {
	body := bytes.NewBufferString(`{"ids":[],"status":"resolved"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/bulk", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty ids, got %d", rec.Code)
	}
}

func TestBulkUpdateIssues_badBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/bulk",
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- handleUpdateIssueGlobal ---

func TestUpdateIssueGlobal_statusChange(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-upd-global", "Update Global", "error", "error", "", "", time.Now().UTC())

	body := bytes.NewBufferString(`{"status":"resolved"}`)
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/issues/%s", iss.ID), body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated storage.Issue
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Status != "resolved" {
		t.Errorf("status: got %q, want resolved", updated.Status)
	}
}

func TestUpdateIssueGlobal_sameStatus(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-same-status", "Same Status", "error", "error", "", "", time.Now().UTC())

	body := bytes.NewBufferString(`{"status":"open"}`) // already open
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/issues/%s", iss.ID), body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for no-op status change, got %d", rec.Code)
	}
}

func TestUpdateIssueGlobal_notFound(t *testing.T) {
	body := bytes.NewBufferString(`{"status":"resolved"}`)
	req := httptest.NewRequest(http.MethodPatch,
		"/api/issues/00000000-0000-0000-0000-000000000000", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- handleUpdateProject ---

func TestUpdateProject_success(t *testing.T) {
	p, err := storage.CreateProject(context.Background(), testPool, "upd-proj", "Upd Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	body := bytes.NewBufferString(`{"name":"Updated Name","slug":"upd-proj"}`)
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/projects/%s", p.ID), body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated storage.Project
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Name != "Updated Name" {
		t.Errorf("name: got %q", updated.Name)
	}
}

func TestUpdateProject_invalidSlug(t *testing.T) {
	p, err := storage.CreateProject(context.Background(), testPool, "slug-upd-inv", "Slug Inv")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	body := bytes.NewBufferString(`{"name":"X","slug":"INVALID SLUG!!"}`)
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/projects/%s", p.ID), body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid slug, got %d", rec.Code)
	}
}

func TestUpdateProject_badBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/projects/%s", testProject.ID),
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", rec.Code)
	}
}

func TestUpdateProject_notFound(t *testing.T) {
	body := bytes.NewBufferString(`{"name":"X","slug":"some-slug"}`)
	req := httptest.NewRequest(http.MethodPatch,
		"/api/projects/00000000-0000-0000-0000-000000000000", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- handleDeleteProject ---

func TestDeleteProject_success(t *testing.T) {
	p, err := storage.CreateProject(context.Background(), testPool, "to-del-proj", "To Delete")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/projects/%s", p.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteProject_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete,
		"/api/projects/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- handleListAllTransactions ---

func TestListAllTransactions_success(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Transactions []*storage.Transaction `json:"transactions"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Transactions == nil {
		t.Error("expected non-nil transactions")
	}
}

func TestListAllTransactions_withFilters(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/transactions?op=http.server&status=ok", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with filters, got %d", rec.Code)
	}
}

// --- handleGetTransactionGlobal ---

func TestGetTransactionGlobal_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/transactions/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- handleGetSpansGlobal ---

func TestGetSpansGlobal_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/transactions/00000000-0000-0000-0000-000000000000/spans", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestGetSpansGlobal_returnsStartTimestampAndData(t *testing.T) {
	truncateTransactions(t)
	tx := seedTransactionRow(t, "/api/span-fields", 100)

	start := tx.StartTimestamp.Add(5 * time.Millisecond)
	end := start.Add(20 * time.Millisecond)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO spans (transaction_id, span_id, op, start_timestamp, timestamp, duration_ms, status, data)
		VALUES ($1, 'sp-fields', 'db.query', $2, $3, 20, 'ok', '{"db.system":"postgres"}')
	`, tx.ID, start, end); err != nil {
		t.Fatalf("insert span: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/"+tx.ID+"/spans", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var spans []struct {
		SpanID           string          `json:"span_id"`
		StartTimestampMs int64           `json:"start_timestamp_ms"`
		Data             json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&spans); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	s := spans[0]
	if s.StartTimestampMs == 0 {
		t.Error("expected non-zero start_timestamp_ms")
	}
	wantMs := start.UnixMilli()
	if s.StartTimestampMs != wantMs {
		t.Errorf("start_timestamp_ms: got %d, want %d", s.StartTimestampMs, wantMs)
	}
	if len(s.Data) == 0 || string(s.Data) == "null" {
		t.Errorf("expected non-empty data, got %q", s.Data)
	}
}

func TestGetSpansGlobal_nullDataReturnsEmptyObject(t *testing.T) {
	truncateTransactions(t)
	tx := seedTransactionRow(t, "/api/span-nulldata", 50)

	start := tx.StartTimestamp.Add(2 * time.Millisecond)
	end := start.Add(5 * time.Millisecond)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO spans (transaction_id, span_id, op, start_timestamp, timestamp, duration_ms, status)
		VALUES ($1, 'sp-null', 'http.client', $2, $3, 5, 'ok')
	`, tx.ID, start, end); err != nil {
		t.Fatalf("insert span: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/"+tx.ID+"/spans", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var spans []struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&spans); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if string(spans[0].Data) == "null" || len(spans[0].Data) == 0 {
		t.Errorf("expected '{}' for null data, got %q", spans[0].Data)
	}
}

// --- handleListAllTokens ---

func TestListAllTokens_empty(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE api_tokens")

	req := httptest.NewRequest(http.MethodGet, "/api/tokens", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var tokens []*storage.APIToken
	if err := json.NewDecoder(rec.Body).Decode(&tokens); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tokens) != 0 {
		t.Errorf("expected 0 tokens after truncate, got %d", len(tokens))
	}
}

// --- handleCreateTokenGlobal ---

func TestCreateTokenGlobal_success(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE api_tokens")

	body, _ := json.Marshal(map[string]string{
		"name":       "ci-token",
		"project_id": testProject.ID,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/tokens", bytes.NewBuffer(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Token string            `json:"token"`
		Meta  *storage.APIToken `json:"meta"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Token == "" {
		t.Error("expected non-empty token plaintext")
	}
	if resp.Meta == nil || resp.Meta.Name != "ci-token" {
		t.Errorf("meta.name: got %v", resp.Meta)
	}
}

func TestCreateTokenGlobal_badBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/tokens",
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestCreateTokenGlobal_missingProjectID(t *testing.T) {
	body := bytes.NewBufferString(`{"name":"ci-token"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tokens", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing project_id, got %d", rec.Code)
	}
}

// --- handleDeleteTokenGlobal ---

func TestDeleteTokenGlobal_found(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE api_tokens")

	tok, _, _ := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "del-tok", false)

	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/tokens/%s", tok.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteTokenGlobal_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete,
		"/api/tokens/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- handleUpdateTokenGlobal ---

func TestUpdateTokenGlobal_success(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE api_tokens")

	tok, _, _ := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "old-name", false)

	body, _ := json.Marshal(map[string]any{
		"name":       "new-name",
		"project_id": testProject.ID,
		"writable":   true,
	})
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/tokens/%s", tok.ID), bytes.NewBuffer(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated storage.APIToken
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Name != "new-name" {
		t.Errorf("name: got %q, want %q", updated.Name, "new-name")
	}
	if !updated.Writable {
		t.Error("expected Writable=true after update")
	}
}

func TestUpdateTokenGlobal_badBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/tokens/some-id", bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestUpdateTokenGlobal_missingName(t *testing.T) {
	body := bytes.NewBufferString(fmt.Sprintf(`{"project_id":%q,"writable":false}`, testProject.ID))
	req := httptest.NewRequest(http.MethodPatch, "/api/tokens/some-id", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d", rec.Code)
	}
}

func TestUpdateTokenGlobal_missingProjectID(t *testing.T) {
	body := bytes.NewBufferString(`{"name":"x","writable":false}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/tokens/some-id", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing project_id, got %d", rec.Code)
	}
}

func TestUpdateTokenGlobal_notFound(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"name":       "x",
		"project_id": testProject.ID,
		"writable":   false,
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/tokens/00000000-0000-0000-0000-000000000000", bytes.NewBuffer(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- handleTestAlertRule ---

func TestTestAlertRule_noEvaluator(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE alert_rules CASCADE")
	url := "https://x.example.com/wh"
	rule, _ := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "test rule", Enabled: true,
		Trigger: "new_issue", Channel: "webhook", WebhookURL: &url, CooldownMins: 0,
	})

	// Router created without an evaluator → 503.
	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, nil)
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/alert-rules/%s/test", rule.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 without evaluator, got %d", rec.Code)
	}
}

func TestTestAlertRule_notFound(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE alert_rules CASCADE")

	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, nil)
	req := httptest.NewRequest(http.MethodPost,
		"/api/alert-rules/00000000-0000-0000-0000-000000000000/test", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- handleGetInstanceHealth ---

func TestGetInstanceHealth_success(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/instance/health", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{
		"db_size_bytes", "events_total", "tx_total", "logs_total",
		"events_24h", "tx_24h", "logs_24h", "retention_days",
		"events_size_bytes", "tx_size_bytes", "logs_size_bytes",
	} {
		if _, ok := resp[key]; !ok {
			t.Errorf("expected key %q in response", key)
		}
	}
}

func TestGetInstanceHealth_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/instance/health", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestGetInstanceHealth_forbidden(t *testing.T) {
	cookie := makeReadOnlyUser(t, "health-noperm@example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/instance/health", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for user without manage_projects, got %d", rec.Code)
	}
}

// --- handleGetIssueTrace ---

func TestGetIssueTrace_noEvent(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-trace-noev", "Trace No Event", "error", "error", "", "", time.Now().UTC())

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/issues/%s/trace", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != "null" {
		t.Errorf("expected null body, got %s", rec.Body.String())
	}
}

func TestGetIssueTrace_eventNoTrace(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-trace-notrace", "Trace No TraceID", "error", "error", "", "", time.Now().UTC())

	testPool.Exec(context.Background(), `
		INSERT INTO events (project_id, issue_id, payload, fingerprint, timestamp, received_at)
		VALUES ($1, $2, '{}', 'fp-trace-notrace', NOW(), NOW())
	`, testProject.ID, iss.ID)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/issues/%s/trace", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != "null" {
		t.Errorf("expected null body (no trace_id on event), got %s", rec.Body.String())
	}
}

func TestGetIssueTrace_withLinkedTransaction(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues, transactions CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-trace-linked", "Trace Linked", "error", "error", "", "", time.Now().UTC())

	const traceID = "api-test-trace-linked-xyz"
	testPool.Exec(context.Background(), `
		INSERT INTO events (project_id, issue_id, trace_id, payload, fingerprint, timestamp, received_at)
		VALUES ($1, $2, $3, '{}', 'fp-trace-linked', NOW(), NOW())
	`, testProject.ID, iss.ID, traceID)

	start := time.Now().UTC()
	var txID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, trace_id)
		VALUES ($1, '/api/linked', 'http.server', 'ok', 150, $2, $3, $4)
		RETURNING id
	`, testProject.ID, start, start.Add(150*time.Millisecond), traceID).Scan(&txID); err != nil {
		t.Fatalf("seed transaction: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/issues/%s/trace", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["id"] != txID {
		t.Errorf("id: got %v, want %q", got["id"], txID)
	}
}

func TestGetIssueTrace_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues/00000000-0000-0000-0000-000000000000/trace", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- handleListTransactionSummaries ---

func TestListTransactionSummaries_success(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/summaries", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var summaries []json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&summaries); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestListTransactionSummaries_withHours(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/summaries?hours=48", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with hours param, got %d", rec.Code)
	}
}

// --- handleTransactionTimeseries ---

func TestTransactionTimeseries_success(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/timeseries", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- handleGetTransactionErrors ---

func TestGetTransactionErrors_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/transactions/00000000-0000-0000-0000-000000000000/errors", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestGetTransactionErrors_found(t *testing.T) {
	truncateTransactions(t)
	tx := seedTransactionRow(t, "/api/tx-errors", 50)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/transactions/%s/errors", tx.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var errs []json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&errs); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// --- handleSpanSamples ---

func TestSpanSamples_success(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/spans/samples", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSpanSamples_withFilters(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/spans/samples?op=db.query&hours=48", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with filters, got %d", rec.Code)
	}
}

// --- handleGetWebVitals ---

func TestGetWebVitals_success(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/vitals", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetWebVitals_withHours(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/vitals?hours=72", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with hours param, got %d", rec.Code)
	}
}

// --- handleGetWebVitalsPages ---

func TestGetWebVitalsPages_success(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/vitals/pages", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var pages []json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&pages); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// --- handleUpdateProjectPrivacy ---

func TestUpdateProjectPrivacy_success(t *testing.T) {
	p, err := storage.CreateProject(context.Background(), testPool, "privacy-proj", "Privacy Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", p.ID)
	})

	body := bytes.NewBufferString(`{"scrub_fields":["password","token"],"scrub_patterns":[]}`)
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/projects/%s/privacy", p.ID), body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got storage.Project
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != p.ID {
		t.Errorf("project ID: got %q, want %q", got.ID, p.ID)
	}
}

func TestUpdateProjectPrivacy_badBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/projects/%s/privacy", testProject.ID),
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestUpdateProjectPrivacy_notFound(t *testing.T) {
	body := bytes.NewBufferString(`{"scrub_fields":[],"scrub_patterns":[]}`)
	req := httptest.NewRequest(http.MethodPatch,
		"/api/projects/00000000-0000-0000-0000-000000000000/privacy", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestUpdateProjectPrivacy_forbidden(t *testing.T) {
	roCookie := makeReadOnlyUser(t, "ro-privacy@example.com")
	body := bytes.NewBufferString(`{"scrub_fields":[],"scrub_patterns":[]}`)
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/projects/%s/privacy", testProject.ID), body)
	req.AddCookie(roCookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for user without manage_projects, got %d", rec.Code)
	}
}

// --- handleExportIssues ---

func TestExportIssues_csv(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues/export", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("expected text/csv content-type, got %q", ct)
	}
	if rec.Header().Get("Content-Disposition") == "" {
		t.Error("expected Content-Disposition header")
	}
}

func TestExportIssues_json(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues/export?format=json", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var issues []json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&issues); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestExportIssues_invalidFormat(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues/export?format=xml", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid format, got %d", rec.Code)
	}
}

func TestExportIssues_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues/export", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleGetProjectStats ---

func TestGetProjectStats_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/projects/stats", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestGetProjectStats_success(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/projects/stats", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var stats []*storage.ProjectIssueCount
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var found bool
	for _, s := range stats {
		if s.ProjectID == testProject.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("testProject.ID %q not found in stats response", testProject.ID)
	}
}

func TestGetProjectStats_returnsOpenIssueCounts(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE issues CASCADE")

	ts := time.Now().UTC()
	storage.UpsertIssue(context.Background(), testPool, testProject.ID, "fp-stats-1", "Stats Error 1", "error", "error", "", "", ts)
	storage.UpsertIssue(context.Background(), testPool, testProject.ID, "fp-stats-2", "Stats Error 2", "error", "error", "", "", ts)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/stats", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var stats []*storage.ProjectIssueCount
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var found *storage.ProjectIssueCount
	for _, s := range stats {
		if s.ProjectID == testProject.ID {
			found = s
			break
		}
	}
	if found == nil {
		t.Fatalf("testProject.ID %q not found in stats response", testProject.ID)
	}
	if found.OpenIssues != 2 {
		t.Errorf("expected 2 open issues, got %d", found.OpenIssues)
	}
}

func TestGetProjectStats_scopedByBearerToken(t *testing.T) {
	tok, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "stats-scope-token", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM api_tokens WHERE id = $1", tok.ID)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/projects/stats", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var stats []*storage.ProjectIssueCount
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, s := range stats {
		if s.ProjectID != testProject.ID {
			t.Errorf("bearer-scoped response includes unexpected project %q", s.ProjectID)
		}
	}
}
