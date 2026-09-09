package retention_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/retention"
)

func seedProfileAgeBacklog(t *testing.T, expired, fresh int) {
	t.Helper()
	clearProfiles(t)
	_, err := testPool.Exec(context.Background(), `INSERT INTO profile_chunks
 (project_id,format,profiler_id,chunk_id,start_ts,end_ts,received_at,sample_count,size_bytes,encoding,data)
 SELECT $1,2,'profile-age-budget',gen_random_uuid()::text,
 NOW()+interval '1 year',NOW()+interval '1 year',
 CASE WHEN i <= $2 THEN NOW()-interval '30 days' ELSE NOW() END,
 10,128,1,decode(repeat('00',128),'hex')
 FROM generate_series(1,$3::int) i`, testProject.ID, expired, expired+fresh)
	require.NoError(t, err)
}
func TestWorkerProfileAgePassBudget(t *testing.T) {
	seedProfileAgeBacklog(t, 200001, 7)
	ctx := context.Background()
	trace := &capTrace{matchSQL: "DELETE FROM profile_chunks"}
	worker := retention.NewWorker(capPool(t, trace), 0).WithProfileLimits(7, 0)
	worker.RunOnce(ctx)
	require.Len(t, trace.batches, 40)
	for _, n := range trace.batches {
		require.EqualValues(t, 5000, n)
	}
	require.Equal(t, 8, countProfiles(t))
	var fresh int
	require.NoError(t, testPool.QueryRow(ctx, "SELECT count(*) FROM profile_chunks WHERE received_at > NOW()-interval '1 day'").Scan(&fresh))
	require.Equal(t, 7, fresh)
	worker.RunOnce(ctx)
	require.Equal(t, 7, countProfiles(t))
	require.Equal(t, int64(1), trace.batches[40])
}
func TestWorkerProfileAgeCancellationResumes(t *testing.T) {
	seedProfileAgeBacklog(t, 12000, 7)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	trace := &capTrace{matchSQL: "DELETE FROM profile_chunks", cancel: cancel}
	retention.NewWorker(capPool(t, trace), 0).WithProfileLimits(7, 0).RunOnce(ctx)
	require.Equal(t, []int64{5000}, trace.batches)
	require.Equal(t, 7007, countProfiles(t))
	retention.NewWorker(testPool, 0).WithProfileLimits(7, 0).RunOnce(context.Background())
	require.Equal(t, 7, countProfiles(t))
}
func TestWorkerProfileAgeAlreadyCancelled(t *testing.T) {
	seedProfileAgeBacklog(t, 3, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	trace := &capTrace{matchSQL: "DELETE FROM profile_chunks"}
	retention.NewWorker(capPool(t, trace), 0).WithProfileLimits(7, 0).RunOnce(ctx)
	require.Empty(t, trace.batches)
	require.Equal(t, 4, countProfiles(t))
}
