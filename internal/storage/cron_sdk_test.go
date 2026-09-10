package storage_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestSDKCheckinLifecycle(t *testing.T) {
	p := setupProjectForCron(t)
	for _, status := range []string{"ok", "error"} {
		t.Run(status, func(t *testing.T) {
			m := seedCronMonitor(t, p.ID, status, "* * * * *")
			id, env := "shared-sdk-id", "production"
			now := time.Now().UTC().Truncate(time.Microsecond)
			start := &storage.CronCheckin{SDKCheckinID: &id, Status: "in_progress", StartedAt: &now, Environment: &env}
			created, err := storage.RecordCheckin(t.Context(), testPool, m.ID, start)
			require.NoError(t, err)
			require.NotNil(t, created)
			retry, err := storage.RecordCheckin(t.Context(), testPool, m.ID, start)
			require.NoError(t, err)
			require.Nil(t, retry)
			duration := 1250
			finishedAt := now.Add(time.Second)
			terminal := &storage.CronCheckin{SDKCheckinID: &id, Status: status, DurationMs: &duration, FinishedAt: &finishedAt}
			finished, err := storage.RecordCheckin(t.Context(), testPool, m.ID, terminal)
			require.NoError(t, err)
			require.NotNil(t, finished)
			require.Equal(t, created.ID, finished.ID)
			require.NotNil(t, finished.StartedAt)
			require.True(t, now.Equal(*finished.StartedAt))
			require.Equal(t, &env, finished.Environment)
			require.Equal(t, &duration, finished.DurationMs)
			require.NotNil(t, finished.FinishedAt)
			require.True(t, finishedAt.Equal(*finished.FinishedAt))
			before, err := storage.GetCronMonitor(t.Context(), testPool, m.ID)
			require.NoError(t, err)
			for _, duplicate := range []*storage.CronCheckin{terminal, start, {SDKCheckinID: &id, Status: "error", FinishedAt: &now}} {
				retry, err = storage.RecordCheckin(t.Context(), testPool, m.ID, duplicate)
				require.NoError(t, err)
				require.Nil(t, retry)
			}
			after, err := storage.GetCronMonitor(t.Context(), testPool, m.ID)
			require.NoError(t, err)
			require.Equal(t, before, after, "retries must not advance the schedule or change monitor state")
			rows, err := storage.ListCheckins(t.Context(), testPool, m.ID, 10)
			require.NoError(t, err)
			require.Equal(t, []*storage.CronCheckin{finished}, rows)
		})
	}
}

func TestSDKCheckinConcurrentDeliveries(t *testing.T) {
	p := setupProjectForCron(t)
	m := seedCronMonitor(t, p.ID, "concurrent", "* * * * *")
	id := "concurrent-sdk-id"
	now := time.Now().UTC()
	for _, phase := range []string{"in_progress", "ok"} {
		const workers = 8
		type result struct {
			ci  *storage.CronCheckin
			err error
		}
		results := make(chan result, workers)
		gate := make(chan struct{})
		for range workers {
			go func() {
				<-gate
				ci, err := storage.RecordCheckin(t.Context(), testPool, m.ID, &storage.CronCheckin{
					SDKCheckinID: &id, Status: phase, StartedAt: &now, FinishedAt: &now,
				})
				results <- result{ci, err}
			}()
		}
		close(gate)
		changed := 0
		for range workers {
			r := <-results
			require.NoError(t, r.err)
			if r.ci != nil {
				changed++
			}
		}
		require.Equal(t, 1, changed)
	}
	rows, err := storage.ListCheckins(t.Context(), testPool, m.ID, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "ok", rows[0].Status)
}

func TestSDKCheckinFailuresRollBack(t *testing.T) {
	for _, finish := range []bool{false, true} {
		for _, stage := range []string{"begin", "INSERT INTO cron_checkins", "UPDATE cron_monitors", "commit"} {
			t.Run(fmt.Sprintf("finish=%v/%s", finish, stage), func(t *testing.T) {
				p := setupProjectForCron(t)
				m := seedCronMonitor(t, p.ID, "rollback", "* * * * *")
				id := "retryable-sdk-id"
				now := time.Now().UTC()
				ci := &storage.CronCheckin{SDKCheckinID: &id, Status: "in_progress", StartedAt: &now}
				if finish {
					_, err := storage.RecordCheckin(t.Context(), testPool, m.ID, ci)
					require.NoError(t, err)
					ci = &storage.CronCheckin{SDKCheckinID: &id, Status: "ok", FinishedAt: &now}
				}
				before, err := storage.ListCheckins(t.Context(), testPool, m.ID, 10)
				require.NoError(t, err)
				monitorBefore, err := storage.GetCronMonitor(t.Context(), testPool, m.ID)
				require.NoError(t, err)
				trace := &cancelGroupingQuery{match: stage}
				cfg := testPool.Config()
				cfg.ConnConfig.Tracer = trace
				failing, err := pgxpool.NewWithConfig(t.Context(), cfg)
				require.NoError(t, err)
				got, err := storage.RecordCheckin(t.Context(), failing, m.ID, ci)
				failing.Close()
				require.Error(t, err)
				require.Nil(t, got)
				require.True(t, trace.hit.Load())
				after, err := storage.ListCheckins(t.Context(), testPool, m.ID, 10)
				require.NoError(t, err)
				require.Equal(t, before, after)
				monitorAfter, err := storage.GetCronMonitor(t.Context(), testPool, m.ID)
				require.NoError(t, err)
				require.Equal(t, monitorBefore, monitorAfter)
				got, err = storage.RecordCheckin(t.Context(), testPool, m.ID, ci)
				require.NoError(t, err)
				require.NotNil(t, got)
			})
		}
	}
}
