package storage_test

import (
	"context"
	"encoding/json"
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

// Opt in: TINDRA_PERF_ROWS=1000000 go test -v ./internal/storage
// -run '^TestAppUserQueryPerformance$' -count=1 -timeout=20m
// Uses the disposable test database, never the application's database.
type queryCapture struct {
	sql  string
	args []any
}

func (c *queryCapture) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	c.sql = d.SQL
	c.args = append([]any(nil), d.Args...)
	return ctx
}
func (*queryCapture) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestAppUserQueryPerformance(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("TINDRA_PERF_ROWS"))
	if err != nil || n < 10000 {
		t.Skip("set TINDRA_PERF_ROWS >= 10000 to run the large-data benchmark")
	}
	ctx := context.Background()
	exec := func(sql string, args ...any) { _, err := testPool.Exec(ctx, sql, args...); require.NoError(t, err) }
	start := time.Now()
	exec(`INSERT INTO projects(id,slug,name,public_key) SELECT md5('bench-project-'||i)::uuid,'bench-'||i,'Bench '||i,md5('bench-key-'||i) FROM generate_series(0,19) i`)
	exec(`INSERT INTO issues(id,project_id,fingerprint,title,first_seen,last_seen)
 SELECT md5('bench-issue-'||i)::uuid,md5('bench-project-'||(i%20))::uuid,'bench-'||i,'Issue '||i,NOW()-interval '30 days',NOW()-i*interval '1 second' FROM generate_series(1,$1::int/10) i`, n)
	exec(`INSERT INTO app_users(project_id,identity,user_id,username,email,name,last_seen)
 SELECT md5('bench-project-'||(i%20))::uuid,'user-'||i,'user-'||i,'person-'||i,'person-'||i||'@example.test','Person '||i,NOW()-i*interval '1 second' FROM generate_series(1,$1::int) i`, n/5)
	// 10% of telemetry belongs to a hot user; remaining identities are selective.
	exec(`INSERT INTO transactions(id,project_id,transaction,op,status,duration_ms,start_timestamp,timestamp,user_identity,measurements)
 SELECT md5('bench-tx-'||i)::uuid,md5('bench-project-'||(i%20))::uuid,'/page/'||(i%100),CASE WHEN i%3=0 THEN 'pageload' ELSE 'http.server' END,'ok',i%5000,NOW()-i*interval '1 second',NOW()-i*interval '1 second',CASE WHEN i%10=0 THEN 'hot-user' ELSE 'user-'||(i%($1::int/5)) END,'{"lcp":{"value":1200}}'::jsonb FROM generate_series(1,$1::int) i`, n)
	exec(`INSERT INTO logs(id,project_id,timestamp,level,body,attributes)
 SELECT md5('bench-log-'||i)::uuid,md5('bench-project-'||(i%20))::uuid,NOW()-i*interval '1 second','info','Benchmark log',jsonb_build_object('user.id',CASE WHEN i%10=0 THEN 'hot-user' ELSE 'user-'||(i%($1::int/5)) END) FROM generate_series(1,$1::int) i`, n)
	exec(`INSERT INTO events(project_id,issue_id,timestamp,payload)
 SELECT md5('bench-project-'||((i%($1::int/10)+1)%20))::uuid,md5('bench-issue-'||(i%($1::int/10)+1))::uuid,NOW()-i*interval '1 second',jsonb_build_object('user',jsonb_build_object('id',CASE WHEN i%10=0 THEN 'hot-user' ELSE 'user-'||(i%($1::int/5)) END)) FROM generate_series(1,$1::int) i`, n)
	for _, table := range []string{"app_users", "issues", "events", "transactions", "logs"} {
		exec("VACUUM ANALYZE " + table)
	}
	t.Logf("seeded %d events, transactions and logs; %d issues; %d users in %s", n, n/10, n/5, time.Since(start))
	capture := &queryCapture{}
	cfg := testPool.Config()
	cfg.ConnConfig.Tracer = capture
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()
	var project string
	require.NoError(t, testPool.QueryRow(ctx, `SELECT md5('bench-project-3')::uuid::text`).Scan(&project))
	now := time.Now().UTC()
	since := now.Add(-720 * time.Hour)
	type scenario struct {
		name string
		run  func() error
	}
	scenarios := []scenario{}
	for _, scope := range []struct {
		name string
		ids  []string
	}{{"all", nil}, {"project", []string{project}}} {
		ids := scope.ids
		scenarios = append(scenarios, scenario{"picker/" + scope.name, func() error { _, err := storage.ListAppUsers(ctx, pool, ids, "", 20); return err }}, scenario{"search/" + scope.name, func() error { _, err := storage.ListAppUsers(ctx, pool, ids, "person-42423", 20); return err }}, scenario{"search-short/" + scope.name, func() error { _, err := storage.ListAppUsers(ctx, pool, ids, "42", 20); return err }}, scenario{"lookup/" + scope.name, func() error { _, err := storage.GetAppUser(ctx, pool, ids, "user-42423"); return err }})
		for _, identity := range []string{"user-42423", "hot-user", "missing-user"} {
			name := scope.name + "/" + identity
			scenarios = append(scenarios, scenario{"transactions/" + name, func() error {
				_, err := storage.ListAllTransactions(ctx, pool, storage.TransactionFilter{ProjectIDs: ids, UserIdentity: identity, Since: &since})
				return err
			}}, scenario{"logs/" + name, func() error {
				_, _, err := storage.ListLogs(ctx, pool, storage.LogFilter{ProjectIDs: ids, UserIdentity: identity})
				return err
			}}, scenario{"issues/" + name, func() error {
				_, err := storage.ListAllIssues(ctx, pool, storage.IssueFilter{ProjectIDs: ids, UserIdentity: identity, Status: "open"})
				return err
			}}, scenario{"issue-count/" + name, func() error {
				_, err := storage.CountAllIssues(ctx, pool, storage.IssueFilter{ProjectIDs: ids, UserIdentity: identity, Status: "open"})
				return err
			}}, scenario{"timeseries/" + name, func() error {
				_, err := storage.GetTransactionTimeseries(ctx, pool, ids, 720, "", "", "", identity)
				return err
			}}, scenario{"vitals/" + name, func() error {
				_, err := storage.GetWebVitalsSummary(ctx, pool, ids, since, now, "", identity)
				return err
			}})
		}
	}
	type result struct {
		Name     string
		Mode     string
		MedianMS float64
		Plan     json.RawMessage
	}
	results := []result{}
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		_, err := pool.Exec(ctx, "SET plan_cache_mode = "+mode)
		require.NoError(t, err)
		for _, s := range scenarios {
			timings := []float64{}
			for i := 0; i < 4; i++ {
				start := time.Now()
				require.NoError(t, s.run(), s.name)
				if i > 0 {
					timings = append(timings, float64(time.Since(start).Microseconds())/1000)
				}
			}
			sql, args := capture.sql, append([]any(nil), capture.args...)
			var plan json.RawMessage
			require.NoError(t, testPool.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+sql, args...).Scan(&plan))
			sort.Float64s(timings)
			results = append(results, result{s.name, mode, timings[1], plan})
			t.Logf("%-36s %-18s %9.3f ms", s.name, mode, timings[1])
		}
	}
	if path := os.Getenv("TINDRA_PERF_OUTPUT"); path != "" {
		data, err := json.MarshalIndent(results, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, data, 0600))
	}
	var size string
	require.NoError(t, testPool.QueryRow(ctx, `SELECT pg_size_pretty(pg_database_size(current_database()))`).Scan(&size))
	t.Logf("database size: %s", size)
}
