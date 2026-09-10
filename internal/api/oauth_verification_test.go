package api

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/blendbyte/tindra/internal/storage"
)

// Exercise real signature validation and token exchange, not a mocked identity.
func signedEmailProvider(t *testing.T, claims map[string]any) *oidcProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	claims["iss"] = "https://verification.test"
	claims["aud"] = "verification-client"
	claims["exp"] = time.Now().Add(time.Hour).Unix()
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	input := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	require.NoError(t, err)
	token := input + "." + base64.RawURLEncoding.EncodeToString(signature)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "token_type": "Bearer", "id_token": token})
	}))
	t.Cleanup(server.Close)
	return &oidcProvider{name: "verification", cfg: oauth2.Config{ClientID: "verification-client", Endpoint: oauth2.Endpoint{AuthURL: server.URL + "/authorize", TokenURL: server.URL}}, verifier: oidc.NewVerifier("https://verification.test", &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}, &oidc.Config{ClientID: "verification-client"})}
}

func verificationCallback(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	redirect := httptest.NewRecorder()
	h.ServeHTTP(redirect, httptest.NewRequest("GET", "/api/auth/verification/redirect", nil))
	require.Equal(t, http.StatusFound, redirect.Code)
	location, err := url.Parse(redirect.Header().Get("Location"))
	require.NoError(t, err)
	req := httptest.NewRequest("GET", "/api/auth/verification/callback?code=code&state="+location.Query().Get("state"), nil)
	for _, cookie := range redirect.Result().Cookies() {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	return response
}

func TestOAuthEmailVerificationAdmission(t *testing.T) {
	pool := oauthDB(t)
	for _, kind := range []string{"existing admin", "invitation", "linked identity"} {
		for _, verification := range []string{"missing", "false", "null", "true"} {
			t.Run(kind+"/"+verification, func(t *testing.T) {
				email := uuid.NewString() + "@verification.test"
				sub := uuid.NewString()
				var userID string
				if kind == "invitation" {
					_, err := storage.CreateInvite(t.Context(), pool, "", email, "")
					require.NoError(t, err)
				} else {
					user, err := storage.CreateAdminUser(t.Context(), pool, email, "Admin", "password1234")
					require.NoError(t, err)
					userID = user.ID
					if kind == "linked identity" {
						err = storage.LinkOAuthIdentity(t.Context(), pool, user.ID, "verification", sub)
						require.NoError(t, err)
					}
				}
				claims := map[string]any{"sub": sub, "email": email}
				switch verification {
				case "true":
					claims["email_verified"] = true
				case "false":
					claims["email_verified"] = false
				case "null":
					claims["email_verified"] = nil
				}
				if kind == "linked identity" {
					delete(claims, "email")
				}
				response := verificationCallback(t, routerWithSSOAndPool(pool, signedEmailProvider(t, claims)))
				allowed := kind == "linked identity" || verification == "true"
				var session string
				for _, cookie := range response.Result().Cookies() {
					if cookie.Name == "tindra_session" {
						session = cookie.Value
					}
				}
				if allowed {
					require.Equal(t, http.StatusFound, response.Code, response.Body.String())
					require.NotEmpty(t, session)
					identity, err := storage.GetSessionIdentity(t.Context(), pool, session)
					require.NoError(t, err)
					require.NotNil(t, identity)
					if userID != "" {
						require.Equal(t, userID, identity.UserID)
					} else {
						require.False(t, identity.Permissions.ManageUsers)
					}
				} else {
					require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
					require.Empty(t, session)
					var count int
					require.NoError(t, pool.QueryRow(t.Context(), "SELECT count(*) FROM oauth_identities WHERE provider = 'verification' AND sub = $1", sub).Scan(&count))
					require.Zero(t, count)
					if kind == "invitation" {
						require.NoError(t, pool.QueryRow(t.Context(), "SELECT count(*) FROM user_invites WHERE email = $1 AND accepted_at IS NULL", email).Scan(&count))
						require.Equal(t, 1, count)
						require.NoError(t, pool.QueryRow(t.Context(), "SELECT count(*) FROM users WHERE email = $1", email).Scan(&count))
						require.Zero(t, count)
					}
				}
			})
		}
	}
}

func TestOIDCProviderVerificationClaims(t *testing.T) {
	for _, tc := range []struct {
		name    string
		claims  map[string]any
		wantErr bool
	}{
		{"invalid verification type", map[string]any{"sub": "subject", "email": "a@example.com", "email_verified": "true"}, true},
		{"missing subject", map[string]any{"email": "a@example.com", "email_verified": true}, true},
		{"missing email", map[string]any{"sub": "subject", "email_verified": true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := signedEmailProvider(t, tc.claims)
			_, _, _, err := p.Exchange(t.Context(), "code", "verifier")
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

type emailVerificationTransport func(*http.Request) (*http.Response, error)

func (f emailVerificationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGitHubProviderEmailVerification(t *testing.T) {
	for _, tc := range []struct {
		name, user, emails string
		verified, wantErr  bool
		tokenStatus        int
	}{
		{"verified primary", `{"id":42,"email":"public@example.com"}`, `[{"email":"primary@example.com","primary":true,"verified":true}]`, true, false, 200},
		{"unverified public and primary", `{"id":42,"email":"public@example.com"}`, `[{"email":"public@example.com","primary":true,"verified":false}]`, false, false, 200},
		{"verification omitted", `{"id":42}`, `[{"email":"primary@example.com","primary":true}]`, false, false, 200},
		{"no emails", `{"id":42}`, `[]`, false, false, 200},
		{"nonprimary verified", `{"id":42}`, `[{"email":"secondary@example.com","primary":false,"verified":true}]`, false, false, 200},
		{"invalid ID", `{"id":0}`, `[]`, false, true, 200},
		{"bad user response", `invalid`, `[]`, false, true, 200},
		{"bad email response", `{"id":42}`, `invalid`, false, true, 200},
		{"token rejection", `{"id":42}`, `[]`, false, true, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := http.DefaultClient
			http.DefaultClient = &http.Client{Transport: emailVerificationTransport(func(r *http.Request) (*http.Response, error) {
				response := httptest.NewRecorder()
				response.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/token":
					response.WriteHeader(tc.tokenStatus)
					_, _ = response.WriteString(`{"access_token":"access","token_type":"Bearer"}`)
				case "/user":
					require.Equal(t, "Bearer access", r.Header.Get("Authorization"))
					_, _ = response.WriteString(tc.user)
				case "/user/emails":
					require.Equal(t, "Bearer access", r.Header.Get("Authorization"))
					_, _ = response.WriteString(tc.emails)
				default:
					t.Fatalf("unexpected URL: %s", r.URL)
				}
				return response.Result(), nil
			})}
			t.Cleanup(func() { http.DefaultClient = original })
			p := newGitHubProvider("id", "secret", "https://app.test")
			p.cfg.Endpoint.TokenURL = "https://github.test/token"
			email, sub, verified, err := p.Exchange(t.Context(), "code", "")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "42", sub)
			require.Equal(t, tc.verified, verified)
			if tc.verified {
				require.Equal(t, "primary@example.com", email)
			} else {
				require.Empty(t, email)
			}
		})
	}
}
