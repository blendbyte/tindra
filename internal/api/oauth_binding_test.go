package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func boundOAuthRequest(target, verifier string, secure bool) *http.Request {
	req := httptest.NewRequest("GET", target, nil)
	req.AddCookie(oauthBindingCookie(req.URL.Query().Get("state"), verifier, secure))
	return req
}

// Expiring the browser binding is not the issuance of authentication credentials.
func activeOAuthResponseCookies(rec *httptest.ResponseRecorder) []*http.Cookie {
	var cookies []*http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.MaxAge >= 0 {
			cookies = append(cookies, cookie)
		}
	}
	return cookies
}

func TestOAuthStateBoundToInitiatingBrowser(t *testing.T) {
	pool := oauthDB(t)
	for _, secure := range []bool{false, true} {
		email := uuid.NewString() + "@binding.example.com"
		_, err := storage.CreateOAuthUser(t.Context(), pool, email)
		require.NoError(t, err)
		h := NewRouter(pool, nil, nil, nil, nil, nil, []oauthProvider{admissionProvider{mockProvider{name: "admission"}, email}}, secure, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, false, nil)
		start := func() (string, *http.Cookie) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/auth/admission/redirect", nil))
			require.Equal(t, 302, rec.Code)
			location, err := url.Parse(rec.Header().Get("Location"))
			require.NoError(t, err)
			state := location.Query().Get("state")
			require.NotEmpty(t, state)
			cookies := rec.Result().Cookies()
			require.Len(t, cookies, 1)
			cookie := cookies[0]
			require.True(t, cookie.HttpOnly)
			require.Equal(t, secure, cookie.Secure)
			require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
			require.Equal(t, "/", cookie.Path)
			require.Empty(t, cookie.Domain)
			require.Equal(t, 600, cookie.MaxAge)
			require.Equal(t, secure, strings.HasPrefix(cookie.Name, "__Host-"))
			require.NotContains(t, location.String(), cookie.Value)
			return state, cookie
		}
		state, cookie := start()
		secondState, secondCookie := start()
		require.NotEqual(t, cookie.Name, secondCookie.Name)
		callback := func(state string, cookie *http.Cookie) *httptest.ResponseRecorder {
			req := httptest.NewRequest("GET", "/api/auth/admission/callback?state="+state+"&code=code", nil)
			if cookie != nil {
				req.AddCookie(cookie)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			return rec
		}
		require.Equal(t, 400, callback(state, nil).Code)
		empty := *cookie
		empty.Value = ""
		require.Equal(t, 400, callback(state, &empty).Code)
		// A cookie from a different attempt must not authorize this one.
		require.Equal(t, 400, callback(state, secondCookie).Code)
		wrong := *cookie
		wrong.Value = secondCookie.Value
		denied := callback(state, &wrong)
		require.Equal(t, 400, denied.Code)
		require.Empty(t, activeOAuthResponseCookies(denied))
		// Neither rejection consumed the real attempt, and both tabs can finish.
		for _, attempt := range []struct {
			state  string
			cookie *http.Cookie
		}{{state, cookie}, {secondState, secondCookie}} {
			rec := callback(attempt.state, attempt.cookie)
			require.Equal(t, 302, rec.Code, rec.Body.String())
			require.Equal(t, "/", rec.Header().Get("Location"))
			issued := activeOAuthResponseCookies(rec)
			require.Len(t, issued, 1)
			require.Equal(t, "tindra_session", issued[0].Name)
			cleared := false
			for _, c := range rec.Result().Cookies() {
				if c.Name == attempt.cookie.Name && c.MaxAge < 0 {
					cleared = true
					require.Empty(t, c.Value)
					require.Equal(t, attempt.cookie.Path, c.Path)
				}
			}
			require.True(t, cleared)
			require.Equal(t, 400, callback(attempt.state, attempt.cookie).Code)
		}
	}
}

func TestBoundOAuthStateRejectsProviderMismatchAndExpiry(t *testing.T) {
	pool := oauthDB(t)
	token, err := storage.CreateOAuthState(t.Context(), pool, "google", "browser-secret")
	require.NoError(t, err)
	state, err := storage.ConsumeBoundOAuthState(t.Context(), pool, token, "github", "browser-secret")
	require.NoError(t, err)
	require.Nil(t, state)
	state, err = storage.ConsumeBoundOAuthState(t.Context(), pool, token, "google", "different-secret")
	require.NoError(t, err)
	require.Nil(t, state)
	state, err = storage.ConsumeBoundOAuthState(t.Context(), pool, token, "google", "browser-secret")
	require.NoError(t, err)
	require.NotNil(t, state)
	require.Equal(t, "browser-secret", state.Verifier)
	expired, err := storage.CreateOAuthState(t.Context(), pool, "google", "expired-secret")
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), "UPDATE oauth_states SET expires_at=NOW()-interval '1 second' WHERE verifier=$1", "expired-secret")
	require.NoError(t, err)
	state, err = storage.ConsumeBoundOAuthState(t.Context(), pool, expired, "google", "expired-secret")
	require.NoError(t, err)
	require.Nil(t, state)
}

func TestOAuthRedirectDatabaseFailureSetsNoCookie(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://unused@localhost:1/unused?sslmode=disable")
	require.NoError(t, err)
	pool.Close()
	h := routerWithSSOAndPool(pool, mockProvider{name: "google"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/auth/google/redirect", nil))
	require.Equal(t, 500, rec.Code)
	require.Equal(t, "internal error\n", rec.Body.String())
	require.Empty(t, rec.Result().Cookies())
	require.Empty(t, rec.Header().Get("Location"))
}

func TestOAuthProviderErrorClearsMatchedBinding(t *testing.T) {
	pool := oauthDB(t)
	state, err := storage.CreateOAuthState(t.Context(), pool, "google", "browser-secret")
	require.NoError(t, err)
	h := routerWithSSOAndPool(pool, mockProviderExchangeErr{name: "google"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, boundOAuthRequest("/api/auth/google/callback?state="+state+"&code=bad-code", "browser-secret", false))
	require.Equal(t, 401, rec.Code)
	require.Empty(t, activeOAuthResponseCookies(rec))
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	require.Equal(t, oauthBindingCookie(state, "", false).Name, cookies[0].Name)
	require.Less(t, cookies[0].MaxAge, 0)
	require.Empty(t, cookies[0].Value)
}
