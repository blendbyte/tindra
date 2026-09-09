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
// -run '^TestGlobalListQueryPerformance$' -count=1 -timeout=20m
// Measures identical first/deep pages before and after the global indexes on
// synthetic data in the disposable test database. Never use an application DB.
func TestGlobalListQueryPerformance(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("TINDRA_PERF_ROWS"))
	if err != nil || n < 10000 {
		t.Skip("set TINDRA_PERF_ROWS >= 10000")
	}
	ctx := context.Background()
	exec := func(q string, args ...any) { _, err := testPool.Exec(ctx, q, args...); require.NoError(t, err) }
	truncateProjects(t)
	exec(`INSERT INTO projects(id,slug,name,public_key)
 SELECT md5('global-project-'||i)::uuid,'global-'||i,'Global '||i,md5('global-key-'||i) FROM generate_series(0,19) i`)
	// Seed without the new indexes, then build them on populated tables. Timestamp
	// ties exercise the UUID tiebreaker; some logs also resolve a transaction.
	exec("DROP INDEX transactions_start_id")
	exec("DROP INDEX logs_timestamp_id")
	now := time.Now().UTC().Truncate(time.Second)
	exec(`INSERT INTO transactions(id,project_id,transaction,trace_id,duration_ms,start_timestamp,timestamp)
 SELECT md5('global-tx-'||i)::uuid,md5('global-project-'||(i%20))::uuid,'/page/'||(i%100),md5('trace-'||i),i%5000,
 $2::timestamptz-(i/10)*interval '1 second',$2::timestamptz-(i/10)*interval '1 second' FROM generate_series(1,$1::int) i`, n, now)
	exec(`INSERT INTO logs(id,project_id,timestamp,body,trace_id)
 SELECT md5('global-log-'||i)::uuid,md5('global-project-'||(i%20))::uuid,
 $2::timestamptz-(i/10)*interval '1 second','Benchmark log',CASE WHEN i%10=0 THEN md5('trace-'||i) END FROM generate_series(1,$1::int) i`, n, now)
	exec("VACUUM ANALYZE transactions")
	exec("VACUUM ANALYZE logs")

	capture := &queryCapture{}
	cfg := testPool.Config()
	cfg.ConnConfig.Tracer = capture
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()
	deepTime := now.Add(-time.Duration(n/20) * time.Second)
	cursorID := "80000000-0000-0000-0000-000000000000"
	var projectID string
	require.NoError(t, testPool.QueryRow(ctx, `SELECT md5('global-project-3')::uuid::text`).Scan(&projectID))
	type scenario struct {
		name string
		run  func() []string
	}
	var scenarios []scenario
	for _, scope := range []struct {
		name     string
		projects []string
		cursor   *time.Time
	}{
		{"global/first", nil, nil}, {"global/deep", nil, &deepTime}, {"project/first", []string{projectID}, nil},
	} {
		scenarios = append(scenarios, scenario{"transactions/" + scope.name, func() []string {
			rows, e := storage.ListAllTransactions(ctx, pool, storage.TransactionFilter{ProjectIDs: scope.projects, CursorTime: scope.cursor, CursorID: &cursorID})
			require.NoError(t, e)
			require.Len(t, rows, 50)
			ids := make([]string, 0, len(rows))
			for _, r := range rows {
				ids = append(ids, r.ID)
			}
			return ids
		}}, scenario{"logs/" + scope.name, func() []string {
			rows, more, e := storage.ListLogs(ctx, pool, storage.LogFilter{ProjectIDs: scope.projects, CursorTime: scope.cursor, CursorID: &cursorID})
			require.NoError(t, e)
			require.Len(t, rows, 100)
			require.True(t, more)
			ids := make([]string, 0, len(rows))
			for _, r := range rows {
				id := r.ID
				if r.TransactionID != nil {
					id += "/" + *r.TransactionID
				}
				ids = append(ids, id)
			}
			return ids
		}})
	}
	type result struct {
		Phase, Mode, Name string
		MedianMS          float64
		Plan              json.RawMessage
	}
	var results []result
	baseline := map[string][]string{}
	for _, phase := range []string{"before", "after"} {
		if phase == "after" {
			for _, sql := range []string{
				"CREATE INDEX CONCURRENTLY transactions_start_id ON transactions(start_timestamp DESC,id DESC)",
				"CREATE INDEX CONCURRENTLY logs_timestamp_id ON logs(timestamp DESC,id DESC)",
			} {
				start := time.Now()
				exec(sql)
				t.Logf("build %s: %s", sql, time.Since(start))
			}
		}
		for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
			_, err := pool.Exec(ctx, "SET plan_cache_mode = "+mode)
			require.NoError(t, err)
			for _, s := range scenarios {
				var timings []float64
				for i := 0; i < 6; i++ {
					start := time.Now()
					ids := s.run()
					elapsed := float64(time.Since(start).Microseconds()) / 1000
					if phase == "before" && i == 0 {
						baseline[s.name] = ids
					} else {
						require.Equal(t, baseline[s.name], ids, s.name)
					}
					if i > 0 {
						timings = append(timings, elapsed)
					}
				}
				sql, args := capture.sql, append([]any(nil), capture.args...)
				var plan json.RawMessage
				require.NoError(t, testPool.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+sql, args...).Scan(&plan))
				sort.Float64s(timings)
				results = append(results, result{phase, mode, s.name, timings[len(timings)/2], plan})
				t.Logf("%s %-18s %-28s %.3f ms", phase, mode, s.name, timings[len(timings)/2])
			}
		}
	}
	for _, name := range []string{"transactions_start_id", "logs_timestamp_id"} {
		var size int64
		var valid bool
		require.NoError(t, testPool.QueryRow(ctx, `SELECT pg_relation_size(indexrelid),indisvalid FROM pg_index WHERE indexrelid=$1::regclass`, name).Scan(&size, &valid))
		require.True(t, valid)
		t.Logf("%s: %d bytes", name, size)
	}
	if path := os.Getenv("TINDRA_PERF_OUTPUT"); path != "" {
		data, e := json.MarshalIndent(results, "", "  ")
		require.NoError(t, e)
		require.NoError(t, os.WriteFile(path, data, 0600))
	}
	t.Logf("rows per table: %d", n)
}
