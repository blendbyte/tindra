package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func authHandler() http.Handler {
	return api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
}

func TestHandleLogin_success(t *testing.T) {
	body := bytes.NewBufferString(`{"email":"test@example.com","password":"testpassword"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var cookieFound bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "tindra_session" && c.Value != "" {
			cookieFound = true
		}
	}
	if !cookieFound {
		t.Error("expected tindra_session cookie to be set")
	}
}

func TestHandleLogin_wrongPassword(t *testing.T) {
	body := bytes.NewBufferString(`{"email":"test@example.com","password":"wrongpassword"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleLogin_unknownUser(t *testing.T) {
	body := bytes.NewBufferString(`{"email":"nobody@example.com","password":"pw"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleLogin_invalidBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString("not json"))
	rec := httptest.NewRecorder()

	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleLogout(t *testing.T) {
	// Use a fresh session so we don't invalidate the shared testSession
	sess, err := storage.CreateSession(context.Background(), testPool, testUser.ID)
	if err != nil {
		t.Fatalf("create temp session: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "tindra_session", Value: sess.Token})
	rec := httptest.NewRecorder()

	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	for _, c := range rec.Result().Cookies() {
		if c.Name == "tindra_session" && c.MaxAge != -1 {
			t.Error("expected tindra_session cookie to be cleared")
		}
	}
}

func TestHandleLogout_noCookie(t *testing.T) {
	// Logout without a session cookie - should still return 200.
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for logout with no cookie, got %d", rec.Code)
	}
}

func TestRequireAuth_noSession(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/issues", nil)
	rec := httptest.NewRecorder()

	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth, got %d", rec.Code)
	}
}

func TestRequireAuth_invalidSessionCookie(t *testing.T) {
	// Cookie present but session doesn't exist in DB → 401
	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/issues", nil)
	req.AddCookie(&http.Cookie{Name: "tindra_session", Value: "not-a-valid-session-token"})
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid session cookie, got %d", rec.Code)
	}
}

func TestRequireSessionAuth_invalidSessionCookie(t *testing.T) {
	// requireSessionAuth with an invalid cookie → 401
	req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/tokens",
		bytes.NewBufferString(`{"name":"x"}`))
	req.AddCookie(&http.Cookie{Name: "tindra_session", Value: "not-a-valid-token"})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid session, got %d", rec.Code)
	}
}

func TestHandleLogin_emptyFields(t *testing.T) {
	body := bytes.NewBufferString(`{"email":"","password":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty email/password, got %d", rec.Code)
	}
}

// --- MFA setup ---

func TestMFASetup_success(t *testing.T) {
	// Clean any pending secret first so the test is idempotent.
	testPool.Exec(context.Background(), "UPDATE users SET mfa_secret = NULL WHERE id = $1", testUser.ID)
	defer testPool.Exec(context.Background(), "UPDATE users SET mfa_secret = NULL WHERE id = $1", testUser.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/mfa/setup", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Secret string `json:"secret"`
		URI    string `json:"uri"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Secret == "" {
		t.Error("expected non-empty secret")
	}
	if resp.URI == "" {
		t.Error("expected non-empty uri")
	}
}

// --- MFA confirm ---

func TestMFAConfirm_wrongCode(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXP"
	testPool.Exec(context.Background(), "UPDATE users SET mfa_secret = $1 WHERE id = $2", secret, testUser.ID)
	defer testPool.Exec(context.Background(), "UPDATE users SET mfa_secret = NULL WHERE id = $1", testUser.ID)

	body := bytes.NewBufferString(`{"code":"000000"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/confirm", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong code, got %d", rec.Code)
	}
}

// --- MFA disable ---

func TestMFADisable_success(t *testing.T) {
	body := bytes.NewBufferString(`{"password":"testpassword"}`)
	req := httptest.NewRequest(http.MethodDelete, "/api/auth/mfa", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for correct password, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- MFA verify success path ---

func TestMFAVerify_success(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXP"
	testPool.Exec(context.Background(), "UPDATE users SET mfa_secret = $1 WHERE id = $2", secret, testUser.ID)
	defer testPool.Exec(context.Background(), "UPDATE users SET mfa_secret = NULL WHERE id = $1", testUser.ID)

	token, err := storage.CreateMFAChallenge(context.Background(), testPool, testUser.ID)
	if err != nil {
		t.Fatalf("create challenge: %v", err)
	}

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}

	b, _ := json.Marshal(map[string]string{"mfa_token": token, "code": code})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/verify", bytes.NewBuffer(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for valid TOTP, got %d: %s", rec.Code, rec.Body.String())
	}
	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "tindra_session" && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("expected tindra_session cookie after successful MFA verify")
	}
}
