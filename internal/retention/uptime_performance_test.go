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
	"github.com/blendbyte/tindra/migrations"
)

// Opt in: TINDRA_UPTIME_RETENTION_PERF=1 go test -v ./internal/retention
// -run '^TestUptimeRetentionPerformance$' -count=1 -timeout=15m
func TestUptimeRetentionPerformance(t *testing.T) {
	if os.Getenv("TINDRA_UPTIME_RETENTION_PERF") != "1" {
		t.Skip("set TINDRA_UPTIME_RETENTION_PERF=1")
	}
	expired := 10000
	if value := os.Getenv("TINDRA_UPTIME_EXPIRED"); value != "" {
		var err error
		expired, err = strconv.Atoi(value)
		require.NoError(t, err)
		require.Greater(t, expired, 0)
		require.LessOrEqual(t, expired, 100000)
	}
	var expected []int64
	for remaining := expired; remaining >= 5000; remaining -= 5000 {
		expected = append(expected, 5000)
	}
	expected = append(expected, int64(expired%5000))
	ctx := context.Background()
	exec := func(q string) { _, err := testPool.Exec(ctx, q); require.NoError(t, err) }
	sql, err := migrations.FS.ReadFile("0019_uptime_performance.up.sql")
	require.NoError(t, err)
	exec("DROP INDEX uptime_checks_checked_id")
	t.Cleanup(func() {
		_, e := testPool.Exec(ctx, "CREATE INDEX IF NOT EXISTS uptime_checks_checked_id ON uptime_checks(checked_at,id)")
		require.NoError(t, e)
	})
	for _, batched := range []bool{false, true} {
		if batched {
			exec(string(sql))
		}
		var totals, longest []float64
		for run := 0; run < 4; run++ {
			seedUptimeBacklog(t, expired, 100000-expired)
			exec("VACUUM ANALYZE uptime_checks")
			var total, maxDelete time.Duration
			if batched {
				tracer := &capTrace{matchSQL: "DELETE FROM uptime_checks"}
				pool := capPool(t, tracer)
				retention.NewWorker(pool, 90).RunOnce(ctx)
				pool.Close()
				require.Equal(t, expected, tracer.batches)
				for _, d := range tracer.durations {
					total += d
					maxDelete = max(maxDelete, d)
				}
			} else {
				start := time.Now()
				tag, e := testPool.Exec(ctx, "DELETE FROM uptime_checks WHERE checked_at < $1", time.Now().AddDate(0, 0, -90))
				total = time.Since(start)
				maxDelete = total
				require.NoError(t, e)
				require.EqualValues(t, expired, tag.RowsAffected())
			}
			require.Equal(t, 100000-expired, capCount(t, "uptime_checks"))
			require.Equal(t, 2, capCount(t, "uptime_monitors"))
			var remainingExpired int
			require.NoError(t, testPool.QueryRow(ctx, "SELECT count(*) FROM uptime_checks WHERE checked_at < NOW()-interval '90 days'").Scan(&remainingExpired))
			require.Zero(t, remainingExpired)
			if run > 0 {
				totals = append(totals, float64(total.Microseconds())/1000)
				longest = append(longest, float64(maxDelete.Microseconds())/1000)
			}
		}
		sort.Float64s(totals)
		sort.Float64s(longest)
		t.Logf("batched=%v total %.3f ms longest DELETE %.3f ms", batched, totals[1], longest[1])
		var plan json.RawMessage
		query := "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) SELECT id FROM uptime_checks WHERE checked_at < $1"
		if batched {
			query += " ORDER BY checked_at,id LIMIT 5000"
		}
		require.NoError(t, testPool.QueryRow(ctx, query, time.Now().AddDate(0, 0, -90)).Scan(&plan))
		t.Logf("empty candidate plan batched=%v: %s", batched, plan)
	}
	var size int64
	require.NoError(t, testPool.QueryRow(ctx, "SELECT pg_relation_size('uptime_checks_checked_id')").Scan(&size))
	t.Logf("index bytes: %d", size)
}
