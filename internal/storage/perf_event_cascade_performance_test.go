package storage_test

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// Opt in with TINDRA_PERF_ROWS=1000000. All deletes are rolled back in the
// disposable test database, so both index states see the same logical rows.
func TestPerfEventCascadePerformance(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("TINDRA_PERF_ROWS"))
	if err != nil || n < 10000 || n%10000 != 0 {
		t.Skip("set TINDRA_PERF_ROWS to a positive multiple of 10000")
	}
	ctx := context.Background()
	p, issue, _ := setupProjectAndIssueAndTx(t)
	exec := func(q string, args ...any) { _, e := testPool.Exec(ctx, q, args...); require.NoError(t, e) }
	exec(`INSERT INTO transactions(id,project_id,transaction,duration_ms,start_timestamp,timestamp)
 SELECT lpad(to_hex(i),32,'0')::uuid,$1,'cascade-test',10,NOW(),NOW() FROM generate_series(1,10100) i`, p.ID)
	exec(`INSERT INTO perf_events(issue_id,transaction_id,span_count,total_ms)
 SELECT $1,lpad(to_hex(1+(i-1)%10000),32,'0')::uuid,1,10 FROM generate_series(1,$2::int) i`, issue.ID, n)
	exec("DROP INDEX perf_events_transaction")
	migration := "CREATE INDEX CONCURRENTLY perf_events_transaction ON perf_events(transaction_id)"
	// Restore the schema even if a measurement fails.
	t.Cleanup(func() {
		_, e := testPool.Exec(ctx, "CREATE INDEX IF NOT EXISTS perf_events_transaction ON perf_events(transaction_id)")
		require.NoError(t, e)
	})
	type result struct {
		Indexed  bool
		Case     string
		MedianMS float64
		Plan     json.RawMessage
	}
	var results []result
	for _, indexed := range []bool{false, true} {
		if indexed {
			exec(migration)
		}
		exec("VACUUM ANALYZE perf_events")
		exec("VACUUM ANALYZE transactions")
		for _, withChildren := range []bool{true, false} {
			low, high, removed, name := 1, 100, n/100, "with_children"
			if !withChildren {
				low, high, removed, name = 10001, 10100, 0, "without_children"
			}
			var timings []float64
			var plan json.RawMessage
			for i := 0; i < 4; i++ {
				func() {
					tx, e := testPool.Begin(ctx)
					require.NoError(t, e)
					defer tx.Rollback(ctx)
					require.NoError(t, tx.QueryRow(ctx, `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
 DELETE FROM transactions WHERE id >= lpad(to_hex($1::int),32,'0')::uuid AND id <= lpad(to_hex($2::int),32,'0')::uuid`, low, high).Scan(&plan))
					var parsed []struct {
						ExecutionTime float64 `json:"Execution Time"`
					}
					require.NoError(t, json.Unmarshal(plan, &parsed))
					if i > 0 {
						timings = append(timings, parsed[0].ExecutionTime)
					}
					var parents, children, issues int
					require.NoError(t, tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM transactions),(SELECT count(*) FROM perf_events),(SELECT count(*) FROM issues)`).Scan(&parents, &children, &issues))
					require.Equal(t, 10001, parents)
					require.Equal(t, n-removed, children)
					require.Equal(t, 1, issues)
					require.NoError(t, tx.Rollback(ctx))
				}()
			}
			sort.Float64s(timings)
			results = append(results, result{indexed, name, timings[1], append(json.RawMessage(nil), plan...)})
			t.Logf("indexed=%v %s median %.3f ms", indexed, name, timings[1])
		}
	}
	var size int64
	require.NoError(t, testPool.QueryRow(ctx, `SELECT pg_relation_size('perf_events_transaction')`).Scan(&size))
	t.Logf("index bytes: %d", size)
	var parents, children int
	require.NoError(t, testPool.QueryRow(ctx, `SELECT (SELECT count(*) FROM transactions),(SELECT count(*) FROM perf_events)`).Scan(&parents, &children))
	require.Equal(t, 10101, parents)
	require.Equal(t, n, children)
	if path := os.Getenv("TINDRA_PERF_OUTPUT"); path != "" {
		b, e := json.MarshalIndent(results, "", "  ")
		require.NoError(t, e)
		require.NoError(t, os.WriteFile(path, b, 0600))
	}
}
