package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func permHandler() http.Handler {
	return api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, nil)
}

// makeReadOnlyUser creates a user with no permissions (testUser is already first,
// so any subsequent user gets defaults=false) and returns their session cookie.
// The user is deleted via t.Cleanup so the global testSession stays intact.
func makeReadOnlyUser(t *testing.T, email string) *http.Cookie {
	t.Helper()
	u, err := storage.CreateUser(context.Background(), testPool, email, "readonlypass123")
	if err != nil {
		t.Fatalf("create read-only user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", u.ID)
	})
	sess, err := storage.CreateSession(context.Background(), testPool, u.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return &http.Cookie{Name: "tindra_session", Value: sess.Token}
}

// --- PUT /api/users/{userID}/permissions ---

func TestUpdateUserPermissions_success(t *testing.T) {
	// testUser (created in TestMain as first user) has all permissions.
	// Create a second user as the target - they start with no permissions.
	target, err := storage.CreateUser(context.Background(), testPool, "target-perms@example.com", "targetpass1234")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", target.ID)
	})

	body := `{"manage_projects":true,"manage_users":false,"manage_alerts":true,"manage_issues":false}`
	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/api/users/%s/permissions", target.ID),
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie()) // testUser has manage_users
	rec := httptest.NewRecorder()
	permHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got storage.User
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Permissions.ManageProjects {
		t.Error("manage_projects should be true")
	}
	if got.Permissions.ManageUsers {
		t.Error("manage_users should be false")
	}
	if !got.Permissions.ManageAlerts {
		t.Error("manage_alerts should be true")
	}
	if got.Permissions.ManageIssues {
		t.Error("manage_issues should be false")
	}
}

func TestUpdateUserPermissions_forbidden(t *testing.T) {
	// Read-only user (no manage_users) tries to update testUser's permissions.
	roCookie := makeReadOnlyUser(t, "ro-perms@example.com")

	body := `{"manage_projects":true,"manage_users":true,"manage_alerts":true,"manage_issues":true}`
	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/api/users/%s/permissions", testUser.ID),
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	permHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestUpdateUserPermissions_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut,
		"/api/users/00000000-0000-0000-0000-000000000000/permissions",
		bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	permHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestUpdateUserPermissions_notFound(t *testing.T) {
	body := `{"manage_projects":false,"manage_users":false,"manage_alerts":false,"manage_issues":false}`
	req := httptest.NewRequest(http.MethodPut,
		"/api/users/00000000-0000-0000-0000-000000000000/permissions",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie()) // testUser has manage_users
	rec := httptest.NewRecorder()
	permHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- requirePerm: issue write routes ---

func TestUpdateIssue_forbiddenWithoutPerm(t *testing.T) {
	roCookie := makeReadOnlyUser(t, "ro-issue@example.com")

	req := httptest.NewRequest(http.MethodPatch,
		"/api/issues/00000000-0000-0000-0000-000000000001",
		bytes.NewBufferString(`{"status":"resolved"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestCreateComment_allowedWithoutPerm(t *testing.T) {
	// Any authenticated user can comment - no manage_issues required.
	roCookie := makeReadOnlyUser(t, "ro-comment@example.com")

	req := httptest.NewRequest(http.MethodPost,
		"/api/issues/00000000-0000-0000-0000-000000000001/comments",
		bytes.NewBufferString(`{"body":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	// 404 (issue not found) means we passed the auth + permission gate.
	if rec.Code == http.StatusForbidden {
		t.Error("read-only user should be able to comment, got 403")
	}
	if rec.Code == http.StatusUnauthorized {
		t.Error("authenticated user should not get 401")
	}
}

func TestCreateAlertRule_forbiddenWithoutPerm(t *testing.T) {
	roCookie := makeReadOnlyUser(t, "ro-alert@example.com")

	body := `{"name":"t","project_ids":[],"trigger":"new_issue","channel":"webhook","webhook_url":"https://x.example.com"}`
	req := httptest.NewRequest(http.MethodPost,
		"/api/alert-rules",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestCreateToken_forbiddenWithoutPerm(t *testing.T) {
	roCookie := makeReadOnlyUser(t, "ro-token@example.com")

	body := `{"name":"ci","project_id":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tokens", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	permHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestUploadSourcemap_forbiddenWithoutPerm(t *testing.T) {
	roCookie := makeReadOnlyUser(t, "ro-sm@example.com")

	req := httptest.NewRequest(http.MethodPost,
		"/api/projects/test-project/sourcemaps",
		bytes.NewBufferString(""))
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	permHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

// Verify that the testUser (who has all permissions) can reach issue write routes.
func TestUpdateIssue_allowedWithPerm(t *testing.T) {
	truncateIssues(t)

	req := httptest.NewRequest(http.MethodPatch,
		"/api/issues/00000000-0000-0000-0000-000000000001",
		bytes.NewBufferString(`{"status":"resolved"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	// 404 (issue not found) means we passed the permission gate.
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 (past perm gate, issue not found), got %d: %s", rec.Code, rec.Body.String())
	}
}
