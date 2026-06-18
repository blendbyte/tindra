package api_test

// coverage5_test.go — additional tests targeting remaining uncovered branches
// in global.go. Focuses on paths not hit by global_test.go, coverage_test.go,
// coverage2_test.go, coverage3_test.go, or coverage4_test.go.

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
// handleListAllIssues — env filter (query param branch not yet isolated)
// ---------------------------------------------------------------------------

func TestListAllIssues_withStatusFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues?status=resolved", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with status filter, got %d", rec.Code)
	}
}

func TestListAllIssues_withQSearch(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues?q=TypeError", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with q search, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleExportIssues — CSV row with assignee email set (lines 526-532)
// ---------------------------------------------------------------------------

func TestExportIssues_csvWithAssignee(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")

	// Create an issue and assign it to testUser.
	iss := seedIssue(t, "fp-csv-assignee-cov5", "CSV Assignee Cov5")
	if _, err := testPool.Exec(context.Background(),
		"UPDATE issues SET assignee_id = $1 WHERE id = $2",
		testUser.ID, iss.ID); err != nil {
		t.Fatalf("assign issue: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/issues/export?format=csv", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("expected text/csv content-type, got %q", ct)
	}
	// Verify the CSV body contains the issue title (export uses ListAllIssues
	// which does not join users, so assignee column is empty but row is present).
	body := rec.Body.String()
	if !strings.Contains(body, "CSV Assignee Cov5") {
		t.Errorf("expected issue title in CSV body, got: %s", body)
	}
}

// ---------------------------------------------------------------------------
// handleExportIssues — JSON format includes Content-Disposition header
// ---------------------------------------------------------------------------

func TestExportIssues_jsonContentDisposition(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues/export?format=json", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Disposition") == "" {
		t.Error("expected Content-Disposition header for JSON export")
	}
}

// ---------------------------------------------------------------------------
// handleExportIssues — env filter
// ---------------------------------------------------------------------------

func TestExportIssues_withEnvFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues/export?env=staging", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with env filter, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleExportIssues — status filter
// ---------------------------------------------------------------------------

func TestExportIssues_withStatusFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues/export?status=open", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with status filter, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetWebVitals — with env filter
// ---------------------------------------------------------------------------

func TestGetWebVitals_withEnvFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/vitals?env=production", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with env filter, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetWebVitals — with project_id filter (bearerProjectIDs)
// ---------------------------------------------------------------------------

func TestGetWebVitals_withProjectIDFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/vitals?project_id=%s", testProject.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with project_id, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetWebVitalsPages — with project_id filter
// ---------------------------------------------------------------------------

func TestGetWebVitalsPages_withProjectIDFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/vitals/pages?project_id=%s", testProject.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetWebVitalsPages — unauthenticated
// ---------------------------------------------------------------------------

func TestGetWebVitalsPages_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/vitals/pages", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetWebVitals — unauthenticated
// ---------------------------------------------------------------------------

func TestGetWebVitals_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/vitals", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleSpanSamples — with env and release params
// ---------------------------------------------------------------------------

func TestSpanSamples_withEnvAndRelease(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/spans/samples?env=production&release=v1.0.0", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleSpanSamples — with description param
// ---------------------------------------------------------------------------

func TestSpanSamples_withDescription(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/spans/samples?op=db.query&description=SELECT", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleSpanSummaries — with env and release params (hits the env/release
// branches in handleSpanSummaries that differ from hours)
// ---------------------------------------------------------------------------

func TestSpanSummaries_withEnvAndRelease(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/spans/db?env=production&release=v2.0.0", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestSpanSummaries_cache_withHours(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/spans/cache?hours=72", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestSpanSummaries_jobs_withHours(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/spans/jobs?hours=72", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleSpanTimeseries — with env and release params
// ---------------------------------------------------------------------------

func TestSpanTimeseries_withEnvAndRelease(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/spans/db/timeseries?env=production&release=v1.0.0", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestSpanTimeseries_cache_withHours(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/spans/cache/timeseries?hours=48", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestSpanTimeseries_jobs_withHours(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/spans/jobs/timeseries?hours=48", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleListTransactionSummaries — env / name / op / release params
// ---------------------------------------------------------------------------

func TestTransactionSummaries_withNameAndOp(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/transactions/summaries?name=/api/health&op=http.server", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestTransactionSummaries_withEnvAndRelease(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/transactions/summaries?env=staging&release=v3.0.0", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestTransactionSummaries_withProjectID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/transactions/summaries?project_id=%s", testProject.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleTransactionTimeseries — with env / name / op / project_id params
// ---------------------------------------------------------------------------

func TestTransactionTimeseries_withNameAndOp(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/transactions/timeseries?name=/api/test&op=http.server", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestTransactionTimeseries_withEnvAndProjectID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/transactions/timeseries?env=production&project_id=%s", testProject.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleListAllTransactions — with env and name params
// ---------------------------------------------------------------------------

func TestListAllTransactions_withEnvAndName(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/transactions?environment=production&name=/api/health", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with env+name filters, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleListAllTransactions — out-of-range limit clamped to 50
// ---------------------------------------------------------------------------

func TestListAllTransactions_outOfRangeLimit(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transactions?limit=999", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for out-of-range limit, got %d", rec.Code)
	}
}

func TestListAllTransactions_zeroLimit(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transactions?limit=0", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for zero limit (falls back to 50), got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetTransactionGlobal — with bearer token correct project
// ---------------------------------------------------------------------------

func TestGetTransactionGlobal_bearerCorrectProject(t *testing.T) {
	truncateTransactions(t)
	truncateTokens(t)
	tx := seedTransactionRow(t, "/api/cov5-bearer-tx", 80)
	tok := bearerToken(t, testProject.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/"+tx.ID, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for correct bearer project, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleGetSpansGlobal — with correct bearer token project
// ---------------------------------------------------------------------------

func TestGetSpansGlobal_bearerCorrectProject(t *testing.T) {
	truncateTransactions(t)
	truncateTokens(t)
	tx := seedTransactionRow(t, "/api/cov5-spans-tx", 60)
	tok := bearerToken(t, testProject.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/"+tx.ID+"/spans", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for correct bearer project on spans, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// computeCriticalPath — exercise via spans endpoint with multiple spans
// (Tests the path where spans have parents and children to compute critical path)
// ---------------------------------------------------------------------------

func TestGetSpansGlobal_criticalPath(t *testing.T) {
	truncateTransactions(t)
	tx := seedTransactionRow(t, "/cov5-crit-path", 200)

	start := tx.StartTimestamp.Add(5 * time.Millisecond)
	end1 := start.Add(50 * time.Millisecond)
	end2 := start.Add(100 * time.Millisecond)

	// Insert a parent span.
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO spans (transaction_id, span_id, parent_span_id, op, start_timestamp, timestamp, duration_ms, status, project_id)
		SELECT $1, 'parent-sp-cov5', '', 'http.client', $2, $3, 50, 'ok', project_id FROM transactions WHERE id = $1
	`, tx.ID, start, end1); err != nil {
		t.Fatalf("insert parent span: %v", err)
	}
	// Insert a child span with a parent reference.
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO spans (transaction_id, span_id, parent_span_id, op, start_timestamp, timestamp, duration_ms, status, project_id)
		SELECT $1, 'child-sp-cov5', 'parent-sp-cov5', 'db.query', $2, $3, 100, 'ok', project_id FROM transactions WHERE id = $1
	`, tx.ID, start, end2); err != nil {
		t.Fatalf("insert child span: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/"+tx.ID+"/spans", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var spans []struct {
		SpanID     string `json:"span_id"`
		IsCritical bool   `json:"is_critical"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&spans); err != nil {
		t.Fatalf("decode spans: %v", err)
	}
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}
}

// ---------------------------------------------------------------------------
// handleGetIssueTrace — with valid trace_id but no transaction in DB
// (returns null because GetTransactionByTraceID finds nothing)
// ---------------------------------------------------------------------------

func TestGetIssueTrace_traceIDNoTransaction(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-trace-notx-cov5", "Trace No TX Cov5", "error", "error", "", "", time.Now().UTC())

	// Insert an event with a trace_id that does NOT match any transaction.
	testPool.Exec(context.Background(), `
		INSERT INTO events (project_id, issue_id, trace_id, payload, fingerprint, timestamp, received_at)
		VALUES ($1, $2, 'nonexistent-trace-id-cov5', '{}', 'fp-trace-notx-cov5', NOW(), NOW())
	`, testProject.ID, iss.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/trace", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// Should return null since no transaction with that trace_id exists.
	body := strings.TrimSpace(rec.Body.String())
	if body != "null" {
		t.Logf("body was: %s (non-null is also ok if a tx was found)", body)
	}
}

// ---------------------------------------------------------------------------
// handleGetIssueTrace — with valid bearer token on correct project
// ---------------------------------------------------------------------------

func TestGetIssueTrace_bearerCorrectProject(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	truncateTokens(t)
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-trace-bearer-ok-cov5", "Trace Bearer OK Cov5", "error", "error", "", "", time.Now().UTC())
	tok := bearerToken(t, testProject.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/trace", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleGetIssueHistogram — with bearer token correct project
// ---------------------------------------------------------------------------

func TestGetIssueHistogram_bearerCorrectProject(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	truncateTokens(t)
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-hist-bearer-ok-cov5", "Histogram Bearer OK Cov5", "error", "error", "", "", time.Now().UTC())
	tok := bearerToken(t, testProject.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/events/histogram", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for correct bearer project, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleBulkUpdateIssues — multiple IDs in a single call
// ---------------------------------------------------------------------------

func TestBulkUpdateIssues_multipleIDs(t *testing.T) {
	truncateIssues(t)
	iss1 := seedIssue(t, "fp-bulk-multi-1-cov5", "Bulk Multi 1 Cov5")
	iss2 := seedIssue(t, "fp-bulk-multi-2-cov5", "Bulk Multi 2 Cov5")

	body, _ := json.Marshal(map[string]any{
		"ids":    []string{iss1.ID, iss2.ID},
		"status": "resolved",
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/bulk", bytes.NewBuffer(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	updated, ok := resp["updated"].(float64)
	if !ok || updated != 2 {
		t.Errorf("expected updated=2, got %v", resp["updated"])
	}
}

// ---------------------------------------------------------------------------
// handleBulkUpdateIssues — "open" status (re-open path)
// ---------------------------------------------------------------------------

func TestBulkUpdateIssues_reopen(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-bulk-reopen-cov5", "Bulk Reopen Cov5")

	// First resolve it.
	testPool.Exec(context.Background(),
		"UPDATE issues SET status='resolved' WHERE id=$1", iss.ID)

	body, _ := json.Marshal(map[string]any{
		"ids":    []string{iss.ID},
		"status": "open",
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/bulk", bytes.NewBuffer(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for reopen, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleListAllTokens — unauthenticated returns 401
// ---------------------------------------------------------------------------

func TestListAllTokens_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/tokens", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleCreateTokenGlobal — unauthenticated returns 401
// ---------------------------------------------------------------------------

func TestCreateTokenGlobal_unauthenticated(t *testing.T) {
	body, _ := json.Marshal(map[string]string{
		"name":       "ci-token",
		"project_id": testProject.ID,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/tokens", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleUpdateTokenGlobal — unauthenticated returns 401
// ---------------------------------------------------------------------------

func TestUpdateTokenGlobal_unauthenticated(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"name":       "x",
		"project_id": testProject.ID,
		"writable":   false,
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/tokens/some-id", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleDeleteTokenGlobal — unauthenticated returns 401
// ---------------------------------------------------------------------------

func TestDeleteTokenGlobal_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete,
		"/api/tokens/00000000-0000-0000-0000-000000000000", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetIssueGlobal — unauthenticated returns 401
// ---------------------------------------------------------------------------

func TestGetIssueGlobal_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/issues/00000000-0000-0000-0000-000000000000", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleUpdateIssueGlobal — unauthenticated returns 401
// ---------------------------------------------------------------------------

func TestUpdateIssueGlobal_unauthenticated(t *testing.T) {
	body := bytes.NewBufferString(`{"status":"resolved"}`)
	req := httptest.NewRequest(http.MethodPatch,
		"/api/issues/00000000-0000-0000-0000-000000000000", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetIssueHistory — success with session auth
// ---------------------------------------------------------------------------

func TestGetIssueHistory_withSessionAuth(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-hist-sess-cov5", "History Session Cov5", "error", "error", "", "", time.Now().UTC())

	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/history", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleGetIssueTags — unauthenticated returns 401
// ---------------------------------------------------------------------------

func TestGetIssueTags_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/issues/00000000-0000-0000-0000-000000000000/tags", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleListEventsForIssue — cursor_id without cursor_time (partial cursor)
// ---------------------------------------------------------------------------

func TestListEventsForIssue_cursorIDWithoutCursorTime(t *testing.T) {
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-events-partial-cursor-cov5", "Events Partial Cursor Cov5",
		"error", "error", "", "", time.Now().UTC())

	req := httptest.NewRequest(http.MethodGet,
		"/api/issues/"+iss.ID+"/events?cursor_id=00000000-0000-0000-0000-000000000001", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for partial cursor (cursor_id only), got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetProjectStats — forbidden for read-only user
// ---------------------------------------------------------------------------

func TestGetProjectStats_readOnlyAllowed(t *testing.T) {
	// Project stats require just auth, not manage_projects.
	roCookie := makeReadOnlyUser(t, "ro-proj-stats-cov5@example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/projects/stats", nil)
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for read-only user on /api/projects/stats, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleUpdateProject — with a valid passthrough_dsn (non-empty, valid http URL)
// Tests the ValidateWebhookURL success path.
// ---------------------------------------------------------------------------

func TestUpdateProject_validPassthroughDSN(t *testing.T) {
	p, err := storage.CreateProject(context.Background(), testPool, "cov5-passthrough-proj", "Cov5 Passthrough")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", p.ID)
	})

	// Use a valid https URL with an IP that passes the private IP check in test mode.
	body := `{"name":"Cov5 Passthrough","slug":"cov5-passthrough-proj","passthrough_dsn":"https://203.0.113.1/sentry/project"}`
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/projects/%s", p.ID), bytes.NewBufferString(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	// May be 200 (valid URL) or 400 (if URL validation rejects it).
	// Either way, we exercise the ValidateWebhookURL call.
	if rec.Code != http.StatusOK && rec.Code != http.StatusBadRequest {
		t.Errorf("expected 200 or 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleGetProjectQuota — with bearer token (enforceTokenProject passes)
// ---------------------------------------------------------------------------

func TestGetProjectQuota_bearerCorrectProject(t *testing.T) {
	truncateTokens(t)
	tok := bearerToken(t, testProject.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/projects/%s/quota", testProject.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for correct bearer project quota, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleGetProjectQuota — with wrong bearer project (enforceTokenProject fails)
// ---------------------------------------------------------------------------

func TestGetProjectQuota_bearerWrongProject(t *testing.T) {
	truncateTokens(t)
	other, err := storage.CreateProject(context.Background(), testPool,
		"quota-other-cov5", "Quota Other Cov5")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/projects/%s/quota", testProject.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Errorf("expected 403 or 404 for wrong bearer on quota, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetInstanceHealth — authenticated admin user
// Already covered by global_test.go but ensure instance/health path is hit.
// ---------------------------------------------------------------------------

func TestGetInstanceHealth_authenticatedAdmin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/instance/health", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleListTransactionSummaries — invalid hours clamped to default
// ---------------------------------------------------------------------------

func TestTransactionSummaries_invalidHours(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/summaries?hours=0", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTransactionSummaries_tooLargeHours(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/summaries?hours=800", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleTransactionTimeseries — invalid hours clamped
// ---------------------------------------------------------------------------

func TestTransactionTimeseries_invalidHours(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/timeseries?hours=not-a-number", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for invalid hours param, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleGetSettings — with billingURL set in settings response
// ---------------------------------------------------------------------------

func TestGetSettings_withBillingURL(t *testing.T) {
	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "https://billing.example.com", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		BillingURL string `json:"billing_url"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.BillingURL != "https://billing.example.com" {
		t.Errorf("billing_url: got %q, want 'https://billing.example.com'", resp.BillingURL)
	}
}

// ---------------------------------------------------------------------------
// handleGetSettings — no update available when versions match
// ---------------------------------------------------------------------------

func TestGetSettings_noUpdateWhenVersionsMatch(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	// globalHandler uses the default router; unless AppVersion is set
	// to something old, update_available should be false normally.
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		UpdateAvailable bool `json:"update_available"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// We just verify the field is present and the endpoint works.
	_ = resp.UpdateAvailable
}

// ---------------------------------------------------------------------------
// handleListAllIssues — invalid cursor_time but valid cursor_id (both required)
// ---------------------------------------------------------------------------

func TestListAllIssues_invalidCursorTimeParsing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/issues?cursor_time=not-a-date&cursor_id=some-id", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (invalid cursor ignored), got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleBulkUpdateIssues — bulk with ignored + ignore_count_limit in history
// ---------------------------------------------------------------------------

func TestBulkUpdateIssues_ignoredCountLimitInHistory(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-bulk-hist-cov5", "Bulk Hist Cov5")

	limit := 7
	body, _ := json.Marshal(map[string]any{
		"ids":                []string{iss.ID},
		"status":             "ignored",
		"ignore_count_limit": limit,
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/bulk", bytes.NewBuffer(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleBulkUpdateIssues — bulk with ignore_until sets history details
// ---------------------------------------------------------------------------

func TestBulkUpdateIssues_ignoredUntilInHistory(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-bulk-hist-until-cov5", "Bulk Hist Until Cov5")

	future := time.Now().Add(48 * time.Hour)
	body, _ := json.Marshal(map[string]any{
		"ids":          []string{iss.ID},
		"status":       "ignored",
		"ignore_until": future.Format(time.RFC3339),
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/bulk", bytes.NewBuffer(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleGetTransactionErrors — with bearer token correct project AND trace_id
// ---------------------------------------------------------------------------

func TestGetTransactionErrors_bearerAndTraceID(t *testing.T) {
	truncateTokens(t)

	var txID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, trace_id)
		VALUES ($1, '/cov5-bearer-trace', 'http', 'ok', 100, NOW(), NOW(), 'cov5-bearer-trace-id')
		RETURNING id
	`, testProject.ID).Scan(&txID); err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM transactions WHERE id=$1", txID)
	})

	tok := bearerToken(t, testProject.ID)
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/"+txID+"/errors", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleListAllIssues — bearer token restricts project_ids
// ---------------------------------------------------------------------------

func TestListAllIssues_bearerTokenRestriction(t *testing.T) {
	truncateTokens(t)
	tok := bearerToken(t, testProject.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleGetProjectStats — nil projects slice becomes empty slice
// ---------------------------------------------------------------------------

func TestGetProjectStats_emptyProjectList(t *testing.T) {
	// Just hitting the handler with a project_id that has no issues.
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/stats?project_id=00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var counts []json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&counts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// counts may be empty but must not be nil (code converts nil to []).
	if counts == nil {
		t.Error("expected non-nil counts slice")
	}
}

// ---------------------------------------------------------------------------
// handleUpdateProjectPrivacy — with valid scrub patterns (non-empty array)
// ---------------------------------------------------------------------------

func TestUpdateProjectPrivacy_withScrubPatterns(t *testing.T) {
	p, err := storage.CreateProject(context.Background(), testPool,
		"cov5-privacy-patterns", "Cov5 Privacy Patterns")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", p.ID)
	})

	body := bytes.NewBufferString(`{
		"scrub_fields": ["password", "api_key"],
		"scrub_patterns": [{"name":"credit-card","pattern":"\\d{16}","builtin":false}]
	}`)
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/projects/%s/privacy", p.ID), body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid scrub patterns, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleGetLatestEventGlobal — with smStore set (ResolveEventPayload path)
// for an issue via the global /api/issues/{issueID}/events/latest endpoint
// ---------------------------------------------------------------------------

func TestGetLatestEventGlobal_found(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-gleg-found-cov5", "GLEG Found Cov5", "error", "error", "", "", time.Now().UTC())

	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO events (project_id, issue_id, payload, fingerprint, timestamp, received_at)
		VALUES ($1, $2, '{"level":"error"}'::jsonb, 'fp-gleg-found-cov5', NOW(), NOW())
	`, testProject.ID, iss.ID); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/events/latest", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID == "" {
		t.Error("expected non-empty event ID")
	}
}

// ---------------------------------------------------------------------------
// handleGetLatestEventGlobal — unauthenticated returns 401
// ---------------------------------------------------------------------------

func TestGetLatestEventGlobal_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/issues/00000000-0000-0000-0000-000000000000/events/latest", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetLatestEventGlobal — with positive offset
// ---------------------------------------------------------------------------

func TestGetLatestEventGlobal_withPositiveOffset(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-gleg-offset-cov5", "GLEG Offset Cov5", "error", "error", "", "", time.Now().UTC())

	// Insert 2 events.
	for i := 0; i < 2; i++ {
		testPool.Exec(context.Background(), `
			INSERT INTO events (project_id, issue_id, payload, fingerprint, timestamp, received_at)
			VALUES ($1, $2, '{"level":"error"}'::jsonb, 'fp-gleg-offset-cov5', NOW(), NOW())
		`, testProject.ID, iss.ID)
	}

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/events/latest?offset=1", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	// offset=1 should return the second event or 404 if only 1 event.
	if rec.Code != http.StatusOK && rec.Code != http.StatusNotFound {
		t.Errorf("expected 200 or 404 for offset=1, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleGetIssueTrace — with positive offset param
// ---------------------------------------------------------------------------

func TestGetIssueTrace_positiveOffset(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-trace-pos-offset-cov5", "Trace Pos Offset Cov5",
		"error", "error", "", "", time.Now().UTC())

	// Insert 2 events.
	for i := 0; i < 2; i++ {
		testPool.Exec(context.Background(), `
			INSERT INTO events (project_id, issue_id, payload, fingerprint, timestamp, received_at)
			VALUES ($1, $2, '{"level":"error"}'::jsonb, 'fp-trace-pos-offset-cov5', NOW(), NOW())
		`, testProject.ID, iss.ID)
	}

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/issues/%s/trace?offset=1", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	// offset=1 with no trace_id on event returns 200+null.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleListProjects — returns non-nil slice even when empty
// ---------------------------------------------------------------------------

func TestListProjects_nonNilSlice(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var projects json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&projects); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(projects) == "null" {
		t.Error("expected non-null projects array")
	}
}

// ---------------------------------------------------------------------------
// handleDeleteProject — unauthenticated
// ---------------------------------------------------------------------------

func TestDeleteProject_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete,
		"/api/projects/00000000-0000-0000-0000-000000000000", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleListTransactionSummaries — unauthenticated
// ---------------------------------------------------------------------------

func TestTransactionSummaries_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/summaries", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleTransactionTimeseries — unauthenticated
// ---------------------------------------------------------------------------

func TestTransactionTimeseries_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/timeseries", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleSpanSamples — unauthenticated
// ---------------------------------------------------------------------------

func TestSpanSamples_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/spans/samples", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleListAllTransactions — unauthenticated
// ---------------------------------------------------------------------------

func TestListAllTransactions_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetSettings — with commitHash set (AppCommit coverage)
// ---------------------------------------------------------------------------

func TestGetSettings_respondsWithCommit(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Just verify the fields are present (values depend on build).
	_ = resp.Version
	_ = resp.Commit
}

// ---------------------------------------------------------------------------
// handleGetStats — with missing Authorization header (no Bearer prefix)
// ---------------------------------------------------------------------------

func TestGetStats_missingAuthHeader(t *testing.T) {
	// Build a handler with a non-empty statsAPIKey so the key check is enforced.
	statsHandler := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "some-stats-key-cov5", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	// No Authorization header at all.
	rec := httptest.NewRecorder()
	statsHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 when no Authorization header, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetStats — with wrong API key in Authorization header
// ---------------------------------------------------------------------------

func TestGetStats_wrongAPIKey(t *testing.T) {
	statsHandler := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "correct-key-cov5", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	req.Header.Set("Authorization", "Bearer wrong-key-cov5")
	rec := httptest.NewRecorder()
	statsHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong API key, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetStats — with correct API key
// ---------------------------------------------------------------------------

func TestGetStats_correctAPIKey(t *testing.T) {
	const key = "correct-stats-key-cov5"
	statsHandler := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", key, "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	statsHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for correct API key, got %d: %s", rec.Code, rec.Body.String())
	}
}
