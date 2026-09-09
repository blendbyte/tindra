package storage_test

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

// Opt in: TINDRA_PERF_ROWS=1000000 go test -v ./internal/storage
// -run '^TestTransactionCountsPerformance$' -count=1 -timeout=15m
func TestTransactionCountsPerformance(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("TINDRA_PERF_ROWS"))
	if err != nil || n < 10000 {
		t.Skip("set TINDRA_PERF_ROWS >= 10000")
	}
	truncateProjects(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "count-perf", "Count performance")
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `INSERT INTO transactions(project_id,transaction,duration_ms,start_timestamp,timestamp)
 SELECT $1,'/heatmap',(i%10000)::int,NOW()-(i%10000)*interval '1 minute',NOW() FROM generate_series(1,$2::int) i`, p.ID, n)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, "VACUUM ANALYZE transactions")
	require.NoError(t, err)
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		cfg := testPool.Config()
		cfg.ConnConfig.RuntimeParams["plan_cache_mode"] = mode
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		require.NoError(t, err)
		full, err := storage.GetTransactionTimeseries(ctx, pool, nil, 168, "", "", "", "")
		require.NoError(t, err)
		counts, err := storage.GetTransactionCounts(ctx, pool, nil, 168, "", "", "", "")
		require.NoError(t, err)
		require.Len(t, counts.Buckets, len(full.Buckets))
		var total int64
		for i, b := range counts.Buckets {
			require.Equal(t, full.Buckets[i].Time, b.Time)
			require.Equal(t, full.Buckets[i].Count, b.Count)
			total += b.Count
		}
		require.EqualValues(t, n, total)
		fullJSON, err := json.Marshal(full)
		require.NoError(t, err)
		countJSON, err := json.Marshal(counts)
		require.NoError(t, err)
		t.Logf("JSON bytes: full=%d counts=%d", len(fullJSON), len(countJSON))
		for _, countOnly := range []bool{false, true} {
			var times []float64
			for i := 0; i < 4; i++ {
				start := time.Now()
				if countOnly {
					_, err = storage.GetTransactionCounts(ctx, pool, nil, 168, "", "", "", "")
				} else {
					_, err = storage.GetTransactionTimeseries(ctx, pool, nil, 168, "", "", "", "")
				}
				require.NoError(t, err)
				if i > 0 {
					times = append(times, float64(time.Since(start).Microseconds())/1000)
				}
			}
			sort.Float64s(times)
			t.Logf("%s counts_only=%v median %.3f ms", mode, countOnly, times[1])
		}
		pool.Close()
	}
}
