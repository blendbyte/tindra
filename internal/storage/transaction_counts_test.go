package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestTransactionCountsMatchTimeseries(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "counts", "Counts")
	require.NoError(t, err)
	other, err := storage.CreateProject(ctx, testPool, "counts-other", "Other")
	require.NoError(t, err)
	for _, project := range []string{p.ID, other.ID} {
		for _, env := range []string{"prod", "dev"} {
			for _, name := range []string{"/one", "/two"} {
				tx := seedTransaction(t, project, name, 123, time.Now().Add(-10*time.Minute))
				_, err := testPool.Exec(ctx, "UPDATE transactions SET environment=$2,op='http.server',user_identity='alice',received_at=start_timestamp WHERE id=$1", tx.ID, env)
				require.NoError(t, err)
			}
		}
	}
	for _, hours := range []int{0, 1, 24, 168, 720, 721} {
		for _, filtered := range []bool{false, true} {
			var ids []string
			env, name, op, user := "", "", "", ""
			want := int64(8)
			if filtered {
				ids = []string{p.ID}
				env, name, op, user = "prod", "/one", "http.server", "alice"
				want = 1
			}
			full, err := storage.GetTransactionTimeseries(ctx, testPool, ids, hours, env, name, op, user)
			require.NoError(t, err)
			counts, err := storage.GetTransactionCounts(ctx, testPool, ids, hours, env, name, op, user)
			require.NoError(t, err)
			require.Equal(t, full.BucketSize, counts.BucketSize)
			require.Len(t, counts.Buckets, len(full.Buckets))
			var total int64
			for i, b := range counts.Buckets {
				require.Equal(t, full.Buckets[i].Time, b.Time)
				require.Equal(t, full.Buckets[i].Count, b.Count)
				total += b.Count
			}
			require.Equal(t, want, total)
		}
	}
	empty, err := storage.GetTransactionCounts(ctx, testPool, nil, 168, "", "", "", "absent")
	require.NoError(t, err)
	require.NotNil(t, empty.Buckets)
	require.Empty(t, empty.Buckets)
}

func seedCountedTransaction(t *testing.T, projectID, env string, start, received time.Time) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), `INSERT INTO transactions(project_id,transaction,duration_ms,start_timestamp,timestamp,received_at,environment)
 VALUES ($1,'/counted',5,$2,$2,$3,NULLIF($4,''))`, projectID, start, received, env)
	require.NoError(t, err)
}

func requireSameCounts(t *testing.T, want *storage.TxTimeseries, got *storage.TxCountTimeseries) {
	t.Helper()
	require.Equal(t, want.BucketSize, got.BucketSize)
	require.Len(t, got.Buckets, len(want.Buckets))
	for i, b := range got.Buckets {
		require.True(t, want.Buckets[i].Time.Equal(b.Time), "bucket %d: %s != %s", i, want.Buckets[i].Time, b.Time)
		require.Equal(t, want.Buckets[i].Count, b.Count)
	}
}

func TestTransactionCountsUsageMatchesRawBuckets(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "usage-counts", "Usage counts")
	require.NoError(t, err)
	other, err := storage.CreateProject(ctx, testPool, "usage-counts-other", "Other")
	require.NoError(t, err)
	now := time.Now().UTC()
	// Received in the same hour each started, so both bucketings agree. Ages
	// cover the partial first hour, complete hours and the partial current hour.
	for i, age := range []time.Duration{167*time.Hour + 50*time.Minute, 167*time.Hour + 50*time.Minute, 100 * time.Hour, 30 * time.Hour, 3 * time.Hour, 3 * time.Hour, time.Minute} {
		start := now.Add(-age)
		seedCountedTransaction(t, p.ID, "prod", start, start)
		if i%2 == 0 {
			seedCountedTransaction(t, other.ID, "", start, start)
		}
	}
	// Older than the window: neither path may count it.
	seedCountedTransaction(t, p.ID, "prod", now.Add(-170*time.Hour), now.Add(-170*time.Hour))
	for _, ids := range [][]string{nil, {p.ID}, {other.ID}, {p.ID, other.ID}} {
		raw, err := storage.GetTransactionTimeseries(ctx, testPool, ids, 168, "", "", "", "")
		require.NoError(t, err)
		counts, err := storage.GetTransactionCounts(ctx, testPool, ids, 168, "", "", "", "")
		require.NoError(t, err)
		requireSameCounts(t, raw, counts)
		require.NotEmpty(t, counts.Buckets)
	}
	unknown, err := storage.GetTransactionCounts(ctx, testPool, []string{"00000000-0000-0000-0000-000000000001"}, 168, "", "", "", "")
	require.NoError(t, err)
	require.NotNil(t, unknown.Buckets)
	require.Empty(t, unknown.Buckets)
}

func TestTransactionCountsUsageExactWindowEdges(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "usage-edges", "Usage edges")
	require.NoError(t, err)
	hour := time.Now().UTC().Truncate(time.Hour).Add(-48 * time.Hour)
	for _, offset := range []time.Duration{10 * time.Minute, 40 * time.Minute, 2*time.Hour + 5*time.Minute, 3 * time.Hour, 5*time.Hour + 10*time.Minute, 5*time.Hour + 20*time.Minute} {
		seedCountedTransaction(t, p.ID, "prod", hour.Add(offset), hour.Add(offset))
	}
	for _, window := range []storage.TimeRange{
		{From: hour.Add(30 * time.Minute), To: hour.Add(5*time.Hour + 15*time.Minute)},
		{From: hour, To: hour.Add(5 * time.Hour)},
		{From: hour.Add(5 * time.Minute), To: hour.Add(50 * time.Minute)},
		{From: hour.Add(3 * time.Hour), To: hour.Add(3*time.Hour + time.Second)},
	} {
		scoped := storage.WithTimeRange(ctx, window)
		raw, err := storage.GetTransactionTimeseries(scoped, testPool, nil, 168, "", "", "", "")
		require.NoError(t, err)
		counts, err := storage.GetTransactionCounts(scoped, testPool, nil, 168, "", "", "", "")
		require.NoError(t, err)
		requireSameCounts(t, raw, counts)
		require.NotEmpty(t, counts.Buckets)
	}
}

func TestTransactionCountsBucketByReceivedUnlessFiltered(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "usage-received", "Usage received")
	require.NoError(t, err)
	started := time.Now().UTC().Truncate(time.Hour).Add(-5*time.Hour + 50*time.Minute)
	received := started.Add(20 * time.Minute)
	seedCountedTransaction(t, p.ID, "prod", started, received)
	_, err = testPool.Exec(ctx, "UPDATE transactions SET op='http.server',user_identity='alice',release='1.0' WHERE project_id=$1", p.ID)
	require.NoError(t, err)

	counts, err := storage.GetTransactionCounts(ctx, testPool, []string{p.ID}, 168, "", "", "", "")
	require.NoError(t, err)
	require.Len(t, counts.Buckets, 1)
	require.True(t, received.Truncate(time.Hour).Equal(counts.Buckets[0].Time))

	type filter struct{ env, name, op, user, release string }
	for _, f := range []filter{{env: "prod"}, {name: "/counted"}, {op: "http.server"}, {user: "alice"}, {release: "1.0"}} {
		counts, err := storage.GetTransactionCounts(ctx, testPool, []string{p.ID}, 168, f.env, f.name, f.op, f.user, f.release)
		require.NoError(t, err)
		require.Len(t, counts.Buckets, 1, "%+v", f)
		require.True(t, started.Truncate(time.Hour).Equal(counts.Buckets[0].Time), "%+v", f)
	}
	// Minute and day buckets cannot come from hourly counters.
	for _, hours := range []int{1, 720} {
		raw, err := storage.GetTransactionTimeseries(ctx, testPool, []string{p.ID}, hours, "", "", "", "")
		require.NoError(t, err)
		counts, err := storage.GetTransactionCounts(ctx, testPool, []string{p.ID}, hours, "", "", "", "")
		require.NoError(t, err)
		requireSameCounts(t, raw, counts)
	}
}
