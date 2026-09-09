package storage_test

import (
	"context"
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
// -run '^TestReleaseHealthPerformance$' -count=1 -timeout=15m
func TestReleaseHealthPerformance(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("TINDRA_PERF_ROWS"))
	if err != nil || n < 10000 {
		t.Skip("set TINDRA_PERF_ROWS >= 10000")
	}
	truncateProjects(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "release-health-perf", "Health performance")
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `INSERT INTO releases(project_id,version,deployed_at) SELECT $1,'v'||i,NOW()-i*interval '1 hour' FROM generate_series(0,49) i`, p.ID)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `INSERT INTO transactions(project_id,release,transaction,duration_ms,start_timestamp,timestamp)
 SELECT $1,'v'||(i%50),'/health',i%5000,NOW(),NOW() FROM generate_series(1,$2::int) i`, p.ID, n)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, "VACUUM ANALYZE transactions")
	require.NoError(t, err)
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		cfg := testPool.Config()
		cfg.ConnConfig.RuntimeParams["plan_cache_mode"] = mode
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		require.NoError(t, err)
		filter := storage.ReleaseFilter{Limit: 50}
		full, err := storage.ListReleases(ctx, pool, filter)
		require.NoError(t, err)
		require.Len(t, full, 50)
		health, err := storage.ListRecentReleaseHealth(ctx, pool, nil)
		require.NoError(t, err)
		require.Len(t, health, 5)
		for i, r := range health {
			require.Equal(t, full[i].ID, r.ID)
			require.Equal(t, full[i].NewIssues, r.NewIssues)
			require.Equal(t, full[i].RegressedIssues, r.RegressedIssues)
		}
		for _, light := range []bool{false, true} {
			var times []float64
			for i := 0; i < 4; i++ {
				start := time.Now()
				if light {
					_, err = storage.ListRecentReleaseHealth(ctx, pool, nil)
				} else {
					_, err = storage.CountReleases(ctx, pool, filter)
					require.NoError(t, err)
					_, err = storage.ListReleases(ctx, pool, filter)
				}
				require.NoError(t, err)
				if i > 0 {
					times = append(times, float64(time.Since(start).Microseconds())/1000)
				}
			}
			sort.Float64s(times)
			t.Logf("%s health_only=%v median %.3f ms", mode, light, times[1])
		}
		pool.Close()
	}
}
