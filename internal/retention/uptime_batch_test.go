package retention_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/retention"
)

func seedUptimeBacklog(t *testing.T, expired, fresh int) []string {
	t.Helper()
	ctx := context.Background()
	_, err := testPool.Exec(ctx, "TRUNCATE uptime_monitors CASCADE")
	require.NoError(t, err)
	t.Cleanup(func() { _, e := testPool.Exec(ctx, "TRUNCATE uptime_monitors CASCADE"); require.NoError(t, e) })
	var ids []string
	for _, state := range []string{"up", "down"} {
		var id string
		require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO uptime_monitors(project_id,name,url,state,last_checked_at,consecutive_failures) VALUES ($1,$2,'https://example.com', $2,NOW(),2) RETURNING id`, testProject.ID, state).Scan(&id))
		ids = append(ids, id)
	}
	_, err = testPool.Exec(ctx, `INSERT INTO uptime_checks(id,monitor_id,status,checked_at)
 SELECT lpad(to_hex(i),32,'0')::uuid,CASE WHEN i%2=0 THEN $1::uuid ELSE $2::uuid END,
 CASE WHEN i%2=0 THEN 'up' ELSE 'down' END,
 CASE WHEN i <= $3 THEN NOW()-interval '91 days' ELSE NOW() END
 FROM generate_series(1,$4::int) i`, ids[0], ids[1], expired, expired+fresh)
	require.NoError(t, err)
	return ids
}
func TestWorkerUptimeBatchesPreserveMonitorsAndFreshChecks(t *testing.T) {
	ids := seedUptimeBacklog(t, 12000, 7)
	ctx := context.Background()
	var before, after string
	query := `SELECT jsonb_agg(to_jsonb(m) ORDER BY id)::text FROM uptime_monitors m`
	require.NoError(t, testPool.QueryRow(ctx, query).Scan(&before))
	trace := &capTrace{matchSQL: "DELETE FROM uptime_checks"}
	trace.afterBatch = func() {
		trace.afterBatch = nil
		_, err := testPool.Exec(ctx, `INSERT INTO uptime_checks(monitor_id,status) VALUES ($1,'up')`, ids[0])
		require.NoError(t, err)
	}
	retention.NewWorker(capPool(t, trace), 90).RunOnce(ctx)
	require.Equal(t, []int64{5000, 5000, 2000}, trace.batches)
	require.Equal(t, 8, capCount(t, "uptime_checks"))
	var expired int
	require.NoError(t, testPool.QueryRow(ctx, "SELECT count(*) FROM uptime_checks WHERE checked_at < NOW()-interval '90 days'").Scan(&expired))
	require.Zero(t, expired)
	require.NoError(t, testPool.QueryRow(ctx, query).Scan(&after))
	require.Equal(t, before, after)
}
func TestWorkerUptimeCancellationResumes(t *testing.T) {
	seedUptimeBacklog(t, 12000, 7)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	trace := &capTrace{matchSQL: "DELETE FROM uptime_checks", cancel: cancel}
	retention.NewWorker(capPool(t, trace), 90).RunOnce(ctx)
	require.Equal(t, []int64{5000}, trace.batches)
	require.Equal(t, 7007, capCount(t, "uptime_checks"))
	retention.NewWorker(testPool, 90).RunOnce(context.Background())
	require.Equal(t, 7, capCount(t, "uptime_checks"))
}
func TestWorkerUptimeBudgetAndDisabled(t *testing.T) {
	seedUptimeBacklog(t, 205000, 7)
	ctx := context.Background()
	retention.NewWorker(testPool, 0).RunOnce(ctx)
	require.Equal(t, 205007, capCount(t, "uptime_checks"))
	trace := &capTrace{matchSQL: "DELETE FROM uptime_checks"}
	worker := retention.NewWorker(capPool(t, trace), 90)
	worker.RunOnce(ctx)
	require.Len(t, trace.batches, 40)
	for _, n := range trace.batches {
		require.EqualValues(t, 5000, n)
	}
	require.Equal(t, 5007, capCount(t, "uptime_checks"))
	var first string
	require.NoError(t, testPool.QueryRow(ctx, "SELECT id::text FROM uptime_checks ORDER BY checked_at,id LIMIT 1").Scan(&first))
	require.Equal(t, "00000000-0000-0000-0000-000000030d41", first) // 200001
	worker.RunOnce(ctx)
	require.Equal(t, 7, capCount(t, "uptime_checks"))
}
