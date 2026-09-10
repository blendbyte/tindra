package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func clearSSOEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"OAUTH_REDIRECT_BASE", "OIDC_ISSUER_URL", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "MICROSOFT_CLIENT_ID", "MICROSOFT_CLIENT_SECRET", "GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET", "ZITADEL_ISSUER_URL", "ZITADEL_CLIENT_ID", "ZITADEL_CLIENT_SECRET", "AUTH0_DOMAIN", "AUTH0_CLIENT_ID", "AUTH0_CLIENT_SECRET"} {
		t.Setenv(key, "")
	}
}

func TestSSOPolicySurvivesProviderFailure(t *testing.T) {
	clearSSOEnvironment(t)
	pool := oauthDB(t)
	u, err := storage.CreateUser(t.Context(), pool, uuid.NewString()+"@policy.test", "password-123456")
	require.NoError(t, err)
	discovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer discovery.Close()
	for _, scenario := range []string{"discovery failure", "incomplete credentials", "missing redirect", "redirect only", "local"} {
		t.Run(scenario, func(t *testing.T) {
			clearSSOEnvironment(t)
			switch scenario {
			case "discovery failure":
				t.Setenv("OAUTH_REDIRECT_BASE", "https://tindra.test")
				t.Setenv("OIDC_ISSUER_URL", discovery.URL)
				t.Setenv("OIDC_CLIENT_ID", "client")
				t.Setenv("OIDC_CLIENT_SECRET", "secret")
			case "incomplete credentials":
				t.Setenv("GITHUB_CLIENT_ID", "client")
			case "missing redirect":
				t.Setenv("GITHUB_CLIENT_ID", "client")
				t.Setenv("GITHUB_CLIENT_SECRET", "secret")
			case "redirect only":
				t.Setenv("OAUTH_REDIRECT_BASE", "https://tindra.test")
			}
			providers := LoadOAuthProviders(t.Context())
			require.Empty(t, providers)
			h := NewRouter(pool, nil, nil, nil, nil, nil, providers, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, false, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/auth/providers", nil))
			var config struct {
				SSORequired bool `json:"sso_required"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &config))
			require.Equal(t, scenario != "local", config.SSORequired)
			// Configuration is captured at router construction, not read per request.
			clearSSOEnvironment(t)
			rec = httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"email":"`+u.Email+`","password":"password-123456"}`)))
			if scenario == "local" {
				require.Equal(t, 200, rec.Code)
			} else {
				require.Equal(t, 403, rec.Code)
				require.Empty(t, rec.Result().Cookies())
			}
		})
	}
}

func TestSSOPolicyRejectsLocalAdmissionAndRecovery(t *testing.T) {
	pool := oauthDB(t)
	for _, loaded := range []bool{false, true} {
		t.Run(map[bool]string{false: "unavailable", true: "available"}[loaded], func(t *testing.T) {
			email := uuid.NewString() + "@invited-policy.test"
			invite, err := storage.CreateInvite(t.Context(), pool, "", email, "Invited")
			require.NoError(t, err)
			u, err := storage.CreateUser(t.Context(), pool, uuid.NewString()+"@reset-policy.test", "password-123456")
			require.NoError(t, err)
			require.NoError(t, storage.StoreMFASecret(t.Context(), pool, u.ID, "secret"))
			require.NoError(t, storage.EnableMFA(t.Context(), pool, u.ID))
			reset, err := storage.CreatePasswordResetToken(t.Context(), pool, u.ID)
			require.NoError(t, err)
			ro := &router{pool: pool, ssoOnly: !loaded}
			if loaded {
				ro.oauthProviders = []oauthProvider{mockProvider{name: "google"}}
			}
			routes := chi.NewRouter()
			routes.Get("/invite/{token}", ro.handleGetInvite)
			routes.Post("/invite/{token}", ro.handleAcceptInvite)
			routes.Get("/reset/{token}", ro.handleGetPasswordReset)
			routes.Post("/reset/{token}", ro.handleDoPasswordReset)
			for _, path := range []string{"/invite/" + invite, "/reset/" + reset} {
				rec := httptest.NewRecorder()
				routes.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
				require.Equal(t, 200, rec.Code)
				var body map[string]any
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
				require.Equal(t, true, body["sso_required"])
				rec = httptest.NewRecorder()
				routes.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(`{"password":"replacement-1234"}`)))
				require.Equal(t, 403, rec.Code)
				require.Empty(t, rec.Result().Cookies())
			}
			inv, err := storage.GetInvite(t.Context(), pool, invite)
			require.NoError(t, err)
			require.NotNil(t, inv)
			admitted, err := storage.GetUserByEmail(t.Context(), pool, email)
			require.NoError(t, err)
			require.Nil(t, admitted)
			after, err := storage.GetPasswordResetUser(t.Context(), pool, reset)
			require.NoError(t, err)
			require.NotNil(t, after)
			require.Equal(t, u.PasswordHash, after.PasswordHash)
			require.True(t, after.MFAEnabled)
			if loaded {
				h := routerWithSSOAndPool(pool, admissionProvider{mockProvider{name: "admission"}, email})
				state, err := storage.CreateOAuthState(t.Context(), pool, "admission", "verifier")
				require.NoError(t, err)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, boundOAuthRequest("/api/auth/admission/callback?state="+state+"&code=code", "verifier", false))
				require.Equal(t, http.StatusFound, rec.Code, rec.Body.String())
				require.NotEmpty(t, activeOAuthResponseCookies(rec))
				admitted, err := storage.GetUserByEmail(t.Context(), pool, email)
				require.NoError(t, err)
				require.NotNil(t, admitted)
				require.False(t, admitted.HasPassword)
				inv, err := storage.GetInvite(t.Context(), pool, invite)
				require.NoError(t, err)
				require.Nil(t, inv)
			}

		})
	}
}
