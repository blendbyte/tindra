package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blendbyte/tindra/internal/ingest"
)

// mockProvider satisfies oauthProvider without any real OAuth infrastructure.
type mockProvider struct{ name string }

func (m mockProvider) Name() string { return m.name }
func (m mockProvider) AuthCodeURL(state, _ string) string {
	return "/fake-redirect?state=" + state
}
func (m mockProvider) Exchange(_ context.Context, _, _ string) (string, string, error) {
	return "user@example.com", "sub123", nil
}

// routerWithSSO builds a handler with one or more mock OAuth providers configured.
// The pool is nil because the 403 short-circuits before any DB call.
func routerWithSSO(providers ...oauthProvider) http.Handler {
	return NewRouter(nil, ingest.NewBuffer(1), nil, nil, nil, providers, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, nil)
}

func TestHandleLogin_ssoEnabled_rejectsPassword(t *testing.T) {
	h := routerWithSSO(mockProvider{name: "google"})

	body := bytes.NewBufferString(`{"email":"test@example.com","password":"testpassword"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 when SSO enabled, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleLogin_ssoEnabled_multipleProviders(t *testing.T) {
	h := routerWithSSO(mockProvider{name: "google"}, mockProvider{name: "github"})

	body := bytes.NewBufferString(`{"email":"test@example.com","password":"testpassword"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 with multiple SSO providers, got %d", rec.Code)
	}
}

func TestHandleListProviders_withSSO(t *testing.T) {
	h := routerWithSSO(mockProvider{name: "google"}, mockProvider{name: "github"})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if body == "" {
		t.Error("expected non-empty body")
	}
}

func TestHandleListProviders_noSSO(t *testing.T) {
	h := routerWithSSO() // no providers

	req := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}
