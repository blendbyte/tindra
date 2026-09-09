package retention_test

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/retention"
)

// Opt in: TINDRA_PERF_ROWS=100000 go test -v ./internal/retention
// -run '^TestAgePerformance$' -count=1 -timeout=15m
func TestAgePerformance(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("TINDRA_PERF_ROWS"))
	if err != nil || n < 100000 || n > 1000000 {
		t.Skip("set TINDRA_PERF_ROWS between 100000 and 1000000")
	}
	ctx := context.Background()
	for _, table := range []string{"logs", "transactions"} {
		t.Run(table, func(t *testing.T) {
			exec := func(q string) { _, e := testPool.Exec(ctx, q); require.NoError(t, e) }
			index := table + "_received_id"
			sql := "CREATE INDEX CONCURRENTLY " + index + " ON " + table + "(received_at,id)"
			exec("DROP INDEX " + index)
			t.Cleanup(func() {
				_, e := testPool.Exec(ctx, "CREATE INDEX IF NOT EXISTS "+index+" ON "+table+"(received_at,id)")
				require.NoError(t, e)
			})
			for _, batched := range []bool{false, true} {
				if batched {
					exec(sql)
				}
				var totals, maxima []float64
				for run := 0; run < 4; run++ {
					seedAgeRows(t, table, 10000, n-10000)
					exec("VACUUM ANALYZE " + table)
					if table == "transactions" {
						exec("VACUUM ANALYZE spans")
						exec("VACUUM ANALYZE perf_events")
					}
					var total, longest time.Duration
					if batched {
						if run == 3 {
							var plan json.RawMessage
							require.NoError(t, testPool.QueryRow(ctx, "EXPLAIN (FORMAT JSON) DELETE FROM "+table+" WHERE id = ANY(ARRAY(SELECT id FROM "+table+" WHERE received_at < $1 ORDER BY received_at,id LIMIT $2))", time.Now().AddDate(0, 0, -90), 5000).Scan(&plan))
							t.Logf("batch plan: %s", plan)
						}
						trace := &capTrace{matchSQL: "DELETE FROM " + table}
						pool := capPool(t, trace)
						retention.NewWorker(pool, 90).RunOnce(ctx)
						for _, d := range trace.durations {
							total += d
							longest = max(longest, d)
						}
						require.Equal(t, []int64{5000, 5000, 0}, trace.batches)
						pool.Close()
					} else {
						start := time.Now()
						tag, e := testPool.Exec(ctx, "DELETE FROM "+table+" WHERE received_at < $1", time.Now().AddDate(0, 0, -90))
						total = time.Since(start)
						longest = total
						require.NoError(t, e)
						require.EqualValues(t, 10000, tag.RowsAffected())
					}
					require.Equal(t, n-10000, capCount(t, table))
					if table == "transactions" {
						require.Equal(t, n-10000, capCount(t, "spans"))
						require.Equal(t, n-10000, capCount(t, "perf_events"))
					}
					if run > 0 {
						totals = append(totals, float64(total.Microseconds())/1000)
						maxima = append(maxima, float64(longest.Microseconds())/1000)
					}
				}
				sort.Float64s(totals)
				sort.Float64s(maxima)
				t.Logf("%s batched=%v total %.3f ms longest DELETE %.3f ms", table, batched, totals[1], maxima[1])
				// After cleanup, check the cost of finding that no expired rows remain.
				var plan json.RawMessage
				q := "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) SELECT id FROM " + table + " WHERE received_at < $1"
				if batched {
					q += " ORDER BY received_at,id LIMIT 5000"
				}
				require.NoError(t, testPool.QueryRow(ctx, q, time.Now().AddDate(0, 0, -90)).Scan(&plan))
				t.Logf("empty candidate plan batched=%v: %s", batched, plan)
			}
			var size int64
			require.NoError(t, testPool.QueryRow(ctx, "SELECT pg_relation_size($1::regclass)", index).Scan(&size))
			t.Logf("%s index bytes after cleanup: %d", table, size)
		})
	}
}
