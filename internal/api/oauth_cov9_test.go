package api

// oauth_cov9_test.go — additional tests for oauth.go handler branches
// that require white-box (package api) access to the unexported helpers.
//
// Gaps covered:
//   - handleOAuthCallback: p.Exchange returns error -> 401 "authentication failed"
//   - handleListProviders: response body contains exact provider names in order
//   - handleListProviders: empty providers list returns {"providers":[]} not null
//   - handleOAuthRedirect: Location header contains the state token returned by AuthCodeURL

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/storage"
)

// mockProviderExchangeErr is a mock OAuth provider whose Exchange always returns
// an error. Used to exercise the 401 path in handleOAuthCallback.
type mockProviderExchangeErr struct{ name string }

func (m mockProviderExchangeErr) Name() string { return m.name }
func (m mockProviderExchangeErr) AuthCodeURL(state, _ string) string {
	return "/fake-redirect?state=" + state
}
func (m mockProviderExchangeErr) Exchange(_ context.Context, _, _ string) (string, string, error) {
	return "", "", errors.New("token exchange rejected by upstream provider")
}

// ---------------------------------------------------------------------------
// handleOAuthCallback: Exchange error returns 401 "authentication failed"
// (oauth.go:336-341)
// ---------------------------------------------------------------------------

func TestHandleOAuthCallback_exchangeErrorReturns401(t *testing.T) {
	pool := oauthDB(t)
	ctx := context.Background()

	// Insert a valid OAuth state so ConsumeOAuthState succeeds and we reach Exchange.
	stateToken, err := storage.CreateOAuthState(ctx, pool, "google", "pkce-cov9-exchange-err")
	if err != nil {
		t.Fatalf("create oauth state: %v", err)
	}

	h := routerWithSSOAndPool(pool, mockProviderExchangeErr{name: "google"})

	req := httptest.NewRequest(http.MethodGet,
		"/api/auth/google/callback?state="+stateToken+"&code=badcode", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 when Exchange fails, got %d: %s",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "authentication failed") {
		t.Errorf("expected 'authentication failed' in body, got: %s", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleListProviders: JSON body contains exact provider names in order
// (oauth.go:275-279)
// ---------------------------------------------------------------------------

func TestHandleListProviders_bodyContainsExactNames(t *testing.T) {
	h := routerWithSSO(mockProvider{name: "google"}, mockProvider{name: "github"})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	rawProviders, ok := resp["providers"]
	if !ok {
		t.Fatal("expected 'providers' key in response")
	}
	providers, ok := rawProviders.([]any)
	if !ok {
		t.Fatalf("expected providers to be array, got %T", rawProviders)
	}
	if len(providers) != 2 {
		t.Fatalf("expected 2 providers, got %d: %v", len(providers), providers)
	}
	if providers[0].(string) != "google" {
		t.Errorf("providers[0]: got %q, want google", providers[0])
	}
	if providers[1].(string) != "github" {
		t.Errorf("providers[1]: got %q, want github", providers[1])
	}
}

// ---------------------------------------------------------------------------
// handleListProviders: empty list returns {"providers":[]} not null
// ---------------------------------------------------------------------------

func TestHandleListProviders_emptyListNotNull(t *testing.T) {
	h := routerWithSSO() // no providers

	req := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	providers, ok := resp["providers"].([]any)
	if !ok {
		t.Fatalf("expected providers to be array, got %T", resp["providers"])
	}
	if len(providers) != 0 {
		t.Errorf("expected empty providers array, got %d items", len(providers))
	}
}

// ---------------------------------------------------------------------------
// handleOAuthRedirect: Location header contains state token from AuthCodeURL
// (oauth.go:307: http.Redirect uses p.AuthCodeURL(state, verifier))
// ---------------------------------------------------------------------------

func TestHandleOAuthRedirect_locationContainsState(t *testing.T) {
	pool := oauthDB(t)
	h := routerWithSSOAndPool(pool, mockProvider{name: "google"})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/redirect", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if loc == "" {
		t.Fatal("expected non-empty Location header")
	}
	// mockProvider.AuthCodeURL returns "/fake-redirect?state=<stateToken>".
	if !strings.HasPrefix(loc, "/fake-redirect?state=") {
		t.Errorf("Location header: expected '/fake-redirect?state=...' prefix, got %q", loc)
	}
	// State token must be non-empty.
	parts := strings.SplitN(loc, "=", 2)
	if len(parts) < 2 || parts[1] == "" {
		t.Errorf("Location header missing state token: %q", loc)
	}
}
