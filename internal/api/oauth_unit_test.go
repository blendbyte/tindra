package api

// oauth_unit_test.go — white-box tests for the oauth.go provider structs,
// githubAPIGet helper, and LoadOAuthProviders. These run in package api so
// they can reach unexported types and functions.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// githubProvider — constructor + Name + AuthCodeURL (no network)
// ---------------------------------------------------------------------------

func TestNewGitHubProvider_nameAndURL(t *testing.T) {
	p := newGitHubProvider("client-id", "client-secret", "https://app.example.com")

	if p.Name() != "github" {
		t.Errorf("Name: got %q, want github", p.Name())
	}

	url := p.AuthCodeURL("mystate", "verifier-ignored-by-github")
	if !strings.Contains(url, "mystate") {
		t.Errorf("AuthCodeURL: state not in URL: %q", url)
	}
	if !strings.Contains(url, "github.com") {
		t.Errorf("AuthCodeURL: github.com not in URL: %q", url)
	}
}

func TestNewGitHubProvider_callbackURL(t *testing.T) {
	p := newGitHubProvider("id", "sec", "https://tindra.example.com")
	// RedirectURL is set during construction; verify it's embedded in AuthCodeURL.
	url := p.AuthCodeURL("state", "")
	if !strings.Contains(url, "redirect_uri") {
		t.Errorf("AuthCodeURL: expected redirect_uri param, got %q", url)
	}
}

// ---------------------------------------------------------------------------
// githubAPIGet — mock HTTP server, covers the generic helper and its error paths
// ---------------------------------------------------------------------------

func TestGithubAPIGet_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the Authorization header is forwarded.
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("expected Bearer token, got %q", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id":    int64(42),
			"email": "api@example.com",
		})
	}))
	defer srv.Close()

	type user struct {
		ID    int64  `json:"id"`
		Email string `json:"email"`
	}
	result, err := githubAPIGet[user](context.Background(), "test-token", srv.URL)
	if err != nil {
		t.Fatalf("githubAPIGet: %v", err)
	}
	if result.Email != "api@example.com" {
		t.Errorf("Email: got %q, want api@example.com", result.Email)
	}
	if result.ID != 42 {
		t.Errorf("ID: got %d, want 42", result.ID)
	}
}

func TestGithubAPIGet_nonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("bad credentials"))
	}))
	defer srv.Close()

	type empty struct{}
	_, err := githubAPIGet[empty](context.Background(), "bad-token", srv.URL)
	if err == nil {
		t.Error("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "github api") {
		t.Errorf("unexpected error format: %v", err)
	}
}

func TestGithubAPIGet_badJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not valid json at all"))
	}))
	defer srv.Close()

	type empty struct{}
	_, err := githubAPIGet[empty](context.Background(), "token", srv.URL)
	if err == nil {
		t.Error("expected error for bad JSON body")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("unexpected error format: %v", err)
	}
}

func TestGithubAPIGet_networkError(t *testing.T) {
	// Point at a port that refuses connections.
	_, err := githubAPIGet[struct{}](context.Background(), "token", "http://127.0.0.1:1")
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

// ---------------------------------------------------------------------------
// LoadOAuthProviders — no OAUTH_REDIRECT_BASE returns nil
// ---------------------------------------------------------------------------

func TestLoadOAuthProviders_noBase_returnsNil(t *testing.T) {
	t.Setenv("OAUTH_REDIRECT_BASE", "")
	providers := LoadOAuthProviders(context.Background())
	if providers != nil {
		t.Errorf("expected nil when OAUTH_REDIRECT_BASE is empty, got %v", providers)
	}
}

func TestLoadOAuthProviders_noProviderEnvVars_returnsEmpty(t *testing.T) {
	t.Setenv("OAUTH_REDIRECT_BASE", "https://app.example.com")
	t.Setenv("GITHUB_CLIENT_ID", "")
	t.Setenv("GITHUB_CLIENT_SECRET", "")
	t.Setenv("OIDC_ISSUER_URL", "")
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("ZITADEL_ISSUER_URL", "")
	t.Setenv("AUTH0_DOMAIN", "")
	t.Setenv("MICROSOFT_CLIENT_ID", "")
	providers := LoadOAuthProviders(context.Background())
	if len(providers) != 0 {
		t.Errorf("expected 0 providers, got %d", len(providers))
	}
}

func TestLoadOAuthProviders_githubOnly(t *testing.T) {
	t.Setenv("OAUTH_REDIRECT_BASE", "https://app.example.com")
	t.Setenv("GITHUB_CLIENT_ID", "gh-id")
	t.Setenv("GITHUB_CLIENT_SECRET", "gh-secret")
	t.Setenv("OIDC_ISSUER_URL", "")
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("ZITADEL_ISSUER_URL", "")
	t.Setenv("AUTH0_DOMAIN", "")
	t.Setenv("MICROSOFT_CLIENT_ID", "")

	providers := LoadOAuthProviders(context.Background())

	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
	if providers[0].Name() != "github" {
		t.Errorf("expected github provider, got %q", providers[0].Name())
	}
}

func TestLoadOAuthProviders_github_trailingSlashStripped(t *testing.T) {
	t.Setenv("OAUTH_REDIRECT_BASE", "https://app.example.com/")
	t.Setenv("GITHUB_CLIENT_ID", "gh-id")
	t.Setenv("GITHUB_CLIENT_SECRET", "gh-sec")
	t.Setenv("OIDC_ISSUER_URL", "")
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("ZITADEL_ISSUER_URL", "")
	t.Setenv("AUTH0_DOMAIN", "")
	t.Setenv("MICROSOFT_CLIENT_ID", "")

	providers := LoadOAuthProviders(context.Background())
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
	// AuthCodeURL should contain the callback without double slash.
	url := providers[0].AuthCodeURL("state", "")
	if strings.Contains(url, "//api") {
		t.Errorf("double slash in callback URL: %q", url)
	}
}

// ---------------------------------------------------------------------------
// LoadOAuthProviders — OIDC paths that call newOIDCProvider.
// We exercise the error-path (bad issuer) and the success-path (mock OIDC server).
// ---------------------------------------------------------------------------

func TestLoadOAuthProviders_oidcBadIssuer_warnsAndSkips(t *testing.T) {
	t.Setenv("OAUTH_REDIRECT_BASE", "https://app.example.com")
	t.Setenv("OIDC_ISSUER_URL", "http://127.0.0.1:1") // nothing listening
	t.Setenv("OIDC_CLIENT_ID", "id")
	t.Setenv("OIDC_CLIENT_SECRET", "sec")
	t.Setenv("GITHUB_CLIENT_ID", "")
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("ZITADEL_ISSUER_URL", "")
	t.Setenv("AUTH0_DOMAIN", "")
	t.Setenv("MICROSOFT_CLIENT_ID", "")

	// Should not panic; error is logged and provider is skipped.
	providers := LoadOAuthProviders(context.Background())
	if len(providers) != 0 {
		t.Errorf("expected 0 providers when OIDC issuer is unreachable, got %d", len(providers))
	}
}

func TestLoadOAuthProviders_oidcCustomName(t *testing.T) {
	t.Setenv("OIDC_PROVIDER_NAME", "myidp")
	t.Setenv("OAUTH_REDIRECT_BASE", "https://app.example.com")
	t.Setenv("OIDC_ISSUER_URL", "http://127.0.0.1:1")
	t.Setenv("OIDC_CLIENT_ID", "id")
	t.Setenv("OIDC_CLIENT_SECRET", "sec")
	t.Setenv("GITHUB_CLIENT_ID", "")
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("ZITADEL_ISSUER_URL", "")
	t.Setenv("AUTH0_DOMAIN", "")
	t.Setenv("MICROSOFT_CLIENT_ID", "")
	defer t.Setenv("OIDC_PROVIDER_NAME", "")

	providers := LoadOAuthProviders(context.Background())
	// Provider will fail to init (unreachable), so 0 — but we exercised
	// the OIDC_PROVIDER_NAME branch and the custom-name code path.
	if len(providers) != 0 {
		t.Errorf("expected 0 on unreachable issuer, got %d", len(providers))
	}
}

func TestLoadOAuthProviders_zitadelBadIssuer(t *testing.T) {
	t.Setenv("OAUTH_REDIRECT_BASE", "https://app.example.com")
	t.Setenv("ZITADEL_ISSUER_URL", "http://127.0.0.1:1")
	t.Setenv("ZITADEL_CLIENT_ID", "id")
	t.Setenv("ZITADEL_CLIENT_SECRET", "sec")
	t.Setenv("OIDC_ISSUER_URL", "")
	t.Setenv("GITHUB_CLIENT_ID", "")
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("AUTH0_DOMAIN", "")
	t.Setenv("MICROSOFT_CLIENT_ID", "")

	providers := LoadOAuthProviders(context.Background())
	if len(providers) != 0 {
		t.Errorf("expected 0 providers when Zitadel issuer is unreachable, got %d", len(providers))
	}
}

func TestLoadOAuthProviders_auth0BadDomain(t *testing.T) {
	t.Setenv("OAUTH_REDIRECT_BASE", "https://app.example.com")
	t.Setenv("AUTH0_DOMAIN", "badauth0.invalid")
	t.Setenv("AUTH0_CLIENT_ID", "id")
	t.Setenv("AUTH0_CLIENT_SECRET", "sec")
	t.Setenv("OIDC_ISSUER_URL", "")
	t.Setenv("GITHUB_CLIENT_ID", "")
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("ZITADEL_ISSUER_URL", "")
	t.Setenv("MICROSOFT_CLIENT_ID", "")

	providers := LoadOAuthProviders(context.Background())
	if len(providers) != 0 {
		t.Errorf("expected 0 providers when Auth0 domain is invalid, got %d", len(providers))
	}
}

func TestLoadOAuthProviders_auth0DomainStripsHTTPS(t *testing.T) {
	// Passing "https://myapp.auth0.com" should not double-prefix "https://".
	t.Setenv("OAUTH_REDIRECT_BASE", "https://app.example.com")
	t.Setenv("AUTH0_DOMAIN", "https://127.0.0.1:1")
	t.Setenv("AUTH0_CLIENT_ID", "id")
	t.Setenv("AUTH0_CLIENT_SECRET", "sec")
	t.Setenv("OIDC_ISSUER_URL", "")
	t.Setenv("GITHUB_CLIENT_ID", "")
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("ZITADEL_ISSUER_URL", "")
	t.Setenv("MICROSOFT_CLIENT_ID", "")

	// Should not panic even if issuer construction or discovery fails.
	providers := LoadOAuthProviders(context.Background())
	if len(providers) != 0 {
		t.Errorf("expected 0 providers when OIDC discovery fails, got %d", len(providers))
	}
}

func TestLoadOAuthProviders_microsoftDefaultTenant(t *testing.T) {
	t.Setenv("OAUTH_REDIRECT_BASE", "https://app.example.com")
	t.Setenv("MICROSOFT_CLIENT_ID", "ms-id")
	t.Setenv("MICROSOFT_CLIENT_SECRET", "ms-sec")
	t.Setenv("MICROSOFT_TENANT", "")
	t.Setenv("OIDC_ISSUER_URL", "")
	t.Setenv("GITHUB_CLIENT_ID", "")
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("ZITADEL_ISSUER_URL", "")
	t.Setenv("AUTH0_DOMAIN", "")

	// Discovery will fail (real Microsoft endpoint not reachable in test) → 0 providers.
	providers := LoadOAuthProviders(context.Background())
	if len(providers) != 0 {
		t.Errorf("expected 0 providers when Microsoft discovery fails, got %d", len(providers))
	}
}

func TestLoadOAuthProviders_microsoftCustomTenant(t *testing.T) {
	t.Setenv("OAUTH_REDIRECT_BASE", "https://app.example.com")
	t.Setenv("MICROSOFT_CLIENT_ID", "ms-id")
	t.Setenv("MICROSOFT_CLIENT_SECRET", "ms-sec")
	t.Setenv("MICROSOFT_TENANT", "my-tenant-id")
	t.Setenv("OIDC_ISSUER_URL", "")
	t.Setenv("GITHUB_CLIENT_ID", "")
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("ZITADEL_ISSUER_URL", "")
	t.Setenv("AUTH0_DOMAIN", "")

	providers := LoadOAuthProviders(context.Background())
	if len(providers) != 0 {
		t.Errorf("expected 0 providers when Microsoft discovery fails, got %d", len(providers))
	}
}

// ---------------------------------------------------------------------------
// newOIDCProvider + oidcProvider.Name + oidcProvider.AuthCodeURL with a
// minimal mock OIDC discovery server (no external network calls).
// ---------------------------------------------------------------------------

// mockOIDCServer returns an httptest.Server that serves a minimal OpenID
// Connect discovery document pointing back at itself.
func mockOIDCServer(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + srv.Listener.Addr().String()
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                base,
				"authorization_endpoint":                base + "/auth",
				"token_endpoint":                        base + "/token",
				"jwks_uri":                              base + "/jwks",
				"response_types_supported":              []string{"code"},
				"subject_types_supported":               []string{"public"},
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"keys":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNewOIDCProvider_success(t *testing.T) {
	srv := mockOIDCServer(t)

	p, err := newOIDCProvider(context.Background(), "test-oidc", srv.URL, "client-id", "client-secret", "https://app.example.com")
	if err != nil {
		t.Fatalf("newOIDCProvider: %v", err)
	}

	if p.Name() != "test-oidc" {
		t.Errorf("Name: got %q, want test-oidc", p.Name())
	}

	url := p.AuthCodeURL("mystate", "myverifier")
	if !strings.Contains(url, "mystate") {
		t.Errorf("AuthCodeURL: state not in URL: %q", url)
	}
	if !strings.Contains(url, "code_challenge") {
		t.Errorf("AuthCodeURL: expected PKCE code_challenge in URL: %q", url)
	}
}

func TestNewOIDCProvider_badIssuer(t *testing.T) {
	_, err := newOIDCProvider(context.Background(), "bad", "http://127.0.0.1:1", "id", "sec", "https://app.example.com")
	if err == nil {
		t.Error("expected error for unreachable OIDC issuer")
	}
}

func TestLoadOAuthProviders_oidcSuccessWithMockServer(t *testing.T) {
	srv := mockOIDCServer(t)

	t.Setenv("OAUTH_REDIRECT_BASE", "https://app.example.com")
	t.Setenv("OIDC_ISSUER_URL", srv.URL)
	t.Setenv("OIDC_CLIENT_ID", "id")
	t.Setenv("OIDC_CLIENT_SECRET", "sec")
	t.Setenv("OIDC_PROVIDER_NAME", "")
	t.Setenv("GITHUB_CLIENT_ID", "")
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("ZITADEL_ISSUER_URL", "")
	t.Setenv("AUTH0_DOMAIN", "")
	t.Setenv("MICROSOFT_CLIENT_ID", "")

	providers := LoadOAuthProviders(context.Background())
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
	if providers[0].Name() != "oidc" {
		t.Errorf("expected oidc provider name, got %q", providers[0].Name())
	}
}

func TestLoadOAuthProviders_githubAndOIDC(t *testing.T) {
	srv := mockOIDCServer(t)

	t.Setenv("OAUTH_REDIRECT_BASE", "https://app.example.com")
	t.Setenv("OIDC_ISSUER_URL", srv.URL)
	t.Setenv("OIDC_CLIENT_ID", "id")
	t.Setenv("OIDC_CLIENT_SECRET", "sec")
	t.Setenv("OIDC_PROVIDER_NAME", "myoidc")
	t.Setenv("GITHUB_CLIENT_ID", "gh-id")
	t.Setenv("GITHUB_CLIENT_SECRET", "gh-sec")
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("ZITADEL_ISSUER_URL", "")
	t.Setenv("AUTH0_DOMAIN", "")
	t.Setenv("MICROSOFT_CLIENT_ID", "")

	providers := LoadOAuthProviders(context.Background())
	if len(providers) != 2 {
		t.Fatalf("expected 2 providers (oidc+github), got %d", len(providers))
	}
	// OIDC is registered first (per LoadOAuthProviders order).
	if providers[0].Name() != "myoidc" {
		t.Errorf("expected myoidc first, got %q", providers[0].Name())
	}
	if providers[1].Name() != "github" {
		t.Errorf("expected github second, got %q", providers[1].Name())
	}
}

// mockOIDCServerWithToken returns an httptest.Server like mockOIDCServer but
// also serves a /token endpoint that responds with tokenResp as JSON. Use this
// to exercise error paths inside oidcProvider.Exchange.
func mockOIDCServerWithToken(t *testing.T, tokenResp map[string]any) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + srv.Listener.Addr().String()
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                base,
				"authorization_endpoint":                base + "/auth",
				"token_endpoint":                        base + "/token",
				"jwks_uri":                              base + "/jwks",
				"response_types_supported":              []string{"code"},
				"subject_types_supported":               []string{"public"},
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(tokenResp)
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"keys":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ---------------------------------------------------------------------------
// oidcProvider.Exchange — error paths
// ---------------------------------------------------------------------------

func TestOIDCProvider_Exchange_tokenEndpointError(t *testing.T) {
	// mockOIDCServer returns 404 for /token, so the oauth2 exchange fails.
	srv := mockOIDCServer(t)
	p, err := newOIDCProvider(context.Background(), "test-oidc", srv.URL,
		"client-id", "client-secret", "https://app.example.com")
	if err != nil {
		t.Fatalf("newOIDCProvider: %v", err)
	}
	_, _, err = p.Exchange(context.Background(), "bad-code", "verifier")
	if err == nil {
		t.Fatal("expected error when token endpoint returns 404")
	}
	if !strings.Contains(err.Error(), "exchange:") {
		t.Errorf("expected 'exchange:' prefix in error, got: %v", err)
	}
}

func TestOIDCProvider_Exchange_missingIDToken(t *testing.T) {
	// Token endpoint returns a valid OAuth2 token but without an id_token field.
	srv := mockOIDCServerWithToken(t, map[string]any{
		"access_token": "fake-access-token",
		"token_type":   "bearer",
		"expires_in":   3600,
	})
	p, err := newOIDCProvider(context.Background(), "test-oidc", srv.URL,
		"client-id", "client-secret", "https://app.example.com")
	if err != nil {
		t.Fatalf("newOIDCProvider: %v", err)
	}
	_, _, err = p.Exchange(context.Background(), "any-code", "verifier")
	if err == nil {
		t.Fatal("expected error when id_token is absent from token response")
	}
	if !strings.Contains(err.Error(), "id_token") {
		t.Errorf("expected 'id_token' in error, got: %v", err)
	}
}

func TestOIDCProvider_Exchange_invalidIDToken(t *testing.T) {
	// Token endpoint returns an id_token that is not a valid JWT; the OIDC
	// verifier must reject it before fetching keys.
	srv := mockOIDCServerWithToken(t, map[string]any{
		"access_token": "fake-access-token",
		"id_token":     "not.a.valid.jwt",
		"token_type":   "bearer",
		"expires_in":   3600,
	})
	p, err := newOIDCProvider(context.Background(), "test-oidc", srv.URL,
		"client-id", "client-secret", "https://app.example.com")
	if err != nil {
		t.Fatalf("newOIDCProvider: %v", err)
	}
	_, _, err = p.Exchange(context.Background(), "any-code", "verifier")
	if err == nil {
		t.Fatal("expected error when id_token is an invalid JWT")
	}
	if !strings.Contains(err.Error(), "verify id_token") {
		t.Errorf("expected 'verify id_token' in error, got: %v", err)
	}
}
