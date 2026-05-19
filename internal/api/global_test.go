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
	for _, key := range []string{"db_size_bytes", "events_total", "tx_total", "logs_total", "events_24h", "tx_24h", "logs_24h", "retention_days"} {
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
