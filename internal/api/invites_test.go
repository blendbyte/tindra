package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

// fakeEmailSender captures sent messages; shared by invite and user_admin tests.
type fakeEmailSender struct {
	mu      sync.Mutex
	sent    []alerts.EmailMessage
	failErr error
}

func (f *fakeEmailSender) Send(_ context.Context, msg alerts.EmailMessage) error {
	if f.failErr != nil {
		return f.failErr
	}
	f.mu.Lock()
	f.sent = append(f.sent, msg)
	f.mu.Unlock()
	return nil
}

func (f *fakeEmailSender) Sent() []alerts.EmailMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]alerts.EmailMessage, len(f.sent))
	copy(cp, f.sent)
	return cp
}

// setAppEmailSender replaces api.AppEmailSender for one test, then restores.
func setAppEmailSender(t *testing.T, s alerts.EmailSender) {
	t.Helper()
	prev := api.AppEmailSender
	api.AppEmailSender = s
	t.Cleanup(func() { api.AppEmailSender = prev })
}

// limitedHandler creates a router with userLimit set.
func limitedHandler(userLimit int) http.Handler {
	return api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "https://example.com", "", "", 0, 0, 0, userLimit, 0, 0, nil, false, true, nil)
}

// seedInvite creates an invite record and removes it on cleanup. Returns the plaintext token.
func seedInvite(t *testing.T, email, name string) string {
	t.Helper()
	token, err := storage.CreateInvite(context.Background(), testPool, testUser.ID, email, name)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	inv, err := storage.GetInvite(context.Background(), testPool, token)
	if err != nil || inv == nil {
		t.Fatalf("get invite for cleanup setup: %v", err)
	}
	t.Cleanup(func() {
		storage.DeleteInvite(context.Background(), testPool, inv.ID)
	})
	return token
}

// --- handleListInvites ---

func TestListInvites_empty(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM invites")

	req := httptest.NewRequest(http.MethodGet, "/api/invites", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var result []any
	json.NewDecoder(rec.Body).Decode(&result)
	if len(result) != 0 {
		t.Errorf("expected empty list, got %d items", len(result))
	}
}

func TestListInvites_withPending(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM invites")
	seedInvite(t, "list-pending@example.com", "Pending")

	req := httptest.NewRequest(http.MethodGet, "/api/invites", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var result []map[string]any
	json.NewDecoder(rec.Body).Decode(&result)
	if len(result) == 0 {
		t.Error("expected at least one pending invite")
	}
}

func TestListInvites_forbidden(t *testing.T) {
	roCookie := makeReadOnlyUser(t, "ro-list-invites@example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/invites", nil)
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

// --- handleCreateInvite ---

func TestCreateInvite_success(t *testing.T) {
	email := "create-invite-ok@example.com"
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM invites WHERE email = $1", email)
	})

	body := bytes.NewBufferString(`{"email":"` + email + `","name":"New User"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/invites", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["invite_url"] == "" {
		t.Error("expected non-empty invite_url")
	}
	if sent, _ := resp["email_sent"].(bool); sent {
		t.Error("email_sent should be false when AppEmailSender is nil")
	}
	if configured, _ := resp["email_configured"].(bool); configured {
		t.Error("email_configured should be false when AppEmailSender is nil")
	}
}

func TestCreateInvite_missingEmail(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/invites", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestCreateInvite_badJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/invites", bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestCreateInvite_emailAlreadyExists(t *testing.T) {
	// testUser's email already exists in the DB.
	body := bytes.NewBufferString(`{"email":"test@example.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/invites", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateInvite_userLimitReached(t *testing.T) {
	// userLimit=1 and testUser already exists → count(1) >= limit(1) → 429.
	body := bytes.NewBufferString(`{"email":"limited-invite@example.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/invites", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	limitedHandler(1).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateInvite_withEmailSender_success(t *testing.T) {
	email := "invite-email-ok@example.com"
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM invites WHERE email = $1", email)
	})

	fake := &fakeEmailSender{}
	setAppEmailSender(t, fake)

	body := bytes.NewBufferString(`{"email":"` + email + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/invites", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	// Use a handler with publicURL so the invite_url has a real base.
	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "https://tindra.example.com", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if sent, _ := resp["email_sent"].(bool); !sent {
		t.Error("expected email_sent=true when sender is configured and succeeds")
	}
	if len(fake.Sent()) != 1 {
		t.Errorf("expected 1 email sent, got %d", len(fake.Sent()))
	}
}

func TestCreateInvite_withEmailSender_failure(t *testing.T) {
	email := "invite-email-fail@example.com"
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM invites WHERE email = $1", email)
	})

	fake := &fakeEmailSender{failErr: errors.New("smtp down")}
	setAppEmailSender(t, fake)

	body := bytes.NewBufferString(`{"email":"` + email + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/invites", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	// Still 201 - email failure doesn't block invite creation.
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 even when email fails, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if sent, _ := resp["email_sent"].(bool); sent {
		t.Error("expected email_sent=false on sender error")
	}
	if resp["email_error"] == "" {
		t.Error("expected email_error to be set on send failure")
	}
}

func TestCreateInvite_forbidden(t *testing.T) {
	roCookie := makeReadOnlyUser(t, "ro-create-invite@example.com")
	body := bytes.NewBufferString(`{"email":"x@example.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/invites", body)
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

// --- handleRevokeInvite ---

func TestRevokeInvite_success(t *testing.T) {
	token := seedInvite(t, "revoke-me@example.com", "")
	inv, err := storage.GetInvite(context.Background(), testPool, token)
	if err != nil || inv == nil {
		t.Fatalf("get invite: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/invites/"+inv.ID, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRevokeInvite_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/api/invites/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestRevokeInvite_forbidden(t *testing.T) {
	roCookie := makeReadOnlyUser(t, "ro-revoke@example.com")
	req := httptest.NewRequest(http.MethodDelete, "/api/invites/anytoken", nil)
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

// --- handleGetInvite (public) ---

func TestGetInvite_found(t *testing.T) {
	token := seedInvite(t, "get-invite@example.com", "Get User")

	req := httptest.NewRequest(http.MethodGet, "/api/auth/invite/"+token, nil)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["email"] != "get-invite@example.com" {
		t.Errorf("email: got %q", resp["email"])
	}
	if resp["name"] != "Get User" {
		t.Errorf("name: got %q", resp["name"])
	}
}

func TestGetInvite_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/auth/invite/nosuchtoken", nil)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- handleAcceptInvite (public) ---

func TestAcceptInvite_success(t *testing.T) {
	email := "accept-ok@example.com"
	token := seedInvite(t, email, "Accept User")
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM users WHERE email = $1", email)
	})

	body := bytes.NewBufferString(`{"password":"securepass123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/invite/"+token+"/accept", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var cookieFound bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "tindra_session" && c.Value != "" {
			cookieFound = true
		}
	}
	if !cookieFound {
		t.Error("expected tindra_session cookie after accept")
	}
	var u storage.User
	json.NewDecoder(rec.Body).Decode(&u)
	if u.Email != email {
		t.Errorf("email: got %q, want %q", u.Email, email)
	}
}

func TestAcceptInvite_withName(t *testing.T) {
	email := "accept-name@example.com"
	token := seedInvite(t, email, "Invite Name")
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM users WHERE email = $1", email)
	})

	// Provide a name override in the accept request.
	body := bytes.NewBufferString(`{"password":"securepass456","name":"Override Name"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/invite/"+token+"/accept", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var u storage.User
	json.NewDecoder(rec.Body).Decode(&u)
	if u.Name != "Override Name" {
		t.Errorf("name: got %q, want Override Name", u.Name)
	}
}

func TestAcceptInvite_nameFromInvite(t *testing.T) {
	email := "accept-invname@example.com"
	token := seedInvite(t, email, "Invite Provided Name")
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM users WHERE email = $1", email)
	})

	// No name in request - falls back to the invite's name.
	body := bytes.NewBufferString(`{"password":"securepass789"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/invite/"+token+"/accept", body)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var u storage.User
	json.NewDecoder(rec.Body).Decode(&u)
	if u.Name != "Invite Provided Name" {
		t.Errorf("name: got %q, want 'Invite Provided Name'", u.Name)
	}
}

func TestAcceptInvite_missingPassword(t *testing.T) {
	token := seedInvite(t, "accept-nopw@example.com", "")

	req := httptest.NewRequest(http.MethodPost, "/api/auth/invite/"+token+"/accept",
		bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestAcceptInvite_invalidToken(t *testing.T) {
	body := bytes.NewBufferString(`{"password":"securepass123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/invite/nosuchtoken/accept", body)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestAcceptInvite_userLimitReached(t *testing.T) {
	email := "accept-limited@example.com"
	token := seedInvite(t, email, "")

	// userLimit=1; testUser already exists → count >= limit → 429.
	body := bytes.NewBufferString(`{"password":"securepass123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/invite/"+token+"/accept", body)
	rec := httptest.NewRecorder()
	limitedHandler(1).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d: %s", rec.Code, rec.Body.String())
	}
}
