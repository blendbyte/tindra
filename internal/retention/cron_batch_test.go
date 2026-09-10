package retention_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/retention"
	"github.com/blendbyte/tindra/internal/storage"
)

func seedCronBacklog(t *testing.T, expired int) string {
	t.Helper()
	ctx := t.Context()
	_, err := testPool.Exec(ctx, "TRUNCATE cron_monitors CASCADE")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := testPool.Exec(context.Background(), "TRUNCATE cron_monitors CASCADE")
		require.NoError(t, err)
	})
	var id string
	require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO cron_monitors
		(project_id,name,schedule,is_running,last_checkin_status,last_checkin_at,last_ok_at,next_expected_at)
		VALUES ($1,'retention-job','* * * * *',true,'error',NOW(),NOW(),NOW()) RETURNING id`, testProject.ID).Scan(&id))
	_, err = testPool.Exec(ctx, `INSERT INTO cron_checkins(monitor_id,status,received_at,finished_at)
		SELECT $1,CASE WHEN i%2=0 THEN 'ok' ELSE 'error' END,NOW()-interval '365 days',
		CASE WHEN i%3=0 THEN NULL ELSE NOW()-interval '91 days' END
		FROM generate_series(1,$2::int) i`, id, expired)
	require.NoError(t, err)
	// Preserve an ancient active run, a recently completed long-running failure,
	// and a recent legacy terminal ping.
	_, err = testPool.Exec(ctx, `INSERT INTO cron_checkins(monitor_id,status,received_at,finished_at) VALUES
		($1,'in_progress',NOW()-interval '365 days',NULL),
		($1,'error',NOW()-interval '365 days',NOW()),
		($1,'ok',NOW(),NULL)`, id)
	require.NoError(t, err)
	return id
}

func TestCronRetentionPreservesActiveRunsSummariesAndRecentErrors(t *testing.T) {
	id := seedCronBacklog(t, 12000)
	ctx := t.Context()
	var before, after string
	query := `SELECT to_jsonb(m)::text FROM cron_monitors m WHERE id=$1`
	require.NoError(t, testPool.QueryRow(ctx, query, id).Scan(&before))
	trace := &capTrace{matchSQL: "DELETE FROM cron_checkins"}
	require.False(t, retention.NewWorker(capPool(t, trace), 90).RunOnce(ctx))
	require.Equal(t, []int64{5000, 5000, 2000}, trace.batches)
	require.Equal(t, 3, capCount(t, "cron_checkins"))
	require.NoError(t, testPool.QueryRow(ctx, query, id).Scan(&after))
	require.Equal(t, before, after)
	var running, oldCompleted int
	require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM cron_checkins WHERE status='in_progress'`).Scan(&running))
	require.Equal(t, 1, running)
	require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM cron_checkins
		WHERE status IN ('ok','error') AND COALESCE(finished_at,received_at)<NOW()-interval '90 days'`).Scan(&oldCompleted))
	require.Zero(t, oldCompleted)
	monitors, err := storage.ListMonitorsWithRecentErrors(ctx, testPool, []string{testProject.ID}, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, monitors, 1, "recently completed failures must remain available to alerts")
	require.Equal(t, id, monitors[0].ID)
}

func TestCronRetentionCancellationResumes(t *testing.T) {
	seedCronBacklog(t, 12000)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	trace := &capTrace{matchSQL: "DELETE FROM cron_checkins", cancel: cancel}
	retention.NewWorker(capPool(t, trace), 90).RunOnce(ctx)
	require.Equal(t, []int64{5000}, trace.batches)
	require.Equal(t, 7003, capCount(t, "cron_checkins"))
	require.False(t, retention.NewWorker(testPool, 90).RunOnce(t.Context()))
	require.Equal(t, 3, capCount(t, "cron_checkins"))
}

func TestCronRetentionBudgetAndDisabled(t *testing.T) {
	seedCronBacklog(t, 205000)
	ctx := t.Context()
	// Other policies can enable the worker without enabling cron age cleanup.
	require.False(t, retention.NewWorker(testPool, 0).WithRowLimits(1000000, 1000000).RunOnce(ctx))
	require.Equal(t, 205003, capCount(t, "cron_checkins"))
	trace := &capTrace{matchSQL: "DELETE FROM cron_checkins"}
	w := retention.NewWorker(capPool(t, trace), 90)
	require.True(t, w.RunOnce(ctx), "a capped pass must request an earlier follow-up")
	require.Len(t, trace.batches, 40)
	for _, n := range trace.batches {
		require.EqualValues(t, 5000, n)
	}
	require.Equal(t, 5003, capCount(t, "cron_checkins"))
	require.False(t, w.RunOnce(ctx))
	require.Equal(t, 3, capCount(t, "cron_checkins"))
}
