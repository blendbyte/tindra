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
// -run '^TestIssueMetadataPerformance$' -count=1 -timeout=15m
// Compares the enriched lookup previously used for prechecks with metadata on
// the same hot issue, in the disposable test database.
func TestIssueMetadataPerformance(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("TINDRA_PERF_ROWS"))
	if err != nil || n < 10000 {
		t.Skip("set TINDRA_PERF_ROWS >= 10000")
	}
	ctx := context.Background()
	truncateProjects(t)
	p, err := storage.CreateProject(ctx, testPool, "issue-metadata-perf", "Issue metadata performance")
	require.NoError(t, err)
	issue, _, _, err := storage.UpsertIssue(ctx, testPool, p.ID, "hot-issue", "Hot issue", "error", "error", "", "v1", time.Now())
	require.NoError(t, err)
	exec := func(q string, args ...any) { _, err := testPool.Exec(ctx, q, args...); require.NoError(t, err) }
	exec(`INSERT INTO releases(project_id,version) VALUES ($1,'v1')`, p.ID)
	exec(`INSERT INTO events(project_id,issue_id,timestamp,payload)
 SELECT $1,$2,NOW()-i*interval '1 second',jsonb_build_object('level','error','release','v1','user',jsonb_build_object('id','user-'||(i%10000))) FROM generate_series(1,$3::int) i`, p.ID, issue.ID, n)
	exec(`UPDATE issues SET event_count=$2 WHERE id=$1`, issue.ID, n)
	exec("VACUUM ANALYZE events")
	exec("ANALYZE issues")
	capture := &queryCapture{}
	cfg := testPool.Config()
	cfg.MaxConns = 1
	cfg.ConnConfig.Tracer = capture
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()
	full, err := storage.GetIssue(ctx, pool, issue.ID)
	require.NoError(t, err)
	require.EqualValues(t, 10000, full.UserCount)
	expected := &storage.IssueMetadata{ID: full.ID, ProjectID: full.ProjectID, FirstSeen: full.FirstSeen, Status: full.Status}
	type result struct {
		Name, Mode string
		MedianMS   float64
		Plan       json.RawMessage
	}
	var results []result
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		_, err := pool.Exec(ctx, "SET plan_cache_mode = "+mode)
		require.NoError(t, err)
		for _, name := range []string{"enriched", "metadata"} {
			var timings []float64
			for i := 0; i < 4; i++ {
				start := time.Now()
				if name == "enriched" {
					got, e := storage.GetIssue(ctx, pool, issue.ID)
					require.NoError(t, e)
					require.Equal(t, full, got)
				} else {
					got, e := storage.GetIssueMetadata(ctx, pool, issue.ID)
					require.NoError(t, e)
					require.Equal(t, expected, got)
				}
				if i > 0 {
					timings = append(timings, float64(time.Since(start).Microseconds())/1000)
				}
			}
			sql, args := capture.sql, append([]any(nil), capture.args...)
			var plan json.RawMessage
			require.NoError(t, testPool.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+sql, args...).Scan(&plan))
			sort.Float64s(timings)
			results = append(results, result{name, mode, timings[1], plan})
			t.Logf("%s %s %.3f ms", name, mode, timings[1])
		}
	}
	if p := os.Getenv("TINDRA_PERF_OUTPUT"); p != "" {
		b, e := json.MarshalIndent(results, "", "  ")
		require.NoError(t, e)
		require.NoError(t, os.WriteFile(p, b, 0600))
	}
}
