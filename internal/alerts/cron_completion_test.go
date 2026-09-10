package alerts

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestCronErrorCompletionWindow(t *testing.T) {
	for _, kind := range []string{"paired", "SDK", "single shot", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			ctx := t.Context()
			p, err := storage.CreateProject(ctx, testPool, uuid.NewString(), "Completion test")
			require.NoError(t, err)
			m, err := storage.CreateCronMonitor(ctx, testPool, &storage.CronMonitor{
				ProjectID: p.ID, Name: "job", Schedule: "* * * * *",
			})
			require.NoError(t, err)
			started := time.Now().Add(-30 * time.Minute)
			cutoff := time.Now().Add(-5 * time.Minute)
			rule := &storage.AlertRule{ProjectIDs: []string{p.ID}, Trigger: "cron_error", CreatedAt: cutoff}
			e := testEvaluator(nil)
			var ci *storage.CronCheckin
			sdkID := uuid.NewString()
			if kind == "paired" || kind == "SDK" {
				ci, err = storage.RecordCheckin(ctx, testPool, m.ID, &storage.CronCheckin{
					Status: "in_progress", StartedAt: &started, SDKCheckinID: &sdkID,
				})
				require.NoError(t, err)
				_, err = testPool.Exec(ctx, `UPDATE cron_checkins SET received_at=$2 WHERE id=$1`, ci.ID, started)
				require.NoError(t, err)
				met, _, err := e.conditionMet(ctx, rule)
				require.NoError(t, err)
				require.False(t, met, "running jobs must not alert")
			}
			finished := time.Now()
			switch kind {
			case "paired":
				ci, err = storage.FinishCheckin(ctx, testPool, m.ID, ci.ID, "error", nil)
			case "SDK":
				ci, err = storage.RecordCheckin(ctx, testPool, m.ID, &storage.CronCheckin{
					Status: "error", FinishedAt: &finished, SDKCheckinID: &sdkID,
				})
			case "single shot":
				ci, err = storage.RecordCheckin(ctx, testPool, m.ID, &storage.CronCheckin{Status: "error", FinishedAt: &finished})
			case "legacy":
				ci, err = storage.RecordCheckin(ctx, testPool, m.ID, &storage.CronCheckin{Status: "error"})
			}
			require.NoError(t, err)
			require.NotNil(t, ci)
			for _, previousAlert := range []bool{false, true} {
				if previousAlert {
					rule.CreatedAt = started.Add(-time.Hour)
					rule.LastFiredAt = &cutoff
				}
				met, details, err := e.conditionMet(ctx, rule)
				require.NoError(t, err)
				require.True(t, met)
				require.Equal(t, 1, details["error_count"])
			}
			// A success from an overlapping run must not hide the earlier failure.
			_, err = storage.RecordCheckin(ctx, testPool, m.ID, &storage.CronCheckin{Status: "ok", FinishedAt: &finished})
			require.NoError(t, err)
			payload := &AlertPayload{Trigger: "cron_error"}
			e.enrichPayload(ctx, payload, rule)
			require.Len(t, payload.Monitors, 1)
			require.Equal(t, m.ID, payload.Monitors[0].ID)
			met, details, err := e.conditionMet(ctx, rule)
			require.NoError(t, err)
			require.True(t, met)
			require.Equal(t, 1, details["error_count"])
			// Once covered by an alert, duplicate terminal deliveries cannot re-alert.
			after := time.Now().Add(time.Second)
			rule.LastFiredAt = &after
			switch kind {
			case "SDK":
				retriedAt := after.Add(time.Second)
				retry, err := storage.RecordCheckin(ctx, testPool, m.ID, &storage.CronCheckin{
					Status: "error", FinishedAt: &retriedAt, SDKCheckinID: &sdkID,
				})
				require.NoError(t, err)
				require.Nil(t, retry)
			case "paired":
				retry, err := storage.FinishCheckin(ctx, testPool, m.ID, ci.ID, "error", nil)
				require.NoError(t, err)
				require.Nil(t, retry)
			}
			met, _, err = e.conditionMet(ctx, rule)
			require.NoError(t, err)
			require.False(t, met)
			monitors, err := storage.ListMonitorsWithRecentErrors(ctx, testPool, rule.ProjectIDs, after)
			require.NoError(t, err)
			require.Empty(t, monitors)
		})
	}
}

func TestCronErrorCompletionFilters(t *testing.T) {
	ctx := t.Context()
	p, err := storage.CreateProject(ctx, testPool, uuid.NewString(), "Completion filters")
	require.NoError(t, err)
	m, err := storage.CreateCronMonitor(ctx, testPool, &storage.CronMonitor{ProjectID: p.ID, Name: "job", Schedule: "* * * * *"})
	require.NoError(t, err)
	cutoff := time.Now().Add(-time.Minute).Truncate(time.Microsecond)
	old := cutoff.Add(-time.Minute)
	for _, finished := range []time.Time{old, cutoff} {
		_, err := storage.RecordCheckin(ctx, testPool, m.ID, &storage.CronCheckin{Status: "error", FinishedAt: &finished})
		require.NoError(t, err)
	}
	rule := &storage.AlertRule{ProjectIDs: []string{p.ID}, Trigger: "cron_error", CreatedAt: cutoff}
	e := testEvaluator(nil)
	met, _, err := e.conditionMet(ctx, rule)
	require.NoError(t, err)
	require.False(t, met, "recent receipt must not override an old or boundary completion")
	finished := time.Now()
	for range 2 {
		_, err = storage.RecordCheckin(ctx, testPool, m.ID, &storage.CronCheckin{Status: "error", FinishedAt: &finished})
		require.NoError(t, err)
	}
	met, details, err := e.conditionMet(ctx, rule)
	require.NoError(t, err)
	require.True(t, met)
	require.Equal(t, 2, details["error_count"])
	monitors, err := storage.ListMonitorsWithRecentErrors(ctx, testPool, rule.ProjectIDs, cutoff)
	require.NoError(t, err)
	require.Len(t, monitors, 1, "multiple failed runs must not duplicate the monitor")
	rule.ProjectIDs = []string{uuid.NewString()}
	met, _, err = e.conditionMet(ctx, rule)
	require.NoError(t, err)
	require.False(t, met)
	rule.ProjectIDs = nil
	met, _, err = e.conditionMet(ctx, rule)
	require.NoError(t, err)
	require.True(t, met, "unscoped rules include completed failures")
	m.Status = "paused"
	_, err = storage.UpdateCronMonitor(ctx, testPool, m)
	require.NoError(t, err)
	rule.ProjectIDs = []string{p.ID}
	met, _, err = e.conditionMet(ctx, rule)
	require.NoError(t, err)
	require.False(t, met)
	monitors, err = storage.ListMonitorsWithRecentErrors(ctx, testPool, rule.ProjectIDs, cutoff)
	require.NoError(t, err)
	require.Empty(t, monitors)
}

func TestCronErrorQueryFailureDoesNotTrigger(t *testing.T) {
	pool, err := pgxpool.NewWithConfig(t.Context(), testPool.Config())
	require.NoError(t, err)
	pool.Close()
	e := &Evaluator{pool: pool}
	rule := &storage.AlertRule{Trigger: "cron_error", CreatedAt: time.Now().Add(-time.Minute)}
	met, details, err := e.conditionMet(t.Context(), rule)
	require.ErrorContains(t, err, "cron_error count")
	require.False(t, met)
	require.Nil(t, details)
}
