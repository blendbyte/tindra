package retention_test

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/retention"
)

// Opt in: TINDRA_ALERT_RETENTION_PERF=1 go test -v ./internal/retention
// -run '^TestAlertRetentionPerformance$' -count=1 -timeout=15m
func TestAlertRetentionPerformance(t *testing.T) {
	if os.Getenv("TINDRA_ALERT_RETENTION_PERF") != "1" {
		t.Skip("set TINDRA_ALERT_RETENTION_PERF=1")
	}
	ctx := context.Background()
	for _, batched := range []bool{false, true} {
		var totals, longest, rankings []float64
		for run := 0; run < 4; run++ {
			ids := seedAlertBacklog(t, 90000, 10000)
			_, err := testPool.Exec(ctx, "VACUUM ANALYZE alert_firings")
			require.NoError(t, err)
			var total, maxDelete, ranking time.Duration
			if batched {
				trace := &alertCleanupTrace{}
				pool := alertPool(t, trace)
				retention.NewWorker(pool, 90).RunOnce(ctx)
				pool.Close()
				require.Equal(t, 1, trace.rankings)
				require.Len(t, trace.batches, 20)
				total = trace.boundaryDuration
				ranking = trace.boundaryDuration
				var removed int64
				for i, d := range trace.durations {
					total += d
					maxDelete = max(maxDelete, d)
					removed += trace.batches[i]
					require.LessOrEqual(t, trace.batches[i], int64(5000))
				}
				require.EqualValues(t, 98000, removed)
			} else {
				start := time.Now()
				tag, e := testPool.Exec(ctx, `DELETE FROM alert_firings WHERE id IN (
 SELECT id FROM (SELECT id,ROW_NUMBER() OVER (PARTITION BY rule_id ORDER BY fired_at DESC) AS rn FROM alert_firings) ranked WHERE rn>1000)`)
				total = time.Since(start)
				maxDelete = total
				require.NoError(t, e)
				require.EqualValues(t, 98000, tag.RowsAffected())
			}
			for _, id := range ids {
				var count int
				require.NoError(t, testPool.QueryRow(ctx, "SELECT count(*) FROM alert_firings WHERE rule_id=$1", id).Scan(&count))
				require.Equal(t, 1000, count)
			}
			if run > 0 {
				totals = append(totals, float64(total.Microseconds())/1000)
				longest = append(longest, float64(maxDelete.Microseconds())/1000)
				rankings = append(rankings, float64(ranking.Microseconds())/1000)
			}
		}
		sort.Float64s(totals)
		sort.Float64s(longest)
		sort.Float64s(rankings)
		t.Logf("batched=%v total %.3f ms longest DELETE %.3f ms ranking %.3f ms", batched, totals[1], longest[1], rankings[1])
	}
}
