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

func cronHandler() http.Handler { return globalHandler() }

// createTestMonitor is a helper that inserts a monitor via the API and returns it.
func createTestMonitor(t *testing.T, name, schedule string) *storage.CronMonitor {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"project_id":        testProject.ID,
		"name":              name,
		"schedule":          schedule,
		"grace_period_secs": 300,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/monitors", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create monitor: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var m storage.CronMonitor
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("decode monitor: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM cron_monitors WHERE id=$1", m.ID)
	})
	return &m
}

// --- Monitor CRUD ---

func TestListMonitors_empty(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE cron_monitors CASCADE")
	req := httptest.NewRequest(http.MethodGet, "/api/monitors", nil)
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
	if len(monitors) != 0 {
		t.Errorf("expected 0 monitors, got %d", len(monitors))
	}
}

func TestListMonitors_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/monitors", nil)
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestCreateMonitor_success(t *testing.T) {
	m := createTestMonitor(t, "Test Job", "*/5 * * * *")
	if m.ID == "" {
		t.Error("expected non-empty ID")
	}
	if m.Schedule != "*/5 * * * *" {
		t.Errorf("schedule: got %q", m.Schedule)
	}
	if m.State != "unknown" {
		t.Errorf("new monitor state: got %q, want %q", m.State, "unknown")
	}
}

func TestCreateMonitor_invalidSchedule(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"project_id": testProject.ID,
		"name":       "Bad Job",
		"schedule":   "not-a-cron",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/monitors", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid schedule, got %d", rec.Code)
	}
}

func TestCreateMonitor_missingFields(t *testing.T) {
	body := bytes.NewBufferString(`{"project_id":"` + testProject.ID + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/monitors", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name/schedule, got %d", rec.Code)
	}
}

func TestCreateMonitor_forbidden(t *testing.T) {
	roCookie := makeReadOnlyUser(t, "ro-monitor@example.com")
	body, _ := json.Marshal(map[string]any{
		"project_id": testProject.ID,
		"name":       "Forbidden",
		"schedule":   "0 * * * *",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/monitors", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(roCookie)
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestGetMonitor_success(t *testing.T) {
	m := createTestMonitor(t, "Get Test", "0 12 * * *")
	req := httptest.NewRequest(http.MethodGet, "/api/monitors/"+m.ID, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestGetMonitor_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/monitors/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestDeleteMonitor_success(t *testing.T) {
	m := createTestMonitor(t, "Delete Me", "0 * * * *")
	req := httptest.NewRequest(http.MethodDelete, "/api/monitors/"+m.ID, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}
}

// --- Ping ingest ---

func TestCronPing_ok(t *testing.T) {
	m := createTestMonitor(t, "Ping Job", "0 * * * *")

	req := httptest.NewRequest(http.MethodPost, "/api/cron/"+m.ID+"?status=ok&duration=2.5", nil)
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// After ok ping, state should be "ok".
	got, err := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if got.State != "ok" {
		t.Errorf("state after ok ping: got %q, want %q", got.State, "ok")
	}
	if got.LastCheckinStatus == nil || *got.LastCheckinStatus != "ok" {
		t.Error("expected last_checkin_status=ok")
	}
}

func TestCronPing_error(t *testing.T) {
	m := createTestMonitor(t, "Error Job", "0 * * * *")

	req := httptest.NewRequest(http.MethodPost, "/api/cron/"+m.ID+"?status=error", nil)
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	got, err := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if got.State != "error" {
		t.Errorf("state after error ping: got %q, want %q", got.State, "error")
	}
}

func TestCronPing_unknownMonitor(t *testing.T) {
	// Unknown UUID should not error - silently return 200.
	req := httptest.NewRequest(http.MethodPost, "/api/cron/00000000-0000-0000-0000-000000000000", nil)
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for unknown monitor, got %d", rec.Code)
	}
}

func TestCronPing_getMethodAccepted(t *testing.T) {
	m := createTestMonitor(t, "GET Ping", "*/10 * * * *")
	req := httptest.NewRequest(http.MethodGet, "/api/cron/"+m.ID, nil)
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for GET ping, got %d", rec.Code)
	}
}

// --- Sentry-compat start/finish ---

func TestCronCheckin_startFinish(t *testing.T) {
	m := createTestMonitor(t, "Sentry Compat Job", "0 0 * * *")

	// Start check-in.
	startBody := bytes.NewBufferString(`{"status":"in_progress"}`)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/cron/%s/checkins/", m.ID), startBody)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for start, got %d: %s", rec.Code, rec.Body.String())
	}
	var startResp struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&startResp); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if startResp.ID == "" {
		t.Fatal("expected non-empty check-in ID")
	}

	// Monitor should now be in_progress.
	got, _ := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if got.State != "in_progress" {
		t.Errorf("state after start: got %q, want in_progress", got.State)
	}

	// Finish check-in.
	finishBody := bytes.NewBufferString(`{"status":"ok","duration":1.5}`)
	req2 := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/api/cron/%s/checkins/%s/", m.ID, startResp.ID), finishBody)
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 for finish, got %d: %s", rec2.Code, rec2.Body.String())
	}

	// Monitor should now be ok.
	got2, _ := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if got2.State != "ok" {
		t.Errorf("state after finish: got %q, want ok", got2.State)
	}
}

// --- Update monitor ---

func TestUpdateMonitor_success(t *testing.T) {
	m := createTestMonitor(t, "Update Me", "0 * * * *")
	body, _ := json.Marshal(map[string]any{
		"name":              "Updated Name",
		"schedule":          "*/15 * * * *",
		"grace_period_secs": 120,
	})
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
	if updated.Name != "Updated Name" {
		t.Errorf("name: got %q, want %q", updated.Name, "Updated Name")
	}
	if updated.Schedule != "*/15 * * * *" {
		t.Errorf("schedule: got %q, want %q", updated.Schedule, "*/15 * * * *")
	}
	if updated.GracePeriodSecs != 120 {
		t.Errorf("grace_period_secs: got %d, want 120", updated.GracePeriodSecs)
	}
}

func TestUpdateMonitor_invalidSchedule(t *testing.T) {
	m := createTestMonitor(t, "Bad Update", "0 * * * *")
	body, _ := json.Marshal(map[string]any{"schedule": "not-valid"})
	req := httptest.NewRequest(http.MethodPatch, "/api/monitors/"+m.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestUpdateMonitor_notFound(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"name": "Ghost"})
	req := httptest.NewRequest(http.MethodPatch, "/api/monitors/00000000-0000-0000-0000-000000000000", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- Oh Dear / Spatie compat ---

func TestOhDearStarting_setsInProgress(t *testing.T) {
	m := createTestMonitor(t, "Oh Dear Start", "*/5 * * * *")

	req := httptest.NewRequest(http.MethodPost, "/api/cron/"+m.ID+"/starting", nil)
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil || resp.ID == "" {
		t.Fatalf("expected check-in ID in response")
	}

	got, _ := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if got.State != "in_progress" {
		t.Errorf("state: got %q, want in_progress", got.State)
	}
}

func TestOhDearFinished_setsOk(t *testing.T) {
	m := createTestMonitor(t, "Oh Dear Finish", "0 * * * *")

	body := bytes.NewBufferString(`{"runtime":3.2}`)
	req := httptest.NewRequest(http.MethodPost, "/api/cron/"+m.ID+"/finished", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	got, _ := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if got.State != "ok" {
		t.Errorf("state: got %q, want ok", got.State)
	}
	if got.LastCheckinStatus == nil || *got.LastCheckinStatus != "ok" {
		t.Error("expected last_checkin_status=ok")
	}
}

func TestOhDearFailed_setsError(t *testing.T) {
	m := createTestMonitor(t, "Oh Dear Fail", "0 * * * *")

	req := httptest.NewRequest(http.MethodPost, "/api/cron/"+m.ID+"/failed", nil)
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	got, _ := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if got.State != "error" {
		t.Errorf("state: got %q, want error", got.State)
	}
}

func TestOhDear_startFinishCycle(t *testing.T) {
	m := createTestMonitor(t, "Oh Dear Cycle", "*/10 * * * *")

	// Start
	req := httptest.NewRequest(http.MethodPost, "/api/cron/"+m.ID+"/starting", nil)
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("starting: expected 201, got %d", rec.Code)
	}
	got, _ := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if got.State != "in_progress" {
		t.Errorf("after starting: got %q, want in_progress", got.State)
	}

	// Finish
	req2 := httptest.NewRequest(http.MethodPost, "/api/cron/"+m.ID+"/finished", bytes.NewBufferString(`{"runtime":1.0}`))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("finished: expected 200, got %d", rec2.Code)
	}
	got2, _ := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if got2.State != "ok" {
		t.Errorf("after finished: got %q, want ok", got2.State)
	}
}

func TestOhDear_unknownMonitor(t *testing.T) {
	uuid := "00000000-0000-0000-0000-000000000000"
	for _, path := range []string{"/starting", "/finished", "/failed"} {
		req := httptest.NewRequest(http.MethodPost, "/api/cron/"+uuid+path, nil)
		rec := httptest.NewRecorder()
		cronHandler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s unknown monitor: expected 200, got %d", path, rec.Code)
		}
	}
}

// --- Check-in list ---

func TestListCheckins_empty(t *testing.T) {
	m := createTestMonitor(t, "Checkin List", "0 * * * *")
	req := httptest.NewRequest(http.MethodGet, "/api/monitors/"+m.ID+"/checkins", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var checkins []*storage.CronCheckin
	if err := json.NewDecoder(rec.Body).Decode(&checkins); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(checkins) != 0 {
		t.Errorf("expected 0 checkins, got %d", len(checkins))
	}
}

func TestListCheckins_afterPing(t *testing.T) {
	m := createTestMonitor(t, "Checkin Count", "*/30 * * * *")

	// Send two pings.
	for i := range 2 {
		req := httptest.NewRequest(http.MethodPost, "/api/cron/"+m.ID+"?status=ok", nil)
		rec := httptest.NewRecorder()
		cronHandler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ping %d: expected 200, got %d", i, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/monitors/"+m.ID+"/checkins", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	cronHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var checkins []*storage.CronCheckin
	if err := json.NewDecoder(rec.Body).Decode(&checkins); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(checkins) != 2 {
		t.Errorf("expected 2 checkins, got %d", len(checkins))
	}
}
