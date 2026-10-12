package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func tokenHandler() http.Handler {
	return api.NewRouter(api.Config{Pool: testPool, Events: ingest.NewBuffer(1)})
}

func truncateTokens(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE api_tokens CASCADE"); err != nil {
		t.Fatalf("truncate api_tokens: %v", err)
	}
}

func TestBearerTokenAuth_allowsDataAccess(t *testing.T) {
	truncateTokens(t)
	truncateIssues(t)

	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "bearer-test", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/issues", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with valid bearer token, got %d", rec.Code)
	}
}

func TestBearerTokenAuth_invalidToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/issues", nil)
	req.Header.Set("Authorization", "Bearer tindra_notavalidtoken00000000000000000000000000000000000000000000000")
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with invalid bearer token, got %d", rec.Code)
	}
}

func TestBearerTokenAuth_wrongProject(t *testing.T) {
	truncateTokens(t)

	// Create a second project and a token for it
	other, err := storage.CreateProject(context.Background(), testPool, "other-project", "Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	_, plaintext, _ := storage.CreateAPIToken(context.Background(), testPool, other.ID, "other-tok", false)

	// Use that token to access test-project - must be forbidden
	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/issues", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 when token project != requested project, got %d", rec.Code)
	}
}

func createGlobalToken(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/tokens", bytes.NewBufferString(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)
	return rec
}

func TestCreateToken_writableFlag(t *testing.T) {
	truncateTokens(t)
	for body, want := range map[string]bool{
		`{"name":"rw-token","writable":true,"project_id":"` + testProject.ID + `"}`: true,
		`{"name":"ro-token","project_id":"` + testProject.ID + `"}`:                 false,
	} {
		rec := createGlobalToken(t, body)
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
		if resp.Token == "" || resp.Meta.Writable != want {
			t.Errorf("%s: token=%q writable=%v, want writable=%v", body, resp.Token, resp.Meta.Writable, want)
		}
	}
}

func TestCreateToken_unknownProject(t *testing.T) {
	for _, projectID := range []string{"00000000-0000-0000-0000-000000000000", "not-a-uuid"} {
		rec := createGlobalToken(t, `{"name":"x","project_id":"`+projectID+`"}`)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "unknown project") {
			t.Errorf("project %q: got %d %q, want 400 unknown project", projectID, rec.Code, rec.Body.String())
		}
	}
}

func TestListTokens_writableFieldPresent(t *testing.T) {
	truncateTokens(t)
	storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "rw", true)
	storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "ro", false)

	req := httptest.NewRequest(http.MethodGet, "/api/tokens", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var tokens []*storage.APIToken
	if err := json.NewDecoder(rec.Body).Decode(&tokens); err != nil {
		t.Fatalf("decode: %v", err)
	}
	writable := 0
	for _, tok := range tokens {
		if tok.Writable {
			writable++
		}
	}
	if len(tokens) != 2 || writable != 1 {
		t.Errorf("got %d tokens with %d writable, want 2 with 1 writable", len(tokens), writable)
	}
}

// The project-scoped token routes were removed; /api/tokens replaces them.
func TestProjectTokenRoutesAreGone(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/projects/test-project/tokens"},
		{http.MethodPost, "/api/projects/test-project/tokens"},
		{http.MethodDelete, "/api/projects/test-project/tokens/00000000-0000-0000-0000-000000000000"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(`{"name":"x"}`))
		req.AddCookie(authCookie())
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		tokenHandler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: got %d, want the route to be gone", tc.method, tc.path, rec.Code)
		}
		if rec.Code == http.StatusNotFound && !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s %s: 404 should be JSON, got %q", tc.method, tc.path, rec.Header().Get("Content-Type"))
		}
	}
}

func TestUnknownAPIPathIsJSONNotFound(t *testing.T) {
	for _, path := range []string{"/api/does-not-exist", "/api/projects/test-project/nothing-here"} {
		rec := httptest.NewRecorder()
		tokenHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s: got %d %q, want a JSON 404", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}
