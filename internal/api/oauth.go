package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"

	"github.com/blendbyte/tindra/internal/storage"
)

// oauthProvider is the interface each SSO provider must implement.
type oauthProvider interface {
	// Name returns the URL-safe provider identifier (e.g. "google", "github", "oidc").
	Name() string
	// AuthCodeURL returns the provider's authorization endpoint URL.
	AuthCodeURL(state, pkceVerifier string) string
	// Exchange trades the authorization code for an email address and opaque subject ID.
	Exchange(ctx context.Context, code, pkceVerifier string) (email, sub string, err error)
}

// ---- OIDC provider (covers Zitadel, Auth0, Cloudflare Access, Google, Microsoft) ----

type oidcProvider struct {
	name     string
	cfg      oauth2.Config
	verifier *gooidc.IDTokenVerifier
}

func newOIDCProvider(ctx context.Context, name, issuer, clientID, clientSecret, redirectBase string) (*oidcProvider, error) {
	p, err := gooidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery for %s (%s): %w", name, issuer, err)
	}
	return &oidcProvider{
		name: name,
		cfg: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectBase + "/api/auth/" + name + "/callback",
			Endpoint:     p.Endpoint(),
			Scopes:       []string{gooidc.ScopeOpenID, "email", "profile"},
		},
		verifier: p.Verifier(&gooidc.Config{ClientID: clientID}),
	}, nil
}

func (p *oidcProvider) Name() string { return p.name }

func (p *oidcProvider) AuthCodeURL(state, pkceVerifier string) string {
	return p.cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(pkceVerifier))
}

func (p *oidcProvider) Exchange(ctx context.Context, code, pkceVerifier string) (string, string, error) {
	token, err := p.cfg.Exchange(ctx, code, oauth2.VerifierOption(pkceVerifier))
	if err != nil {
		return "", "", fmt.Errorf("exchange: %w", err)
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok {
		return "", "", fmt.Errorf("no id_token in response")
	}
	idToken, err := p.verifier.Verify(ctx, rawID)
	if err != nil {
		return "", "", fmt.Errorf("verify id_token: %w", err)
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", "", fmt.Errorf("claims: %w", err)
	}
	if claims.Email == "" {
		return "", "", fmt.Errorf("email claim missing")
	}
	if claims.EmailVerified != nil && !*claims.EmailVerified {
		return "", "", fmt.Errorf("email address has not been verified by the provider")
	}
	return claims.Email, idToken.Subject, nil
}

// ---- GitHub provider (OAuth2, not OIDC) ----

type githubProvider struct {
	cfg oauth2.Config
}

func newGitHubProvider(clientID, clientSecret, redirectBase string) *githubProvider {
	return &githubProvider{cfg: oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectBase + "/api/auth/github/callback",
		Endpoint:     github.Endpoint,
		Scopes:       []string{"user:email"},
	}}
}

func (p *githubProvider) Name() string { return "github" }

func (p *githubProvider) AuthCodeURL(state, pkceVerifier string) string {
	// GitHub does not support PKCE - state alone provides CSRF protection.
	return p.cfg.AuthCodeURL(state)
}

func (p *githubProvider) Exchange(ctx context.Context, code, _ string) (string, string, error) {
	token, err := p.cfg.Exchange(ctx, code)
	if err != nil {
		return "", "", fmt.Errorf("exchange: %w", err)
	}

	// Get numeric user ID (stable sub).
	user, err := githubAPIGet[struct {
		ID    int64  `json:"id"`
		Email string `json:"email"`
	}](ctx, token.AccessToken, "https://api.github.com/user")
	if err != nil {
		return "", "", err
	}

	email := user.Email
	if email == "" {
		// Primary email may not be public - fetch from the emails endpoint.
		type ghEmail struct {
			Email   string `json:"email"`
			Primary bool   `json:"primary"`
		}
		emails, err := githubAPIGet[[]ghEmail](ctx, token.AccessToken, "https://api.github.com/user/emails")
		if err != nil {
			return "", "", err
		}
		for _, e := range *emails {
			if e.Primary {
				email = e.Email
				break
			}
		}
	}
	if email == "" {
		return "", "", fmt.Errorf("no email returned by GitHub")
	}
	return email, fmt.Sprintf("%d", user.ID), nil
}

func githubAPIGet[T any](ctx context.Context, accessToken, url string) (*T, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Tindra/"+AppVersion+" (+https://tindra.sh)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api %s: %s", url, body)
	}
	var result T
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode github response: %w", err)
	}
	return &result, nil
}

// ---- Provider loading from environment ----

// LoadOAuthProviders initialises all configured OAuth providers from env vars.
// Missing/incomplete providers are skipped with a warning rather than crashing.
//
// Env vars:
//
//	OAUTH_REDIRECT_BASE   - base URL of this Tindra instance, e.g. https://tindra.example.com
//	OIDC_ISSUER_URL       - generic OIDC provider discovery URL
//	OIDC_CLIENT_ID        - generic OIDC client ID
//	OIDC_CLIENT_SECRET    - generic OIDC client secret
//	OIDC_PROVIDER_NAME    - label shown in UI and used in callback path (default: "oidc")
//	GOOGLE_CLIENT_ID      - Google OAuth2 client ID
//	GOOGLE_CLIENT_SECRET  - Google OAuth2 client secret
//	MICROSOFT_CLIENT_ID   - Microsoft OAuth2 client ID
//	MICROSOFT_CLIENT_SECRET
//	MICROSOFT_TENANT      - Azure AD tenant ID or "common" / "organizations" / "consumers"
//	GITHUB_CLIENT_ID      - GitHub OAuth2 client ID
//	GITHUB_CLIENT_SECRET  - GitHub OAuth2 client secret
//	ZITADEL_ISSUER_URL    - Zitadel instance URL (e.g. https://auth.example.com)
//	ZITADEL_CLIENT_ID
//	ZITADEL_CLIENT_SECRET
//	AUTH0_DOMAIN          - Auth0 domain (e.g. myapp.us.auth0.com)
//	AUTH0_CLIENT_ID
//	AUTH0_CLIENT_SECRET
func LoadOAuthProviders(ctx context.Context) []oauthProvider {
	base := strings.TrimRight(os.Getenv("OAUTH_REDIRECT_BASE"), "/")
	if base == "" {
		return nil
	}

	var providers []oauthProvider

	add := func(name string, fn func() (oauthProvider, error)) {
		p, err := fn()
		if err != nil {
			slog.Warn("oauth provider disabled", "provider", name, "err", err)
			return
		}
		providers = append(providers, p)
		slog.Info("oauth provider enabled", "provider", name)
	}

	// Generic OIDC
	if issuer, id, secret := os.Getenv("OIDC_ISSUER_URL"), os.Getenv("OIDC_CLIENT_ID"), os.Getenv("OIDC_CLIENT_SECRET"); issuer != "" && id != "" && secret != "" {
		name := os.Getenv("OIDC_PROVIDER_NAME")
		if name == "" {
			name = "oidc"
		}
		add(name, func() (oauthProvider, error) {
			return newOIDCProvider(ctx, name, issuer, id, secret, base)
		})
	}

	// Zitadel
	if issuer, id, secret := os.Getenv("ZITADEL_ISSUER_URL"), os.Getenv("ZITADEL_CLIENT_ID"), os.Getenv("ZITADEL_CLIENT_SECRET"); issuer != "" && id != "" && secret != "" {
		add("zitadel", func() (oauthProvider, error) {
			return newOIDCProvider(ctx, "zitadel", issuer, id, secret, base)
		})
	}

	// Auth0
	if domain, id, secret := os.Getenv("AUTH0_DOMAIN"), os.Getenv("AUTH0_CLIENT_ID"), os.Getenv("AUTH0_CLIENT_SECRET"); domain != "" && id != "" && secret != "" {
		issuer := "https://" + strings.TrimPrefix(domain, "https://")
		add("auth0", func() (oauthProvider, error) {
			return newOIDCProvider(ctx, "auth0", issuer, id, secret, base)
		})
	}

	// Google
	if id, secret := os.Getenv("GOOGLE_CLIENT_ID"), os.Getenv("GOOGLE_CLIENT_SECRET"); id != "" && secret != "" {
		add("google", func() (oauthProvider, error) {
			return newOIDCProvider(ctx, "google", "https://accounts.google.com", id, secret, base)
		})
	}

	// Microsoft
	if id, secret := os.Getenv("MICROSOFT_CLIENT_ID"), os.Getenv("MICROSOFT_CLIENT_SECRET"); id != "" && secret != "" {
		tenant := os.Getenv("MICROSOFT_TENANT")
		if tenant == "" {
			tenant = "common"
		}
		issuer := "https://login.microsoftonline.com/" + tenant + "/v2.0"
		add("microsoft", func() (oauthProvider, error) {
			return newOIDCProvider(ctx, "microsoft", issuer, id, secret, base)
		})
	}

	// GitHub
	if id, secret := os.Getenv("GITHUB_CLIENT_ID"), os.Getenv("GITHUB_CLIENT_SECRET"); id != "" && secret != "" {
		providers = append(providers, newGitHubProvider(id, secret, base))
		slog.Info("oauth provider enabled", "provider", "github")
	}

	return providers
}

// ---- HTTP handlers ----

func (ro *router) handleListProviders(w http.ResponseWriter, r *http.Request) {
	names := make([]string, len(ro.oauthProviders))
	for i, p := range ro.oauthProviders {
		names[i] = p.Name()
	}
	writeJSON(w, map[string]any{"providers": names})
}

func (ro *router) providerByName(name string) oauthProvider {
	for _, p := range ro.oauthProviders {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

func (ro *router) handleOAuthRedirect(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "provider")
	p := ro.providerByName(name)
	if p == nil {
		http.Error(w, "unknown provider", http.StatusNotFound)
		return
	}

	verifier := oauth2.GenerateVerifier()
	state, err := storage.CreateOAuthState(r.Context(), ro.pool, name, verifier)
	if err != nil {
		slog.Error("create oauth state", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, p.AuthCodeURL(state, verifier), http.StatusFound)
}

func (ro *router) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "provider")
	p := ro.providerByName(name)
	if p == nil {
		http.Error(w, "unknown provider", http.StatusNotFound)
		return
	}

	stateToken := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if stateToken == "" || code == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	oauthState, err := storage.ConsumeOAuthState(r.Context(), ro.pool, stateToken)
	if err != nil {
		slog.Error("consume oauth state", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if oauthState == nil || oauthState.Provider != name {
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	email, sub, err := p.Exchange(r.Context(), code, oauthState.Verifier)
	if err != nil {
		slog.Error("oauth exchange", "provider", name, "err", err)
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}

	user, err := storage.FindOrCreateOAuthUser(r.Context(), ro.pool, name, sub, email)
	if err != nil {
		slog.Error("find or create oauth user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	session, err := storage.CreateSession(r.Context(), ro.pool, user.ID)
	if err != nil {
		slog.Error("create session", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "tindra_session",
		Value:    session.Token,
		HttpOnly: true,
		Secure:   ro.cookieSecure,
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
		Expires:  session.ExpiresAt,
	})

	// Redirect to the app root; the frontend detects the session cookie.
	http.Redirect(w, r, "/", http.StatusFound)
}
