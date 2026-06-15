package api_test

// coverage3_test.go — additional tests targeting uncovered branches in global.go and cron.go.

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

// ---------------------------------------------------------------------------
// handleUpdateIssueGlobal — ignored status with ignore_until (lines 654, 672-674)
// ---------------------------------------------------------------------------

func TestUpdateIssueGlobal_ignoredWithIgnoreUntil(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-ign-until", "Ignored Until Issue")

	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"status":"ignored","ignore_until":"%s"}`, future)
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/"+iss.ID, bytes.NewBufferString(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateIssueGlobal_ignoredWithCountLimit(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-ign-count", "Ignored Count Issue")

	body := `{"status":"ignored","ignore_count_limit":5}`
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/"+iss.ID, bytes.NewBufferString(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleUpdateIssueGlobal — assignee set to non-nil (lines 698-700)
// ---------------------------------------------------------------------------

func TestUpdateIssueGlobal_assigneeSetNotNull(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-assignee-set", "Assignee Set Issue")

	body := fmt.Sprintf(`{"assignee_id":"%s"}`, testUser.ID)
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/"+iss.ID, bytes.NewBufferString(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleListAllIssues — cursor_time+cursor_id parsing (lines 418-424)
// ---------------------------------------------------------------------------

func TestListAllIssues_hardcodedCursor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/issues?cursor_time=2024-01-01T00:00:00.000000000Z&cursor_id=00000000-0000-0000-0000-000000000001",
		nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleSpanSummaries — ?hours param branch (lines 1327-1330)
// ---------------------------------------------------------------------------

func TestSpanSummaries_withHours(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/spans/db?hours=48", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleSpanTimeseries — ?hours param branch (lines 1370-1373)
// ---------------------------------------------------------------------------

func TestSpanTimeseries_withHours(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/spans/db/timeseries?hours=48", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleListTransactionSummaries — ?hours and ?offset params (lines 919-927)
// ---------------------------------------------------------------------------

func TestTransactionSummaries_withHoursAndOffset(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/summaries?hours=48&offset=5", nil) //nolint
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleTransactionTimeseries — ?hours param (lines 954-957)
// ---------------------------------------------------------------------------

func TestTransactionTimeseries_withHours(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/timeseries?hours=48", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetTransactionErrors — non-empty trace_id (lines 1203-1212)
// ---------------------------------------------------------------------------

func TestGetTransactionErrors_withTraceID(t *testing.T) {
	var txID string
	err := testPool.QueryRow(context.Background(),
		`INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, trace_id)
		 VALUES ($1, '/trace-err-test', 'http', 'ok', 100, NOW(), NOW(), 'trace-abc-cov3')
		 RETURNING id`,
		testProject.ID).Scan(&txID)
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM transactions WHERE id = $1", txID)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/"+txID+"/errors", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleGetTransactionGlobal — wrong bearer project (line 1039)
// ---------------------------------------------------------------------------

func TestGetTransactionGlobal_wrongBearerProject(t *testing.T) {
	var txID string
	testPool.QueryRow(context.Background(),
		`SELECT id FROM transactions WHERE project_id = $1 LIMIT 1`, testProject.ID).Scan(&txID)
	if txID == "" {
		if err := testPool.QueryRow(context.Background(),
			`INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
			 VALUES ($1, '/bearer-tx-cov3', 'http', 'ok', 100, NOW(), NOW()) RETURNING id`,
			testProject.ID).Scan(&txID); err != nil {
			t.Skip("can't insert tx:", err)
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(), "DELETE FROM transactions WHERE id = $1", txID)
		})
	}

	other, err := storage.CreateProject(context.Background(), testPool, "tx-bearer-other-cov3", "TX Bearer Other Cov3")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/"+txID, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong bearer project, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetSpansGlobal — wrong bearer project (line 1149)
// ---------------------------------------------------------------------------

func TestGetSpansGlobal_wrongBearerProject(t *testing.T) {
	var txID string
	testPool.QueryRow(context.Background(),
		`SELECT id FROM transactions WHERE project_id = $1 LIMIT 1`, testProject.ID).Scan(&txID)
	if txID == "" {
		t.Skip("no transaction found in testProject")
	}

	other, err := storage.CreateProject(context.Background(), testPool, "spans-bearer-other-cov3", "Spans Bearer Cov3")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/"+txID+"/spans", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong bearer project, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleUpdateProjectPrivacy — null/empty fields (lines 263-268)
// ---------------------------------------------------------------------------

func TestUpdateProjectPrivacy_nullFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch,
		"/api/projects/"+testProject.ID+"/privacy",
		bytes.NewBufferString(`{}`))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleUpdateProject — conflicting slug (lines 233-236)
// ---------------------------------------------------------------------------

func TestUpdateProject_duplicateSlug(t *testing.T) {
	other, err := storage.CreateProject(context.Background(), testPool, "dup-slug-proj-cov3", "Dup Slug Cov3")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})

	body := fmt.Sprintf(`{"name":%q,"slug":%q}`, other.Name, other.Slug)
	req := httptest.NewRequest(http.MethodPatch, "/api/projects/"+testProject.ID, bytes.NewBufferString(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409 for dup slug, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleBulkUpdateIssues — ignore_until branch (lines 580-582)
// ---------------------------------------------------------------------------

func TestBulkUpdateIssues_ignoredWithIgnoreUntil(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "fp-bulk-ign-until-cov3", "Bulk Ignored Until Cov3")

	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"ids":["%s"],"status":"ignored","ignore_until":"%s"}`, iss.ID, future)
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/bulk", bytes.NewBufferString(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleExportIssues — CSV with environment set (lines 526-532)
// ---------------------------------------------------------------------------

func TestExportIssues_csvWithEnvironment(t *testing.T) {
	truncateIssues(t)
	storage.UpsertIssue(context.Background(), testPool,
		testProject.ID, "fp-csv-env-cov3", "CSV Env Issue Cov3", "error", "error", "production", "", time.Now().UTC())

	req := httptest.NewRequest(http.MethodGet,
		"/api/issues/export?format=csv&project_id="+testProject.ID, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "csv") {
		t.Errorf("unexpected content-type: %s", ct)
	}
}

// ---------------------------------------------------------------------------
// handleGetIssueHistory — wrong bearer project (line 722)
// ---------------------------------------------------------------------------

func TestGetIssueHistory_wrongBearerProject(t *testing.T) {
	iss := seedIssue(t, "fp-hist-bearer-cov3", "History Bearer Issue Cov3")

	other, err := storage.CreateProject(context.Background(), testPool, "hist-bearer-other-cov3", "Hist Bearer Cov3")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/history", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong bearer project, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetIssueTrace — wrong bearer project (line 825)
// ---------------------------------------------------------------------------

func TestGetIssueTrace_wrongBearerProject(t *testing.T) {
	iss := seedIssue(t, "fp-trace-bearer-cov3", "Trace Bearer Issue Cov3")

	other, err := storage.CreateProject(context.Background(), testPool, "trace-bearer-other-cov3", "Trace Bearer Cov3")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID)
	})
	tok := bearerToken(t, other.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/trace", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong bearer project, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleGetIssueTrace — ?offset param parsing (lines 830-832)
// ---------------------------------------------------------------------------

func TestGetIssueTrace_withOffset(t *testing.T) {
	iss := seedIssue(t, "fp-trace-offset-cov3", "Trace Offset Issue Cov3")

	req := httptest.NewRequest(http.MethodGet, "/api/issues/"+iss.ID+"/trace?offset=1", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	// No event for this issue, so returns 200 with null body
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleGetWebVitalsPages — ?hours param (lines 1415-1419)
// ---------------------------------------------------------------------------

func TestGetWebVitalsPages_withHours(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/vitals/pages?hours=48", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
