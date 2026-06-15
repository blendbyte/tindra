package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blendbyte/tindra/internal/storage"
)

func uptimeHandler() http.Handler { return globalHandler() }

func truncateUptimeMonitors(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE uptime_monitors CASCADE"); err != nil {
		t.Fatalf("truncate uptime_monitors: %v", err)
	}
}

func seedUptimeMonitor(t *testing.T) *storage.UptimeMonitor {
	t.Helper()
	m, err := storage.CreateUptimeMonitor(context.Background(), testPool, &storage.UptimeMonitor{
		ProjectID:     testProject.ID,
		Name:          "test-uptime",
		URL:           "https://example.com",
		Method:        "GET",
		IntervalSecs:  300,
		TimeoutSecs:   10,
		ExpectedCodes: "200-299",
	})
	if err != nil {
		t.Fatalf("create uptime monitor: %v", err)
	}
	return m
}

// --- handleListUptimeMonitors ---

func TestListUptimeMonitors_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/uptime-monitors", nil)
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestListUptimeMonitors_empty(t *testing.T) {
	truncateUptimeMonitors(t)
	req := httptest.NewRequest(http.MethodGet, "/api/uptime-monitors", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var monitors []*storage.UptimeMonitor
	if err := json.NewDecoder(rec.Body).Decode(&monitors); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(monitors) != 0 {
		t.Errorf("expected 0 monitors, got %d", len(monitors))
	}
}

func TestListUptimeMonitors_withMonitor(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	req := httptest.NewRequest(http.MethodGet, "/api/uptime-monitors", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var monitors []*storage.UptimeMonitor
	if err := json.NewDecoder(rec.Body).Decode(&monitors); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(monitors) != 1 {
		t.Fatalf("expected 1 monitor, got %d", len(monitors))
	}
	if monitors[0].ID != m.ID {
		t.Errorf("id: got %q, want %q", monitors[0].ID, m.ID)
	}
	if monitors[0].Name != "test-uptime" {
		t.Errorf("name: got %q", monitors[0].Name)
	}
	if monitors[0].RecentChecks == nil {
		t.Error("recent_checks should not be nil")
	}
}

func TestListUptimeMonitors_projectFilter(t *testing.T) {
	truncateUptimeMonitors(t)
	seedUptimeMonitor(t)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/uptime-monitors?project_id=%s", testProject.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var monitors []*storage.UptimeMonitor
	if err := json.NewDecoder(rec.Body).Decode(&monitors); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(monitors) != 1 {
		t.Errorf("expected 1 monitor with project filter, got %d", len(monitors))
	}
}

func TestListUptimeMonitors_unknownProjectFilter(t *testing.T) {
	truncateUptimeMonitors(t)
	seedUptimeMonitor(t)

	req := httptest.NewRequest(http.MethodGet,
		"/api/uptime-monitors?project_id=00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var monitors []*storage.UptimeMonitor
	if err := json.NewDecoder(rec.Body).Decode(&monitors); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(monitors) != 0 {
		t.Errorf("expected 0 monitors for unknown project, got %d", len(monitors))
	}
}

// --- handleCreateUptimeMonitor ---

func TestCreateUptimeMonitor_unauthenticated(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"project_id": testProject.ID,
		"name":       "My Monitor",
		"url":        "https://example.com",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestCreateUptimeMonitor_forbidden(t *testing.T) {
	roCookie := makeReadOnlyUser(t, "uptime-ro@example.com")
	body, _ := json.Marshal(map[string]any{
		"project_id": testProject.ID,
		"name":       "My Monitor",
		"url":        "https://example.com",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors", bytes.NewReader(body))
	req.AddCookie(roCookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestCreateUptimeMonitor_badBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors",
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestCreateUptimeMonitor_missingProjectID(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"name": "My Monitor", "url": "https://example.com"})
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors", bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestCreateUptimeMonitor_missingName(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"project_id": testProject.ID, "url": "https://example.com"})
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors", bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestCreateUptimeMonitor_missingURL(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"project_id": testProject.ID, "name": "Monitor"})
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors", bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestCreateUptimeMonitor_invalidURL(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"project_id": testProject.ID,
		"name":       "Monitor",
		"url":        "ftp://example.com",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors", bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for ftp URL, got %d", rec.Code)
	}
}

func TestCreateUptimeMonitor_notHTTPURL(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"project_id": testProject.ID,
		"name":       "Monitor",
		"url":        "not-a-url",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors", bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-URL, got %d", rec.Code)
	}
}

func TestCreateUptimeMonitor_invalidMethod(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"project_id": testProject.ID,
		"name":       "Monitor",
		"url":        "https://example.com",
		"method":     "POST",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors", bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid method, got %d", rec.Code)
	}
}

func TestCreateUptimeMonitor_invalidExpectedCodes(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"project_id":     testProject.ID,
		"name":           "Monitor",
		"url":            "https://example.com",
		"expected_codes": "not-a-code",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors", bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid expected_codes, got %d", rec.Code)
	}
}

func TestCreateUptimeMonitor_success(t *testing.T) {
	truncateUptimeMonitors(t)

	body, _ := json.Marshal(map[string]any{
		"project_id":     testProject.ID,
		"name":           "Prod Monitor",
		"url":            "https://example.com/health",
		"method":         "GET",
		"interval_secs":  60,
		"timeout_secs":   5,
		"expected_codes": "200",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors", bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var m storage.UptimeMonitor
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.ID == "" {
		t.Error("expected non-empty ID")
	}
	if m.Name != "Prod Monitor" {
		t.Errorf("name: got %q", m.Name)
	}
	if m.URL != "https://example.com/health" {
		t.Errorf("url: got %q", m.URL)
	}
	if m.IntervalSecs != 60 {
		t.Errorf("interval_secs: got %d, want 60", m.IntervalSecs)
	}
	if m.State != "unknown" {
		t.Errorf("initial state: got %q, want unknown", m.State)
	}
}

func TestCreateUptimeMonitor_defaultsApplied(t *testing.T) {
	truncateUptimeMonitors(t)

	body, _ := json.Marshal(map[string]any{
		"project_id": testProject.ID,
		"name":       "Defaults Monitor",
		"url":        "https://example.com",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors", bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var m storage.UptimeMonitor
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.Method != "GET" {
		t.Errorf("default method: got %q, want GET", m.Method)
	}
	if m.IntervalSecs != 300 {
		t.Errorf("default interval_secs: got %d, want 300", m.IntervalSecs)
	}
	if m.TimeoutSecs != 10 {
		t.Errorf("default timeout_secs: got %d, want 10", m.TimeoutSecs)
	}
	if m.ExpectedCodes != "200-299" {
		t.Errorf("default expected_codes: got %q, want 200-299", m.ExpectedCodes)
	}
}

func TestCreateUptimeMonitor_headMethod(t *testing.T) {
	truncateUptimeMonitors(t)

	body, _ := json.Marshal(map[string]any{
		"project_id": testProject.ID,
		"name":       "HEAD Monitor",
		"url":        "https://example.com",
		"method":     "HEAD",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors", bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var m storage.UptimeMonitor
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.Method != "HEAD" {
		t.Errorf("method: got %q, want HEAD", m.Method)
	}
}

func TestCreateUptimeMonitor_withBodyContains(t *testing.T) {
	truncateUptimeMonitors(t)
	needle := "ok"
	body, _ := json.Marshal(map[string]any{
		"project_id":    testProject.ID,
		"name":          "Body Monitor",
		"url":           "https://example.com",
		"body_contains": needle,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors", bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var m storage.UptimeMonitor
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.BodyContains == nil || *m.BodyContains != needle {
		t.Errorf("body_contains: got %v, want %q", m.BodyContains, needle)
	}
}

func TestCreateUptimeMonitor_httpURL(t *testing.T) {
	truncateUptimeMonitors(t)

	body, _ := json.Marshal(map[string]any{
		"project_id": testProject.ID,
		"name":       "HTTP Monitor",
		"url":        "http://example.com",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/uptime-monitors", bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for http URL, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- handleGetUptimeMonitor ---

func TestGetUptimeMonitor_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/uptime-monitors/00000000-0000-0000-0000-000000000000", nil)
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestGetUptimeMonitor_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/uptime-monitors/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestGetUptimeMonitor_success(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/uptime-monitors/%s", m.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got storage.UptimeMonitor
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != m.ID {
		t.Errorf("id: got %q, want %q", got.ID, m.ID)
	}
	if got.URL != "https://example.com" {
		t.Errorf("url: got %q", got.URL)
	}
}

// --- handleUpdateUptimeMonitor ---

func TestUpdateUptimeMonitor_unauthenticated(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"name": "New Name"})
	req := httptest.NewRequest(http.MethodPatch,
		"/api/uptime-monitors/00000000-0000-0000-0000-000000000000",
		bytes.NewReader(body))
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestUpdateUptimeMonitor_forbidden(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)
	roCookie := makeReadOnlyUser(t, "uptime-update-ro@example.com")

	body, _ := json.Marshal(map[string]any{"name": "Renamed"})
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/uptime-monitors/%s", m.ID), bytes.NewReader(body))
	req.AddCookie(roCookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestUpdateUptimeMonitor_notFound(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"name": "Renamed"})
	req := httptest.NewRequest(http.MethodPatch,
		"/api/uptime-monitors/00000000-0000-0000-0000-000000000000",
		bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestUpdateUptimeMonitor_badBody(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/uptime-monitors/%s", m.ID),
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestUpdateUptimeMonitor_invalidURL(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	newURL := "ftp://bad-scheme.com"
	body, _ := json.Marshal(map[string]any{"url": newURL})
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/uptime-monitors/%s", m.ID), bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for ftp URL, got %d", rec.Code)
	}
}

func TestUpdateUptimeMonitor_invalidMethod(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	body, _ := json.Marshal(map[string]any{"method": "DELETE"})
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/uptime-monitors/%s", m.ID), bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid method, got %d", rec.Code)
	}
}

func TestUpdateUptimeMonitor_invalidExpectedCodes(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	body, _ := json.Marshal(map[string]any{"expected_codes": "bad"})
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/uptime-monitors/%s", m.ID), bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid expected_codes, got %d", rec.Code)
	}
}

func TestUpdateUptimeMonitor_invalidStatus(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	body, _ := json.Marshal(map[string]any{"status": "disabled"})
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/uptime-monitors/%s", m.ID), bytes.NewReader(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid status, got %d", rec.Code)
	}
}

func TestUpdateUptimeMonitor_success(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	body, _ := json.Marshal(map[string]any{
		"name":           "Renamed Monitor",
		"url":            "https://new.example.com",
		"method":         "HEAD",
		"interval_secs":  120,
		"timeout_secs":   30,
		"expected_codes": "200,201",
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
	if got.Name != "Renamed Monitor" {
		t.Errorf("name: got %q, want 'Renamed Monitor'", got.Name)
	}
	if got.URL != "https://new.example.com" {
		t.Errorf("url: got %q", got.URL)
	}
	if got.Method != "HEAD" {
		t.Errorf("method: got %q, want HEAD", got.Method)
	}
	if got.IntervalSecs != 120 {
		t.Errorf("interval_secs: got %d, want 120", got.IntervalSecs)
	}
}

func TestUpdateUptimeMonitor_pause(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	body, _ := json.Marshal(map[string]any{"status": "paused"})
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
	if got.Status != "paused" {
		t.Errorf("status: got %q, want paused", got.Status)
	}
}

func TestUpdateUptimeMonitor_setBodyContains(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	body, _ := json.Marshal(map[string]any{"body_contains": "healthy"})
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
	if got.BodyContains == nil || *got.BodyContains != "healthy" {
		t.Errorf("body_contains: got %v, want 'healthy'", got.BodyContains)
	}
}

func TestUpdateUptimeMonitor_clearBodyContains(t *testing.T) {
	truncateUptimeMonitors(t)
	needle := "ok"
	m, err := storage.CreateUptimeMonitor(context.Background(), testPool, &storage.UptimeMonitor{
		ProjectID:     testProject.ID,
		Name:          "body-monitor",
		URL:           "https://example.com",
		Method:        "GET",
		IntervalSecs:  300,
		TimeoutSecs:   10,
		ExpectedCodes: "200-299",
		BodyContains:  &needle,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Sending empty string clears body_contains
	body, _ := json.Marshal(map[string]any{"body_contains": ""})
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
	if got.BodyContains != nil {
		t.Errorf("expected body_contains to be cleared, got %v", got.BodyContains)
	}
}

// --- handleDeleteUptimeMonitor ---

func TestDeleteUptimeMonitor_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete,
		"/api/uptime-monitors/00000000-0000-0000-0000-000000000000", nil)
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestDeleteUptimeMonitor_forbidden(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)
	roCookie := makeReadOnlyUser(t, "uptime-delete-ro@example.com")

	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/uptime-monitors/%s", m.ID), nil)
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestDeleteUptimeMonitor_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete,
		"/api/uptime-monitors/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestDeleteUptimeMonitor_success(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/uptime-monitors/%s", m.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Confirm it's gone
	req2 := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/uptime-monitors/%s", m.ID), nil)
	req2.AddCookie(authCookie())
	rec2 := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", rec2.Code)
	}
}

// --- handleListUptimeChecks ---

func TestListUptimeChecks_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/uptime-monitors/00000000-0000-0000-0000-000000000000/checks", nil)
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestListUptimeChecks_monitorNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/uptime-monitors/00000000-0000-0000-0000-000000000000/checks", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestListUptimeChecks_empty(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/uptime-monitors/%s/checks", m.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var checks []*storage.UptimeCheck
	if err := json.NewDecoder(rec.Body).Decode(&checks); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(checks) != 0 {
		t.Errorf("expected 0 checks, got %d", len(checks))
	}
}

func TestListUptimeChecks_withChecks(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	code := 200
	ms := 42
	_, err := storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{
		Status:     "up",
		StatusCode: &code,
		ResponseMs: &ms,
	})
	if err != nil {
		t.Fatalf("record check: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/uptime-monitors/%s/checks", m.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var checks []*storage.UptimeCheck
	if err := json.NewDecoder(rec.Body).Decode(&checks); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if checks[0].Status != "up" {
		t.Errorf("status: got %q, want up", checks[0].Status)
	}
}

func TestListUptimeChecks_withLimit(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	// Insert 3 checks
	for i := 0; i < 3; i++ {
		testPool.Exec(context.Background(), `UPDATE uptime_monitors SET next_check_at = NOW() - INTERVAL '1 second' WHERE id=$1`, m.ID)
		code := 200
		ms := 10
		storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{
			Status: "up", StatusCode: &code, ResponseMs: &ms,
		})
	}

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/uptime-monitors/%s/checks?limit=2", m.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var checks []*storage.UptimeCheck
	if err := json.NewDecoder(rec.Body).Decode(&checks); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(checks) != 2 {
		t.Errorf("expected 2 checks with limit=2, got %d", len(checks))
	}
}

// --- handleGetUptimeStats ---

func TestGetUptimeStats_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/uptime-monitors/00000000-0000-0000-0000-000000000000/stats", nil)
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestGetUptimeStats_monitorNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/uptime-monitors/00000000-0000-0000-0000-000000000000/stats", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestGetUptimeStats_noChecks(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/uptime-monitors/%s/stats", m.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var stats storage.UptimeStats
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// With no checks, uptime should default to 0
	if stats.UptimePct24h != 0 {
		t.Errorf("uptime_pct_24h: got %f, want 0", stats.UptimePct24h)
	}
	if stats.AvgResponseMs != nil {
		t.Errorf("avg_response_ms: expected nil with no checks, got %v", stats.AvgResponseMs)
	}
}

func TestGetUptimeStats_withChecks(t *testing.T) {
	truncateUptimeMonitors(t)
	m := seedUptimeMonitor(t)

	code := 200
	ms := 50
	_, err := storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{
		Status: "up", StatusCode: &code, ResponseMs: &ms,
	})
	if err != nil {
		t.Fatalf("record check: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/uptime-monitors/%s/stats", m.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	uptimeHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var stats storage.UptimeStats
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stats.UptimePct24h != 100 {
		t.Errorf("uptime_pct_24h: got %f, want 100", stats.UptimePct24h)
	}
	if stats.AvgResponseMs == nil {
		t.Error("expected non-nil avg_response_ms with up checks")
	} else if *stats.AvgResponseMs != 50 {
		t.Errorf("avg_response_ms: got %d, want 50", *stats.AvgResponseMs)
	}
}
