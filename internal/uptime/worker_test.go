package uptime_test

import (
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
	"github.com/blendbyte/tindra/internal/uptime"
)

var (
	testPool    *pgxpool.Pool
	testProject *storage.Project
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	pool, cleanup := testutil.SetupDB(ctx)

	project, err := storage.CreateProject(ctx, pool, "uptime-worker-test", "Uptime Worker Test")
	if err != nil {
		log.Fatalf("create project: %v", err)
	}

	testPool = pool
	testProject = project

	code := m.Run()
	cleanup()
	os.Exit(code)
}

func truncate(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE uptime_monitors CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func seedMonitor(t *testing.T, url, method, expectedCodes string) *storage.UptimeMonitor {
	t.Helper()
	m, err := storage.CreateUptimeMonitor(context.Background(), testPool, &storage.UptimeMonitor{
		ProjectID:     testProject.ID,
		Name:          "test-monitor",
		URL:           url,
		Method:        method,
		IntervalSecs:  300,
		TimeoutSecs:   5,
		ExpectedCodes: expectedCodes,
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	return m
}

func TestWorker_probesAndRecordsUp(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := seedMonitor(t, srv.URL, "GET", "200-299")
	uptime.NewWorker(testPool).RunOnce(ctx)

	checks, err := storage.ListUptimeChecks(ctx, testPool, m.ID, 10)
	if err != nil {
		t.Fatalf("list checks: %v", err)
	}
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if checks[0].Status != "up" {
		t.Errorf("check status: got %q, want up", checks[0].Status)
	}
	if checks[0].StatusCode == nil || *checks[0].StatusCode != 200 {
		t.Errorf("status_code: got %v, want 200", checks[0].StatusCode)
	}
	if checks[0].ResponseMs == nil || *checks[0].ResponseMs < 0 {
		t.Errorf("response_ms should be a non-negative value, got %v", checks[0].ResponseMs)
	}

	updated, _ := storage.GetUptimeMonitor(ctx, testPool, m.ID)
	if updated.State != "up" {
		t.Errorf("monitor state: got %q, want up", updated.State)
	}
}

func TestWorker_probesAndRecordsDown_wrongStatusCode(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	m := seedMonitor(t, srv.URL, "GET", "200-299")
	w := uptime.NewWorker(testPool)

	// Two probes to cross the failure threshold; reset next_check_at between
	// calls so the monitor is immediately due again (interval_secs=300 by default).
	w.RunOnce(ctx)
	testPool.Exec(ctx, `UPDATE uptime_monitors SET next_check_at = NOW() - INTERVAL '1 second' WHERE id=$1`, m.ID)
	w.RunOnce(ctx)

	updated, _ := storage.GetUptimeMonitor(ctx, testPool, m.ID)
	if updated.State != "down" {
		t.Errorf("monitor state after 2 failures: got %q, want down", updated.State)
	}

	checks, err := storage.ListUptimeChecks(ctx, testPool, m.ID, 10)
	if err != nil {
		t.Fatalf("list checks: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(checks))
	}
	if checks[0].Status != "down" {
		t.Errorf("check status: got %q, want down", checks[0].Status)
	}
}

func TestWorker_headMethod(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	var receivedMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := seedMonitor(t, srv.URL, "HEAD", "200-299")
	uptime.NewWorker(testPool).RunOnce(ctx)

	if receivedMethod != "HEAD" {
		t.Errorf("expected HEAD request, got %q", receivedMethod)
	}
	checks, _ := storage.ListUptimeChecks(ctx, testPool, m.ID, 10)
	if len(checks) != 1 || checks[0].Status != "up" {
		t.Errorf("expected 1 up check, got %d checks", len(checks))
	}
}

func TestWorker_bodyContainsMatch(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello, Tindra!"))
	}))
	defer srv.Close()

	needle := "Tindra"
	m, err := storage.CreateUptimeMonitor(ctx, testPool, &storage.UptimeMonitor{
		ProjectID:     testProject.ID,
		Name:          "body-match",
		URL:           srv.URL,
		Method:        "GET",
		IntervalSecs:  300,
		TimeoutSecs:   5,
		ExpectedCodes: "200-299",
		BodyContains:  &needle,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	uptime.NewWorker(testPool).RunOnce(ctx)

	checks, _ := storage.ListUptimeChecks(ctx, testPool, m.ID, 10)
	if len(checks) != 1 || checks[0].Status != "up" {
		t.Errorf("expected 1 up check, got %+v", checks)
	}
}

func TestWorker_bodyContainsMismatch(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Different content"))
	}))
	defer srv.Close()

	needle := "Tindra"
	m, err := storage.CreateUptimeMonitor(ctx, testPool, &storage.UptimeMonitor{
		ProjectID:     testProject.ID,
		Name:          "body-mismatch",
		URL:           srv.URL,
		Method:        "GET",
		IntervalSecs:  300,
		TimeoutSecs:   5,
		ExpectedCodes: "200-299",
		BodyContains:  &needle,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	uptime.NewWorker(testPool).RunOnce(ctx)

	checks, _ := storage.ListUptimeChecks(ctx, testPool, m.ID, 10)
	if len(checks) != 1 || checks[0].Status != "down" {
		t.Errorf("expected 1 down check (body mismatch), got %+v", checks)
	}
	if checks[0].Error == nil || !strings.Contains(*checks[0].Error, "body does not contain") {
		t.Errorf("expected body error message, got %v", checks[0].Error)
	}
}

func TestWorker_connectionRefused(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	m := seedMonitor(t, "http://127.0.0.1:19999", "GET", "200-299")
	uptime.NewWorker(testPool).RunOnce(ctx)

	checks, _ := storage.ListUptimeChecks(ctx, testPool, m.ID, 10)
	if len(checks) != 1 || checks[0].Status != "down" {
		t.Errorf("expected 1 down check for refused connection, got %+v", checks)
	}
	if checks[0].Error == nil || *checks[0].Error == "" {
		t.Error("expected non-empty error for refused connection")
	}
}

func TestWorker_pausedMonitorSkipped(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := seedMonitor(t, srv.URL, "GET", "200-299")
	testPool.Exec(ctx, `UPDATE uptime_monitors SET status='paused' WHERE id=$1`, m.ID)

	uptime.NewWorker(testPool).RunOnce(ctx)

	checks, _ := storage.ListUptimeChecks(ctx, testPool, m.ID, 10)
	if len(checks) != 0 {
		t.Errorf("expected 0 checks for paused monitor, got %d", len(checks))
	}
}

func TestWorker_notYetDueSkipped(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := seedMonitor(t, srv.URL, "GET", "200-299")
	// Advance next_check_at into the future
	testPool.Exec(ctx, `UPDATE uptime_monitors SET next_check_at = NOW() + INTERVAL '10 minutes' WHERE id=$1`, m.ID)

	uptime.NewWorker(testPool).RunOnce(ctx)

	checks, _ := storage.ListUptimeChecks(ctx, testPool, m.ID, 10)
	if len(checks) != 0 {
		t.Errorf("expected 0 checks for not-yet-due monitor, got %d", len(checks))
	}
}

func TestWorker_userAgentHeader(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	var receivedUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	seedMonitor(t, srv.URL, "GET", "200-299")
	uptime.NewWorker(testPool).RunOnce(ctx)

	if receivedUA != "Tindra-Uptime/1.0" {
		t.Errorf("User-Agent: got %q, want Tindra-Uptime/1.0", receivedUA)
	}
}

func TestWorker_customExpectedCode(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted) // 202
	}))
	defer srv.Close()

	// Monitor only accepts 200 — 202 should be treated as down
	m := seedMonitor(t, srv.URL, "GET", "200")
	uptime.NewWorker(testPool).RunOnce(ctx)

	checks, _ := storage.ListUptimeChecks(ctx, testPool, m.ID, 10)
	if len(checks) != 1 || checks[0].Status != "down" {
		t.Errorf("expected 1 down check (202 not in [200]), got %+v", checks)
	}
}

func TestWorker_doesNotFollowRedirects(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	redirected := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/destination" {
			redirected = true
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/destination", http.StatusFound)
	}))
	defer srv.Close()

	// expected_codes includes only 200-299 so a 302 should be recorded as down
	m := seedMonitor(t, srv.URL, "GET", "200-299")
	uptime.NewWorker(testPool).RunOnce(ctx)

	if redirected {
		t.Error("worker followed a redirect but should not have")
	}
	checks, _ := storage.ListUptimeChecks(ctx, testPool, m.ID, 10)
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if checks[0].Status != "down" {
		t.Errorf("status: got %q, want down (302 not in expected_codes)", checks[0].Status)
	}
	if checks[0].StatusCode == nil || *checks[0].StatusCode != http.StatusFound {
		t.Errorf("status_code: got %v, want 302", checks[0].StatusCode)
	}
}

func TestWorker_Run_stopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		uptime.NewWorker(testPool).Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("Run did not stop after context cancellation")
	}
}

func TestWorker_timeout(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	// Server that sleeps longer than the monitor timeout
	ready := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(ready)
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m, err := storage.CreateUptimeMonitor(ctx, testPool, &storage.UptimeMonitor{
		ProjectID:     testProject.ID,
		Name:          "timeout-monitor",
		URL:           srv.URL,
		Method:        "GET",
		IntervalSecs:  300,
		TimeoutSecs:   1, // 1-second timeout, server sleeps 3s
		ExpectedCodes: "200-299",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	uptime.NewWorker(testPool).RunOnce(ctx)

	checks, _ := storage.ListUptimeChecks(ctx, testPool, m.ID, 10)
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if checks[0].Status != "down" {
		t.Errorf("status: got %q, want down (timeout)", checks[0].Status)
	}
	if checks[0].Error == nil || *checks[0].Error != "timeout" {
		t.Errorf("expected error='timeout', got %v", checks[0].Error)
	}
}

func TestWorker_invalidExpectedCodes(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := seedMonitor(t, srv.URL, "GET", "200-299")
	// Corrupt expected_codes directly in the DB to simulate misconfiguration
	testPool.Exec(ctx, `UPDATE uptime_monitors SET expected_codes='invalid-codes' WHERE id=$1`, m.ID)

	uptime.NewWorker(testPool).RunOnce(ctx)

	checks, _ := storage.ListUptimeChecks(ctx, testPool, m.ID, 10)
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if checks[0].Status != "down" {
		t.Errorf("status: got %q, want down (invalid expected_codes)", checks[0].Status)
	}
	if checks[0].Error == nil || !strings.Contains(*checks[0].Error, "invalid expected_codes") {
		t.Errorf("expected 'invalid expected_codes' error, got %v", checks[0].Error)
	}
}
