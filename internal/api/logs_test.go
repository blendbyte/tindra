package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func logsHandler() http.Handler {
	return api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
}

func truncateLogs(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE logs CASCADE"); err != nil {
		t.Fatalf("truncate logs: %v", err)
	}
}

func seedLog(t *testing.T, level, body string) *storage.Log {
	t.Helper()
	var l storage.Log
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO logs (project_id, timestamp, received_at, level, body, attributes)
		VALUES ($1, $2, $2, $3, $4, '{}')
		RETURNING id, project_id, timestamp, received_at, level, body, attributes
	`, testProject.ID, time.Now().UTC(), level, body).Scan(
		&l.ID, &l.ProjectID, &l.Timestamp, &l.ReceivedAt, &l.Level, &l.Body, &l.Attributes,
	)
	if err != nil {
		t.Fatalf("seed log: %v", err)
	}
	return &l
}

func TestListLogs_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/logs", nil)
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestListLogs_empty(t *testing.T) {
	truncateLogs(t)

	req := httptest.NewRequest(http.MethodGet, "/api/logs?project_id="+testProject.ID, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Logs    []json.RawMessage `json:"logs"`
		HasMore bool              `json:"has_more"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Logs) != 0 {
		t.Errorf("expected 0 logs, got %d", len(resp.Logs))
	}
	if resp.HasMore {
		t.Error("has_more should be false for empty result")
	}
}

func TestListLogs_withData(t *testing.T) {
	truncateLogs(t)

	seedLog(t, "error", "something broke")
	seedLog(t, "warn", "something might break")
	seedLog(t, "info", "all good")

	req := httptest.NewRequest(http.MethodGet, "/api/logs?project_id="+testProject.ID, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Logs []*storage.Log `json:"logs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Logs) != 3 {
		t.Errorf("expected 3 logs, got %d", len(resp.Logs))
	}
}

func TestListLogs_levelFilter(t *testing.T) {
	truncateLogs(t)

	seedLog(t, "error", "an error")
	seedLog(t, "error", "another error")
	seedLog(t, "info", "informational")

	req := httptest.NewRequest(http.MethodGet, "/api/logs?project_id="+testProject.ID+"&level=error", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Logs []*storage.Log `json:"logs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Logs) != 2 {
		t.Errorf("expected 2 error logs, got %d", len(resp.Logs))
	}
	for _, l := range resp.Logs {
		if l.Level != "error" {
			t.Errorf("expected level=error, got %q", l.Level)
		}
	}
}

func TestListLogs_searchFilter(t *testing.T) {
	truncateLogs(t)

	seedLog(t, "error", "database connection refused")
	seedLog(t, "error", "timeout waiting for lock")
	seedLog(t, "info", "database query completed")

	req := httptest.NewRequest(http.MethodGet, "/api/logs?project_id="+testProject.ID+"&search=database", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Logs []*storage.Log `json:"logs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Logs) != 2 {
		t.Errorf("expected 2 logs matching 'database', got %d", len(resp.Logs))
	}
}

func TestListLogs_cursorPagination(t *testing.T) {
	truncateLogs(t)

	// Seed 3 logs with known ordering.
	seedLog(t, "info", "log-1")
	time.Sleep(2 * time.Millisecond)
	seedLog(t, "info", "log-2")
	time.Sleep(2 * time.Millisecond)
	l3 := seedLog(t, "info", "log-3")
	_ = l3

	// First page - get all 3.
	req := httptest.NewRequest(http.MethodGet, "/api/logs?project_id="+testProject.ID, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Logs []*storage.Log `json:"logs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Logs) != 3 {
		t.Fatalf("expected 3 logs on first page, got %d", len(resp.Logs))
	}
}

func TestListLogs_noProjectFilter(t *testing.T) {
	truncateLogs(t)

	seedLog(t, "info", "global log")

	// Without project_id query param, should still return 200.
	req := httptest.NewRequest(http.MethodGet, "/api/logs", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for no project filter, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListLogs_cursorTimeWithoutCursorID(t *testing.T) {
	truncateLogs(t)
	cursorTime := time.Now().UTC().Format(time.RFC3339Nano)

	req := httptest.NewRequest(http.MethodGet,
		"/api/logs?project_id="+testProject.ID+"&cursor_time="+cursorTime, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)

	// cursor_time without cursor_id should not crash - falls back gracefully.
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for cursor_time without cursor_id, got %d", rec.Code)
	}
}

// TestListLogs_transactionIDSerialized verifies the log list exposes the
// transaction sharing a log's trace_id, which the UI uses to link a log row to
// its trace, and omits the field entirely when there is no such transaction.
func TestListLogs_transactionIDSerialized(t *testing.T) {
	truncateLogs(t)
	if _, err := testPool.Exec(context.Background(), "TRUNCATE transactions CASCADE"); err != nil {
		t.Fatalf("truncate transactions: %v", err)
	}

	now := time.Now().UTC()
	var txID string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO transactions
			(project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, received_at, trace_id)
		VALUES ($1, '/api/traced', 'http.server', 'ok', 200, $2, $2, $2, 'trace-api-1')
		RETURNING id
	`, testProject.ID, now).Scan(&txID)
	if err != nil {
		t.Fatalf("seed transaction: %v", err)
	}

	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO logs (project_id, timestamp, received_at, level, body, trace_id, attributes)
		VALUES ($1, $2, $2, 'info', 'traced log', 'trace-api-1', '{}'),
		       ($1, $3, $3, 'info', 'plain log', NULL, '{}')
	`, testProject.ID, now, now.Add(-time.Minute)); err != nil {
		t.Fatalf("seed logs: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/logs?project_id="+testProject.ID, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Logs []map[string]any `json:"logs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(resp.Logs))
	}

	traced, plain := resp.Logs[0], resp.Logs[1]
	if traced["body"] != "traced log" {
		t.Fatalf("expected newest log first, got %v", traced["body"])
	}
	if traced["transaction_id"] != txID {
		t.Errorf("transaction_id: got %v, want %q", traced["transaction_id"], txID)
	}
	if _, ok := plain["transaction_id"]; ok {
		t.Errorf("expected transaction_id omitted for untraced log, got %v", plain["transaction_id"])
	}
}

func TestListLogs_minLevel(t *testing.T) {
	truncateLogs(t)
	seedLog(t, "warning", "retry")
	seedLog(t, "error", "failed")
	seedLog(t, "fatal", "dead")
	seedLog(t, "info", "ok")

	req := httptest.NewRequest(http.MethodGet,
		"/api/logs?project_id="+testProject.ID+"&min_level=error", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Logs []*storage.Log `json:"logs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Logs) != 2 {
		t.Fatalf("min_level=error: got %d, want 2 (error+fatal)", len(resp.Logs))
	}
}

func TestListLogs_minLevelWarn(t *testing.T) {
	truncateLogs(t)
	seedLog(t, "warn", "legacy")
	seedLog(t, "warning", "normalized")
	seedLog(t, "info", "skip")

	req := httptest.NewRequest(http.MethodGet,
		"/api/logs?project_id="+testProject.ID+"&min_level=warn", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Logs []*storage.Log `json:"logs"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Logs) != 2 {
		t.Fatalf("min_level=warn: got %d, want 2", len(resp.Logs))
	}
}

func TestCountLogs_ok(t *testing.T) {
	truncateLogs(t)
	seedLog(t, "error", "stripe failed")
	seedLog(t, "fatal", "dead")
	seedLog(t, "warning", "retry")

	req := httptest.NewRequest(http.MethodGet,
		"/api/logs/count?project_id="+testProject.ID+"&level=error&window_mins=5", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Count      int `json:"count"`
		WindowMins int `json:"window_mins"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 2 {
		t.Errorf("count: got %d, want 2 (error+fatal)", resp.Count)
	}
	if resp.WindowMins != 5 {
		t.Errorf("window_mins: got %d", resp.WindowMins)
	}
}

func TestCountLogs_requiresProjectAndLevel(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/logs/count?level=error", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing project: got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/logs/count?project_id="+testProject.ID+"&level=info", nil)
	req.AddCookie(authCookie())
	rec = httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("info level: got %d", rec.Code)
	}
}

func TestCountLogs_defaultWindow(t *testing.T) {
	truncateLogs(t)
	seedLog(t, "error", "x")
	req := httptest.NewRequest(http.MethodGet,
		"/api/logs/count?project_id="+testProject.ID+"&level=error", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		WindowMins int `json:"window_mins"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.WindowMins != 5 {
		t.Errorf("default window: got %d", resp.WindowMins)
	}
}

func TestCountLogs_warnLevelAndWindow(t *testing.T) {
	truncateLogs(t)
	seedLog(t, "warning", "retry")
	req := httptest.NewRequest(http.MethodGet,
		"/api/logs/count?project_id="+testProject.ID+"&level=warn&window_mins=5", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCountLogs_badWindow(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/logs/count?project_id="+testProject.ID+"&level=error&window_mins=0", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("window 0: got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet,
		"/api/logs/count?project_id="+testProject.ID+"&level=error&window_mins=nope", nil)
	req.AddCookie(authCookie())
	rec = httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("window nope: got %d", rec.Code)
	}
}

func TestCountLogs_searchTooLong(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/logs/count?project_id="+testProject.ID+"&level=error&search="+strings.Repeat("a", 201), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("long search: got %d", rec.Code)
	}
}

func TestCountLogs_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/logs/count?project_id="+testProject.ID+"&level=error", nil)
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestCountLogs_timeout(t *testing.T) {
	ctx := context.Background()
	conn, err := testPool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer conn.Exec(ctx, "ROLLBACK")
	if _, err := conn.Exec(ctx, "LOCK TABLE logs IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/api/logs/count?project_id="+testProject.ID+"&level=error", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("expected 504, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCountLogs_invalidProject(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/logs/count?project_id=not-a-uuid&level=error", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCountLogs_windowTooLarge(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/logs/count?project_id="+testProject.ID+"&level=error&window_mins=61", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	logsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("window 61: got %d", rec.Code)
	}
}
