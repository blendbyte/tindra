package retention_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Opt in: TINDRA_PERF_ROWS=100000 go test -v ./internal/retention
// -run '^TestRowCapPerformance$' -count=1 -timeout=15m
func TestRowCapPerformance(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("TINDRA_PERF_ROWS"))
	if err != nil || n < 10000 || n > 200100 {
		t.Skip("set TINDRA_PERF_ROWS between 10000 and 200100")
	}
	ctx := context.Background()
	for _, table := range []string{"logs", "transactions"} {
		column := "timestamp"
		if table == "transactions" {
			column = "start_timestamp"
		}
		for _, batched := range []bool{false, true} {
			var totals, maxima []float64
			for run := 0; run < 4; run++ {
				seedCapRows(t, table, n)
				// Include one child span per transaction to exercise cascade work.
				if table == "transactions" {
					_, err := testPool.Exec(ctx, `INSERT INTO spans(transaction_id,project_id,span_id,start_timestamp,timestamp,duration_ms) SELECT id,project_id,'span',NOW(),NOW(),1 FROM transactions`)
					require.NoError(t, err)
					_, err = testPool.Exec(ctx, "VACUUM ANALYZE spans")
					require.NoError(t, err)
				}
				_, err := testPool.Exec(ctx, "VACUUM ANALYZE "+table)
				require.NoError(t, err)
				var total, longest time.Duration
				if batched {
					trace := &capTrace{}
					pool := capPool(t, trace)
					runCap(ctx, pool, table, 100)
					total = trace.boundaryDuration
					var deleted int64
					for i, d := range trace.durations {
						total += d
						longest = max(longest, d)
						require.LessOrEqual(t, trace.batches[i], int64(5000))
						deleted += trace.batches[i]
					}
					require.EqualValues(t, n-100, deleted)
					pool.Close()
				} else {
					start := time.Now()
					tag, err := testPool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id IN (SELECT id FROM %s ORDER BY %s ASC LIMIT GREATEST(0,(SELECT COUNT(*) FROM %s)-$1))`, table, table, column, table), 100)
					total = time.Since(start)
					longest = total
					require.NoError(t, err)
					require.EqualValues(t, n-100, tag.RowsAffected())
				}
				require.Equal(t, 100, capCount(t, table))
				if table == "transactions" {
					require.Equal(t, 100, capCount(t, "spans"))
				}
				if run > 0 {
					totals = append(totals, float64(total.Microseconds())/1000)
					maxima = append(maxima, float64(longest.Microseconds())/1000)
				}
			}
			sort.Float64s(totals)
			sort.Float64s(maxima)
			t.Logf("%s batched=%v median total cap SQL %.3f ms; median longest DELETE %.3f ms", table, batched, totals[1], maxima[1])
		}
	}
}
