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

// --- bearer token scope checks for release sub-resources ---

func TestGetReleaseTransactions_bearerTokenWrongProject(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE releases")
	truncateTokens(t)

	// Seed a release that belongs to testProject.
	var relID string
	testPool.QueryRow(context.Background(),
		"INSERT INTO releases (project_id, version) VALUES ($1, $2) RETURNING id",
		testProject.ID, "v1.0.0-scope-txn",
	).Scan(&relID)

	// Create a second project and a bearer token scoped to it.
	other, err := storage.CreateProject(context.Background(), testPool, "scope-txn-other", "Scope Txn Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/releases/%s/transactions", relID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 when bearer token project != release project, got %d: %s",
			rec.Code, rec.Body.String())
	}
}

func TestGetReleaseIssues_bearerTokenWrongProject(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE releases")
	truncateTokens(t)

	// Seed a release that belongs to testProject.
	var relID string
	testPool.QueryRow(context.Background(),
		"INSERT INTO releases (project_id, version) VALUES ($1, $2) RETURNING id",
		testProject.ID, "v1.0.0-scope-iss",
	).Scan(&relID)

	// Create a second project and a bearer token scoped to it.
	other, err := storage.CreateProject(context.Background(), testPool, "scope-iss-other", "Scope Iss Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/releases/%s/issues", relID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 when bearer token project != release project, got %d: %s",
			rec.Code, rec.Body.String())
	}
}

func TestUpdateMe_weeklyDigest(t *testing.T) {
	body := bytes.NewBufferString(`{"email":"test@example.com","weekly_digest":true}`)
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
	if !u.WeeklyDigest {
		t.Error("expected weekly_digest to be true")
	}
}

func TestListReleases_paginationCursor(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE releases")
	for i := range 50 {
		testPool.Exec(context.Background(),
			"INSERT INTO releases (project_id, version) VALUES ($1, $2)",
			testProject.ID, fmt.Sprintf("v%d.0.0", i+1))
	}

	req := httptest.NewRequest(http.MethodGet, "/api/releases", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Releases       []json.RawMessage `json:"releases"`
		HasMore        bool              `json:"has_more"`
		NextCursorTime string            `json:"next_cursor_time"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.HasMore {
		t.Error("expected has_more=true when 50 releases returned")
	}
	if resp.NextCursorTime == "" {
		t.Error("expected next_cursor_time to be set")
	}
}

func TestDeleteComment_forbidden(t *testing.T) {
	_, commentID := seedCommentForTest(t)

	roCookie := makeReadOnlyUser(t, "ro-delete-comment@example.com")

	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/comments/%s", commentID), nil)
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteComment_manageIssues(t *testing.T) {
	// A non-author with manage_issues permission can delete another user's comment.
	_, commentID := seedCommentForTest(t)

	// Create a second user and grant them manage_issues.
	u, err := storage.CreateUser(context.Background(), testPool, "mgr-delete-comment@example.com", "managerpass123")
	if err != nil {
		t.Fatalf("create manager user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", u.ID)
	})
	if _, err := storage.UpdateUserPermissions(context.Background(), testPool, u.ID,
		storage.UserPermissions{ManageIssues: true}); err != nil {
		t.Fatalf("grant manage_issues: %v", err)
	}
	sess, err := storage.CreateSession(context.Background(), testPool, u.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	mgrCookie := &http.Cookie{Name: "tindra_session", Value: sess.Token}

	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/comments/%s", commentID), nil)
	req.AddCookie(mgrCookie)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListAuditLog_withSearchQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/audit?q=project", nil)
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
	if rows == nil {
		t.Error("expected non-nil slice")
	}
}

func TestGetLatestEventGlobal_withOffset(t *testing.T) {
	// seed an issue and event
	ts := time.Now().UTC()
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-gleg-offset", "GLEG Offset", "error", "error", "", "", ts)
	testPool.Exec(context.Background(),
		"INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id) VALUES ($1,$2,$2,'{\"level\":\"error\"}'::jsonb,'fp-gleg-offset',$3)",
		testProject.ID, ts, iss.ID)
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/events/latest?offset=0", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	// offset=0 still returns the event
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with offset=0, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListReleases_withCursor(t *testing.T) {
	// GET with cursor_time + cursor_id to exercise the cursor parsing branch
	req := httptest.NewRequest(http.MethodGet,
		"/api/releases?cursor_time=2024-01-01T00:00:00.000000000Z&cursor_id=00000000-0000-0000-0000-000000000001", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateComment_differentUser(t *testing.T) {
	// Create comment as testUser, then try to update as a different user → 403
	_, commentID := seedCommentForTest(t)
	// Create a second user session
	other, _ := storage.CreateUser(context.Background(), testPool, "upd-comment-other@example.com", "password1234")
	sess, _ := storage.CreateSession(context.Background(), testPool, other.ID)
	otherCookie := &http.Cookie{Name: "tindra_session", Value: sess.Token}

	body := bytes.NewBufferString(`{"body":"hijack"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/comments/"+commentID, body)
	req.AddCookie(otherCookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 when non-author updates comment, got %d", rec.Code)
	}
}

func TestListAllIssues_paginationNextCursor(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	for i := range 51 {
		testPool.Exec(context.Background(),
			"INSERT INTO issues (project_id, fingerprint, title, level, kind, first_seen, last_seen) VALUES ($1,$2,$3,'error','error',NOW(),NOW())",
			testProject.ID, fmt.Sprintf("fp-page-%d", i), fmt.Sprintf("Issue %d", i))
	}
	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		HasMore        bool    `json:"has_more"`
		NextCursorTime *string `json:"next_cursor_time"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.HasMore {
		t.Error("expected has_more=true with 51 issues")
	}
	if resp.NextCursorTime == nil {
		t.Error("expected next_cursor_time to be set")
	}
}

func TestGetMe_withBearerToken(t *testing.T) {
	body := bytes.NewBufferString(fmt.Sprintf(`{"name":"bearer-test","project_id":%q}`, testProject.ID))
	createReq := httptest.NewRequest(http.MethodPost, "/api/tokens", body)
	createReq.AddCookie(authCookie())
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	globalHandler().ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create token: expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(createRec.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("decode token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+tokenResp.Token)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for bearer token on /api/me, got %d", rec.Code)
	}
}
