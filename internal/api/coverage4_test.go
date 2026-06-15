package api_test

// coverage4_test.go — additional tests targeting uncovered branches across
// issues.go, extra.go, global.go, alerts.go, cron.go, mcp.go, events.go,
// logs.go, ingest.go, middleware.go, and invites.go.

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

	"github.com/blendbyte/tindra/internal/storage"
)

// ---------------------------------------------------------------------------
// issues.go — handleListIssues cursor parsing (lines 55-63)
// ---------------------------------------------------------------------------

func TestListIssues_validCursorParsing(t *testing.T) {
	truncateIssues(t)
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/issues?cursor_time=2024-01-01T00:00:00.000000000Z&cursor_id=00000000-0000-0000-0000-000000000001",
		nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// issues.go — handleGetIssue bad slug (line 86)
// ---------------------------------------------------------------------------

func TestGetIssue_badSlug(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/nonexistent-slug-cov4/issues/00000000-0000-0000-0000-000000000001",
		nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for bad slug, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// issues.go — handleUpdateIssue bad slug and notFound paths
// ---------------------------------------------------------------------------

func TestUpdateIssue_badSlug(t *testing.T) {
	body := bytes.NewBufferString(`{"status":"resolved"}`)
	req := httptest.NewRequest(http.MethodPatch,
		"/api/projects/nonexistent-slug-cov4/issues/00000000-0000-0000-0000-000000000001",
		body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for bad slug, got %d", rec.Code)
	}
}

func TestUpdateIssue_notFound(t *testing.T) {
	body := bytes.NewBufferString(`{"status":"resolved"}`)
	req := httptest.NewRequest(http.MethodPatch,
		"/api/projects/test-project/issues/00000000-0000-0000-0000-000000000001",
		body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for nonexistent issue, got %d", rec.Code)
	}
}

func TestUpdateIssue_ignoredHistory(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-upd-ignored-cov4", "Update Ignored Cov4")

	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"status":"ignored","ignore_until":"%s"}`, future)
	req := httptest.NewRequest(http.MethodPatch,
		"/api/projects/test-project/issues/"+iss.ID,
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// issues.go — handleGetIssueFingerprints bad slug (line 180)
// ---------------------------------------------------------------------------

func TestGetIssueFingerprints_badSlug(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/nonexistent-slug-cov4/issues/00000000-0000-0000-0000-000000000001/fingerprints",
		nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for bad slug, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// issues.go — handleMergeIssues bad slug (line 205)
// ---------------------------------------------------------------------------

func TestMergeIssues_badSlug(t *testing.T) {
	body := bytes.NewBufferString(`{"issue_ids":["00000000-0000-0000-0000-000000000001","00000000-0000-0000-0000-000000000002"]}`)
	req := httptest.NewRequest(http.MethodPost,
		"/api/projects/nonexistent-slug-cov4/issues/merge",
		body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for bad slug, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// issues.go — handleUnmergeIssue bad slug (line 277)
// ---------------------------------------------------------------------------

func TestUnmergeIssue_badSlug(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		"/api/projects/nonexistent-slug-cov4/issues/00000000-0000-0000-0000-000000000001/unmerge",
		nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for bad slug, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// issues.go — handleListPerfEvents wrong bearer project (line 348)
// ---------------------------------------------------------------------------

func TestListPerfEvents_wrongBearer(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-perf-bearer-cov4", "Perf Events Bearer Cov4")

	other, err := storage.CreateProject(context.Background(), testPool, "perf-bearer-other-cov4", "Perf Bearer Other Cov4")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/perf-events", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong-project bearer, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleUpdateMe actorID nil via bearer token (line 38)
// ---------------------------------------------------------------------------

func TestUpdateMe_bearerToken(t *testing.T) {
	tok := bearerToken(t, testProject.ID)
	body := bytes.NewBufferString(`{"email":"x@example.com"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/me", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for bearer token on /api/me, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleChangePassword non-ErrInvalidPassword (lines 160-162)
// ---------------------------------------------------------------------------

func TestChangePassword_shortNewPassword(t *testing.T) {
	body := bytes.NewBufferString(`{"current_password":"testpassword","new_password":"short"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/me/password", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for short new password, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleCreateComment actorID nil via bearer token (line 208)
// ---------------------------------------------------------------------------

func TestCreateComment_bearerToken(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-comment-bearer-cov4", "Comment Bearer Cov4")
	tok := bearerToken(t, testProject.ID)

	body := bytes.NewBufferString(`{"body":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+iss.ID+"/comments", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for bearer token on create comment, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleUpdateComment actorID nil via bearer token (line 233)
// ---------------------------------------------------------------------------

func TestUpdateComment_bearerToken(t *testing.T) {
	tok := bearerToken(t, testProject.ID)

	body := bytes.NewBufferString(`{"body":"updated"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/comments/00000000-0000-0000-0000-000000000001", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for bearer token on update comment, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleDeleteComment actorID nil via bearer token (line 271)
// ---------------------------------------------------------------------------

func TestDeleteComment_bearerToken(t *testing.T) {
	tok := bearerToken(t, testProject.ID)

	req := httptest.NewRequest(http.MethodDelete, "/api/comments/00000000-0000-0000-0000-000000000001", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for bearer token on delete comment, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// global.go — truncSearch >200 chars (line 27)
// ---------------------------------------------------------------------------

func TestListAllIssues_longSearch(t *testing.T) {
	longQ := strings.Repeat("x", 201)
	req := httptest.NewRequest(http.MethodGet, "/api/issues?q="+longQ, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// global.go — handleUpdateProject bad passthrough_dsn (lines 224-228)
// ---------------------------------------------------------------------------

func TestUpdateProject_badPassthroughDSN(t *testing.T) {
	body := `{"name":"Test Project","slug":"test-project","passthrough_dsn":"ftp://bad.example.com"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/projects/"+testProject.ID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad passthrough_dsn, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// global.go — handleBulkUpdateIssues invalid status (lines 566-569)
// ---------------------------------------------------------------------------

func TestBulkUpdateIssues_invalidStatus(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-bulk-regressed-cov4", "Bulk Regressed Cov4")

	body := fmt.Sprintf(`{"ids":["%s"],"status":"regressed"}`, iss.ID)
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/bulk", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid status, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// global.go — handleGetTransactionErrors wrong bearer project (line 1196)
// ---------------------------------------------------------------------------

func TestGetTransactionErrors_wrongBearer(t *testing.T) {
	var txID string
	err := testPool.QueryRow(context.Background(),
		`INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
		 VALUES ($1, '/cov4-tx-errors', 'http', 'ok', 100, NOW(), NOW()) RETURNING id`,
		testProject.ID).Scan(&txID)
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM transactions WHERE id=$1", txID)
	})

	other, err := storage.CreateProject(context.Background(), testPool, "tx-errors-other-cov4", "TX Errors Other Cov4")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/"+txID+"/errors", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong-project bearer on tx errors, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// global.go — handleGetIssueTags with no events (tags==nil path, line 749)
// ---------------------------------------------------------------------------

func TestGetIssueTags_noEvents(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-tags-empty-cov4", "Tags Empty Cov4")

	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/tags", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// alerts.go — handleCreateAlertRule bad webhook URL (line 74)
// ---------------------------------------------------------------------------

func TestCreateAlertRule_badWebhookURL(t *testing.T) {
	truncateAlertRules(t)
	body := `{"name":"bad-webhook","trigger":"new_issue","channel":"webhook","webhook_url":"ftp://example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad webhook URL, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// alerts.go — handleGetAlertRule bearer token match found=true (line 125)
// ---------------------------------------------------------------------------

func TestGetAlertRule_bearerTokenMatch(t *testing.T) {
	truncateAlertRules(t)
	url := "https://hooks.example.com/cov4-get-rule"
	rule, err := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID},
		Name:       "bearer-match-rule-cov4",
		Enabled:    true,
		Trigger:    "new_issue",
		Channel:    "webhook",
		WebhookURL: &url,
	})
	if err != nil {
		t.Fatalf("create alert rule: %v", err)
	}
	tok := bearerToken(t, testProject.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/alert-rules/"+rule.ID, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for bearer token with matching project, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// alerts.go — handleUpdateAlertRule filter fields set (lines 203,209,215)
// ---------------------------------------------------------------------------

func TestUpdateAlertRule_setFilters(t *testing.T) {
	truncateAlertRules(t)
	emailTo := "alerts-set-filters@example.com"
	rule, err := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		Name:    "filter-set-rule-cov4",
		Enabled: true,
		Trigger: "new_issue",
		Channel: "email",
		EmailTo: &emailTo,
	})
	if err != nil {
		t.Fatalf("create alert rule: %v", err)
	}

	body := `{"filter_level":"error","filter_environment":"production","min_occurrences":2}`
	req := httptest.NewRequest(http.MethodPatch, "/api/alert-rules/"+rule.ID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for filter set, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// alerts.go — handleUpdateAlertRule parse error (lines 222,226)
// ---------------------------------------------------------------------------

func TestUpdateAlertRule_parseError(t *testing.T) {
	truncateAlertRules(t)
	url := "https://hooks.example.com/cov4-parse-err"
	rule, err := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		Name:       "parse-err-rule-cov4",
		Enabled:    true,
		Trigger:    "new_issue",
		Channel:    "webhook",
		WebhookURL: &url,
	})
	if err != nil {
		t.Fatalf("create alert rule: %v", err)
	}

	// Send filter_level as a number (should be string) → parseErr
	body := `{"filter_level":123}`
	req := httptest.NewRequest(http.MethodPatch, "/api/alert-rules/"+rule.ID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for parse error, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// alerts.go — handleUpdateAlertRule clear filter fields (lines 258,263,268)
// ---------------------------------------------------------------------------

func TestUpdateAlertRule_clearFilters(t *testing.T) {
	truncateAlertRules(t)
	emailTo := "alerts-clear-filters@example.com"
	level := "error"
	env := "production"
	min := 3
	rule, err := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		Name:              "clear-filter-rule-cov4",
		Enabled:           true,
		Trigger:           "new_issue",
		Channel:           "email",
		EmailTo:           &emailTo,
		FilterLevel:       &level,
		FilterEnvironment: &env,
		MinOccurrences:    &min,
	})
	if err != nil {
		t.Fatalf("create alert rule: %v", err)
	}

	// Send null to clear each filter field
	body := `{"filter_level":null,"filter_environment":null,"min_occurrences":null}`
	req := httptest.NewRequest(http.MethodPatch, "/api/alert-rules/"+rule.ID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for clear filters, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// alerts.go — handleUpdateAlertRule bad webhook URL on update (line 282)
// ---------------------------------------------------------------------------

func TestUpdateAlertRule_badWebhookURL(t *testing.T) {
	truncateAlertRules(t)
	url := "https://hooks.example.com/cov4-update-bad-webhook"
	rule, err := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		Name:       "bad-wh-update-rule-cov4",
		Enabled:    true,
		Trigger:    "new_issue",
		Channel:    "webhook",
		WebhookURL: &url,
	})
	if err != nil {
		t.Fatalf("create alert rule: %v", err)
	}

	body := `{"webhook_url":"ftp://bad.example.com"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/alert-rules/"+rule.ID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad webhook URL on update, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// cron.go — handleCreateMonitor missing schedule (line 50)
// ---------------------------------------------------------------------------

func TestCreateMonitor_missingSchedule(t *testing.T) {
	body := fmt.Sprintf(`{"project_id":"%s","name":"no-schedule-cov4"}`, testProject.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/monitors", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing schedule, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// cron.go — handleListCheckins unknown monitorID (line 171)
// ---------------------------------------------------------------------------

func TestListCheckins_unknownMonitor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/monitors/00000000-0000-0000-0000-000000000001/checkins", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown monitor, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// events.go — handleGetLatestEvent bad slug (line 16)
// ---------------------------------------------------------------------------

func TestGetLatestEvent_badSlug(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/nonexistent-slug-cov4/issues/00000000-0000-0000-0000-000000000001/events/latest",
		nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for bad slug on events/latest, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// logs.go — handleListLogs cursor_id param (line 28)
// ---------------------------------------------------------------------------

func TestListLogs_cursorID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/logs?cursor_id=00000000-0000-0000-0000-000000000001", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// ingest.go — handleEnvelope bad gzip body (lines 84-86)
// ---------------------------------------------------------------------------

func TestEnvelope_badGzip(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/%s/envelope/", testProject.ID),
		bytes.NewBufferString("this is not gzip data"))
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad gzip body, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// middleware.go — requirePerm blocks bearer token (lines 205-208)
// ---------------------------------------------------------------------------

func TestRequirePerm_bearerTokenForbidden(t *testing.T) {
	tok := bearerToken(t, testProject.ID)
	body := `{"name":"Test Project","slug":"test-project"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/projects/"+testProject.ID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for bearer token on requirePerm endpoint, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// invites.go — handleAcceptInvite CreateUser fails (weak password, lines 185-187)
// ---------------------------------------------------------------------------

func TestAcceptInvite_weakPassword(t *testing.T) {
	token, err := storage.CreateInvite(context.Background(), testPool,
		testUser.ID, "weak-pw-invite-cov4@example.com", "Weak PW User")
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM user_invites WHERE email='weak-pw-invite-cov4@example.com'")
		testPool.Exec(context.Background(), "DELETE FROM users WHERE email='weak-pw-invite-cov4@example.com'")
	})

	body := `{"password":"short"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/invite/"+token+"/accept",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for weak password on invite accept, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// mcp.go — rawIDToAny empty raw (line 78): send request without "id" field
// ---------------------------------------------------------------------------

func TestMCP_rawIDToAny_noIDField(t *testing.T) {
	// Sending a bad jsonrpc version with no id field: handler calls rawIDToAny(nil)
	// at the version-check branch and returns 200 with an RPC error body.
	rec := postMCPRaw(t, mcpHandler(),
		`{"jsonrpc":"1.0","method":"ping"}`,
		"application/json", authCookie())
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for bad jsonrpc version, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpCallTool invalid params (line 388): send params as non-object
// ---------------------------------------------------------------------------

func TestMCP_callTool_invalidParams(t *testing.T) {
	rec := postMCPRaw(t, mcpHandler(),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":123}`,
		"application/json", authCookie())
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 (RPC error), got %d", rec.Code)
	}
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error == nil {
		t.Error("expected RPC error for invalid params")
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpProjectIDs session+project_id arg (line 456)
// ---------------------------------------------------------------------------

func TestMCP_projectIDs_sessionWithArg(t *testing.T) {
	result := toolCall(t, mcpHandler(), "list_issues",
		map[string]any{"project_id": testProject.ID}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Errorf("unexpected isError: %s", toolText(t, result))
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpArgLimit n<1 (line 479): send limit=0
// ---------------------------------------------------------------------------

func TestMCP_argLimit_zero(t *testing.T) {
	result := toolCall(t, mcpHandler(), "list_issues",
		map[string]any{"limit": float64(0)}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Errorf("unexpected isError for limit=0: %s", toolText(t, result))
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpGetOverview: monitorStates loop body (line 507)
// ---------------------------------------------------------------------------

func TestMCP_getOverview_withMonitor(t *testing.T) {
	m, err := storage.CreateCronMonitor(context.Background(), testPool, &storage.CronMonitor{
		ProjectID:       testProject.ID,
		Name:            "cov4-overview-monitor",
		Schedule:        "0 * * * *",
		GracePeriodSecs: 300,
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM cron_monitors WHERE id=$1", m.ID)
	})

	result := toolCall(t, mcpHandler(), "get_overview", map[string]any{}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpGetOverview firing alert loop: nil LastFiredAt → continue (line 518)
// ---------------------------------------------------------------------------

func TestMCP_getOverview_nilLastFiredAt(t *testing.T) {
	truncateAlertRules(t)
	url := "https://hooks.example.com/cov4-nil-fired"
	_, err := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID},
		Name:       "nil-fired-rule-cov4",
		Enabled:    true,
		Trigger:    "new_issue",
		Channel:    "webhook",
		WebhookURL: &url,
	})
	if err != nil {
		t.Fatalf("create alert rule: %v", err)
	}

	result := toolCall(t, mcpHandler(), "get_overview", map[string]any{}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpGetOverview firing alert: firingCount++ (line 524)
// ---------------------------------------------------------------------------

func TestMCP_getOverview_firingAlert(t *testing.T) {
	truncateAlertRules(t)
	url := "https://hooks.example.com/cov4-firing"
	rule, err := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID},
		Name:       "firing-rule-cov4",
		Enabled:    true,
		Trigger:    "new_issue",
		Channel:    "webhook",
		WebhookURL: &url,
	})
	if err != nil {
		t.Fatalf("create alert rule: %v", err)
	}
	// Set last_fired_at to 1 hour ago (within 24-hour window).
	if _, err := testPool.Exec(context.Background(),
		`UPDATE alert_rules SET last_fired_at = NOW() - INTERVAL '1 hour' WHERE id = $1`,
		rule.ID); err != nil {
		t.Fatalf("set last_fired_at: %v", err)
	}

	result := toolCall(t, mcpHandler(), "get_overview", map[string]any{}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var overview map[string]any
	if err := json.Unmarshal([]byte(text), &overview); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if firing, ok := overview["firing_alerts_24h"].(float64); !ok || firing < 1 {
		t.Errorf("expected firing_alerts_24h >= 1, got %v", overview["firing_alerts_24h"])
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpGetOverview session+project_id: second if true→continue (line 521)
// ---------------------------------------------------------------------------

func TestMCP_getOverview_sessionProjectID_mismatch(t *testing.T) {
	truncateAlertRules(t)
	url := "https://hooks.example.com/cov4-proj-mismatch"
	rule, err := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID},
		Name:       "proj-mismatch-rule-cov4",
		Enabled:    true,
		Trigger:    "new_issue",
		Channel:    "webhook",
		WebhookURL: &url,
	})
	if err != nil {
		t.Fatalf("create alert rule: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`UPDATE alert_rules SET last_fired_at = NOW() - INTERVAL '1 hour' WHERE id = $1`,
		rule.ID); err != nil {
		t.Fatalf("set last_fired_at: %v", err)
	}

	other, err := storage.CreateProject(context.Background(), testPool, "cov4-proj-mismatch", "Cov4 Proj Mismatch")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})

	result := toolCall(t, mcpHandler(), "get_overview",
		map[string]any{"project_id": other.ID}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpGetMonitor wrong bearer project (lines 644-647)
// ---------------------------------------------------------------------------

func TestMCP_getMonitor_wrongBearer(t *testing.T) {
	m, err := storage.CreateCronMonitor(context.Background(), testPool, &storage.CronMonitor{
		ProjectID:       testProject.ID,
		Name:            "cov4-wrong-bearer-monitor",
		Schedule:        "0 * * * *",
		GracePeriodSecs: 300,
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM cron_monitors WHERE id=$1", m.ID)
	})

	other, err := storage.CreateProject(context.Background(), testPool, "cov4-monitor-other", "Cov4 Monitor Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})
	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, other.ID, "cov4-monitor-tok", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM api_tokens WHERE project_id=$1", other.ID)
	})

	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_monitor","arguments":{"id":%q}}}`, m.ID)
	rec := bearerRequest(t, mcpHandler(), plaintext, body)
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for wrong-project bearer on get_monitor, got %v", resp.Result)
	}
}

// ---------------------------------------------------------------------------
// mcp.go — ruleMatchesProjects returns false (line 701) via list_alerts
// ---------------------------------------------------------------------------

func TestMCP_listAlerts_sessionProjectIDFiltersGlobalRule(t *testing.T) {
	truncateAlertRules(t)
	url := "https://hooks.example.com/cov4-global-rule"
	// Global rule: no ProjectIDs → ruleMatchesProjects returns false for any projectIDs
	_, err := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		Name:       "global-rule-cov4",
		Enabled:    true,
		Trigger:    "new_issue",
		Channel:    "webhook",
		WebhookURL: &url,
	})
	if err != nil {
		t.Fatalf("create alert rule: %v", err)
	}

	result := toolCall(t, mcpHandler(), "list_alerts",
		map[string]any{"project_id": testProject.ID}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Errorf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var rules []any
	if err := json.Unmarshal([]byte(text), &rules); err != nil {
		t.Fatalf("decode rules: %v", err)
	}
	// Global rule should be filtered out because it has no matching ProjectIDs.
	if len(rules) != 0 {
		t.Errorf("expected 0 rules after filtering global rule, got %d", len(rules))
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpCheckWrite manage_alerts denied (line 725)
// ---------------------------------------------------------------------------

func TestMCP_createAlertRule_noManageAlerts(t *testing.T) {
	nopermCookie := makeReadOnlyUser(t, "cov4-no-alerts@example.com")
	result := toolCall(t, mcpHandler(), "create_alert_rule",
		map[string]any{"name": "x", "trigger": "new_issue", "channel": "webhook"},
		nopermCookie)
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for session without manage_alerts, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpListIssueEvents issue not found (line 792)
// ---------------------------------------------------------------------------

func TestMCP_listIssueEvents_notFound(t *testing.T) {
	result := toolCall(t, mcpHandler(), "list_issue_events",
		map[string]any{"issue_id": "00000000-0000-0000-0000-000000000001"}, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for nonexistent issue, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpListSpanSummaries hours clamping (lines 823,826)
// ---------------------------------------------------------------------------

func TestMCP_listSpanSummaries_hoursClampLow(t *testing.T) {
	result := toolCall(t, mcpHandler(), "list_span_summaries",
		map[string]any{"type": "db", "hours": float64(0)}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Errorf("unexpected isError for hours=0: %s", toolText(t, result))
	}
}

func TestMCP_listSpanSummaries_hoursClampHigh(t *testing.T) {
	result := toolCall(t, mcpHandler(), "list_span_summaries",
		map[string]any{"type": "db", "hours": float64(721)}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Errorf("unexpected isError for hours=721: %s", toolText(t, result))
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpUpdateIssue id=="" (line 868)
// ---------------------------------------------------------------------------

func TestMCP_updateIssue_noID(t *testing.T) {
	result := toolCall(t, mcpHandler(), "update_issue",
		map[string]any{"status": "resolved"}, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for update_issue with no id, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpBulkUpdateIssues paths (lines 954,963,967)
// ---------------------------------------------------------------------------

func TestMCP_bulkUpdate_emptyIDs(t *testing.T) {
	result := toolCall(t, mcpHandler(), "bulk_update_issues",
		map[string]any{"ids": []any{}, "status": "resolved"}, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for empty ids, got %v", result)
	}
}

func TestMCP_bulkUpdate_allEmptyStrings(t *testing.T) {
	result := toolCall(t, mcpHandler(), "bulk_update_issues",
		map[string]any{"ids": []any{""}, "status": "resolved"}, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for all-empty-string ids, got %v", result)
	}
}

func TestMCP_bulkUpdate_noStatus(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-bulk-nostatus-cov4", "Bulk No Status Cov4")
	result := toolCall(t, mcpHandler(), "bulk_update_issues",
		map[string]any{"ids": []any{iss.ID}}, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for missing status, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpCreateAlertRule paths (lines 1029,1035,1038,1042,1045,1046)
// ---------------------------------------------------------------------------

// Covers: webhook_url set (1029), threshold>0 (1035), window_mins>0 (1038),
//
//	webhook channel check (1045), ValidateWebhookURL fail (1046).
func TestMCP_createAlertRule_eventCountBadWebhook(t *testing.T) {
	truncateAlertRules(t)
	result := toolCall(t, mcpHandler(), "create_alert_rule", map[string]any{
		"name":        "count-alert-cov4",
		"trigger":     "event_count",
		"channel":     "webhook",
		"webhook_url": "ftp://bad.example.com",
		"threshold":   float64(5),
		"window_mins": float64(10),
	}, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for bad webhook URL, got %v", result)
	}
}

// Covers: validateAlertRule fail (1042) — bad trigger name.
func TestMCP_createAlertRule_badTrigger(t *testing.T) {
	truncateAlertRules(t)
	result := toolCall(t, mcpHandler(), "create_alert_rule", map[string]any{
		"name":        "bad-trigger-cov4",
		"trigger":     "no-such-trigger",
		"channel":     "webhook",
		"webhook_url": "ftp://x",
	}, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for bad trigger, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// logs.go — handleListLogs hasMore cursor path (lines 49-54)
// Insert 101 logs so the list endpoint returns has_more=true and sets the
// cursor fields.
// ---------------------------------------------------------------------------

func TestListLogs_hasMore(t *testing.T) {
	// Clean up before and after so we don't affect other tests.
	testPool.Exec(context.Background(), "DELETE FROM logs WHERE project_id = $1", testProject.ID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM logs WHERE project_id = $1", testProject.ID)
	})

	// Insert 101 log rows to exceed the default limit of 100.
	for i := range 101 {
		testPool.Exec(context.Background(),
			`INSERT INTO logs (project_id, timestamp, level, body) VALUES ($1, NOW() - ($2 * interval '1 second'), 'info', 'test log')`,
			testProject.ID, i)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/logs", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		HasMore        bool    `json:"has_more"`
		NextCursorTime *string `json:"next_cursor_time"`
		NextCursorID   *string `json:"next_cursor_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.HasMore {
		t.Error("expected has_more=true with 101 logs")
	}
	if resp.NextCursorTime == nil || resp.NextCursorID == nil {
		t.Error("expected cursor fields to be set when has_more=true")
	}
}

// ---------------------------------------------------------------------------
// global.go — handleListEventsForIssue hasMore cursor path (lines 906-911)
// Insert 51 events for one issue so has_more=true and cursor fields are set.
// ---------------------------------------------------------------------------

func TestListEventsForIssue_hasMore(t *testing.T) {
	ts := time.Now().UTC()
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-events-hasmore-cov4", "Events HasMore Cov4", "error", "error", "", "", ts)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM events WHERE issue_id = $1", iss.ID)
		testPool.Exec(context.Background(), "DELETE FROM issues WHERE id = $1", iss.ID)
	})

	// Insert 51 events for this issue to exceed the default limit of 50.
	testPool.Exec(context.Background(),
		`INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		 SELECT $1, NOW() - (n * interval '1 second'), NOW() - (n * interval '1 second'),
		        '{"level":"error"}'::jsonb, 'fp-events-hasmore-cov4', $2
		 FROM generate_series(1, 51) AS n`,
		testProject.ID, iss.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/events", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		HasMore        bool    `json:"has_more"`
		NextCursorTime *string `json:"next_cursor_time"`
		NextCursorID   *string `json:"next_cursor_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.HasMore {
		t.Error("expected has_more=true with 51 events")
	}
	if resp.NextCursorTime == nil || resp.NextCursorID == nil {
		t.Error("expected cursor fields to be set when has_more=true")
	}
}

// ---------------------------------------------------------------------------
// storage/events.go — ListEventsForIssue tag-fetching loop body (lines 286-288)
// Insert an event WITH a tag so the tag-fetching query returns rows and the
// for tagRows.Next() body executes.
// ---------------------------------------------------------------------------

func TestListEventsForIssue_withTags(t *testing.T) {
	ts := time.Now().UTC()
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-events-tags-cov4", "Events Tags Cov4", "error", "error", "", "", ts)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM event_tags WHERE issue_id = $1", iss.ID)
		testPool.Exec(context.Background(), "DELETE FROM events WHERE issue_id = $1", iss.ID)
		testPool.Exec(context.Background(), "DELETE FROM issues WHERE id = $1", iss.ID)
	})

	// Insert one event and one tag for it.
	var evID string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		 VALUES ($1, $2, $2, '{"level":"error"}'::jsonb, 'fp-events-tags-cov4', $3)
		 RETURNING id`,
		testProject.ID, ts, iss.ID).Scan(&evID); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	testPool.Exec(context.Background(),
		`INSERT INTO event_tags (event_id, issue_id, project_id, key, value) VALUES ($1, $2, $3, 'user', 'alice')`,
		evID, iss.ID, testProject.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/events", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// storage/releases.go — ListReleases cursor path (lines 107-114)
// Call storage.ListReleases directly with a cursor to cover the WHERE clause.
// ---------------------------------------------------------------------------

func TestStorage_ListReleases_withCursor(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM releases WHERE project_id = $1", testProject.ID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM releases WHERE project_id = $1", testProject.ID)
	})

	// Insert a release so there's something to cursor past.
	testPool.Exec(context.Background(),
		"INSERT INTO releases (project_id, version, deployed_at) VALUES ($1, 'v1.0.0-cursor-test', NOW() - interval '1 day')",
		testProject.ID)

	// Use a cursor that is "now" — all releases are older, so query returns them.
	now := time.Now().UTC()
	fakeID := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	releases, err := storage.ListReleases(context.Background(), testPool, storage.ReleaseFilter{
		ProjectIDs: []string{testProject.ID},
		CursorTime: &now,
		CursorID:   &fakeID,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListReleases with cursor: %v", err)
	}
	// Should find the release inserted above (deployed_at < now).
	if len(releases) == 0 {
		t.Error("expected at least 1 release with cursor set to now")
	}
}
