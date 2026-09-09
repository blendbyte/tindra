package storage_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestAnalyticsPerformance(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("TINDRA_PERF_ROWS"))
	if n < 10000 {
		t.Skip("set TINDRA_PERF_ROWS >= 10000")
	}
	truncateProjects(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "analytics", "Analytics")
	require.NoError(t, err)
	var issue string
	require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO issues(project_id,fingerprint,title,first_seen,last_seen) VALUES($1,'hot','Hot',now(),now()) RETURNING id`, p.ID).Scan(&issue))
	_, err = testPool.Exec(ctx, `INSERT INTO events(project_id,issue_id,timestamp,payload) SELECT $1,$2,now(),jsonb_build_object('user',jsonb_build_object('id','user-'||(i%10000)), 'stacktrace',repeat(md5(i::text),128)) FROM generate_series(1,$3) i`, p.ID, issue, n)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, "VACUUM ANALYZE events")
	require.NoError(t, err)
	expr := `COALESCE(payload->'user'->>'id',payload->'user'->>'username',payload->'user'->>'email',payload->'user'->>'ip_address')`
	for _, grouped := range []bool{false, true} {
		query := "SELECT count(DISTINCT " + expr + ") FROM events WHERE issue_id=$1"
		if grouped {
			query = "SELECT count(identity) FROM (SELECT " + expr + " AS identity FROM events WHERE issue_id=$1 GROUP BY 1) identities"
		}
		elapsed := usageMedian(t, func() {
			var count int
			require.NoError(t, testPool.QueryRow(ctx, query, issue).Scan(&count))
			require.Equal(t, 10000, count)
		})
		t.Logf("%d hot-issue events grouped=%v median=%s", n, grouped, elapsed)
	}

	// A selective project among 100 projects, with high-cardinality span groups.
	_, err = testPool.Exec(ctx, `INSERT INTO projects(id,slug,name,public_key) SELECT lpad(to_hex(i),32,'0')::uuid,'span-'||i,'Span '||i,'span-key-'||i FROM generate_series(1,100) i`)
	require.NoError(t, err)
	var transaction string
	require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO transactions(project_id,start_timestamp,timestamp,transaction,duration_ms) VALUES($1,now(),now(),'span-parent',1) RETURNING id`, p.ID).Scan(&transaction))
	_, err = testPool.Exec(ctx, `INSERT INTO spans(transaction_id,project_id,span_id,op,description,start_timestamp,timestamp,duration_ms)
 SELECT $1,lpad(to_hex(i%100+1),32,'0')::uuid,i::text,'db.sql','query-'||(i%5000),now()-(i%10000)*interval '1 minute',now(),i%1000 FROM generate_series(1,$2) i`, transaction, n)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, "VACUUM ANALYZE spans")
	require.NoError(t, err)
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		capture := &queryCapture{}
		cfg := testPool.Config().Copy()
		cfg.ConnConfig.Tracer = capture
		cfg.ConnConfig.RuntimeParams["plan_cache_mode"] = mode
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		require.NoError(t, err)
		current, err := storage.GetSpanSummaries(ctx, pool, "db", []string{"00000000-0000-0000-0000-000000000001"}, 168, "", "")
		require.NoError(t, err)
		require.NotEmpty(t, current)
		query, args := capture.sql, capture.args
		for _, optional := range []bool{true, false} {
			sql := query
			if optional {
				sql = strings.Replace(sql, "s.project_id = ANY($2::uuid[])", "(CARDINALITY($2::uuid[]) = 0 OR s.project_id = ANY($2::uuid[]))", 1)
			}
			elapsed := usageMedian(t, func() {
				rows, err := pool.Query(ctx, sql, args...)
				require.NoError(t, err)
				for rows.Next() {
				}
				require.NoError(t, rows.Err())
				rows.Close()
			})
			t.Logf("%s scoped spans optional_predicate=%v median=%s", mode, optional, elapsed)
		}
		pool.Close()
	}
}
