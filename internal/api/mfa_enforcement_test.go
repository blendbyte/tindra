package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestRequiredMFAEnrollment(t *testing.T) {
	pool := oauthDB(t)
	user, err := storage.CreateOAuthUser(t.Context(), pool, uuid.NewString()+"@mfa.example.com")
	if err != nil {
		t.Fatal(err)
	}
	session, err := storage.CreateSession(t.Context(), pool, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	h := NewRouter(pool, nil, nil, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: "tindra_session", Value: session.Token})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, endpoint := range []struct{ method, path string }{
		{"GET", "/api/projects"}, {"POST", "/api/projects"}, {"GET", "/api/tokens"},
		{"GET", "/api/projects/example/tokens"}, {"POST", "/mcp"}, {"DELETE", "/api/auth/mfa"},
	} {
		rec := request(endpoint.method, endpoint.path, "{}")
		if rec.Code != 403 || rec.Header().Get("X-Tindra-MFA-Required") != "setup" {
			t.Fatalf("%s %s: %d %s", endpoint.method, endpoint.path, rec.Code, rec.Body.String())
		}
	}
	for _, path := range []string{"/api/me", "/api/config"} {
		if rec := request("GET", path, ""); rec.Code != 200 {
			t.Fatalf("%s: %d", path, rec.Code)
		}
	}
	rec := request("GET", "/api/auth/mfa/setup", "")
	if rec.Code != 200 {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}
	var setup struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &setup); err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rec := request("POST", "/api/auth/mfa/confirm", `{"code":"`+code+`"}`); rec.Code != 200 {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body.String())
	}
	if rec := request("GET", "/api/projects", ""); rec.Code != 200 {
		t.Fatalf("enrolled access: %d %s", rec.Code, rec.Body.String())
	}
	if err := storage.DisableMFA(t.Context(), pool, user.ID); err != nil {
		t.Fatal(err)
	}
	if rec := request("GET", "/api/projects", ""); rec.Code != 403 {
		t.Fatalf("disabled MFA: %d", rec.Code)
	}
	if rec := request("POST", "/api/auth/logout", ""); rec.Code != 200 {
		t.Fatalf("logout: %d", rec.Code)
	}
}

func TestMFAEnrollmentAllowlist(t *testing.T) {
	for _, required := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			for _, endpoint := range []struct {
				method, path string
				allowed      bool
			}{
				{"GET", "/api/me", true}, {"PATCH", "/api/me", true}, {"PATCH", "/api/me/password", true},
				{"GET", "/api/auth/mfa/setup", true}, {"POST", "/api/auth/mfa/confirm", true},
				{"POST", "/api/me", false}, {"GET", "/api/me/password", false}, {"GET", "/api/users", false},
			} {
				ro := &router{requireMFA: required}
				got := ro.allowMFAEnrollmentRequest(httptest.NewRecorder(), httptest.NewRequest(endpoint.method, endpoint.path, nil), &storage.SessionIdentity{MFAEnabled: enabled})
				if want := !required || enabled || endpoint.allowed; got != want {
					t.Fatalf("required=%v enabled=%v %s %s: %v", required, enabled, endpoint.method, endpoint.path, got)
				}
			}
		}
	}
}

func TestOAuthMFAChallenge(t *testing.T) {
	pool := oauthDB(t)
	for _, required := range []bool{false, true} {
		email := uuid.NewString() + "@mfa.example.com"
		user, err := storage.CreateOAuthUser(t.Context(), pool, email)
		if err != nil {
			t.Fatal(err)
		}
		key, err := totp.Generate(totp.GenerateOpts{Issuer: "Tindra", AccountName: email})
		if err != nil {
			t.Fatal(err)
		}
		if err := storage.StoreMFASecret(t.Context(), pool, user.ID, key.Secret()); err != nil {
			t.Fatal(err)
		}
		if err := storage.EnableMFA(t.Context(), pool, user.ID); err != nil {
			t.Fatal(err)
		}
		h := NewRouter(pool, nil, nil, nil, nil, nil, []oauthProvider{admissionProvider{mockProvider{name: "admission"}, email}}, true, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, required, nil)
		state, err := storage.CreateOAuthState(t.Context(), pool, "admission", "verifier")
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/auth/admission/callback?state="+state+"&code=code", nil))
		if rec.Code != 302 || rec.Header().Get("Location") != "/login?mfa=1" {
			t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
		}
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != "tindra_mfa" {
			t.Fatalf("unexpected callback cookies: %v", cookies)
		}
		challenge := cookies[0]
		if !challenge.HttpOnly || !challenge.Secure || challenge.SameSite != http.SameSiteStrictMode || challenge.Path != "/api/auth/mfa/verify" || challenge.MaxAge != 600 {
			t.Fatal("unsafe challenge cookie attributes")
		}
		var sessions int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE user_id=$1", user.ID).Scan(&sessions); err != nil {
			t.Fatal(err)
		}
		if sessions != 0 {
			t.Fatal("OAuth issued session before MFA")
		}
		verify := func(body string) *httptest.ResponseRecorder {
			req := httptest.NewRequest("POST", "/api/auth/mfa/verify", strings.NewReader(body))
			req.AddCookie(challenge)
			result := httptest.NewRecorder()
			h.ServeHTTP(result, req)
			return result
		}
		if bad := verify(`{"code":"invalid"}`); bad.Code != 401 || len(bad.Result().Cookies()) != 0 {
			t.Fatalf("invalid code: %d", bad.Code)
		}
		code, err := totp.GenerateCode(key.Secret(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		body := `{"code":"` + code + `"}`
		good := verify(body)
		if good.Code != 200 {
			t.Fatalf("verify: %d %s", good.Code, good.Body.String())
		}
		issued, cleared := false, false
		for _, cookie := range good.Result().Cookies() {
			if cookie.Name == "tindra_session" && cookie.Value != "" {
				issued = true
			}
			if cookie.Name == "tindra_mfa" && cookie.MaxAge < 0 && cookie.Path == challenge.Path {
				cleared = true
			}
		}
		if !issued || !cleared {
			t.Fatal("successful verification must issue session and clear challenge")
		}
		if replay := verify(body); replay.Code != 401 {
			t.Fatalf("replay: %d", replay.Code)
		}
	}
}

func TestUnenrolledOAuthSessionIsRestricted(t *testing.T) {
	pool := oauthDB(t)
	email := uuid.NewString() + "@enrollment.example.com"
	if _, err := storage.CreateInvite(t.Context(), pool, "", email, "Invited"); err != nil {
		t.Fatal(err)
	}
	h := NewRouter(pool, nil, nil, nil, nil, nil, []oauthProvider{admissionProvider{mockProvider{name: "admission"}, email}}, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
	state, err := storage.CreateOAuthState(t.Context(), pool, "admission", "verifier")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/auth/admission/callback?state="+state+"&code=code", nil))
	if rec.Code != 302 {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "tindra_session" {
		t.Fatal("missing enrollment session")
	}
	req := httptest.NewRequest("GET", "/api/projects", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 || rec.Header().Get("X-Tindra-MFA-Required") != "setup" {
		t.Fatalf("unenrolled SSO access: %d", rec.Code)
	}
}

func TestMFAVerifyDatabaseFailures(t *testing.T) {
	pool := oauthDB(t)
	user, err := storage.CreateOAuthUser(t.Context(), pool, uuid.NewString()+"@mfa-fail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Tindra", AccountName: user.Email})
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.StoreMFASecret(t.Context(), pool, user.ID, key.Secret()); err != nil {
		t.Fatal(err)
	}
	if err := storage.EnableMFA(t.Context(), pool, user.ID); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"SELECT user_id FROM mfa_challenges", "SELECT mfa_secret", "DELETE FROM mfa_challenges", "INSERT INTO sessions"} {
		t.Run(stage, func(t *testing.T) {
			token, err := storage.CreateMFAChallenge(t.Context(), pool, user.ID)
			if err != nil {
				t.Fatal(err)
			}
			trace := &cancelDashboardQuery{match: stage}
			cfg := pool.Config()
			cfg.ConnConfig.Tracer = trace
			failing, err := pgxpool.NewWithConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer failing.Close()
			ro := &router{pool: failing}
			code, err := totp.GenerateCode(key.Secret(), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", "/api/auth/mfa/verify", strings.NewReader(`{"code":"`+code+`"}`))
			req.AddCookie(&http.Cookie{Name: "tindra_mfa", Value: token})
			rec := httptest.NewRecorder()
			ro.handleMFAVerify(rec, req)
			if !trace.hit.Load() {
				t.Fatal("target query was not reached")
			}
			if rec.Code != 500 || rec.Body.String() != "internal error\n" {
				t.Fatalf("failure response: %d %s", rec.Code, rec.Body.String())
			}
			if len(rec.Result().Cookies()) != 0 {
				t.Fatal("failed verification issued cookies")
			}
			var sessions int
			if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE user_id=$1", user.ID).Scan(&sessions); err != nil {
				t.Fatal(err)
			}
			if sessions != 0 {
				t.Fatal("failed verification created a session")
			}
		})
	}
}

func TestMFAVerifyChallengeInput(t *testing.T) {
	for _, body := range []string{`{`, `{}`, `{"code":"123456"}`} {
		rec := httptest.NewRecorder()
		(&router{}).handleMFAVerify(rec, httptest.NewRequest("POST", "/api/auth/mfa/verify", strings.NewReader(body)))
		if rec.Code != 400 {
			t.Fatalf("missing challenge/code: %d", rec.Code)
		}
	}
	pool := oauthDB(t)
	user, err := storage.CreateOAuthUser(t.Context(), pool, uuid.NewString()+"@precedence.example.com")
	if err != nil {
		t.Fatal(err)
	}
	token, err := storage.CreateMFAChallenge(t.Context(), pool, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/auth/mfa/verify", strings.NewReader(`{"mfa_token":"invalid","code":"123456"}`))
	req.AddCookie(&http.Cookie{Name: "tindra_mfa", Value: token})
	rec := httptest.NewRecorder()
	(&router{pool: pool}).handleMFAVerify(rec, req)
	if rec.Code != 401 || rec.Body.String() != "invalid or expired MFA token\n" {
		t.Fatalf("body challenge must take precedence: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAuthenticationDatabaseFailures(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://unused@localhost:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	ro := &router{pool: pool, requireMFA: true}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("failed authentication reached handler") })
	for _, scenario := range []struct {
		name    string
		handler http.Handler
		bearer  bool
	}{
		{"session", ro.requireAuth(next), false},
		{"session-only", ro.requireSessionAuth(next), false},
		{"bearer", ro.requireAuth(next), true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/projects", nil)
			req.AddCookie(&http.Cookie{Name: "tindra_session", Value: "session"})
			if scenario.bearer {
				req.Header.Set("Authorization", "Bearer token")
			}
			rec := httptest.NewRecorder()
			scenario.handler.ServeHTTP(rec, req)
			if rec.Code != 500 || rec.Body.String() != "internal error\n" {
				t.Fatalf("unexpected response: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
