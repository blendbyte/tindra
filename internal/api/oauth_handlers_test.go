package api

// oauth_handlers_test.go — integration tests for handleOAuthRedirect and
// handleOAuthCallback. Uses the white-box (package api) approach so that the
// unexported oauthProvider interface and mockProvider type (defined in
// auth_sso_test.go) are available. Each test that needs a real database creates
// its own pool and project via testutil.SetupDB to avoid coupling to the
// package api_test globals (testPool, testProject) which live in a separate
// test binary.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
)

// ---------------------------------------------------------------------------
// Shared pool for oauth handler tests (created once per test binary run).
// ---------------------------------------------------------------------------

var (
	oauthPool    *pgxpool.Pool
	oauthOnce    sync.Once
	oauthCleanup func()
)

// oauthDB returns a lazily-initialised pool shared across all oauth handler
// tests in this file. The pool is intentionally not torn down between
// individual tests; the process exit cleans it up.
func oauthDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	oauthOnce.Do(func() {
		ctx := context.Background()
		pool, cleanup := testutil.SetupDB(ctx)
		oauthPool = pool
		oauthCleanup = cleanup
	})
	return oauthPool
}

// routerWithSSOAndPool builds a handler that has both real DB access and one
// or more mock OAuth providers configured. This lets us exercise the full
// redirect / callback flow including state storage.
func routerWithSSOAndPool(pool *pgxpool.Pool, providers ...oauthProvider) http.Handler {
	return NewRouter(pool, ingest.NewBuffer(1), nil, nil, nil, providers, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
}

// ---------------------------------------------------------------------------
// handleOAuthRedirect
// ---------------------------------------------------------------------------

func TestHandleOAuthRedirect_unknownProvider(t *testing.T) {
	// Pool is nil — the 404 short-circuits before any DB call.
	h := routerWithSSO(mockProvider{name: "google"})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/nonexistent/redirect", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown provider, got %d", rec.Code)
	}
}

func TestHandleOAuthRedirect_knownProvider(t *testing.T) {
	pool := oauthDB(t)
	h := routerWithSSOAndPool(pool, mockProvider{name: "google"})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/redirect", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Redirect to the mock AuthCodeURL.
	if rec.Code != http.StatusFound {
		t.Errorf("expected 302 redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if loc == "" {
		t.Error("expected non-empty Location header")
	}
}

// ---------------------------------------------------------------------------
// handleOAuthCallback
// ---------------------------------------------------------------------------

func TestHandleOAuthCallback_unknownProvider(t *testing.T) {
	h := routerWithSSO(mockProvider{name: "google"})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/nonexistent/callback?state=x&code=y", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown provider, got %d", rec.Code)
	}
}

func TestHandleOAuthCallback_missingState(t *testing.T) {
	pool := oauthDB(t)
	h := routerWithSSOAndPool(pool, mockProvider{name: "google"})

	// No state query param.
	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?code=somecode", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing state, got %d", rec.Code)
	}
}

func TestHandleOAuthCallback_missingCode(t *testing.T) {
	pool := oauthDB(t)
	h := routerWithSSOAndPool(pool, mockProvider{name: "google"})

	// No code query param.
	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?state=somestate", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing code, got %d", rec.Code)
	}
}

func TestHandleOAuthCallback_invalidState(t *testing.T) {
	pool := oauthDB(t)
	h := routerWithSSOAndPool(pool, mockProvider{name: "google"})

	// Provide a state token that was never inserted — ConsumeOAuthState returns
	// nil, which the handler treats as an invalid/expired state.
	req := httptest.NewRequest(http.MethodGet,
		"/api/auth/google/callback?state=doesnotexist&code=somecode", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid/expired state, got %d", rec.Code)
	}
}

func TestHandleOAuthCallback_providerMismatch(t *testing.T) {
	pool := oauthDB(t)
	ctx := context.Background()

	// Insert a state for "github" but call the "google" callback.
	stateToken, err := storage.CreateOAuthState(ctx, pool, "github", "verifier-abc")
	if err != nil {
		t.Fatalf("create oauth state: %v", err)
	}

	h := routerWithSSOAndPool(pool,
		mockProvider{name: "google"},
		mockProvider{name: "github"},
	)

	req := httptest.NewRequest(http.MethodGet,
		"/api/auth/google/callback?state="+stateToken+"&code=somecode", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// State token was consumed for provider "github" but callback is for
	// "google" — oauthState.Provider != name → 400.
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for provider mismatch, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleOAuthCallback_fullFlow(t *testing.T) {
	pool := oauthDB(t)
	ctx := context.Background()

	// Insert a valid state for "google" directly so we can pass it to the
	// callback. The mockProvider.Exchange always returns "user@example.com" /
	// "sub123" without hitting any real OAuth endpoint.
	stateToken, err := storage.CreateOAuthState(ctx, pool, "google", "pkce-verifier")
	if err != nil {
		t.Fatalf("create oauth state: %v", err)
	}

	h := routerWithSSOAndPool(pool, mockProvider{name: "google"})

	req := httptest.NewRequest(http.MethodGet,
		"/api/auth/google/callback?state="+stateToken+"&code=fakecode", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Successful flow: session cookie set, redirect to "/".
	if rec.Code != http.StatusFound {
		t.Errorf("expected 302 redirect after successful OAuth, got %d: %s",
			rec.Code, rec.Body.String())
	}

	loc := rec.Header().Get("Location")
	if loc != "/" {
		t.Errorf("expected redirect to /, got %q", loc)
	}

	// Verify that a session cookie was set.
	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "tindra_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Error("expected tindra_session cookie to be set after successful OAuth callback")
	}
}
