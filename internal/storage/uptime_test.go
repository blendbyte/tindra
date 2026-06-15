package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func setupProjectForUptime(t *testing.T) *storage.Project {
	t.Helper()
	truncateProjects(t)
	p, err := storage.CreateProject(context.Background(), testPool, "uptime-proj", "Uptime Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p
}

func seedUptimeMonitor(t *testing.T, projectID, name, url string) *storage.UptimeMonitor {
	t.Helper()
	m, err := storage.CreateUptimeMonitor(context.Background(), testPool, &storage.UptimeMonitor{
		ProjectID:     projectID,
		Name:          name,
		URL:           url,
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

// ---------- ParseExpectedCodes ----------

func TestParseExpectedCodes_range(t *testing.T) {
	codes, err := storage.ParseExpectedCodes("200-299")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(codes) != 100 {
		t.Errorf("expected 100 codes (200..299), got %d", len(codes))
	}
	if codes[0] != 200 || codes[len(codes)-1] != 299 {
		t.Errorf("range bounds wrong: %d..%d", codes[0], codes[len(codes)-1])
	}
}

func TestParseExpectedCodes_single(t *testing.T) {
	codes, err := storage.ParseExpectedCodes("200")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(codes) != 1 || codes[0] != 200 {
		t.Errorf("expected [200], got %v", codes)
	}
}

func TestParseExpectedCodes_list(t *testing.T) {
	codes, err := storage.ParseExpectedCodes("200,301,404")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(codes) != 3 {
		t.Fatalf("expected 3 codes, got %d", len(codes))
	}
	if codes[0] != 200 || codes[1] != 301 || codes[2] != 404 {
		t.Errorf("unexpected codes: %v", codes)
	}
}

func TestParseExpectedCodes_mixed(t *testing.T) {
	codes, err := storage.ParseExpectedCodes("200-299,301")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(codes) != 101 {
		t.Errorf("expected 101 codes, got %d", len(codes))
	}
}

func TestParseExpectedCodes_empty(t *testing.T) {
	_, err := storage.ParseExpectedCodes("")
	if err == nil {
		t.Error("expected error for empty string")
	}
}

func TestParseExpectedCodes_invalidRange(t *testing.T) {
	_, err := storage.ParseExpectedCodes("299-200")
	if err == nil {
		t.Error("expected error for inverted range")
	}
}

func TestParseExpectedCodes_invalidCode(t *testing.T) {
	_, err := storage.ParseExpectedCodes("abc")
	if err == nil {
		t.Error("expected error for non-numeric code")
	}
}

func TestParseExpectedCodes_outOfRange(t *testing.T) {
	_, err := storage.ParseExpectedCodes("99")
	if err == nil {
		t.Error("expected error for code < 100")
	}
}

// ---------- CRUD ----------

func TestCreateUptimeMonitor(t *testing.T) {
	p := setupProjectForUptime(t)

	m, err := storage.CreateUptimeMonitor(context.Background(), testPool, &storage.UptimeMonitor{
		ProjectID:     p.ID,
		Name:          "my-site",
		URL:           "https://example.com",
		Method:        "GET",
		IntervalSecs:  60,
		TimeoutSecs:   5,
		ExpectedCodes: "200-299",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ID == "" {
		t.Error("expected non-empty ID")
	}
	if m.State != "unknown" {
		t.Errorf("initial state: got %q, want unknown", m.State)
	}
	if m.Status != "active" {
		t.Errorf("initial status: got %q, want active", m.Status)
	}
	if m.ConsecutiveFailures != 0 {
		t.Errorf("consecutive_failures: got %d, want 0", m.ConsecutiveFailures)
	}
	if m.RecentChecks == nil {
		t.Error("recent_checks should be non-nil (empty slice)")
	}
}

func TestCreateUptimeMonitor_withBodyContains(t *testing.T) {
	p := setupProjectForUptime(t)
	s := "Hello"
	m, err := storage.CreateUptimeMonitor(context.Background(), testPool, &storage.UptimeMonitor{
		ProjectID:     p.ID,
		Name:          "body-check",
		URL:           "https://example.com",
		Method:        "GET",
		IntervalSecs:  300,
		TimeoutSecs:   10,
		ExpectedCodes: "200",
		BodyContains:  &s,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.BodyContains == nil || *m.BodyContains != "Hello" {
		t.Errorf("body_contains: got %v, want Hello", m.BodyContains)
	}
}

func TestGetUptimeMonitor_found(t *testing.T) {
	p := setupProjectForUptime(t)
	created := seedUptimeMonitor(t, p.ID, "found-test", "https://example.com")

	got, err := storage.GetUptimeMonitor(context.Background(), testPool, created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.NotNil(t, got)
	if got.ID != created.ID {
		t.Errorf("ID: got %q, want %q", got.ID, created.ID)
	}
	if got.URL != "https://example.com" {
		t.Errorf("URL: got %q", got.URL)
	}
}

func TestGetUptimeMonitor_notFound(t *testing.T) {
	got, err := storage.GetUptimeMonitor(context.Background(), testPool, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestListUptimeMonitors_empty(t *testing.T) {
	p := setupProjectForUptime(t)
	monitors, err := storage.ListUptimeMonitors(context.Background(), testPool, []string{p.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(monitors) != 0 {
		t.Errorf("expected 0 monitors, got %d", len(monitors))
	}
}

func TestListUptimeMonitors_withProjectFilter(t *testing.T) {
	truncateProjects(t)
	p1, _ := storage.CreateProject(context.Background(), testPool, "up-p1", "P1")
	p2, _ := storage.CreateProject(context.Background(), testPool, "up-p2", "P2")

	seedUptimeMonitor(t, p1.ID, "site-p1", "https://p1.example.com")
	seedUptimeMonitor(t, p2.ID, "site-p2", "https://p2.example.com")

	monitors, err := storage.ListUptimeMonitors(context.Background(), testPool, []string{p1.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(monitors) != 1 {
		t.Fatalf("expected 1 monitor for p1, got %d", len(monitors))
	}
	if monitors[0].ProjectID != p1.ID {
		t.Errorf("wrong project_id: %q", monitors[0].ProjectID)
	}
}

func TestListUptimeMonitors_recentChecks(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "recent-checks", "https://example.com")

	statusCode := 200
	responseMs := 100
	if _, err := storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{
		Status:     "up",
		StatusCode: &statusCode,
		ResponseMs: &responseMs,
	}); err != nil {
		t.Fatalf("record check: %v", err)
	}

	monitors, err := storage.ListUptimeMonitors(context.Background(), testPool, []string{p.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.Len(t, monitors, 1)
	if len(monitors[0].RecentChecks) != 1 {
		t.Errorf("expected 1 recent check, got %d", len(monitors[0].RecentChecks))
	}
	if monitors[0].RecentChecks[0].Status != "up" {
		t.Errorf("expected status up, got %q", monitors[0].RecentChecks[0].Status)
	}
}

func TestUpdateUptimeMonitor(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "original", "https://old.example.com")

	m.Name = "updated"
	m.URL = "https://new.example.com"
	m.Method = "HEAD"
	m.IntervalSecs = 60
	m.TimeoutSecs = 5
	m.ExpectedCodes = "200,204"
	m.Status = "paused"

	updated, err := storage.UpdateUptimeMonitor(context.Background(), testPool, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.NotNil(t, updated)
	if updated.Name != "updated" {
		t.Errorf("name: got %q", updated.Name)
	}
	if updated.URL != "https://new.example.com" {
		t.Errorf("URL: got %q", updated.URL)
	}
	if updated.Method != "HEAD" {
		t.Errorf("method: got %q", updated.Method)
	}
	if updated.IntervalSecs != 60 {
		t.Errorf("interval_secs: got %d", updated.IntervalSecs)
	}
	if updated.Status != "paused" {
		t.Errorf("status: got %q", updated.Status)
	}
}

func TestUpdateUptimeMonitor_notFound(t *testing.T) {
	got, err := storage.UpdateUptimeMonitor(context.Background(), testPool, &storage.UptimeMonitor{
		ID:            "00000000-0000-0000-0000-000000000000",
		Name:          "ghost",
		URL:           "https://example.com",
		Method:        "GET",
		IntervalSecs:  300,
		TimeoutSecs:   10,
		ExpectedCodes: "200-299",
		Status:        "active",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for unknown ID, got %+v", got)
	}
}

func TestDeleteUptimeMonitor_found(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "to-delete", "https://example.com")

	deleted, err := storage.DeleteUptimeMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleted {
		t.Error("expected deleted=true")
	}

	got, err := storage.GetUptimeMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestDeleteUptimeMonitor_notFound(t *testing.T) {
	deleted, err := storage.DeleteUptimeMonitor(context.Background(), testPool, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted {
		t.Error("expected deleted=false for missing ID")
	}
}

// ---------- RecordUptimeCheck / state machine ----------

func TestRecordUptimeCheck_up(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "up-check", "https://example.com")

	code, ms := 200, 120
	ci, err := storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{
		Status:     "up",
		StatusCode: &code,
		ResponseMs: &ms,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ci.Status != "up" {
		t.Errorf("check status: got %q, want up", ci.Status)
	}

	updated, err := storage.GetUptimeMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if updated.State != "up" {
		t.Errorf("monitor state: got %q, want up", updated.State)
	}
	if updated.ConsecutiveFailures != 0 {
		t.Errorf("consecutive_failures: got %d, want 0", updated.ConsecutiveFailures)
	}
	if updated.LastOkAt == nil {
		t.Error("expected last_ok_at to be set")
	}
	if updated.NextCheckAt == nil {
		t.Error("expected next_check_at to be set")
	}
	if updated.LastStatusCode == nil || *updated.LastStatusCode != 200 {
		t.Errorf("last_status_code: got %v, want 200", updated.LastStatusCode)
	}
	if updated.LastResponseMs == nil || *updated.LastResponseMs != 120 {
		t.Errorf("last_response_ms: got %v, want 120", updated.LastResponseMs)
	}
}

func TestRecordUptimeCheck_singleFailureStaysUp(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "single-fail", "https://example.com")

	// First put it into "up" state
	code := 200
	storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{Status: "up", StatusCode: &code})

	// One failure: should NOT transition to "down" (threshold is 2)
	code = 503
	if _, err := storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{
		Status:     "down",
		StatusCode: &code,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated, err := storage.GetUptimeMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if updated.State != "up" {
		t.Errorf("state after 1 failure: got %q, want up", updated.State)
	}
	if updated.ConsecutiveFailures != 1 {
		t.Errorf("consecutive_failures: got %d, want 1", updated.ConsecutiveFailures)
	}
}

func TestRecordUptimeCheck_twoFailuresGoesDown(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "two-fail", "https://example.com")

	code := 503
	for i := range 2 {
		if _, err := storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{
			Status:     "down",
			StatusCode: &code,
		}); err != nil {
			t.Fatalf("failure %d: %v", i, err)
		}
	}

	updated, err := storage.GetUptimeMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if updated.State != "down" {
		t.Errorf("state after 2 failures: got %q, want down", updated.State)
	}
	if updated.ConsecutiveFailures != 2 {
		t.Errorf("consecutive_failures: got %d, want 2", updated.ConsecutiveFailures)
	}
}

func TestRecordUptimeCheck_recoveryResetsFailures(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "recovery", "https://example.com")

	// Drive to "down"
	code := 503
	for range 2 {
		storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{Status: "down", StatusCode: &code})
	}

	// Recover
	code = 200
	if _, err := storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{
		Status:     "up",
		StatusCode: &code,
	}); err != nil {
		t.Fatalf("recovery check: %v", err)
	}

	updated, err := storage.GetUptimeMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if updated.State != "up" {
		t.Errorf("state after recovery: got %q, want up", updated.State)
	}
	if updated.ConsecutiveFailures != 0 {
		t.Errorf("consecutive_failures after recovery: got %d, want 0", updated.ConsecutiveFailures)
	}
}

func TestRecordUptimeCheck_checksAt(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "checked-at", "https://example.com")

	before := time.Now().UTC()
	code := 200
	ci, err := storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{
		Status: "up", StatusCode: &code,
	})
	after := time.Now().UTC()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ci.CheckedAt.Before(before.Add(-2*time.Second)) || ci.CheckedAt.After(after.Add(2*time.Second)) {
		t.Errorf("checked_at %v outside expected window", ci.CheckedAt)
	}
}

func TestRecordUptimeCheck_withError(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "error-check", "https://example.com")

	errMsg := "timeout"
	ci, err := storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{
		Status: "down",
		Error:  &errMsg,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ci.Error == nil || *ci.Error != "timeout" {
		t.Errorf("error field: got %v, want timeout", ci.Error)
	}
}

func TestRecordUptimeCheck_nextCheckAtAdvances(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "next-check", "https://example.com")
	// interval_secs = 300

	before := time.Now().UTC()
	code := 200
	storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{Status: "up", StatusCode: &code})

	updated, _ := storage.GetUptimeMonitor(context.Background(), testPool, m.ID)
	if updated.NextCheckAt == nil {
		t.Fatal("expected next_check_at to be set")
	}
	expected := before.Add(300 * time.Second)
	if updated.NextCheckAt.Before(expected.Add(-5*time.Second)) || updated.NextCheckAt.After(expected.Add(5*time.Second)) {
		t.Errorf("next_check_at %v not ~300s after %v", *updated.NextCheckAt, before)
	}
}

// ---------- ListUptimeChecks ----------

func TestListUptimeChecks_mostRecentFirst(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "list-checks", "https://example.com")

	code := 200
	for range 3 {
		storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{Status: "up", StatusCode: &code})
	}

	checks, err := storage.ListUptimeChecks(context.Background(), testPool, m.ID, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(checks) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(checks))
	}
	for i := 1; i < len(checks); i++ {
		if checks[i].CheckedAt.After(checks[i-1].CheckedAt) {
			t.Errorf("not ordered most-recent-first at index %d", i)
		}
	}
}

func TestListUptimeChecks_respectsLimit(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "limit-checks", "https://example.com")

	code := 200
	for range 5 {
		storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{Status: "up", StatusCode: &code})
	}

	checks, err := storage.ListUptimeChecks(context.Background(), testPool, m.ID, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(checks) != 2 {
		t.Errorf("expected 2 checks with limit=2, got %d", len(checks))
	}
}

// ---------- GetUptimeStats ----------

func TestGetUptimeStats_noChecks(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "stats-empty", "https://example.com")

	s, err := storage.GetUptimeStats(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.UptimePct24h != 0 || s.UptimePct7d != 0 || s.UptimePct30d != 0 {
		t.Errorf("expected 0%% uptime with no checks: %+v", s)
	}
	if s.AvgResponseMs != nil {
		t.Errorf("expected nil avg_response_ms, got %v", s.AvgResponseMs)
	}
}

func TestGetUptimeStats_allUp(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "stats-all-up", "https://example.com")

	code, ms := 200, 150
	for range 5 {
		storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{
			Status: "up", StatusCode: &code, ResponseMs: &ms,
		})
	}

	s, err := storage.GetUptimeStats(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.UptimePct24h != 100 {
		t.Errorf("uptime_pct_24h: got %.2f, want 100", s.UptimePct24h)
	}
	if s.AvgResponseMs == nil || *s.AvgResponseMs != 150 {
		t.Errorf("avg_response_ms: got %v, want 150", s.AvgResponseMs)
	}
}

func TestGetUptimeStats_halfUp(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "stats-half", "https://example.com")

	code := 200
	storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{Status: "up", StatusCode: &code})
	storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{Status: "down", StatusCode: &code})

	s, err := storage.GetUptimeStats(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.UptimePct24h != 50 {
		t.Errorf("uptime_pct_24h: got %.2f, want 50", s.UptimePct24h)
	}
}

// ---------- GetDueUptimeMonitors ----------

func TestGetDueUptimeMonitors_neverChecked(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "due-never", "https://example.com")

	due, err := storage.GetDueUptimeMonitors(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, d := range due {
		if d.ID == m.ID {
			found = true
		}
	}
	if !found {
		t.Error("expected never-checked monitor to be due")
	}
}

func TestGetDueUptimeMonitors_notYetDue(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "not-due", "https://example.com")

	// Set next_check_at well into the future
	future := time.Now().UTC().Add(10 * time.Minute)
	testPool.Exec(context.Background(), `UPDATE uptime_monitors SET next_check_at=$2 WHERE id=$1`, m.ID, future)

	due, err := storage.GetDueUptimeMonitors(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, d := range due {
		if d.ID == m.ID {
			t.Error("monitor with future next_check_at should not be due")
		}
	}
}

func TestGetDueUptimeMonitors_pausedExcluded(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "paused-due", "https://example.com")
	testPool.Exec(context.Background(), `UPDATE uptime_monitors SET status='paused' WHERE id=$1`, m.ID)

	due, err := storage.GetDueUptimeMonitors(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, d := range due {
		if d.ID == m.ID {
			t.Error("paused monitor should not be due")
		}
	}
}

// ---------- Alert queries ----------

func TestListDownUptimeMonitors(t *testing.T) {
	p := setupProjectForUptime(t)
	mUp := seedUptimeMonitor(t, p.ID, "is-up", "https://up.example.com")
	mDown := seedUptimeMonitor(t, p.ID, "is-down", "https://down.example.com")

	code := 200
	storage.RecordUptimeCheck(context.Background(), testPool, mUp.ID, &storage.UptimeCheck{Status: "up", StatusCode: &code})
	// Drive mDown to "down" state
	code = 503
	for range 2 {
		storage.RecordUptimeCheck(context.Background(), testPool, mDown.ID, &storage.UptimeCheck{Status: "down", StatusCode: &code})
	}

	monitors, err := storage.ListDownUptimeMonitors(context.Background(), testPool, []string{p.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, mon := range monitors {
		if mon.ID == mDown.ID {
			found = true
		}
		if mon.ID == mUp.ID {
			t.Error("up monitor should not appear in down list")
		}
	}
	if !found {
		t.Error("expected down monitor in results")
	}
}

func TestListRecoveredUptimeMonitors(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "recovered", "https://example.com")

	// Drive to down, then recover
	code := 503
	for range 2 {
		storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{Status: "down", StatusCode: &code})
	}
	code = 200
	storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{Status: "up", StatusCode: &code})

	since := time.Now().UTC().Add(-1 * time.Minute)
	monitors, err := storage.ListRecoveredUptimeMonitors(context.Background(), testPool, []string{p.ID}, since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, mon := range monitors {
		if mon.ID == m.ID {
			found = true
		}
	}
	if !found {
		t.Error("expected recovered monitor in results")
	}
}

func TestListRecoveredUptimeMonitors_sinceFilter(t *testing.T) {
	p := setupProjectForUptime(t)
	m := seedUptimeMonitor(t, p.ID, "recovered-old", "https://example.com")

	code := 200
	storage.RecordUptimeCheck(context.Background(), testPool, m.ID, &storage.UptimeCheck{Status: "up", StatusCode: &code})

	// Query with a since time in the future — should return nothing
	since := time.Now().UTC().Add(1 * time.Hour)
	monitors, err := storage.ListRecoveredUptimeMonitors(context.Background(), testPool, []string{p.ID}, since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(monitors) != 0 {
		t.Errorf("expected 0 monitors with future since, got %d", len(monitors))
	}
}
