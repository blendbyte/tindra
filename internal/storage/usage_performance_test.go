package storage_test

import (
	"context"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestUsagePerformance(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("TINDRA_PERF_ROWS"))
	if n < 10000 {
		t.Skip("set TINDRA_PERF_ROWS >= 10000")
	}
	truncateProjects(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "usage-perf", "Usage")
	require.NoError(t, err)
	for _, table := range []string{"events", "transactions", "logs"} {
		sql := map[string]string{
			"events":       `INSERT INTO events(project_id,timestamp,received_at,payload) SELECT $1,now(),now()-(i%64800)*interval '1 minute','{}' FROM generate_series(1,$2) i`,
			"transactions": `INSERT INTO transactions(project_id,start_timestamp,timestamp,received_at,transaction,duration_ms) SELECT $1,now(),now(),now()-(i%64800)*interval '1 minute','test',1 FROM generate_series(1,$2) i`,
			"logs":         `INSERT INTO logs(project_id,timestamp,received_at,body) SELECT $1,now(),now()-(i%64800)*interval '1 minute','test' FROM generate_series(1,$2) i`,
		}[table]
		_, err = testPool.Exec(ctx, sql, p.ID, n)
		require.NoError(t, err)
		_, err = testPool.Exec(ctx, "VACUUM ANALYZE "+table)
		require.NoError(t, err)
	}
	_, err = testPool.Exec(ctx, "ANALYZE telemetry_usage")
	require.NoError(t, err)
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		cfg := testPool.Config().Copy()
		cfg.ConnConfig.RuntimeParams["plan_cache_mode"] = mode
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		require.NoError(t, err)
		for _, cached := range []bool{false, true} {
			elapsed := usageMedian(t, func() {
				var got int64
				if cached {
					got, err = storage.CountMonthlyEvents(ctx, pool)
				} else {
					err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM events WHERE received_at>=date_trunc('month',now()))+(SELECT count(*) FROM transactions WHERE received_at>=date_trunc('month',now()))`).Scan(&got)
				}
				require.NoError(t, err)
				require.Positive(t, got)
			})
			t.Logf("%s monthly summary=%v median=%s", mode, cached, elapsed)
		}
		elapsed := usageMedian(t, func() { _, err = storage.ListProjects(ctx, pool); require.NoError(t, err) })
		t.Logf("%s project usage summary median=%s", mode, elapsed)
		elapsed = usageMedian(t, func() { _, err = storage.GetInstanceHealth(ctx, pool); require.NoError(t, err) })
		t.Logf("%s health summary median=%s", mode, elapsed)
		pool.Close()
	}
	// Measure the current single-row pgx batch shape before considering changes.
	// Disable triggers only inside rolled-back transactions in this disposable DB.
	for _, enabled := range []bool{false, true} {
		elapsed := usageMedian(t, func() {
			tx, err := testPool.Begin(ctx)
			require.NoError(t, err)
			defer tx.Rollback(ctx)
			if !enabled {
				_, err = tx.Exec(ctx, "ALTER TABLE events DISABLE TRIGGER usage_insert")
				require.NoError(t, err)
			}
			b := &pgx.Batch{}
			for range 1000 {
				b.Queue(`INSERT INTO events(project_id,timestamp,payload) VALUES($1,now(),'{}')`, p.ID)
			}
			result := tx.SendBatch(ctx, b)
			for range 1000 {
				_, err = result.Exec()
				require.NoError(t, err)
			}
			require.NoError(t, result.Close())
		})
		t.Logf("1000 single-row event inserts usage trigger=%v median=%s", enabled, elapsed)
	}
	var buckets int
	require.NoError(t, testPool.QueryRow(ctx, "SELECT count(*) FROM telemetry_usage").Scan(&buckets))
	t.Logf("%d telemetry rows / %d buckets", 3*n, buckets)
}

func usageMedian(t *testing.T, run func()) time.Duration {
	t.Helper()
	var samples []time.Duration
	for i := range 4 {
		start := time.Now()
		run()
		if i > 0 {
			samples = append(samples, time.Since(start))
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return samples[1]
}
