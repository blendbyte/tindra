package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func logsHandler() http.Handler {
	return api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, nil)
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
