package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"

	"github.com/blendbyte/tindra/internal/storage"
)

const emailChangePassword = "current-password-1234"

// emailChangeUser creates a user with its own session (so tests never consume
// the shared testSession) and returns the user and its cookie.
func emailChangeUser(t *testing.T, withPassword bool) (*storage.User, *http.Cookie) {
	t.Helper()
	ctx := context.Background()
	email := uuid.NewString() + "@email-change.example.com"
	var u *storage.User
	var err error
	if withPassword {
		u, err = storage.CreateUser(ctx, testPool, email, emailChangePassword)
	} else {
		u, err = storage.CreateOAuthUser(ctx, testPool, email)
	}
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	sess, err := storage.CreateSession(ctx, testPool, u.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return u, &http.Cookie{Name: "tindra_session", Value: sess.Token}
}

func patchMe(t *testing.T, cookie *http.Cookie, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, "/api/me", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	return rec
}

func storedEmail(t *testing.T, id string) string {
	t.Helper()
	var email string
	testPool.QueryRow(context.Background(), `SELECT email FROM users WHERE id = $1`, id).Scan(&email)
	return email
}

func TestUpdateMe_emailChangeRequiresPassword(t *testing.T) {
	u, cookie := emailChangeUser(t, true)
	newEmail := uuid.NewString() + "@new.example.com"

	if rec := patchMe(t, cookie, map[string]any{"name": "N", "email": newEmail}); rec.Code != http.StatusBadRequest {
		t.Errorf("missing password: expected 400, got %d", rec.Code)
	}
	if rec := patchMe(t, cookie, map[string]any{"name": "N", "email": newEmail, "current_password": "wrong-password"}); rec.Code != http.StatusForbidden {
		t.Errorf("wrong password: expected 403, got %d", rec.Code)
	}
	if got := storedEmail(t, u.ID); got != u.Email {
		t.Fatalf("email changed without confirmation: %s", got)
	}

	rec := patchMe(t, cookie, map[string]any{"name": "N", "email": strings.ToUpper(newEmail), "current_password": emailChangePassword})
	if rec.Code != http.StatusOK {
		t.Fatalf("correct password: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := storedEmail(t, u.ID); got != newEmail {
		t.Errorf("stored email = %q, want %q", got, newEmail)
	}
}

func TestUpdateMe_sameEmailNeedsNoPassword(t *testing.T) {
	u, cookie := emailChangeUser(t, true)
	if rec := patchMe(t, cookie, map[string]any{"name": "Renamed", "email": " " + strings.ToUpper(u.Email) + " "}); rec.Code != http.StatusOK {
		t.Errorf("unchanged email: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateMe_ssoUserWithoutMFACannotChangeEmail(t *testing.T) {
	_, cookie := emailChangeUser(t, false)
	if rec := patchMe(t, cookie, map[string]any{"email": uuid.NewString() + "@new.example.com"}); rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestUpdateMe_ssoUserConfirmsWithMFACode(t *testing.T) {
	u, cookie := emailChangeUser(t, false)
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Tindra", AccountName: u.Email})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := storage.StoreMFASecret(ctx, testPool, u.ID, key.Secret()); err != nil {
		t.Fatal(err)
	}
	if err := storage.EnableMFA(ctx, testPool, u.ID); err != nil {
		t.Fatal(err)
	}
	newEmail := uuid.NewString() + "@new.example.com"

	wrong := ""
	for n := 0; wrong == ""; n++ {
		if c := fmt.Sprintf("%06d", n); !totp.Validate(c, key.Secret()) {
			wrong = c
		}
	}
	if rec := patchMe(t, cookie, map[string]any{"email": newEmail, "mfa_code": wrong}); rec.Code != http.StatusForbidden {
		t.Errorf("wrong code: expected 403, got %d", rec.Code)
	}
	code, _ := totp.GenerateCode(key.Secret(), time.Now())
	if rec := patchMe(t, cookie, map[string]any{"email": newEmail, "mfa_code": code}); rec.Code != http.StatusOK {
		t.Fatalf("valid code: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := storedEmail(t, u.ID); got != newEmail {
		t.Errorf("stored email = %q, want %q", got, newEmail)
	}
}

func TestUpdateMe_emailWithPendingInviteOrTakenIsConflict(t *testing.T) {
	ctx := context.Background()
	_, cookie := emailChangeUser(t, true)

	invited := uuid.NewString() + "@invited.example.com"
	if _, err := storage.CreateInvite(ctx, testPool, "", invited, "Invited"); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if rec := patchMe(t, cookie, map[string]any{"email": invited, "current_password": emailChangePassword}); rec.Code != http.StatusConflict {
		t.Errorf("pending invite: expected 409, got %d", rec.Code)
	}

	other, _ := emailChangeUser(t, true)
	if rec := patchMe(t, cookie, map[string]any{"email": other.Email, "current_password": emailChangePassword}); rec.Code != http.StatusConflict {
		t.Errorf("taken email: expected 409, got %d", rec.Code)
	}
}
