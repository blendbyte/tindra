package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func tokenHandler() http.Handler {
	return api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
}

func truncateTokens(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE api_tokens CASCADE"); err != nil {
		t.Fatalf("truncate api_tokens: %v", err)
	}
}

func TestCreateToken_unauthenticated(t *testing.T) {
	body := bytes.NewBufferString(`{"name":"ci"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/tokens", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestCreateToken_tokenAuthRejected(t *testing.T) {
	// Bootstrap a token to test with
	truncateTokens(t)
	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "bootstrap", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	// Attempt to create another token using Bearer auth - must be rejected
	body := bytes.NewBufferString(`{"name":"new"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/tokens", body)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 (token mgmt requires session), got %d", rec.Code)
	}
}

func TestCreateToken_success(t *testing.T) {
	truncateTokens(t)

	body := bytes.NewBufferString(`{"name":"CI pipeline"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/tokens", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		storage.APIToken
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID == "" {
		t.Error("expected non-empty ID")
	}
	if resp.Name != "CI pipeline" {
		t.Errorf("name: got %q", resp.Name)
	}
	if len(resp.Token) != 71 {
		t.Errorf("token length: got %d, want 71", len(resp.Token))
	}
}

func TestCreateToken_missingName(t *testing.T) {
	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/tokens", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestCreateToken_unknownProject(t *testing.T) {
	body := bytes.NewBufferString(`{"name":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/no-such-project/tokens", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestListTokens_empty(t *testing.T) {
	truncateTokens(t)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/tokens", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Tokens []json.RawMessage `json:"tokens"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tokens) != 0 {
		t.Errorf("expected 0, got %d", len(resp.Tokens))
	}
}

func TestListTokens_withData(t *testing.T) {
	truncateTokens(t)

	storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "tok-a", false)
	storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "tok-b", false)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/tokens", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Tokens []*storage.APIToken `json:"tokens"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tokens) != 2 {
		t.Errorf("expected 2, got %d", len(resp.Tokens))
	}
}

func TestDeleteToken_success(t *testing.T) {
	truncateTokens(t)

	tok, _, _ := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "deleteme", false)

	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/projects/test-project/tokens/%s", tok.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}
}

func TestDeleteToken_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete,
		"/api/projects/test-project/tokens/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestListTokens_unknownProject(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/projects/no-such-project/tokens", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown project, got %d", rec.Code)
	}
}

func TestDeleteToken_unknownProject(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete,
		"/api/projects/no-such-project/tokens/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown project, got %d", rec.Code)
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

func TestCreateToken_writable_true(t *testing.T) {
	truncateTokens(t)

	body := bytes.NewBufferString(`{"name":"rw-token","writable":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/tokens", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		storage.APIToken
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Writable {
		t.Error("expected Writable=true in response")
	}
}

func TestCreateToken_writable_omitted_defaults_false(t *testing.T) {
	truncateTokens(t)

	body := bytes.NewBufferString(`{"name":"ro-token"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/tokens", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		storage.APIToken
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Writable {
		t.Error("expected Writable=false when omitted from request")
	}
}

func TestListTokens_writable_field_present(t *testing.T) {
	truncateTokens(t)

	storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "rw", true)
	storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "ro", false)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/tokens", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	tokenHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Tokens []*storage.APIToken `json:"tokens"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tokens) != 2 {
		t.Fatalf("expected 2, got %d", len(resp.Tokens))
	}
	writableCount := 0
	for _, tok := range resp.Tokens {
		if tok.Writable {
			writableCount++
		}
	}
	if writableCount != 1 {
		t.Errorf("expected 1 writable token in list response, got %d", writableCount)
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
