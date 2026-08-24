package api_test

// coverage6_test.go — additional tests targeting uncovered branches across
// alerts.go, uptime.go, cron.go, extra.go, global.go, and mcp.go.
// Focus: alert rule trigger variants, uptime PATCH paths, comment paths,
// release resources, handleUpdateMe timezone edge cases, cron helpers,
// MCP tool edge cases, SPA static file serving, smStore branch.

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

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

// ---------------------------------------------------------------------------
// handleListAllIssues — query param branches not yet exercised elsewhere
// ---------------------------------------------------------------------------

func TestListAllIssues_withEnvFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues?env=production", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with env filter, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListAllIssues_withKindFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues?kind=error", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with kind filter, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListAllIssues_withProjectIDFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/issues?project_id=%s", testProject.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with project_id filter, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListAllIssues_since24h(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues?since=24h", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with since=24h, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListAllIssues_sinceUnknown(t *testing.T) {
	// Unknown since value causes d to stay zero — SinceLast not set
	req := httptest.NewRequest(http.MethodGet, "/api/issues?since=1year", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with unknown since, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListAllIssues_invalidCursorTimeIgnoredGlobal(t *testing.T) {
	// cursor_time present but unparseable — inner if not entered, no cursor applied
	req := httptest.NewRequest(http.MethodGet, "/api/issues?cursor_time=notadate&cursor_id=someid", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for invalid cursor_time, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListAllIssues_cursorTimeWithoutCursorIDGlobal(t *testing.T) {
	// cursor_time alone (no cursor_id) — inner if not entered
	cursorTime := time.Now().UTC().Format(time.RFC3339Nano)
	req := httptest.NewRequest(http.MethodGet, "/api/issues?cursor_time="+cursorTime, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with cursor_time but no cursor_id, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// alerts.go — additional trigger values (cron_missed, cron_error, regressed)
// ---------------------------------------------------------------------------

func TestCreateAlertRule_cronMissedTrigger(t *testing.T) {
	truncateAlertRules(t)

	b, _ := json.Marshal(map[string]any{
		"name":        "cron missed alert",
		"trigger":     "cron_missed",
		"channel":     "webhook",
		"webhook_url": "https://example.com/wh",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules", bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for cron_missed trigger, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateAlertRule_cronErrorTrigger(t *testing.T) {
	truncateAlertRules(t)

	b, _ := json.Marshal(map[string]any{
		"name":        "cron error alert",
		"trigger":     "cron_error",
		"channel":     "webhook",
		"webhook_url": "https://example.com/wh",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules", bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for cron_error trigger, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateAlertRule_regressedTrigger(t *testing.T) {
	truncateAlertRules(t)

	b, _ := json.Marshal(map[string]any{
		"name":        "regressed alert",
		"trigger":     "regressed",
		"channel":     "webhook",
		"webhook_url": "https://example.com/wh",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules", bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for regressed trigger, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateAlertRule_uptimeDownTrigger(t *testing.T) {
	truncateAlertRules(t)

	b, _ := json.Marshal(map[string]any{
		"name":        "uptime down alert",
		"trigger":     "uptime_down",
		"channel":     "webhook",
		"webhook_url": "https://example.com/wh",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules", bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for uptime_down trigger, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateAlertRule_uptimeRecoveredTrigger(t *testing.T) {
	truncateAlertRules(t)

	b, _ := json.Marshal(map[string]any{
		"name":        "uptime recovered alert",
		"trigger":     "uptime_recovered",
		"channel":     "webhook",
		"webhook_url": "https://example.com/wh",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules", bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for uptime_recovered trigger, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// alerts.go — validateAlertRule: event_count boundary values
// ---------------------------------------------------------------------------

func TestCreateAlertRule_eventCountPositiveValues(t *testing.T) {
	truncateAlertRules(t)

	threshold := 10
	windowMins := 5
	b, _ := json.Marshal(map[string]any{
		"name":        "event count alert",
		"trigger":     "event_count",
		"threshold":   threshold,
		"window_mins": windowMins,
		"channel":     "webhook",
		"webhook_url": "https://example.com/wh",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules", bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for valid event_count rule, got %d: %s", rec.Code, rec.Body.String())
	}
	var rule storage.AlertRule
	if err := json.NewDecoder(rec.Body).Decode(&rule); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rule.Threshold == nil || *rule.Threshold != threshold {
		t.Errorf("threshold: got %v, want %d", rule.Threshold, threshold)
	}
	if rule.WindowMins == nil || *rule.WindowMins != windowMins {
		t.Errorf("window_mins: got %v, want %d", rule.WindowMins, windowMins)
	}
}

func TestCreateAlertRule_eventCountNegativeThreshold(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"name":        "neg threshold",
		"trigger":     "event_count",
		"threshold":   -1,
		"window_mins": 5,
		"channel":     "webhook",
		"webhook_url": "https://example.com/wh",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules", bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for negative threshold, got %d", rec.Code)
	}
}

func TestCreateAlertRule_eventCountNegativeWindowMins(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"name":        "neg window",
		"trigger":     "event_count",
		"threshold":   10,
		"window_mins": -1,
		"channel":     "webhook",
		"webhook_url": "https://example.com/wh",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules", bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for negative window_mins, got %d", rec.Code)
	}
}

func TestCreateAlertRule_defaultCooldownApplied(t *testing.T) {
	truncateAlertRules(t)

	// No cooldown_mins supplied — should default to 60
	b, _ := json.Marshal(map[string]any{
		"name":        "default cooldown",
		"trigger":     "new_issue",
		"channel":     "webhook",
		"webhook_url": "https://example.com/wh",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules", bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var rule storage.AlertRule
	if err := json.NewDecoder(rec.Body).Decode(&rule); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rule.CooldownMins != 60 {
		t.Errorf("expected default cooldown_mins=60, got %d", rule.CooldownMins)
	}
}

// ---------------------------------------------------------------------------
// alerts.go — handleUpdateAlertRule: patch project_ids to empty slice
// ---------------------------------------------------------------------------

func TestUpdateAlertRule_clearProjectIDsCov6(t *testing.T) {
	truncateAlertRules(t)

	created, _ := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "clear-proj-ids-cov6", Enabled: true,
		Trigger: "new_issue", Channel: "webhook",
		WebhookURL: strPtr("https://example.com/wh"), CooldownMins: 60,
	})

	b, _ := json.Marshal(map[string]any{"project_ids": []string{}})
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/alert-rules/%s", created.ID), bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got storage.AlertRule
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.ProjectIDs) != 0 {
		t.Errorf("expected 0 project IDs, got %d", len(got.ProjectIDs))
	}
}

// ---------------------------------------------------------------------------
// uptime.go — handleCreateUptimeMonitor: valid URL with non-standard port
// ---------------------------------------------------------------------------

func TestCreateUptimeMonitor_nonStandardPort(t *testing.T) {
	truncateUptimeMonitors(t)

	body, _ := json.Marshal(map[string]any{
		"project_id": testProject.ID,
		"name":       "Port Monitor Cov6",
		"url":        "https://example.com:8443/health",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors", bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for non-standard port URL, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// uptime.go — handleUpdateUptimeMonitor: resume from paused (active status)
// ---------------------------------------------------------------------------

func TestUpdateUptimeMonitor_resumeFromPaused(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	// First pause it
	pauseBody, _ := json.Marshal(map[string]any{"status": "paused"})
	pauseReq := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/uptime-monitors/%s", m.ID), bytes.NewReader(pauseBody))
	pauseReq.AddCookie(authCookie())
	pauseReq.Header.Set("Content-Type", "application/json")
	pauseRec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(pauseRec, pauseReq)
	if pauseRec.Code != http.StatusOK {
		t.Fatalf("pause: expected 200, got %d", pauseRec.Code)
	}

	// Then resume
	resumeBody, _ := json.Marshal(map[string]any{"status": "active"})
	resumeReq := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/uptime-monitors/%s", m.ID), bytes.NewReader(resumeBody))
	resumeReq.AddCookie(authCookie())
	resumeReq.Header.Set("Content-Type", "application/json")
	resumeRec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(resumeRec, resumeReq)
	if resumeRec.Code != http.StatusOK {
		t.Fatalf("resume: expected 200, got %d: %s", resumeRec.Code, resumeRec.Body.String())
	}
	var got storage.UptimeMonitor
	if err := json.NewDecoder(resumeRec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "active" {
		t.Errorf("status: got %q, want active", got.Status)
	}
}

// ---------------------------------------------------------------------------
// uptime.go — handleUpdateUptimeMonitor: update interval and timeout only
// ---------------------------------------------------------------------------

func TestUpdateUptimeMonitor_updateIntervalAndTimeout(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	body, _ := json.Marshal(map[string]any{
		"interval_secs": 60,
		"timeout_secs":  15,
	})
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/uptime-monitors/%s", m.ID), bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got storage.UptimeMonitor
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.IntervalSecs != 60 {
		t.Errorf("interval_secs: got %d, want 60", got.IntervalSecs)
	}
	if got.TimeoutSecs != 15 {
		t.Errorf("timeout_secs: got %d, want 15", got.TimeoutSecs)
	}
}

// ---------------------------------------------------------------------------
// uptime.go — handleUpdateUptimeMonitor: update name only
// ---------------------------------------------------------------------------

func TestUpdateUptimeMonitor_updateNameOnly(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	body, _ := json.Marshal(map[string]any{"name": "Renamed via PATCH Cov6"})
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/uptime-monitors/%s", m.ID), bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got storage.UptimeMonitor
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "Renamed via PATCH Cov6" {
		t.Errorf("name: got %q, want 'Renamed via PATCH Cov6'", got.Name)
	}
}

// ---------------------------------------------------------------------------
// uptime.go — handleGetUptimeStats: bearer token wrong project → 403/404
// ---------------------------------------------------------------------------

func TestGetUptimeStats_bearerTokenWrongProject(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	other, err := storage.CreateProject(context.Background(), testPool, "uptime-stats-other-cov6", "Uptime Stats Other Cov6")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/uptime-monitors/%s/stats", m.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Errorf("expected 403 or 404 for wrong project bearer, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// uptime.go — handleListUptimeMonitors: bearer token scoped to project
// ---------------------------------------------------------------------------

func TestListUptimeMonitors_bearerTokenScoped(t *testing.T) {
	truncateUptimeMonitors(t)
	seedUptimeMonitor(t)
	truncateTokens(t)
	tok := bearerToken(t, testProject.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/uptime-monitors", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with bearer token, got %d: %s", rec.Code, rec.Body.String())
	}
	var monitors []*storage.UptimeMonitor
	if err := json.NewDecoder(rec.Body).Decode(&monitors); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(monitors) != 1 {
		t.Errorf("expected 1 monitor scoped to project, got %d", len(monitors))
	}
}

// ---------------------------------------------------------------------------
// uptime.go — handleListUptimeChecks: invalid limit param falls back to default
// ---------------------------------------------------------------------------

func TestListUptimeChecks_invalidLimit(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/uptime-monitors/%s/checks?limit=notanumber", m.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with invalid limit, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleListComments: valid issue authenticated
// ---------------------------------------------------------------------------

func TestListComments_validIssueAuthenticated(t *testing.T) {
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
	// Slice should be non-nil (empty is fine)
	if comments == nil {
		t.Error("expected non-nil comment slice")
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleCreateComment: bad JSON body returns 400
// ---------------------------------------------------------------------------

func TestCreateComment_badBodyCov6(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	issueID := seedIssueForCommentAPI(t)

	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/issues/%s/comments", issueID),
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleUpdateComment: bad JSON body returns 400
// ---------------------------------------------------------------------------

func TestUpdateComment_badBodyCov6(t *testing.T) {
	_, commentID := seedCommentForTest(t)

	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/api/comments/%s", commentID),
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body on update comment, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleDeleteComment: read-only user without manage_issues → 403
// ---------------------------------------------------------------------------

func TestDeleteComment_readOnlyUserForbiddenCov6(t *testing.T) {
	_, commentID := seedCommentForTest(t)
	roCookie := makeReadOnlyUser(t, "cov6-del-comment-ro@example.com")

	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/comments/%s", commentID), nil)
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for read-only user deleting comment, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleGetRelease: authenticated with known release ID
// ---------------------------------------------------------------------------

func TestGetRelease_authenticatedKnown(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE releases")
	var relID string
	testPool.QueryRow(context.Background(),
		"INSERT INTO releases (project_id, version) VALUES ($1, 'v-cov6-0.0.1') RETURNING id",
		testProject.ID).Scan(&relID)
	if relID == "" {
		t.Fatal("failed to insert release")
	}

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/releases/%s", relID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var rel storage.Release
	if err := json.NewDecoder(rec.Body).Decode(&rel); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rel.ID != relID {
		t.Errorf("id: got %q, want %q", rel.ID, relID)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleListAuditLog: authenticated with no filters returns rows
// ---------------------------------------------------------------------------

func TestListAuditLog_authenticatedNoFiltersCov6(t *testing.T) {
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
	if rows == nil {
		t.Error("expected non-nil slice")
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleUpdateMe: valid timezone values
// ---------------------------------------------------------------------------

func TestUpdateMe_withUTCTimezone(t *testing.T) {
	body := bytes.NewBufferString(`{"email":"test@example.com","timezone":"UTC"}`)
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

func TestUpdateMe_withAsiaTokyo(t *testing.T) {
	body := bytes.NewBufferString(`{"email":"test@example.com","timezone":"Asia/Tokyo"}`)
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
	if u.Timezone != "Asia/Tokyo" {
		t.Errorf("timezone: got %q, want Asia/Tokyo", u.Timezone)
	}
}

func TestUpdateMe_withInvalidTimezone400(t *testing.T) {
	body := bytes.NewBufferString(`{"email":"test@example.com","timezone":"Galactic/Center"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/me", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid timezone, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleChangePassword: empty field validations
// ---------------------------------------------------------------------------

func TestChangePassword_emptyCurrentPasswordCov6(t *testing.T) {
	body := bytes.NewBufferString(`{"current_password":"","new_password":"newpassword99"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/me/password", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty current_password, got %d", rec.Code)
	}
}

func TestChangePassword_emptyNewPasswordCov6(t *testing.T) {
	body := bytes.NewBufferString(`{"current_password":"testpassword","new_password":""}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/me/password", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty new_password, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// cron.go — handleListMonitors: multiple project_id query params
// ---------------------------------------------------------------------------

func TestListMonitors_multipleProjectIDs(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE cron_monitors CASCADE")
	createTestMonitor(t, "Multi Filter Cov6", "0 * * * *")

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/monitors?project_id=%s&project_id=%s", testProject.ID, testProject.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var monitors []*storage.CronMonitor
	if err := json.NewDecoder(rec.Body).Decode(&monitors); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(monitors) == 0 {
		t.Errorf("expected at least 1 monitor, got 0")
	}
}

// ---------------------------------------------------------------------------
// cron.go — handleUpdateMonitor: grace_period_secs only
// ---------------------------------------------------------------------------

func TestUpdateMonitor_graceperiodOnlyCov6(t *testing.T) {
	m := createTestMonitor(t, "Grace Period Update Cov6", "0 * * * *")
	body, _ := json.Marshal(map[string]any{"grace_period_secs": 600})
	req := httptest.NewRequest(http.MethodPatch, "/api/monitors/"+m.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated storage.CronMonitor
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.GracePeriodSecs != 600 {
		t.Errorf("grace_period_secs: got %d, want 600", updated.GracePeriodSecs)
	}
}

// ---------------------------------------------------------------------------
// cron.go — handleListCheckins: unauthenticated returns 401
// ---------------------------------------------------------------------------

func TestListCheckins_unauthenticatedCov6(t *testing.T) {
	m := createTestMonitor(t, "Unauth Checkins Cov6", "0 * * * *")
	req := httptest.NewRequest(http.MethodGet, "/api/monitors/"+m.ID+"/checkins", nil)
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// cron.go — handleCronPing: invalid duration string is ignored gracefully
// ---------------------------------------------------------------------------

func TestCronPing_invalidDurationCov6(t *testing.T) {
	m := createTestMonitor(t, "Invalid Duration Ping Cov6", "*/5 * * * *")
	req := httptest.NewRequest(http.MethodPost,
		"/api/cron/"+m.ID+"?status=ok&duration=notanumber", nil)
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for invalid duration string, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// global.go — handleGetConfig: public endpoint returns expected keys
// ---------------------------------------------------------------------------

func TestGetConfig_returnsPublicURL(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var cfg map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := cfg["public_url"]; !ok {
		t.Error("expected public_url in config response")
	}
	if _, ok := cfg["require_mfa"]; !ok {
		t.Error("expected require_mfa in config response")
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleUpdateUserPermissions: successful update
// ---------------------------------------------------------------------------

func TestUpdateUserPermissions_successCov6(t *testing.T) {
	other, err := storage.CreateUser(context.Background(), testPool, "cov6-perm-user@example.com", "password1234")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", other.ID)
	})

	body, _ := json.Marshal(map[string]any{"manage_projects": true, "manage_users": false})
	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/api/users/%s/permissions", other.ID),
		bytes.NewBuffer(body))
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
	if !u.Permissions.ManageProjects {
		t.Error("expected manage_projects=true after update")
	}
}

// ---------------------------------------------------------------------------
// uptime.go — handleCreateUptimeMonitor: multiple expected_codes formats
// ---------------------------------------------------------------------------

func TestCreateUptimeMonitor_multipleExpectedCodes(t *testing.T) {
	truncateUptimeMonitors(t)

	body, _ := json.Marshal(map[string]any{
		"project_id":     testProject.ID,
		"name":           "Multi Code Monitor",
		"url":            "https://example.com/health",
		"expected_codes": "200,201,204",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors", bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for comma-separated codes, got %d: %s", rec.Code, rec.Body.String())
	}
	var m storage.UptimeMonitor
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.ExpectedCodes != "200,201,204" {
		t.Errorf("expected_codes: got %q, want 200,201,204", m.ExpectedCodes)
	}
}

// ---------------------------------------------------------------------------
// alerts.go — handleCreateAlertRule: filter_level and min_occurrences combined
// ---------------------------------------------------------------------------

func TestCreateAlertRule_withAllFilterFields(t *testing.T) {
	truncateAlertRules(t)

	b, _ := json.Marshal(map[string]any{
		"name":               "all-filters alert",
		"trigger":            "new_issue",
		"channel":            "webhook",
		"webhook_url":        "https://example.com/wh",
		"filter_level":       "error",
		"filter_environment": "production",
		"min_occurrences":    2,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules", bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 with all filter fields, got %d: %s", rec.Code, rec.Body.String())
	}
	var rule storage.AlertRule
	if err := json.NewDecoder(rec.Body).Decode(&rule); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rule.FilterLevel == nil || *rule.FilterLevel != "error" {
		t.Errorf("filter_level: got %v, want error", rule.FilterLevel)
	}
	if rule.FilterEnvironment == nil || *rule.FilterEnvironment != "production" {
		t.Errorf("filter_environment: got %v, want production", rule.FilterEnvironment)
	}
	if rule.MinOccurrences == nil || *rule.MinOccurrences != 2 {
		t.Errorf("min_occurrences: got %v, want 2", rule.MinOccurrences)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleGetRelease: unauthenticated
// ---------------------------------------------------------------------------

func TestGetRelease_unauthenticatedCov6(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/releases/00000000-0000-0000-0000-000000000000", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleGetReleaseTransactions: unauthenticated
// ---------------------------------------------------------------------------

func TestGetReleaseTransactions_unauthenticatedCov6(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/releases/00000000-0000-0000-0000-000000000000/transactions", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleGetReleaseIssues: unauthenticated
// ---------------------------------------------------------------------------

func TestGetReleaseIssues_unauthenticatedCov6(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/releases/00000000-0000-0000-0000-000000000000/issues", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleGetRelease: not found returns 404 (rel == nil)
// (covers the "if rel == nil" branch in handleGetRelease)
// ---------------------------------------------------------------------------

func TestGetRelease_notFoundCov6(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/releases/00000000-0000-0000-0000-000000000099", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown release, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleGetReleaseTransactions: not-found with bearer token (wrong project)
// (covers the "rel.ProjectID != tokenProjID" branch)
// ---------------------------------------------------------------------------

func TestGetReleaseTransactions_bearerWrongProjectCov6(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE releases CASCADE")
	var relID string
	testPool.QueryRow(context.Background(),
		"INSERT INTO releases (project_id, version) VALUES ($1, 'v-txn-bearer-cov6') RETURNING id",
		testProject.ID).Scan(&relID)
	if relID == "" {
		t.Fatal("failed to insert release")
	}

	truncateTokens(t)
	other, err := storage.CreateProject(context.Background(), testPool,
		"rel-txn-bearer-cov6", "Rel Txn Bearer Other Cov6")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/releases/%s/transactions", relID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong project bearer on release transactions, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleGetReleaseIssues: not-found with bearer token (wrong project)
// (covers the "rel.ProjectID != tokenProjID" branch in handleGetReleaseIssues)
// ---------------------------------------------------------------------------

func TestGetReleaseIssues_bearerWrongProjectCov6(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE releases CASCADE")
	var relID string
	testPool.QueryRow(context.Background(),
		"INSERT INTO releases (project_id, version) VALUES ($1, 'v-iss-bearer-cov6') RETURNING id",
		testProject.ID).Scan(&relID)
	if relID == "" {
		t.Fatal("failed to insert release")
	}

	truncateTokens(t)
	other, err := storage.CreateProject(context.Background(), testPool,
		"rel-iss-bearer-cov6", "Rel Iss Bearer Other Cov6")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/releases/%s/issues", relID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong project bearer on release issues, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleUpdateMe: update with weekly_digest=true
// (covers the "if req.WeeklyDigest != nil" branch at extra.go:70)
// ---------------------------------------------------------------------------

func TestUpdateMe_withWeeklyDigestTrue(t *testing.T) {
	body := bytes.NewBufferString(`{"email":"test@example.com","weekly_digest":true}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/me", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for weekly_digest:true, got %d: %s", rec.Code, rec.Body.String())
	}
	var u storage.User
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !u.WeeklyDigest {
		t.Error("expected weekly_digest=true after update")
	}
}

// ---------------------------------------------------------------------------
// global.go — handleGetLatestEventGlobal: smStore path (lines 816-821)
// When smStore is non-nil, it calls smStore.ResolveEventPayload which covers
// the "if ro.smStore != nil" branch.
// ---------------------------------------------------------------------------

func TestGetLatestEventGlobal_withSmStoreCov6(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-smstore-cov6", "SmStore Cov6", "error", "error", "", "", time.Now().UTC())

	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO events (project_id, issue_id, payload, fingerprint, timestamp, received_at)
		VALUES ($1, $2, '{"level":"error"}'::jsonb, 'fp-smstore-cov6', NOW(), NOW())
	`, testProject.ID, iss.ID); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	// smHandler creates a router with a non-nil smStore
	h, _ := smHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/events/latest", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with smStore, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// router.go — SPA fallback: request for an actual dist file (lines 336-340)
// A GET for /favicon.svg should find the file in dist and serve it directly.
// ---------------------------------------------------------------------------

func TestSPAFallback_existingDistFile(t *testing.T) {
	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, nil,
		false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
	req := httptest.NewRequest(http.MethodGet, "/favicon.svg", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for /favicon.svg, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// router.go — SPA fallback: request for an unknown path serves index.html
// (covers the "dist.Open(index.html)" fallback path)
// ---------------------------------------------------------------------------

func TestSPAFallback_unknownPathServesIndex(t *testing.T) {
	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, nil,
		false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
	req := httptest.NewRequest(http.MethodGet, "/some/spa/route", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for SPA fallback to index.html, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpListReleases: nil releases when table is empty
// (covers the "if releases == nil { releases = [] }" branch at mcp.go:661)
// ---------------------------------------------------------------------------

func TestMCP_listReleases_emptyCov6(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE releases CASCADE")

	result := toolCall(t, mcpHandler(), "list_releases", map[string]any{}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError for empty list_releases: %v", result)
	}
	text := toolText(t, result)
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "[") {
		t.Errorf("expected JSON array response for empty releases, got: %s", text)
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpListReleases: with project_id argument
// ---------------------------------------------------------------------------

func TestMCP_listReleases_withProjectIDCov6(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE releases CASCADE")
	testPool.Exec(context.Background(),
		"INSERT INTO releases (project_id, version) VALUES ($1, 'v-mcplist-cov6')",
		testProject.ID)

	result := toolCall(t, mcpHandler(), "list_releases",
		map[string]any{"project_id": testProject.ID}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %v", result)
	}
	text := toolText(t, result)
	if !strings.Contains(text, "v-mcplist-cov6") {
		t.Errorf("expected release version in result, got: %s", text)
	}
}

// ---------------------------------------------------------------------------
// mcp.go — mcpArgInt: verify normal float64 (JSON) path works for coverage
// The case int: branch (mcp.go:471) is unreachable via normal JSON decode.
// ---------------------------------------------------------------------------

func TestMCP_listMonitors_defaultLimit(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE cron_monitors CASCADE")
	result := toolCall(t, mcpHandler(), "list_monitors",
		map[string]any{}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %v", result)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleListComments: bearer token wrong project returns 403/404
// (covers the enforceIssueProject bearer scope check in handleListComments)
// ---------------------------------------------------------------------------

func TestListComments_bearerTokenWrongProjectCov6(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	issueID := seedIssueForCommentAPI(t)

	truncateTokens(t)
	other, err := storage.CreateProject(context.Background(), testPool,
		"cmt-bearer-cov6", "Comment Bearer Other Cov6")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/comments", issueID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 or 404 for wrong project bearer on comments, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleDeleteComment: author can delete their own comment (204)
// (covers the success path of handleDeleteComment when actor == author)
// ---------------------------------------------------------------------------

func TestDeleteComment_authorCanDeleteCov6(t *testing.T) {
	_, commentID := seedCommentForTest(t)

	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/comments/%s", commentID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 for author deleting own comment, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleUpdateComment: author can update their own comment
// (covers the "comment.UserID == *actorID" success path)
// ---------------------------------------------------------------------------

func TestUpdateComment_authorCanUpdateCov6(t *testing.T) {
	issueID, commentID := seedCommentForTest(t)
	_ = issueID

	body := bytes.NewBufferString(`{"body":"updated by author cov6"}`)
	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/api/comments/%s", commentID), body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for author updating comment, got %d", rec.Code)
	}
	var c storage.Comment
	if err := json.NewDecoder(rec.Body).Decode(&c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if c.Body != "updated by author cov6" {
		t.Errorf("body: got %q, want 'updated by author cov6'", c.Body)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleListReleases: when releases table is empty returns [] not null
// (covers the "if releases == nil" branch at extra.go:335)
// ---------------------------------------------------------------------------

func TestListReleases_emptyTableCov6(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE releases CASCADE")

	req := httptest.NewRequest(http.MethodGet, "/api/releases", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Releases []any `json:"releases"`
		Total    int   `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Releases == nil {
		t.Error("expected non-nil releases array even when empty")
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleGetRelease: enforceTokenProject fails (bearer token for
// a release in a different project) covers the "!enforceTokenProject" branch
// ---------------------------------------------------------------------------

func TestGetRelease_bearerWrongProjectCov6b(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE releases CASCADE")
	var relID string
	testPool.QueryRow(context.Background(),
		"INSERT INTO releases (project_id, version) VALUES ($1, 'v-bearer-cov6b') RETURNING id",
		testProject.ID).Scan(&relID)
	if relID == "" {
		t.Fatal("failed to insert release")
	}

	truncateTokens(t)
	other, err := storage.CreateProject(context.Background(), testPool,
		"rel-bearer-cov6b", "Rel Bearer Other Cov6b")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/releases/%s", relID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Errorf("expected 403 or 404 for wrong project bearer on release, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleListAuditLog: rows == nil when audit log table is empty
// (covers the "if rows == nil" branch at extra.go:438)
// ---------------------------------------------------------------------------

func TestListAuditLog_emptyResultCov6(t *testing.T) {
	// Use a kind filter that will almost certainly return no rows
	req := httptest.NewRequest(http.MethodGet,
		"/api/audit?kind=nonexistent.event.type.xyz", nil)
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
	// Should be a non-nil empty slice
	if rows == nil {
		t.Error("expected non-nil slice even when empty")
	}
}

// ---------------------------------------------------------------------------
// alerts.go — handleTestAlertRule: not found returns 404 (rule == nil)
// ---------------------------------------------------------------------------

func TestTestAlertRule_notFoundCov6(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		"/api/alert-rules/00000000-0000-0000-0000-000000000099/test", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown rule test, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// alerts.go — handleTestAlertRule: evaluator == nil returns 503
// ---------------------------------------------------------------------------

func TestTestAlertRule_evaluatorNilCov6(t *testing.T) {
	truncateAlertRules(t)
	created, err := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs:   []string{testProject.ID},
		Name:         "test-eval-nil-cov6",
		Enabled:      true,
		Trigger:      "new_issue",
		Channel:      "webhook",
		WebhookURL:   strPtr("https://example.com/wh"),
		CooldownMins: 60,
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}

	// The default alertHandler() has evaluator=nil
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/alert-rules/%s/test", created.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when evaluator is nil, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// alerts.go — handleListAlertFirings: not found returns 404 (rule == nil)
// ---------------------------------------------------------------------------

func TestListAlertFirings_notFoundCov6(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/alert-rules/00000000-0000-0000-0000-000000000099/firings", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown rule firings, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// global.go — handleGetIssueHistory: nil entries nil-check
// (covers the "if entries == nil" branch in handleGetIssueHistory)
// ---------------------------------------------------------------------------

func TestGetIssueHistory_nilToEmptySliceCov6(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-hist-nil-cov6", "History Nil To Empty Cov6")

	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/history", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var entries []any
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if entries == nil {
		t.Error("expected non-nil (possibly empty) array for issue history")
	}
}

// ---------------------------------------------------------------------------
// global.go — handleUpdateIssueGlobal: same status as current → early return
// (covers the "req.Status == issue.Status" branch at global.go:669-671)
// ---------------------------------------------------------------------------

func TestUpdateIssueGlobal_sameStatusReturnsCov6(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-same-status-cov6", "Same Status Issue Cov6")
	// Issue starts as "open" by default
	body := bytes.NewBufferString(`{"status":"open"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/"+iss.ID, body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for same-status update, got %d: %s", rec.Code, rec.Body.String())
	}
	var got storage.Issue
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "open" {
		t.Errorf("expected status open, got %q", got.Status)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleGetMe: unauthenticated returns 401
// (covers the "actorID == nil" branch in handleGetMe)
// ---------------------------------------------------------------------------

func TestGetMe_unauthenticatedCov6(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated /api/me, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleUpdateMe: unauthenticated returns 401
// ---------------------------------------------------------------------------

func TestUpdateMe_unauthenticatedCov6(t *testing.T) {
	body := bytes.NewBufferString(`{"email":"x@example.com"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/me", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated PATCH /api/me, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleListUsers: returns non-nil slice
// (covers the "if users == nil { users = [] }" guard)
// ---------------------------------------------------------------------------

func TestListUsers_nonNilSliceCov6(t *testing.T) {
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
	if users == nil {
		t.Error("expected non-nil user list")
	}
}

// ---------------------------------------------------------------------------
// global.go — handleGetIssueTags: for known issue with tags
// (more coverage of handleGetIssueTags success path)
// ---------------------------------------------------------------------------

func TestGetIssueTags_withEventsCov6(t *testing.T) {
	truncateIssues(t)
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-tags-cov6", "Tags Issue Cov6", "error", "error", "", "", time.Now().UTC())

	// Insert an event with tags for this issue
	testPool.Exec(context.Background(), `
		INSERT INTO events (project_id, issue_id, payload, fingerprint, timestamp, received_at)
		VALUES ($1, $2, '{"level":"error","tags":{"browser":"Chrome"}}'::jsonb, 'fp-tags-cov6', NOW(), NOW())
	`, testProject.ID, iss.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/tags", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// cron.go — handleListMonitors: bearerProjectIDs filters (covers the
// "bearerProjectIDs" call with bearer token, hitting the "isBearer" branch)
// ---------------------------------------------------------------------------

func TestListMonitors_bearerTokenScopedCov6(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE cron_monitors CASCADE")
	createTestMonitor(t, "Bearer Scoped Monitor Cov6", "0 * * * *")
	truncateTokens(t)
	tok := bearerToken(t, testProject.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/monitors", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with bearer token, got %d: %s", rec.Code, rec.Body.String())
	}
	var monitors []*storage.CronMonitor
	if err := json.NewDecoder(rec.Body).Decode(&monitors); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// ---------------------------------------------------------------------------
// global.go — handleGetProjectStats: with bearerProjectIDs token
// (covers the bearerProjectIDs != empty path in handleGetProjectStats)
// ---------------------------------------------------------------------------

func TestGetProjectStats_bearerTokenCov6(t *testing.T) {
	truncateTokens(t)
	tok := bearerToken(t, testProject.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/stats/projects", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with bearer token, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// extra.go — handleCreateComment: empty body returns 400
// (covers the "req.Body == ''" branch at extra.go:219)
// ---------------------------------------------------------------------------

func TestCreateComment_emptyBodyCov6(t *testing.T) {
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
