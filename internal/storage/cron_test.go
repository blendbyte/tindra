package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

func setupProjectForCron(t *testing.T) *storage.Project {
	t.Helper()
	truncateProjects(t)
	p, err := storage.CreateProject(context.Background(), testPool, "cron-proj", "Cron Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p
}

func seedCronMonitor(t *testing.T, projectID, name, schedule string) *storage.CronMonitor {
	t.Helper()
	m, err := storage.CreateCronMonitor(context.Background(), testPool, &storage.CronMonitor{
		ProjectID:       projectID,
		Name:            name,
		Schedule:        schedule,
		GracePeriodSecs: 60,
	})
	if err != nil {
		t.Fatalf("create cron monitor: %v", err)
	}
	return m
}

func TestParseCronSchedule_valid(t *testing.T) {
	sched, err := storage.ParseCronSchedule("* * * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sched == nil {
		t.Fatal("expected non-nil schedule")
	}
	next := sched.Next(time.Now())
	if next.IsZero() {
		t.Error("expected non-zero next time")
	}
}

func TestParseCronSchedule_invalid(t *testing.T) {
	_, err := storage.ParseCronSchedule("not a cron expression")
	if err == nil {
		t.Error("expected error for invalid schedule")
	}
}

func TestParseCronSchedule_tooFewFields(t *testing.T) {
	_, err := storage.ParseCronSchedule("* * *")
	if err == nil {
		t.Error("expected error for 3-field schedule")
	}
}

func TestCreateCronMonitor(t *testing.T) {
	p := setupProjectForCron(t)

	m, err := storage.CreateCronMonitor(context.Background(), testPool, &storage.CronMonitor{
		ProjectID:       p.ID,
		Name:            "daily-job",
		Schedule:        "0 0 * * *",
		GracePeriodSecs: 300,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ID == "" {
		t.Error("expected non-empty ID")
	}
	if m.ProjectID != p.ID {
		t.Errorf("project_id: got %q, want %q", m.ProjectID, p.ID)
	}
	if m.Name != "daily-job" {
		t.Errorf("name: got %q, want %q", m.Name, "daily-job")
	}
	if m.Schedule != "0 0 * * *" {
		t.Errorf("schedule: got %q, want %q", m.Schedule, "0 0 * * *")
	}
	if m.GracePeriodSecs != 300 {
		t.Errorf("grace_period_secs: got %d, want 300", m.GracePeriodSecs)
	}
	if m.Status != "active" {
		t.Errorf("status: got %q, want %q", m.Status, "active")
	}
	if m.CreatedAt.IsZero() {
		t.Error("expected non-zero created_at")
	}
}

func TestGetCronMonitor_found(t *testing.T) {
	p := setupProjectForCron(t)
	created := seedCronMonitor(t, p.ID, "hourly", "0 * * * *")

	got, err := storage.GetCronMonitor(context.Background(), testPool, created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected monitor, got nil")
	}
	if got.ID != created.ID {
		t.Errorf("ID: got %q, want %q", got.ID, created.ID)
	}
	if got.Name != "hourly" {
		t.Errorf("name: got %q, want %q", got.Name, "hourly")
	}
}

func TestGetCronMonitor_notFound(t *testing.T) {
	got, err := storage.GetCronMonitor(context.Background(), testPool, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestListCronMonitors_empty(t *testing.T) {
	p := setupProjectForCron(t)

	monitors, err := storage.ListCronMonitors(context.Background(), testPool, []string{p.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(monitors) != 0 {
		t.Errorf("expected 0 monitors, got %d", len(monitors))
	}
}

func TestListCronMonitors_withProjectFilter(t *testing.T) {
	truncateProjects(t)
	p1, _ := storage.CreateProject(context.Background(), testPool, "cron-p1", "P1")
	p2, _ := storage.CreateProject(context.Background(), testPool, "cron-p2", "P2")

	seedCronMonitor(t, p1.ID, "job-p1", "* * * * *")
	seedCronMonitor(t, p2.ID, "job-p2", "* * * * *")

	monitors, err := storage.ListCronMonitors(context.Background(), testPool, []string{p1.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(monitors) != 1 {
		t.Fatalf("expected 1 monitor for p1, got %d", len(monitors))
	}
	if monitors[0].ProjectID != p1.ID {
		t.Errorf("expected monitor for p1, got project_id %q", monitors[0].ProjectID)
	}
}

func TestListCronMonitors_withoutFilter(t *testing.T) {
	truncateProjects(t)
	p1, _ := storage.CreateProject(context.Background(), testPool, "cron-all-p1", "P1")
	p2, _ := storage.CreateProject(context.Background(), testPool, "cron-all-p2", "P2")

	seedCronMonitor(t, p1.ID, "job-a", "* * * * *")
	seedCronMonitor(t, p2.ID, "job-b", "* * * * *")

	monitors, err := storage.ListCronMonitors(context.Background(), testPool, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(monitors) < 2 {
		t.Errorf("expected at least 2 monitors, got %d", len(monitors))
	}
}

func TestListCronMonitors_recentCheckins(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "recent-check", "* * * * *")

	if _, err := storage.RecordCheckin(context.Background(), testPool, m.ID, &storage.CronCheckin{
		Status: "ok",
	}); err != nil {
		t.Fatalf("record checkin: %v", err)
	}

	monitors, err := storage.ListCronMonitors(context.Background(), testPool, []string{p.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(monitors) != 1 {
		t.Fatalf("expected 1 monitor, got %d", len(monitors))
	}
	if len(monitors[0].RecentCheckins) != 1 {
		t.Errorf("expected 1 recent checkin, got %d", len(monitors[0].RecentCheckins))
	}
	if monitors[0].RecentCheckins[0].Status != "ok" {
		t.Errorf("expected checkin status ok, got %q", monitors[0].RecentCheckins[0].Status)
	}
}

func TestUpdateCronMonitor(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "original-name", "* * * * *")

	m.Name = "updated-name"
	m.Schedule = "0 12 * * *"
	m.GracePeriodSecs = 120
	m.Status = "paused"

	updated, err := storage.UpdateCronMonitor(context.Background(), testPool, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated == nil {
		t.Fatal("expected updated monitor, got nil")
	}
	if updated.Name != "updated-name" {
		t.Errorf("name: got %q, want %q", updated.Name, "updated-name")
	}
	if updated.Schedule != "0 12 * * *" {
		t.Errorf("schedule: got %q, want %q", updated.Schedule, "0 12 * * *")
	}
	if updated.GracePeriodSecs != 120 {
		t.Errorf("grace_period_secs: got %d, want 120", updated.GracePeriodSecs)
	}
	if updated.Status != "paused" {
		t.Errorf("status: got %q, want %q", updated.Status, "paused")
	}
}

func TestUpdateCronMonitor_notFound(t *testing.T) {
	updated, err := storage.UpdateCronMonitor(context.Background(), testPool, &storage.CronMonitor{
		ID:       "00000000-0000-0000-0000-000000000000",
		Name:     "ghost",
		Schedule: "* * * * *",
		Status:   "active",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated != nil {
		t.Errorf("expected nil for unknown ID, got %+v", updated)
	}
}

func TestDeleteCronMonitor_found(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "to-delete", "* * * * *")

	deleted, err := storage.DeleteCronMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleted {
		t.Error("expected deleted=true")
	}

	got, err := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestDeleteCronMonitor_missing(t *testing.T) {
	deleted, err := storage.DeleteCronMonitor(context.Background(), testPool, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted {
		t.Error("expected deleted=false for missing ID")
	}
}

func TestRecordCheckin_inProgress(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "in-prog", "* * * * *")

	ci, err := storage.RecordCheckin(context.Background(), testPool, m.ID, &storage.CronCheckin{
		Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ci == nil {
		t.Fatal("expected checkin, got nil")
	}
	if ci.Status != "in_progress" {
		t.Errorf("status: got %q, want in_progress", ci.Status)
	}

	updated, err := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if !updated.IsRunning {
		t.Error("expected is_running=true after in_progress checkin")
	}
	if updated.State != "in_progress" {
		t.Errorf("state: got %q, want in_progress", updated.State)
	}
}

func TestRecordCheckin_ok(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "ok-check", "* * * * *")

	ci, err := storage.RecordCheckin(context.Background(), testPool, m.ID, &storage.CronCheckin{
		Status: "ok",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ci.Status != "ok" {
		t.Errorf("status: got %q, want ok", ci.Status)
	}

	updated, err := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if updated.IsRunning {
		t.Error("expected is_running=false after ok checkin")
	}
	if updated.LastOkAt == nil {
		t.Error("expected last_ok_at to be set after ok checkin")
	}
	if updated.NextExpectedAt == nil {
		t.Error("expected next_expected_at to be set after ok checkin")
	}
	if updated.LastCheckinStatus == nil || *updated.LastCheckinStatus != "ok" {
		t.Errorf("last_checkin_status: got %v, want ok", updated.LastCheckinStatus)
	}
}

func TestRecordCheckin_error(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "err-check", "* * * * *")

	ci, err := storage.RecordCheckin(context.Background(), testPool, m.ID, &storage.CronCheckin{
		Status: "error",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ci.Status != "error" {
		t.Errorf("status: got %q, want error", ci.Status)
	}

	updated, err := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if updated.IsRunning {
		t.Error("expected is_running=false after error checkin")
	}
	if updated.LastCheckinStatus == nil || *updated.LastCheckinStatus != "error" {
		t.Errorf("last_checkin_status: got %v, want error", updated.LastCheckinStatus)
	}
	if updated.State != "error" {
		t.Errorf("state: got %q, want error", updated.State)
	}
}

func TestFinishCheckin_toOk(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "finish-ok", "* * * * *")

	ci, err := storage.RecordCheckin(context.Background(), testPool, m.ID, &storage.CronCheckin{
		Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("start checkin: %v", err)
	}

	dur := 250
	finished, err := storage.FinishCheckin(context.Background(), testPool, m.ID, ci.ID, "ok", &dur)
	if err != nil {
		t.Fatalf("finish checkin: %v", err)
	}
	if finished == nil {
		t.Fatal("expected finished checkin, got nil")
	}
	if finished.Status != "ok" {
		t.Errorf("status: got %q, want ok", finished.Status)
	}
	if finished.DurationMs == nil || *finished.DurationMs != 250 {
		t.Errorf("duration_ms: got %v, want 250", finished.DurationMs)
	}
	if finished.FinishedAt == nil {
		t.Error("expected finished_at to be set")
	}

	updated, err := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if updated.IsRunning {
		t.Error("expected is_running=false after finish to ok")
	}
}

func TestFinishCheckin_toError(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "finish-err", "* * * * *")

	ci, err := storage.RecordCheckin(context.Background(), testPool, m.ID, &storage.CronCheckin{
		Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("start checkin: %v", err)
	}

	finished, err := storage.FinishCheckin(context.Background(), testPool, m.ID, ci.ID, "error", nil)
	if err != nil {
		t.Fatalf("finish checkin: %v", err)
	}
	if finished == nil {
		t.Fatal("expected finished checkin, got nil")
	}
	if finished.Status != "error" {
		t.Errorf("status: got %q, want error", finished.Status)
	}

	updated, err := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if updated.LastCheckinStatus == nil || *updated.LastCheckinStatus != "error" {
		t.Errorf("last_checkin_status: got %v, want error", updated.LastCheckinStatus)
	}
}

func TestFinishCheckin_notFound(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "finish-missing", "* * * * *")

	got, err := storage.FinishCheckin(context.Background(), testPool, m.ID, "00000000-0000-0000-0000-000000000000", "ok", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for unknown checkin, got %+v", got)
	}
}

func TestGetCheckin_found(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "get-ci", "* * * * *")

	created, err := storage.RecordCheckin(context.Background(), testPool, m.ID, &storage.CronCheckin{
		Status: "ok",
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	got, err := storage.GetCheckin(context.Background(), testPool, created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected checkin, got nil")
	}
	if got.ID != created.ID {
		t.Errorf("ID: got %q, want %q", got.ID, created.ID)
	}
	if got.MonitorID != m.ID {
		t.Errorf("monitor_id: got %q, want %q", got.MonitorID, m.ID)
	}
}

func TestGetCheckin_notFound(t *testing.T) {
	got, err := storage.GetCheckin(context.Background(), testPool, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestListCheckins_mostRecentFirst(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "list-ci", "* * * * *")

	for i := range 3 {
		if _, err := storage.RecordCheckin(context.Background(), testPool, m.ID, &storage.CronCheckin{
			Status: "ok",
		}); err != nil {
			t.Fatalf("record checkin %d: %v", i, err)
		}
	}

	checkins, err := storage.ListCheckins(context.Background(), testPool, m.ID, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(checkins) != 3 {
		t.Fatalf("expected 3 checkins, got %d", len(checkins))
	}
	for i := 1; i < len(checkins); i++ {
		if checkins[i].ReceivedAt.After(checkins[i-1].ReceivedAt) {
			t.Errorf("checkins not ordered most-recent-first at index %d", i)
		}
	}
}

func TestListCheckins_respectsLimit(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "limit-ci", "* * * * *")

	for i := range 5 {
		if _, err := storage.RecordCheckin(context.Background(), testPool, m.ID, &storage.CronCheckin{
			Status: "ok",
		}); err != nil {
			t.Fatalf("record checkin %d: %v", i, err)
		}
	}

	checkins, err := storage.ListCheckins(context.Background(), testPool, m.ID, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(checkins) != 2 {
		t.Errorf("expected 2 checkins with limit=2, got %d", len(checkins))
	}
}

func TestListCheckins_zeroLimitClamped(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "clamp-ci", "* * * * *")

	if _, err := storage.RecordCheckin(context.Background(), testPool, m.ID, &storage.CronCheckin{
		Status: "ok",
	}); err != nil {
		t.Fatalf("record checkin: %v", err)
	}

	checkins, err := storage.ListCheckins(context.Background(), testPool, m.ID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(checkins) == 0 {
		t.Error("expected checkins even with limit=0 (should clamp to 50)")
	}
}

func TestListMonitorsWithRecentErrors(t *testing.T) {
	p := setupProjectForCron(t)

	mOk := seedCronMonitor(t, p.ID, "no-errors", "* * * * *")
	mErr := seedCronMonitor(t, p.ID, "has-errors", "* * * * *")

	if _, err := storage.RecordCheckin(context.Background(), testPool, mOk.ID, &storage.CronCheckin{
		Status: "ok",
	}); err != nil {
		t.Fatalf("ok checkin: %v", err)
	}
	if _, err := storage.RecordCheckin(context.Background(), testPool, mErr.ID, &storage.CronCheckin{
		Status: "error",
	}); err != nil {
		t.Fatalf("error checkin: %v", err)
	}

	since := time.Now().UTC().Add(-1 * time.Minute)
	monitors, err := storage.ListMonitorsWithRecentErrors(context.Background(), testPool, []string{p.ID}, since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, mon := range monitors {
		if mon.ID == mErr.ID {
			found = true
		}
		if mon.ID == mOk.ID {
			t.Error("ok monitor should not appear in error list")
		}
	}
	if !found {
		t.Error("expected error monitor in results")
	}
}

func TestListMonitorsWithRecentErrors_sinceFilter(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "old-error", "* * * * *")

	if _, err := storage.RecordCheckin(context.Background(), testPool, m.ID, &storage.CronCheckin{
		Status: "error",
	}); err != nil {
		t.Fatalf("error checkin: %v", err)
	}

	// Query with a since time in the future — should return nothing.
	since := time.Now().UTC().Add(1 * time.Hour)
	monitors, err := storage.ListMonitorsWithRecentErrors(context.Background(), testPool, []string{p.ID}, since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(monitors) != 0 {
		t.Errorf("expected 0 monitors with future since, got %d", len(monitors))
	}
}

func TestListMonitorsWithRecentErrors_noProjectFilter(t *testing.T) {
	truncateProjects(t)
	p1, _ := storage.CreateProject(context.Background(), testPool, "err-all-p1", "P1")
	p2, _ := storage.CreateProject(context.Background(), testPool, "err-all-p2", "P2")

	m1 := seedCronMonitor(t, p1.ID, "err-job-1", "* * * * *")
	m2 := seedCronMonitor(t, p2.ID, "err-job-2", "* * * * *")

	for _, id := range []string{m1.ID, m2.ID} {
		if _, err := storage.RecordCheckin(context.Background(), testPool, id, &storage.CronCheckin{
			Status: "error",
		}); err != nil {
			t.Fatalf("error checkin: %v", err)
		}
	}

	since := time.Now().UTC().Add(-1 * time.Minute)
	monitors, err := storage.ListMonitorsWithRecentErrors(context.Background(), testPool, nil, since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(monitors) < 2 {
		t.Errorf("expected at least 2 monitors without project filter, got %d", len(monitors))
	}
}

func TestListOverdueMonitors(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "overdue", "* * * * *")

	// Manually backdate next_expected_at so it's past the grace period.
	past := time.Now().UTC().Add(-10 * time.Minute)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE cron_monitors SET next_expected_at = $2 WHERE id = $1
	`, m.ID, past); err != nil {
		t.Fatalf("backdate next_expected_at: %v", err)
	}

	monitors, err := storage.ListOverdueMonitors(context.Background(), testPool, []string{p.ID})
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
		t.Error("expected overdue monitor in results")
	}
}

func TestListOverdueMonitors_notYetOverdue(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "not-overdue", "* * * * *")

	// Set next_expected_at far in the future.
	future := time.Now().UTC().Add(1 * time.Hour)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE cron_monitors SET next_expected_at = $2 WHERE id = $1
	`, m.ID, future); err != nil {
		t.Fatalf("set future next_expected_at: %v", err)
	}

	monitors, err := storage.ListOverdueMonitors(context.Background(), testPool, []string{p.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, mon := range monitors {
		if mon.ID == m.ID {
			t.Error("monitor with future next_expected_at should not be overdue")
		}
	}
}

func TestListOverdueMonitors_noProjectFilter(t *testing.T) {
	truncateProjects(t)
	p1, _ := storage.CreateProject(context.Background(), testPool, "over-all-p1", "P1")
	p2, _ := storage.CreateProject(context.Background(), testPool, "over-all-p2", "P2")

	m1 := seedCronMonitor(t, p1.ID, "over-job-1", "* * * * *")
	m2 := seedCronMonitor(t, p2.ID, "over-job-2", "* * * * *")

	past := time.Now().UTC().Add(-10 * time.Minute)
	for _, id := range []string{m1.ID, m2.ID} {
		if _, err := testPool.Exec(context.Background(), `
			UPDATE cron_monitors SET next_expected_at = $2 WHERE id = $1
		`, id, past); err != nil {
			t.Fatalf("backdate: %v", err)
		}
	}

	monitors, err := storage.ListOverdueMonitors(context.Background(), testPool, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(monitors) < 2 {
		t.Errorf("expected at least 2 overdue monitors without project filter, got %d", len(monitors))
	}
}

func TestListOverdueMonitors_pausedExcluded(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "paused-overdue", "* * * * *")

	past := time.Now().UTC().Add(-10 * time.Minute)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE cron_monitors SET next_expected_at = $2, status = 'paused' WHERE id = $1
	`, m.ID, past); err != nil {
		t.Fatalf("set paused + past: %v", err)
	}

	monitors, err := storage.ListOverdueMonitors(context.Background(), testPool, []string{p.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, mon := range monitors {
		if mon.ID == m.ID {
			t.Error("paused monitor should not appear as overdue")
		}
	}
}

func TestMonitorState_unknown(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "fresh-monitor", "* * * * *")

	got, err := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if got.State != "unknown" {
		t.Errorf("new monitor state: got %q, want unknown", got.State)
	}
}

func TestMonitorState_missed(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "missed-monitor", "* * * * *")

	// Record an ok checkin, then backdate next_expected_at past grace period.
	if _, err := storage.RecordCheckin(context.Background(), testPool, m.ID, &storage.CronCheckin{
		Status: "ok",
	}); err != nil {
		t.Fatalf("record ok: %v", err)
	}

	past := time.Now().UTC().Add(-10 * time.Minute)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE cron_monitors SET next_expected_at = $2 WHERE id = $1
	`, m.ID, past); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	got, err := storage.GetCronMonitor(context.Background(), testPool, m.ID)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if got.State != "missed" {
		t.Errorf("state: got %q, want missed", got.State)
	}
}

func TestRecordCheckin_setsReceivedAt(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "recv-at", "* * * * *")

	before := time.Now().UTC()
	ci, err := storage.RecordCheckin(context.Background(), testPool, m.ID, &storage.CronCheckin{
		Status: "ok",
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	after := time.Now().UTC()

	if ci.ReceivedAt.Before(before) || ci.ReceivedAt.After(after) {
		t.Errorf("received_at %v outside expected window [%v, %v]", ci.ReceivedAt, before, after)
	}
}
