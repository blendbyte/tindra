package api

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestLoginResetAfterPasswordValidation(t *testing.T) {
	pool := oauthDB(t)
	for _, mfa := range []bool{false, true} {
		t.Run(fmt.Sprint(mfa), func(t *testing.T) {
			u, err := storage.CreateUser(t.Context(), pool, uuid.NewString()+"@race.test", "old-password-1234")
			require.NoError(t, err)
			if mfa {
				require.NoError(t, storage.EnableMFA(t.Context(), pool, u.ID))
			}
			trace := &changeMFAQuery{match: "SELECT password_hash, mfa_enabled FROM users", change: func() { require.NoError(t, storage.AdminSetPassword(t.Context(), pool, u.ID, "new-password-1234")) }}
			cfg := pool.Config()
			cfg.ConnConfig.Tracer = trace
			traced, err := pgxpool.NewWithConfig(t.Context(), cfg)
			require.NoError(t, err)
			defer traced.Close()
			ro := &router{pool: traced, loginEmailRL: newRateLimiter(0, time.Minute)}
			req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(fmt.Sprintf(`{"email":%q,"password":"old-password-1234"}`, u.Email)))
			rec := httptest.NewRecorder()
			ro.handleLogin(rec, req)
			require.True(t, trace.fired.Load())
			require.Equal(t, 401, rec.Code, rec.Body.String())
			require.Empty(t, rec.Result().Cookies())
			var count int
			require.NoError(t, pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM sessions WHERE user_id=$1)+(SELECT count(*) FROM mfa_challenges WHERE user_id=$1)`, u.ID).Scan(&count))
			require.Zero(t, count)
		})
	}
}

func TestMFALoginResetAfterCodeValidation(t *testing.T) {
	pool := oauthDB(t)
	u, token, code := singleUseMFAFixture(t, pool)
	trace := &changeMFAQuery{match: "SELECT mfa_secret, mfa_enabled FROM users", change: func() { require.NoError(t, storage.AdminSetPassword(t.Context(), pool, u.ID, "new-password-1234")) }}
	cfg := pool.Config()
	cfg.ConnConfig.Tracer = trace
	traced, err := pgxpool.NewWithConfig(t.Context(), cfg)
	require.NoError(t, err)
	defer traced.Close()
	ro := &router{pool: traced}
	rec := httptest.NewRecorder()
	ro.handleMFAVerify(rec, httptest.NewRequest("POST", "/api/auth/mfa/verify", strings.NewReader(fmt.Sprintf(`{"mfa_token":%q,"code":%q}`, token, code))))
	require.True(t, trace.fired.Load())
	require.Equal(t, 401, rec.Code, rec.Body.String())
	require.Empty(t, rec.Result().Cookies())
}

func TestOAuthResetBeforeCredentialIssuance(t *testing.T) {
	pool := oauthDB(t)
	for _, mfa := range []bool{false, true} {
		t.Run(fmt.Sprint(mfa), func(t *testing.T) {
			u, err := storage.CreateOAuthUser(t.Context(), pool, uuid.NewString()+"@oauth-race.test")
			require.NoError(t, err)
			if mfa {
				require.NoError(t, storage.EnableMFA(t.Context(), pool, u.ID))
			}
			state, err := storage.CreateOAuthState(t.Context(), pool, "admission", "verifier")
			require.NoError(t, err)
			trace := &changeMFAQuery{match: "SELECT password_hash, mfa_enabled FROM users", change: func() {
				require.NoError(t, storage.AdminSetPassword(t.Context(), pool, u.ID, "new-password-1234"))
			}}
			cfg := pool.Config()
			cfg.ConnConfig.Tracer = trace
			traced, err := pgxpool.NewWithConfig(t.Context(), cfg)
			require.NoError(t, err)
			defer traced.Close()
			h := routerWithSSOAndPool(traced, admissionProvider{mockProvider{name: "admission"}, u.Email})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, boundOAuthRequest("/api/auth/admission/callback?state="+state+"&code=code", "verifier", false))
			require.True(t, trace.fired.Load())
			require.Equal(t, 401, rec.Code, rec.Body.String())
			require.Empty(t, activeOAuthResponseCookies(rec))
		})
	}
}

func TestInviteAccountIsInvisibleUntilSessionIssuance(t *testing.T) {
	pool := oauthDB(t)
	email := uuid.NewString() + "@invite-race.test"
	token, err := storage.CreateInvite(t.Context(), pool, "", email, "Invited")
	require.NoError(t, err)
	trace := &changeMFAQuery{match: "INSERT INTO sessions", change: func() {
		u, err := storage.GetUserByEmail(t.Context(), pool, email)
		require.NoError(t, err)
		require.Nil(t, u, "an administrator cannot reset an account before admission commits")
	}}
	cfg := pool.Config()
	cfg.ConnConfig.Tracer = trace
	traced, err := pgxpool.NewWithConfig(t.Context(), cfg)
	require.NoError(t, err)
	defer traced.Close()
	ro := &router{pool: traced}
	routes := chi.NewRouter()
	routes.Post("/{token}", ro.handleAcceptInvite)
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest("POST", "/"+token, strings.NewReader(`{"password":"old-password-1234"}`)))
	require.True(t, trace.fired.Load())
	require.Equal(t, 201, rec.Code, rec.Body.String())
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	user, err := storage.GetUserByEmail(t.Context(), pool, email)
	require.NoError(t, err)
	require.NotNil(t, user)
	require.NoError(t, storage.AdminSetPassword(t.Context(), pool, user.ID, "replacement-password"))
	identity, err := storage.GetSessionIdentity(t.Context(), pool, cookies[0].Value)
	require.NoError(t, err)
	require.Nil(t, identity)
}
