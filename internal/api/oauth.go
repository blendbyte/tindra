package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
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
	// Exchange returns an authenticated subject and the provider's email verification status.
	Exchange(ctx context.Context, code, pkceVerifier string) (email, sub string, emailVerified bool, err error)
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

func (p *oidcProvider) Exchange(ctx context.Context, code, pkceVerifier string) (string, string, bool, error) {
	token, err := p.cfg.Exchange(ctx, code, oauth2.VerifierOption(pkceVerifier))
	if err != nil {
		return "", "", false, fmt.Errorf("exchange: %w", err)
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok {
		return "", "", false, fmt.Errorf("no id_token in response")
	}
	idToken, err := p.verifier.Verify(ctx, rawID)
	if err != nil {
		return "", "", false, fmt.Errorf("verify id_token: %w", err)
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", "", false, fmt.Errorf("claims: %w", err)
	}
	if idToken.Subject == "" {
		return "", "", false, fmt.Errorf("subject claim missing")
	}
	return claims.Email, idToken.Subject, claims.EmailVerified != nil && *claims.EmailVerified, nil
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
	// This flow uses state plus the browser binding for CSRF protection.
	return p.cfg.AuthCodeURL(state)
}

func (p *githubProvider) Exchange(ctx context.Context, code, _ string) (string, string, bool, error) {
	token, err := p.cfg.Exchange(ctx, code)
	if err != nil {
		return "", "", false, fmt.Errorf("exchange: %w", err)
	}

	// Get numeric user ID (stable sub).
	user, err := githubAPIGet[struct {
		ID int64 `json:"id"`
	}](ctx, token.AccessToken, "https://api.github.com/user")
	if err != nil {
		return "", "", false, err
	}

	if user.ID <= 0 {
		return "", "", false, fmt.Errorf("GitHub user ID missing or invalid")
	}
	// The public profile email has no verification flag. Only the emails
	// endpoint can establish ownership for linking or invitation acceptance.
	type ghEmail struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	emails, err := githubAPIGet[[]ghEmail](ctx, token.AccessToken, "https://api.github.com/user/emails")
	if err != nil {
		return "", "", false, err
	}
	for _, e := range *emails {
		if e.Primary && e.Verified {
			return e.Email, fmt.Sprintf("%d", user.ID), true, nil
		}
	}
	// An already-linked subject may still sign in without a verified email.
	return "", fmt.Sprintf("%d", user.ID), false, nil
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

// oauthConfigured captures operator intent, including incomplete configuration.
// Authentication policy must not depend on discovery succeeding at startup.
func oauthConfigured() bool {
	for _, key := range []string{
		"OAUTH_REDIRECT_BASE", "OIDC_ISSUER_URL", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET",
		"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "MICROSOFT_CLIENT_ID", "MICROSOFT_CLIENT_SECRET",
		"GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET", "ZITADEL_ISSUER_URL", "ZITADEL_CLIENT_ID", "ZITADEL_CLIENT_SECRET",
		"AUTH0_DOMAIN", "AUTH0_CLIENT_ID", "AUTH0_CLIENT_SECRET",
	} {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}

func (ro *router) ssoRequired() bool {
	return ro.ssoOnly || len(ro.oauthProviders) > 0
}

// LoadOAuthProviders initialises all configured OAuth providers from env vars.
// Failed providers are skipped; configured SSO policy still disables local login.
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
	writeJSON(w, map[string]any{"providers": names, "sso_required": ro.ssoRequired()})
}

func (ro *router) providerByName(name string) oauthProvider {
	for _, p := range ro.oauthProviders {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// Each attempt gets its own cookie so concurrent login tabs do not overwrite
// each other. The URL carries only state, never the secret verifier.
func oauthBindingCookie(state, verifier string, secure bool) *http.Cookie {
	digest := sha256.Sum256([]byte(state))
	name := fmt.Sprintf("tindra_oauth_%x", digest[:16])
	if secure {
		name = "__Host-" + name
	}
	return &http.Cookie{Name: name, Value: verifier, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 600}
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

	http.SetCookie(w, oauthBindingCookie(state, verifier, ro.cookieSecure))
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

	binding := oauthBindingCookie(stateToken, "", ro.cookieSecure)
	cookie, err := r.Cookie(binding.Name)
	if err != nil || cookie.Value == "" {
		http.Error(w, "login attempt does not match this browser; restart sign-in", http.StatusBadRequest)
		return
	}
	oauthState, err := storage.ConsumeBoundOAuthState(r.Context(), ro.pool, stateToken, name, cookie.Value)
	if err != nil {
		slog.Error("consume oauth state", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if oauthState == nil {
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	// Clear only the successfully matched attempt, including on later failures.
	binding.MaxAge = -1
	http.SetCookie(w, binding)

	email, sub, emailVerified, err := p.Exchange(r.Context(), code, oauthState.Verifier)
	if err != nil {
		slog.Error("oauth exchange", "provider", name, "err", err)
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}

	user, err := storage.FindOrCreateOAuthUser(r.Context(), ro.pool, name, sub, email, emailVerified, int(ro.userLimit.Load()))
	if err != nil {
		if errors.Is(err, storage.ErrOAuthEmailUnverified) {
			http.Error(w, "Your sign-in provider must verify your email before this account can be linked. Contact your administrator.", http.StatusForbidden)
			return
		}
		if errors.Is(err, storage.ErrOAuthInviteRequired) {
			http.Error(w, "An invitation is required to sign in. Contact your administrator.", http.StatusForbidden)
			return
		}
		if errors.Is(err, storage.ErrOAuthUserLimit) {
			http.Error(w, "The user limit has been reached. Contact your administrator.", http.StatusTooManyRequests)
			return
		}
		slog.Error("find or create oauth user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if user.MFAEnabled {
		token, err := storage.CreateAuthenticatedMFAChallenge(r.Context(), ro.pool, user)
		if err != nil {
			if errors.Is(err, storage.ErrAuthenticationChanged) {
				http.Error(w, "authentication changed; please sign in again", http.StatusUnauthorized)
				return
			}
			slog.Error("create oauth mfa challenge", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: "tindra_mfa", Value: token, Path: "/api/auth/mfa/verify",
			HttpOnly: true, Secure: ro.cookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: 600,
		})
		http.Redirect(w, r, "/login?mfa=1", http.StatusFound)
		return
	}

	session, err := storage.CreateAuthenticatedSession(r.Context(), ro.pool, user)
	if err != nil {
		if errors.Is(err, storage.ErrAuthenticationChanged) {
			http.Error(w, "authentication changed; please sign in again", http.StatusUnauthorized)
			return
		}
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
