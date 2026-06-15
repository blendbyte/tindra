package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

// bearerToken creates a fresh API token for projectID and returns the plaintext.
func bearerToken(t *testing.T, projectID string) string {
	t.Helper()
	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, projectID, "scope-test-tok", false)
	if err != nil {
		t.Fatalf("create api token: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM api_tokens WHERE project_id = $1 AND name = 'scope-test-tok'", projectID)
	})
	return plaintext
}

// --- bearerProjectIDs: list endpoints restrict to the token's project ---

func TestBearerProjectIDs_restrictsIssueList(t *testing.T) {
	truncateIssues(t)
	truncateTokens(t)

	// Create a second project with its own issue.
	other, err := storage.CreateProject(context.Background(), testPool, "scope-other", "Scope Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", other.ID)
	})

	storage.UpsertIssue(context.Background(), testPool, testProject.ID, "fp-scope-a", "Scope A", "error", "error", "", "", time.Now())
	storage.UpsertIssue(context.Background(), testPool, other.ID, "fp-scope-b", "Scope B", "error", "error", "", "", time.Now())

	tok := bearerToken(t, testProject.ID)

	// GET /api/issues with no project_id filter: bearer token must restrict to its project.
	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBearerProjectIDs_sessionAuthPassesProvidedIDs(t *testing.T) {
	truncateIssues(t)

	storage.UpsertIssue(context.Background(), testPool, testProject.ID, "fp-scope-sess", "Sess", "error", "error", "", "", time.Now())

	// Session auth with an explicit project_id query param: ids must not be overridden.
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues?project_id=%s", testProject.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- enforceTokenProject: single-resource endpoints block cross-project reads ---

func TestEnforceTokenProject_wrongProject(t *testing.T) {
	truncateIssues(t)
	truncateTokens(t)

	// Create an issue in testProject.
	iss, _, _, err := storage.UpsertIssue(context.Background(), testPool, testProject.ID, "fp-enforce-tp", "Enforce", "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	// Create a second project and a token scoped to it.
	other, err := storage.CreateProject(context.Background(), testPool, "enforce-other", "Enforce Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	// Try to read the testProject issue with the other-project token.
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s", iss.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("enforceTokenProject: expected 404 for cross-project read, got %d", rec.Code)
	}
}

func TestEnforceTokenProject_correctProject(t *testing.T) {
	truncateIssues(t)
	truncateTokens(t)

	iss, _, _, err := storage.UpsertIssue(context.Background(), testPool, testProject.ID, "fp-enforce-ok", "Enforce OK", "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	tok := bearerToken(t, testProject.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s", iss.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("enforceTokenProject: expected 200 for same-project read, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEnforceTokenProject_sessionAuthUnaffected(t *testing.T) {
	truncateIssues(t)

	iss, _, _, err := storage.UpsertIssue(context.Background(), testPool, testProject.ID, "fp-enforce-sess", "Enforce Session", "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	// Session auth has no token project - enforceTokenProject must return true.
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for session auth, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- enforceIssueProject: sub-resource endpoints (events, tags, history, comments) ---

func TestEnforceIssueProject_wrongProject_events(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	truncateTokens(t)

	ts := time.Now().UTC()
	iss, _, _, err := storage.UpsertIssue(context.Background(), testPool, testProject.ID, "fp-eip-ev", "EIP Ev", "error", "error", "", "", ts)
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}
	// Insert an event so handleGetLatestEventGlobal doesn't 404 before the scope check.
	testPool.Exec(context.Background(), `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, $2, $2, '{"level":"error"}'::jsonb, 'fp-eip-ev', $3)
	`, testProject.ID, ts, iss.ID)

	other, err := storage.CreateProject(context.Background(), testPool, "eip-other", "EIP Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/events/latest", iss.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("enforceIssueProject: expected 404 for cross-project sub-resource, got %d", rec.Code)
	}
}

func TestEnforceIssueProject_correctProject_events(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	truncateTokens(t)

	ts := time.Now().UTC()
	iss, _, _, err := storage.UpsertIssue(context.Background(), testPool, testProject.ID, "fp-eip-ok", "EIP OK", "error", "error", "", "", ts)
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}
	testPool.Exec(context.Background(), `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, $2, $2, '{"level":"error"}'::jsonb, 'fp-eip-ok', $3)
	`, testProject.ID, ts, iss.ID)

	tok := bearerToken(t, testProject.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/events/latest", iss.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("enforceIssueProject: expected 200 for same-project sub-resource, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEnforceIssueProject_sessionAuth_unaffected(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")

	ts := time.Now().UTC()
	iss, _, _, err := storage.UpsertIssue(context.Background(), testPool, testProject.ID, "fp-eip-sess", "EIP Sess", "error", "error", "", "", ts)
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}
	testPool.Exec(context.Background(), `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, $2, $2, '{"level":"error"}'::jsonb, 'fp-eip-sess', $3)
	`, testProject.ID, ts, iss.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/events/latest", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for session auth on sub-resource, got %d: %s", rec.Code, rec.Body.String())
	}
}

// enforceIssueProject also returns 404 when the issue doesn't exist at all.
func TestEnforceIssueProject_issueNotFound(t *testing.T) {
	truncateTokens(t)
	tok := bearerToken(t, testProject.ID)

	req := httptest.NewRequest(http.MethodGet,
		"/api/issues/00000000-0000-0000-0000-000000000000/events/latest", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown issue, got %d", rec.Code)
	}
}

// --- enforceIssueProject via globalHandler /api/issues/{id}/events ---

func TestEnforceIssueProject_wrongProject_listEvents(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	truncateTokens(t)

	iss, _, _, err := storage.UpsertIssue(context.Background(), testPool, testProject.ID, "fp-eip-lev-w", "EIP List Ev Wrong", "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	other, err := storage.CreateProject(context.Background(), testPool, "eip-lev-other", "EIP List Ev Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/events", iss.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for cross-project list events, got %d", rec.Code)
	}
}

func TestEnforceIssueProject_correctProject_listEvents(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	truncateTokens(t)

	iss, _, _, err := storage.UpsertIssue(context.Background(), testPool, testProject.ID, "fp-eip-lev-ok", "EIP List Ev OK", "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}
	tok := bearerToken(t, testProject.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/events", iss.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for same-project list events, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- handleGetIssueGlobal bearer token scope check ---

func TestGetIssueGlobal_bearerTokenWrongProject(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	truncateTokens(t)

	iss, _, _, err := storage.UpsertIssue(context.Background(), testPool, testProject.ID, "fp-gi-scope-w", "GI Scope Wrong", "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	other, err := storage.CreateProject(context.Background(), testPool, "gi-scope-other", "GI Scope Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s", iss.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong-project bearer token, got %d", rec.Code)
	}
}

func TestEnforceIssueProject_wrongProject_tags(t *testing.T) {
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-eip-tags", "EIP Tags", "error", "error", "", "", time.Now().UTC())

	other, _ := storage.CreateProject(context.Background(), testPool, "eip-tags-other", "EIP Tags Other")
	t.Cleanup(func() { testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID) })
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/tags", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong-project bearer on tags, got %d", rec.Code)
	}
}

func TestGetIssueHistogram_wrongProjectBearer(t *testing.T) {
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-hist-bearer", "Hist Bearer", "error", "error", "", "", time.Now().UTC())
	other, _ := storage.CreateProject(context.Background(), testPool, "hist-scope-other", "Hist Scope")
	t.Cleanup(func() { testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID) })
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/events/histogram", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong-project bearer on histogram, got %d", rec.Code)
	}
}
