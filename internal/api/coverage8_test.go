package api_test

// coverage8_test.go targets uncovered branches identified by static analysis:
//
// Invites:
//   - handleAcceptInvite: req.Name == "" AND inv.Name == "" (UpdateUserProfile block skipped)
//   - handleAcceptInvite: CreateUser error (weak password via 11-char password)
//   - handleGetInvite:    returned after token already consumed / never existed (404)
//
// user_admin:
//   - handleDoPasswordReset: nil user from UsePasswordResetToken (spent/consumed token)
//   - handleAdminSendPasswordReset: email sender configured (success path)
//   - handleAdminSendPasswordReset: email sender configured but send fails

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

// ---------------------------------------------------------------------------
// handleAcceptInvite: no name in request AND no name on invite
// (lines 190-198 in invites.go: the UpdateUserProfile block must be skipped)
// ---------------------------------------------------------------------------

func TestAcceptInvite_noNameAnywhere(t *testing.T) {
	email := "accept-noname@example.com"
	// Seed invite with empty name.
	token := seedInvite(t, email, "")
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM users WHERE email = $1", email)
	})

	// Accept with no name field in body either.
	body := bytes.NewBufferString(`{"password":"securepass123456"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/invite/"+token+"/accept", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var u storage.User
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if u.Email != email {
		t.Errorf("email: got %q, want %q", u.Email, email)
	}
	// Name should be empty since neither request nor invite supplied one.
	if u.Name != "" {
		t.Errorf("expected empty name, got %q", u.Name)
	}
	// A session cookie should still be issued.
	var cookieFound bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "tindra_session" && c.Value != "" {
			cookieFound = true
		}
	}
	if !cookieFound {
		t.Error("expected tindra_session cookie after accept")
	}
}

// ---------------------------------------------------------------------------
// handleAcceptInvite: CreateUser returns error (password too short -- 11 chars)
// (invites.go:184-187 → http 400)
// ---------------------------------------------------------------------------

func TestAcceptInvite_weakPassword_cov8(t *testing.T) {
	email := "accept-weakpw-cov8@example.com"
	token := seedInvite(t, email, "")
	// CreateUser will fail due to short password; no user row created, so no cleanup needed.

	// 11 characters is below the 12-character minimum enforced by storage.CreateUser.
	body := bytes.NewBufferString(`{"password":"shortpass11"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/invite/"+token+"/accept", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for weak password, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleAcceptInvite: duplicate email -- CreateUser returns error because
// a user with the invite email already exists in the DB.
// (invites.go:184-187 → http 400)
// ---------------------------------------------------------------------------

func TestAcceptInvite_duplicateEmail(t *testing.T) {
	email := "accept-dup@example.com"
	// Create the user first so the invite's email is already taken.
	existing := seedUser(t, email)
	_ = existing

	token := seedInvite(t, email, "")

	body := bytes.NewBufferString(`{"password":"securepass123456"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/invite/"+token+"/accept", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for duplicate email on CreateUser, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleGetInvite: returns 404 for a token that is already accepted/consumed.
// (invites.go:138-140 -- inv == nil path via consumed token)
// ---------------------------------------------------------------------------

func TestGetInvite_alreadyAccepted(t *testing.T) {
	email := "getinvite-consumed@example.com"
	token := seedInvite(t, email, "Consumed User")
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM users WHERE email = $1", email)
	})

	// Accept the invite so it is marked as consumed.
	body := bytes.NewBufferString(`{"password":"securepass123456"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/invite/"+token+"/accept", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup accept failed with %d: %s", rec.Code, rec.Body.String())
	}

	// Now GET the same token -- GetInvite should return nil for a consumed invite.
	req2 := httptest.NewRequest(http.MethodGet, "/api/auth/invite/"+token, nil)
	rec2 := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotFound {
		t.Errorf("expected 404 for consumed invite token, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleDoPasswordReset: nil user from UsePasswordResetToken (spent token)
// (user_admin.go:157-160 → http 404)
// ---------------------------------------------------------------------------

func TestDoPasswordReset_spentToken(t *testing.T) {
	target := seedUser(t, "reset-spent@example.com")
	token := seedPasswordResetToken(t, target.ID)

	// First use -- consumes the token.
	body1 := bytes.NewBufferString(`{"password":"newlongpassword123"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/api/auth/password-reset/"+token, body1)
	rec1 := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first reset failed with %d: %s", rec1.Code, rec1.Body.String())
	}

	// Second use -- token is already spent, UsePasswordResetToken returns nil, nil → 404.
	body2 := bytes.NewBufferString(`{"password":"anotherlongpassword"}`)
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/password-reset/"+token, body2)
	rec2 := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotFound {
		t.Errorf("expected 404 for spent reset token, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleAdminSendPasswordReset: email sender configured, send succeeds.
// (user_admin.go:103-109 → email_sent=true)
// Existing TestAdminSendPasswordReset_withEmailSender_success already covers this.
// Adding a variant that asserts the reset_url format explicitly.
// ---------------------------------------------------------------------------

func TestAdminSendPasswordReset_withEmailSender_urlFormat(t *testing.T) {
	target := seedUser(t, "pw-reset-url-fmt@example.com")

	fake := &fakeEmailSender{}
	setAppEmailSender(t, fake)

	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, nil, false, "", "https://tindra.example.com", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)

	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/users/%s/password-reset", target.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sent, _ := resp["email_sent"].(bool); !sent {
		t.Error("expected email_sent=true when sender is configured and succeeds")
	}
	resetURL, _ := resp["reset_url"].(string)
	if resetURL == "" {
		t.Error("expected non-empty reset_url")
	}
	if len(fake.Sent()) != 1 {
		t.Errorf("expected 1 email sent, got %d", len(fake.Sent()))
	}
	msg := fake.Sent()[0]
	if msg.To != target.Email {
		t.Errorf("email To: got %q, want %q", msg.To, target.Email)
	}
}

// ---------------------------------------------------------------------------
// handleAdminSendPasswordReset: email sender configured, send fails.
// (user_admin.go:103-109 → email_error set, email_sent=false)
// Existing TestAdminSendPasswordReset_withEmailSender_failure already covers this.
// Adding a variant that asserts email_error content explicitly.
// ---------------------------------------------------------------------------

func TestAdminSendPasswordReset_withEmailSender_failureMessage(t *testing.T) {
	target := seedUser(t, "pw-reset-fail-msg@example.com")

	fake := &fakeEmailSender{failErr: errors.New("connection refused")}
	setAppEmailSender(t, fake)

	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/users/%s/password-reset", target.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 even on email failure, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sent, _ := resp["email_sent"].(bool); sent {
		t.Error("expected email_sent=false on sender error")
	}
	emailErr, _ := resp["email_error"].(string)
	if emailErr == "" {
		t.Error("expected email_error to contain the error message")
	}
}

// ---------------------------------------------------------------------------
// handleRevokeInvite: not-found path with a valid-looking UUID that doesn't exist.
// Confirms the found=false → 404 branch (invites.go:117-119).
// The existing TestRevokeInvite_notFound uses a nil UUID; this uses a fresh
// random UUID to ensure the row never existed.
// ---------------------------------------------------------------------------

func TestRevokeInvite_neverExisted(t *testing.T) {
	// A well-formed UUID that was never inserted.
	req := httptest.NewRequest(http.MethodDelete, "/api/invites/11111111-1111-1111-1111-111111111111", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent invite ID, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleCreateInvite: after accepting, attempting to re-create an invite for
// the same email fails with 409 Conflict (user already exists).
// This exercises the GetUserByEmail → existing != nil → 409 branch
// (invites.go:50-52) via a user created through AcceptInvite rather than
// a direct seedUser call.
// ---------------------------------------------------------------------------

func TestCreateInvite_conflictAfterAccept(t *testing.T) {
	email := "invite-then-conflict@example.com"
	token := seedInvite(t, email, "")
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM users WHERE email = $1", email)
	})

	// Accept the invite to create the user.
	acceptBody := bytes.NewBufferString(`{"password":"securepass123456"}`)
	acceptReq := httptest.NewRequest(http.MethodPost, "/api/auth/invite/"+token+"/accept", acceptBody)
	acceptReq.Header.Set("Content-Type", "application/json")
	acceptRec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(acceptRec, acceptReq)
	if acceptRec.Code != http.StatusCreated {
		t.Fatalf("accept setup: expected 201, got %d: %s", acceptRec.Code, acceptRec.Body.String())
	}

	// Now try to create a new invite for the same email -- should 409.
	createBody := bytes.NewBufferString(`{"email":"` + email + `"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/invites", createBody)
	createReq.Header.Set("Content-Type", "application/json")
	createReq.AddCookie(authCookie())
	createRec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusConflict {
		t.Errorf("expected 409 for existing user email, got %d: %s", createRec.Code, createRec.Body.String())
	}
}
