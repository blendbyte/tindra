package api_test

// coverage7_test.go — additional tests targeting uncovered branches in
// logs.go, global.go, and transactions.go.
// Focus: logs environment/trace_id filters, logs cursor_time+cursor_id,
// invalid regex in scrub_patterns, transaction stats with custom hours,
// export issues with tag filters, list events cursor_time, combined filters.

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
// logs.go — handleListLogs: environment filter (line 17 filter.Environment)
// The environment field in storage.LogFilter is populated but never exercised
// via ?environment= in any existing test.
// ---------------------------------------------------------------------------

func TestListLogs_withEnvironmentFilter(t *testing.T) {
	truncateLogs(t)

	// Seed one log so the table is non-empty and the filter is meaningful.
	seedLog(t, "info", "production startup")

	req := httptest.NewRequest(http.MethodGet,
		"/api/logs?environment=production", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with environment filter, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Logs    []json.RawMessage `json:"logs"`
		HasMore bool              `json:"has_more"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Response can be empty (logs have no environment column seeded), just
	// verifying the filter path runs without error.
}

// ---------------------------------------------------------------------------
// logs.go — handleListLogs: trace_id filter (line 19 filter.TraceID)
// ---------------------------------------------------------------------------

func TestListLogs_withTraceIDFilter(t *testing.T) {
	truncateLogs(t)

	req := httptest.NewRequest(http.MethodGet,
		"/api/logs?trace_id=abc123def456", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with trace_id filter, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Logs []json.RawMessage `json:"logs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// ---------------------------------------------------------------------------
// logs.go — handleListLogs: cursor_time AND cursor_id together (lines 23-30)
// Setting both exercises the cursor_time parse path (lines 23-27) AND the
// cursor_id path (lines 28-30) in a single request.
// ---------------------------------------------------------------------------

func TestListLogs_withCursorTimeAndCursorID(t *testing.T) {
	truncateLogs(t)
	seedLog(t, "info", "cursor log")

	cursorTime := time.Now().UTC().Format(time.RFC3339Nano)
	cursorID := "00000000-0000-0000-0000-000000000001"

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/logs?cursor_time=%s&cursor_id=%s",
			cursorTime, cursorID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with cursor_time+cursor_id, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// logs.go — handleListLogs: invalid cursor_time is silently ignored (line 25)
// When cursor_time cannot be parsed time.Parse fails, cursorTime stays nil.
// ---------------------------------------------------------------------------

func TestListLogs_withInvalidCursorTimeSilentlyIgnored(t *testing.T) {
	truncateLogs(t)

	req := httptest.NewRequest(http.MethodGet,
		"/api/logs?cursor_time=not-a-timestamp&cursor_id=some-id", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for invalid cursor_time, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// global.go — handleUpdateProjectPrivacy: invalid regex in scrub_patterns
// ValidateScrubPatterns checks the regex compiles; an invalid regex returns 400.
// The existing tests cover: bad JSON structure ("not-an-array") and too many
// patterns (21 entries), but not an invalid regex string.
// ---------------------------------------------------------------------------

func TestUpdateProjectPrivacy_invalidRegexInScrubPattern(t *testing.T) {
	p, err := storage.CreateProject(context.Background(), testPool,
		"cov7-priv-badregex", "Cov7 Privacy Bad Regex")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", p.ID)
	})

	// "[invalid" is not a valid Go regex — regexp.Compile returns an error.
	body := bytes.NewBufferString(
		`{"scrub_fields":[],"scrub_patterns":[{"name":"bad","pattern":"[invalid","builtin":false}]}`)
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/projects/%s/privacy", p.ID), body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid regex in scrub_pattern, got %d: %s",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "regular expression") {
		t.Errorf("expected error message to mention regular expression, got: %s", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// transactions.go — handleTransactionStats: valid non-default hours value
// hours=48 is inside [1,168] so the if-branch at line 98 is entered and
// hours becomes 48 rather than the default 24.
// The existing test TestTransactionStats_withData uses hours=24; this uses 48.
// ---------------------------------------------------------------------------

func TestTransactionStats_withHours48(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/transactions/stats?hours=48", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for hours=48, got %d: %s", rec.Code, rec.Body.String())
	}
	var stats storage.TransactionPercentiles
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// ---------------------------------------------------------------------------
// global.go — handleExportIssues: tag_key + tag_value query params
// Lines 489-490 populate filter.TagKey and filter.TagValue from the export
// endpoint. No existing test sends both to the export route.
// ---------------------------------------------------------------------------

func TestExportIssues_withTagKeyAndValue(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/issues/export?tag_key=browser&tag_value=Chrome", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for export with tag filters, got %d: %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("expected text/csv content type, got %q", ct)
	}
}

// ---------------------------------------------------------------------------
// global.go — handleListAllIssues: tag_key + tag_value combined with status
// Tests a multi-filter path that exercises the TagKey, TagValue, and Status
// fields simultaneously — a path not tested by any single-filter test.
// ---------------------------------------------------------------------------

func TestListAllIssues_tagFiltersWithStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/issues?tag_key=env&tag_value=staging&status=open", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with combined tag+status filters, got %d: %s",
			rec.Code, rec.Body.String())
	}
	var resp struct {
		Issues []json.RawMessage `json:"issues"`
		Total  int               `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// ---------------------------------------------------------------------------
// global.go — handleListEventsForIssue: valid cursor_time (line 913-915)
// Sends a well-formed RFC3339Nano cursor_time so the inner parse succeeds and
// cursorTime is set. No existing test exercises this code path for events.
// ---------------------------------------------------------------------------

func TestListEventsForIssue_withValidCursorTime(t *testing.T) {
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-ev-cursor-cov7", "Events Cursor Cov7",
		"error", "error", "", "", time.Now().UTC())
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM issues WHERE id=$1", iss.ID)
	})

	cursorTime := time.Now().UTC().Format(time.RFC3339Nano)
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/events?cursor_time=%s", iss.ID, cursorTime), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid cursor_time, got %d: %s",
			rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// global.go — handleListEventsForIssue: valid cursor_time AND cursor_id
// Exercises both cursor_time parse branch AND cursor_id branch together.
// ---------------------------------------------------------------------------

func TestListEventsForIssue_withCursorTimeAndID(t *testing.T) {
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-ev-cursor2-cov7", "Events Cursor2 Cov7",
		"error", "error", "", "", time.Now().UTC())
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM issues WHERE id=$1", iss.ID)
	})

	cursorTime := time.Now().UTC().Format(time.RFC3339Nano)
	cursorID := "00000000-0000-0000-0000-000000000001"
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/events?cursor_time=%s&cursor_id=%s",
			iss.ID, cursorTime, cursorID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with cursor_time+cursor_id, got %d: %s",
			rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// global.go — handleListAllIssues: q param truncated to 200 chars
// truncSearch caps q at 200 chars; sending 201+ chars exercises the truncation
// branch (global.go line 27). The existing coverage4 test TestListAllIssues_longSearch
// sends a long q but via handleListIssues (project-scoped), not handleListAllIssues.
// ---------------------------------------------------------------------------

func TestListAllIssues_veryLongQParamTruncated(t *testing.T) {
	longQ := strings.Repeat("x", 205)
	req := httptest.NewRequest(http.MethodGet,
		"/api/issues?q="+longQ, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for long q param, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// global.go — handleExportIssues: q param via export endpoint
// handleExportIssues does NOT call truncSearch on its own q parameter — the
// filter doesn't include a Title field. This tests the export route accepts a
// long status filter without truncation concerns.
// ---------------------------------------------------------------------------

func TestExportIssues_withStatusAndFormatJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/issues/export?status=open&format=json", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for export with status+format=json, got %d: %s",
			rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected application/json for format=json, got %q", ct)
	}
}

// ---------------------------------------------------------------------------
// global.go — handleUpdateProjectPrivacy: non-empty scrub_fields with no
// scrub_patterns (omitting scrub_patterns uses the default "[]" fallback at
// line 271-272) — covers the "if len(req.ScrubPatterns) == 0" branch.
// ---------------------------------------------------------------------------

func TestUpdateProjectPrivacy_scrubFieldsOnlyNoPatterns(t *testing.T) {
	p, err := storage.CreateProject(context.Background(), testPool,
		"cov7-priv-fields-only", "Cov7 Privacy Fields Only")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", p.ID)
	})

	// Omit scrub_patterns entirely so the "len == 0" default branch fires.
	body := bytes.NewBufferString(`{"scrub_fields":["password","secret"]}`)
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/projects/%s/privacy", p.ID), body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for scrub_fields only (no patterns), got %d: %s",
			rec.Code, rec.Body.String())
	}
	var got storage.Project
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != p.ID {
		t.Errorf("project ID mismatch: got %q, want %q", got.ID, p.ID)
	}
}

// ---------------------------------------------------------------------------
// global.go — handleListAllIssues: level filter combined with env filter
// Exercises multiple IssueFilter fields simultaneously.
// ---------------------------------------------------------------------------

func TestListAllIssues_withLevelAndEnvFilters(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/issues?level=error&env=production", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with level+env filters, got %d: %s",
			rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// transactions.go — handleTransactionStats: hours at max boundary (168)
// n=168 satisfies n >= 1 && n <= 168 so the if-branch is entered and
// hours becomes 168.
// ---------------------------------------------------------------------------

func TestTransactionStats_withMaxHours(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/transactions/stats?hours=168", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for hours=168, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// transactions.go — handleTransactionStats: hours above max (169) falls back
// n=169 fails n <= 168 so the if-branch is skipped and hours stays at 24.
// ---------------------------------------------------------------------------

func TestTransactionStats_withAboveMaxHours(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/transactions/stats?hours=169", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for hours=169 (out of range), got %d: %s",
			rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// global.go — handleExportIssues: assignee_id filter populates filter.AssigneeID
// (line 486). No existing export test sends assignee_id.
// ---------------------------------------------------------------------------

func TestExportIssues_withAssigneeIDFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/export?assignee_id=%s", testUser.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for export with assignee_id, got %d: %s",
			rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("expected text/csv content type, got %q", ct)
	}
}

// ---------------------------------------------------------------------------
// global.go — handleExportIssues: since=unknown falls through switch without
// setting SinceLast (the d == 0 guard). Tests same fallthrough path as
// handleListAllIssues_sinceUnknown but via the export route.
// ---------------------------------------------------------------------------

func TestExportIssues_sinceUnknownValueIgnored(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/issues/export?since=6months", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for unknown since value, got %d: %s",
			rec.Code, rec.Body.String())
	}
}
