package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

// seedUser creates a user and removes them on cleanup.
func seedUser(t *testing.T, email string) *storage.User {
	t.Helper()
	u, err := storage.CreateUser(context.Background(), testPool, email, "longpassword123")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", u.ID)
	})
	return u
}

// seedPasswordResetToken creates a reset token for the given user and cleans it up.
func seedPasswordResetToken(t *testing.T, userID string) string {
	t.Helper()
	token, err := storage.CreatePasswordResetToken(context.Background(), testPool, userID)
	if err != nil {
		t.Fatalf("create password reset token: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM password_reset_tokens WHERE token = $1", token)
	})
	return token
}

// --- handleAdminDisableMFA ---

func TestAdminDisableMFA_success(t *testing.T) {
	target := seedUser(t, "mfa-disable-ok@example.com")

	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/users/%s/mfa", target.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminDisableMFA_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete,
		"/api/users/00000000-0000-0000-0000-000000000000/mfa", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestAdminDisableMFA_forbidden(t *testing.T) {
	roCookie := makeReadOnlyUser(t, "ro-mfa-disable@example.com")
	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/users/%s/mfa", testUser.ID), nil)
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

// --- handleAdminSetPassword ---

func TestAdminSetPassword_success(t *testing.T) {
	target := seedUser(t, "set-pw-ok@example.com")

	body := bytes.NewBufferString(`{"password":"verylongpassword1"}`)
	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/api/users/%s/password", target.ID), body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminSetPassword_missingPassword(t *testing.T) {
	body := bytes.NewBufferString(`{"password":""}`)
	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/api/users/%s/password", testUser.ID), body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty password, got %d", rec.Code)
	}
}

func TestAdminSetPassword_badJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/api/users/%s/password", testUser.ID),
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad JSON, got %d", rec.Code)
	}
}

func TestAdminSetPassword_notFound(t *testing.T) {
	body := bytes.NewBufferString(`{"password":"verylongpassword1"}`)
	req := httptest.NewRequest(http.MethodPut,
		"/api/users/00000000-0000-0000-0000-000000000000/password", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminSetPassword_tooShort(t *testing.T) {
	target := seedUser(t, "set-pw-short@example.com")

	body := bytes.NewBufferString(`{"password":"short"}`)
	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/api/users/%s/password", target.ID), body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for too-short password, got %d", rec.Code)
	}
}

func TestAdminSetPassword_forbidden(t *testing.T) {
	roCookie := makeReadOnlyUser(t, "ro-set-pw@example.com")
	body := bytes.NewBufferString(`{"password":"verylongpassword1"}`)
	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/api/users/%s/password", testUser.ID), body)
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

// --- handleAdminSendPasswordReset ---

func TestAdminSendPasswordReset_success_noEmail(t *testing.T) {
	target := seedUser(t, "pw-reset-noemail@example.com")

	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/users/%s/password-reset", target.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	// Use handler with a publicURL so reset_url is absolute.
	api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "https://tindra.example.com", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil).
		ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resetURL, _ := resp["reset_url"].(string); resetURL == "" {
		t.Error("expected non-empty reset_url")
	}
	if emailSent, _ := resp["email_sent"].(bool); emailSent {
		t.Error("email_sent should be false when AppEmailSender is nil")
	}
}

func TestAdminSendPasswordReset_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		"/api/users/00000000-0000-0000-0000-000000000000/password-reset", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestAdminSendPasswordReset_withEmailSender_success(t *testing.T) {
	target := seedUser(t, "pw-reset-email-ok@example.com")

	fake := &fakeEmailSender{}
	setAppEmailSender(t, fake)

	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/users/%s/password-reset", target.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if emailSent, _ := resp["email_sent"].(bool); !emailSent {
		t.Error("expected email_sent=true when sender succeeds")
	}
	if len(fake.Sent()) != 1 {
		t.Errorf("expected 1 email, got %d", len(fake.Sent()))
	}
}

func TestAdminSendPasswordReset_withEmailSender_failure(t *testing.T) {
	target := seedUser(t, "pw-reset-email-fail@example.com")

	fake := &fakeEmailSender{failErr: errors.New("smtp down")}
	setAppEmailSender(t, fake)

	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/users/%s/password-reset", target.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 even when email fails, got %d", rec.Code)
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if emailSent, _ := resp["email_sent"].(bool); emailSent {
		t.Error("email_sent should be false on sender error")
	}
	if resp["email_error"] == nil {
		t.Error("expected email_error to be set on send failure")
	}
}

func TestAdminSendPasswordReset_forbidden(t *testing.T) {
	roCookie := makeReadOnlyUser(t, "ro-pw-reset@example.com")
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/users/%s/password-reset", testUser.ID), nil)
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

// --- handleGetPasswordReset ---

func TestGetPasswordReset_valid(t *testing.T) {
	token := seedPasswordResetToken(t, testUser.ID)

	req := httptest.NewRequest(http.MethodGet,
		"/api/auth/password-reset/"+token, nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["email"] != testUser.Email {
		t.Errorf("email: got %q, want %q", resp["email"], testUser.Email)
	}
}

func TestGetPasswordReset_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/auth/password-reset/nosuchtoken", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- handleDoPasswordReset ---

func TestDoPasswordReset_success(t *testing.T) {
	target := seedUser(t, "do-reset-ok@example.com")
	token := seedPasswordResetToken(t, target.ID)

	body := bytes.NewBufferString(`{"password":"newlongpassword1"}`)
	req := httptest.NewRequest(http.MethodPost,
		"/api/auth/password-reset/"+token, body)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

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
		t.Error("expected tindra_session cookie after password reset")
	}
	var u storage.User
	json.NewDecoder(rec.Body).Decode(&u)
	if u.Email != target.Email {
		t.Errorf("email: got %q, want %q", u.Email, target.Email)
	}
}

func TestDoPasswordReset_missingPassword(t *testing.T) {
	token := seedPasswordResetToken(t, testUser.ID)

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost,
		"/api/auth/password-reset/"+token, body)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing password, got %d", rec.Code)
	}
}

func TestDoPasswordReset_badJSON(t *testing.T) {
	token := seedPasswordResetToken(t, testUser.ID)

	req := httptest.NewRequest(http.MethodPost,
		"/api/auth/password-reset/"+token,
		bytes.NewBufferString("not json"))
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad JSON, got %d", rec.Code)
	}
}

func TestDoPasswordReset_invalidToken(t *testing.T) {
	body := bytes.NewBufferString(`{"password":"newlongpassword1"}`)
	req := httptest.NewRequest(http.MethodPost,
		"/api/auth/password-reset/nosuchtoken", body)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for invalid token, got %d", rec.Code)
	}
}

func TestDoPasswordReset_passwordTooShort(t *testing.T) {
	target := seedUser(t, "do-reset-short@example.com")
	token := seedPasswordResetToken(t, target.ID)

	body := bytes.NewBufferString(`{"password":"short"}`)
	req := httptest.NewRequest(http.MethodPost,
		"/api/auth/password-reset/"+token, body)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for too-short password, got %d", rec.Code)
	}
}
