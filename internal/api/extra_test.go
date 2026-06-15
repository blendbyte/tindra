package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

// globalHandler is a convenience alias - the router handles every route.
func globalHandler() http.Handler {
	return issuesHandler()
}

// --- handleGetMe ---

func TestGetMe_authenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var u storage.User
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if u.Email != "test@example.com" {
		t.Errorf("email: got %q", u.Email)
	}
}

func TestGetMe_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleUpdateMe ---

func TestUpdateMe_success(t *testing.T) {
	body := bytes.NewBufferString(`{"name":"Alice","email":"test@example.com"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/me", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var u storage.User
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if u.Email != "test@example.com" {
		t.Errorf("email: got %q", u.Email)
	}
}

func TestUpdateMe_missingEmail(t *testing.T) {
	body := bytes.NewBufferString(`{"name":"Alice","email":""}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/me", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing email, got %d", rec.Code)
	}
}

func TestUpdateMe_badBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/me", bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", rec.Code)
	}
}

func TestUpdateMe_withTimezone(t *testing.T) {
	body := bytes.NewBufferString(`{"email":"test@example.com","timezone":"America/New_York"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/me", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var u storage.User
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if u.Timezone != "America/New_York" {
		t.Errorf("timezone: got %q, want America/New_York", u.Timezone)
	}
}

func TestUpdateMe_invalidTimezone(t *testing.T) {
	body := bytes.NewBufferString(`{"email":"test@example.com","timezone":"Not/A/Zone"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/me", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid timezone, got %d", rec.Code)
	}
}

func TestUpdateMe_emptyTimezone_defaultsUTC(t *testing.T) {
	body := bytes.NewBufferString(`{"email":"test@example.com","timezone":""}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/me", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var u storage.User
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if u.Timezone != "UTC" {
		t.Errorf("timezone: got %q, want UTC", u.Timezone)
	}
}

func TestUpdateMe_unauthenticated(t *testing.T) {
	body := bytes.NewBufferString(`{"email":"test@example.com"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/me", body)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleListUsers ---

func TestListUsers_success(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var users []*storage.User
	if err := json.NewDecoder(rec.Body).Decode(&users); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(users) == 0 {
		t.Error("expected at least one user (testUser)")
	}
}

func TestListUsers_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestListUsers_bearerToken_forbidden(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE api_tokens")
	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "list-users-token", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for Bearer token auth on /api/users, got %d", rec.Code)
	}
}

// --- handleDeleteUser ---

func TestDeleteUser_self(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/users/%s", testUser.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when deleting own account, got %d", rec.Code)
	}
}

func TestDeleteUser_otherUser(t *testing.T) {
	other, err := storage.CreateUser(context.Background(), testPool,
		"tobedeleted@example.com", "longpassword1")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/users/%s", other.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteUser_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete,
		"/api/users/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- handleChangePassword ---

func TestChangePassword_success(t *testing.T) {
	// Create a fresh user/session so the password change doesn't break testSession.
	ctx := context.Background()
	u, err := storage.CreateUser(ctx, testPool, "pwchange@example.com", "oldpassword1")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	sess, err := storage.CreateSession(ctx, testPool, u.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	cookie := &http.Cookie{Name: "tindra_session", Value: sess.Token}

	body := bytes.NewBufferString(`{"current_password":"oldpassword1","new_password":"newpassword99"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/me/password", body)
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestChangePassword_wrongCurrentPassword(t *testing.T) {
	body := bytes.NewBufferString(`{"current_password":"definitely-wrong","new_password":"newpassword99"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/me/password", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong current password, got %d", rec.Code)
	}
}

func TestChangePassword_badBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/me/password",
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", rec.Code)
	}
}

func TestChangePassword_missingFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/me/password",
		bytes.NewBufferString(`{"current_password":"","new_password":""}`))
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing fields, got %d", rec.Code)
	}
}

func TestChangePassword_newPasswordTooShort(t *testing.T) {
	// New password too short hits the non-ErrInvalidPassword error path.
	body := bytes.NewBufferString(`{"current_password":"testpassword","new_password":"short"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/me/password", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for too-short new password, got %d", rec.Code)
	}
}

// --- handleListComments / handleCreateComment ---

func seedIssueForCommentAPI(t *testing.T) string {
	t.Helper()
	iss, _, _, err := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-comment-api", "Comment API Error", "error", "error", "", "", time.Now().UTC())
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}
	return iss.ID
}

func TestListComments_empty(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	issueID := seedIssueForCommentAPI(t)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/comments", issueID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var comments []*storage.Comment
	if err := json.NewDecoder(rec.Body).Decode(&comments); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("expected 0 comments, got %d", len(comments))
	}
}

func TestCreateComment_success(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	issueID := seedIssueForCommentAPI(t)

	body := bytes.NewBufferString(`{"body":"looks like a nil pointer"}`)
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/issues/%s/comments", issueID), body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var c storage.Comment
	if err := json.NewDecoder(rec.Body).Decode(&c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if c.ID == "" {
		t.Error("expected non-empty ID")
	}
	if c.Body != "looks like a nil pointer" {
		t.Errorf("body: got %q", c.Body)
	}
}

func TestCreateComment_emptyBody(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	issueID := seedIssueForCommentAPI(t)

	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/issues/%s/comments", issueID),
		bytes.NewBufferString(`{"body":""}`))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty body, got %d", rec.Code)
	}
}

func TestCreateComment_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		"/api/issues/00000000-0000-0000-0000-000000000000/comments",
		bytes.NewBufferString(`{"body":"test"}`))
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleListReleases / handleGetRelease ---

func TestListReleases_api_empty(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE releases")

	req := httptest.NewRequest(http.MethodGet, "/api/releases", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Releases []*storage.Release `json:"releases"`
		Total    int                `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Releases) != 0 {
		t.Errorf("expected 0 releases, got %d", len(resp.Releases))
	}
}

func TestListReleases_api_withProjectFilter(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE releases")
	testPool.Exec(context.Background(),
		"INSERT INTO releases (project_id, version) VALUES ($1, 'v1.0.0')", testProject.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/releases?project_id=%s", testProject.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Releases []*storage.Release `json:"releases"`
		Total    int                `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Releases) != 1 {
		t.Errorf("expected 1 release, got %d", len(resp.Releases))
	}
}

func TestGetRelease_api_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/releases/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestGetRelease_api_found(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE releases")
	var id string
	testPool.QueryRow(context.Background(),
		"INSERT INTO releases (project_id, version) VALUES ($1, 'v2.0.0') RETURNING id",
		testProject.ID).Scan(&id)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/releases/%s", id), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- handleListAuditLog ---

func TestListAuditLog_api_success(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/audit", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var rows []*storage.AuditRow
	if err := json.NewDecoder(rec.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// May be empty; just verify the shape.
	if rows == nil {
		t.Error("expected non-nil slice")
	}
}

func TestListAuditLog_api_withKindFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/audit?kind=auth", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with kind filter, got %d", rec.Code)
	}
}

func TestListAuditLog_api_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/audit", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleListEventsForIssue ---

func TestListEventsForIssue_empty(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-evlist", "Ev List", "error", "error", "", "", time.Now().UTC())

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/events", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- handleGetIssueHistogram ---

func TestGetIssueHistogram_found(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-hist", "Histogram Issue", "error", "error", "", "", time.Now().UTC())

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/events/histogram", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetIssueHistogram_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/issues/00000000-0000-0000-0000-000000000000/events/histogram", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- handleGetLatestEventGlobal (offset-based) ---

func TestGetLatestEventGlobal_notFound(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-gleg", "GLEG Issue", "error", "error", "", "", time.Now().UTC())

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/events/latest", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for issue with no events, got %d", rec.Code)
	}
}

func TestGetLatestEventGlobal_issueNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/issues/00000000-0000-0000-0000-000000000000/events/latest", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()

	h := issuesHandler()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- handleUpdateComment ---

func seedCommentForTest(t *testing.T) (issueID, commentID string) {
	t.Helper()
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, err := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-upd-comment", "Upd Comment Issue", "error", "error", "", "", time.Now().UTC())
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	body := bytes.NewBufferString(`{"body":"original comment"}`)
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/issues/%s/comments", iss.ID), body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var c storage.Comment
	if err := json.NewDecoder(rec.Body).Decode(&c); err != nil {
		t.Fatalf("decode comment: %v", err)
	}
	return iss.ID, c.ID
}

func TestUpdateComment_success(t *testing.T) {
	_, commentID := seedCommentForTest(t)

	body := bytes.NewBufferString(`{"body":"updated text"}`)
	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/api/comments/%s", commentID), body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var c storage.Comment
	if err := json.NewDecoder(rec.Body).Decode(&c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if c.Body != "updated text" {
		t.Errorf("body: got %q, want 'updated text'", c.Body)
	}
}

func TestUpdateComment_emptyBody(t *testing.T) {
	_, commentID := seedCommentForTest(t)

	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/api/comments/%s", commentID),
		bytes.NewBufferString(`{"body":""}`))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty body, got %d", rec.Code)
	}
}

func TestUpdateComment_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut,
		"/api/comments/00000000-0000-0000-0000-000000000000",
		bytes.NewBufferString(`{"body":"text"}`))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestUpdateComment_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut,
		"/api/comments/00000000-0000-0000-0000-000000000000",
		bytes.NewBufferString(`{"body":"text"}`))
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleDeleteComment ---

func TestDeleteComment_success(t *testing.T) {
	_, commentID := seedCommentForTest(t)

	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/comments/%s", commentID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteComment_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete,
		"/api/comments/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestDeleteComment_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete,
		"/api/comments/00000000-0000-0000-0000-000000000000", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleGetReleaseTransactions ---

func TestGetReleaseTransactions_unknownRelease(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/releases/00000000-0000-0000-0000-000000000000/transactions", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var txns []json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&txns); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(txns) != 0 {
		t.Errorf("expected empty list for unknown release, got %d", len(txns))
	}
}

func TestGetReleaseTransactions_withRelease(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE releases")
	var relID string
	testPool.QueryRow(context.Background(),
		"INSERT INTO releases (project_id, version) VALUES ($1, 'v5.0.0') RETURNING id",
		testProject.ID).Scan(&relID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/releases/%s/transactions", relID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- handleGetReleaseIssues ---

func TestGetReleaseIssues_api_unknownRelease(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/releases/00000000-0000-0000-0000-000000000000/issues", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var issues []json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&issues); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("expected empty list for unknown release, got %d", len(issues))
	}
}

func TestGetReleaseIssues_api_withRelease(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE releases")
	var relID string
	testPool.QueryRow(context.Background(),
		"INSERT INTO releases (project_id, version) VALUES ($1, 'v7.0.0') RETURNING id",
		testProject.ID).Scan(&relID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/releases/%s/issues", relID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}
