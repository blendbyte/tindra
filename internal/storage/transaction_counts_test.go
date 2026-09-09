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
				_, err := testPool.Exec(ctx, "UPDATE transactions SET environment=$2,op='http.server',user_identity='alice' WHERE id=$1", tx.ID, env)
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
