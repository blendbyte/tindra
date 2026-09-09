package storage_test

import (
	"context"
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
// -run '^TestDashboardIssuesPerformance$' -count=1 -timeout=15m
func TestDashboardIssuesPerformance(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("TINDRA_PERF_ROWS"))
	if err != nil || n < 10000 || n%50 != 0 {
		t.Skip("set TINDRA_PERF_ROWS >= 10000, divisible by 50")
	}
	truncateProjects(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "dashboard-issue-perf", "Dashboard issue performance")
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `INSERT INTO issues(id,project_id,fingerprint,title,first_seen,last_seen,event_count)
 SELECT lpad(to_hex(i),32,'0')::uuid,$1,'fp'||i,'Issue '||i,NOW(),NOW()-i*interval '1 hour',$2::int/50 FROM generate_series(1,50) i`, p.ID, n)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `INSERT INTO events(project_id,issue_id,timestamp,payload)
 SELECT $1,lpad(to_hex(1+(i-1)%50),32,'0')::uuid,NOW()-(i%10000)*interval '1 minute',
 jsonb_build_object('user',jsonb_build_object('id','user-'||(i%10000))) FROM generate_series(1,$2::int) i`, p.ID, n)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, "VACUUM ANALYZE events")
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, "ANALYZE issues")
	require.NoError(t, err)
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		cfg := testPool.Config()
		cfg.ConnConfig.RuntimeParams["plan_cache_mode"] = mode
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		require.NoError(t, err)
		var fullIDs []string
		full, err := storage.ListAllIssues(ctx, pool, storage.IssueFilter{Status: "open", Limit: 50})
		require.NoError(t, err)
		for _, issue := range full {
			fullIDs = append(fullIDs, issue.ID)
		}
		fullLines, err := storage.GetIssueSparklines(ctx, pool, fullIDs)
		require.NoError(t, err)
		small, err := storage.ListDashboardIssues(ctx, pool, nil)
		require.NoError(t, err)
		require.Len(t, small, 5)
		var smallIDs []string
		for i, issue := range small {
			require.Equal(t, full[i].ID, issue.ID)
			smallIDs = append(smallIDs, issue.ID)
		}
		smallLines, err := storage.GetIssueSparklines(ctx, pool, smallIDs)
		require.NoError(t, err)
		for _, id := range smallIDs {
			require.Equal(t, fullLines[id], smallLines[id])
		}
		for _, light := range []bool{false, true} {
			var times []float64
			for i := 0; i < 4; i++ {
				start := time.Now()
				total, e := storage.CountAllIssues(ctx, pool, storage.IssueFilter{Status: "open"})
				require.NoError(t, e)
				require.Equal(t, 50, total)
				ids := []string{}
				if light {
					rows, e := storage.ListDashboardIssues(ctx, pool, nil)
					require.NoError(t, e)
					for _, r := range rows {
						ids = append(ids, r.ID)
					}
				} else {
					rows, e := storage.ListAllIssues(ctx, pool, storage.IssueFilter{Status: "open", Limit: 50})
					require.NoError(t, e)
					for _, r := range rows {
						ids = append(ids, r.ID)
					}
				}
				_, e = storage.GetIssueSparklines(ctx, pool, ids)
				require.NoError(t, e)
				if i > 0 {
					times = append(times, float64(time.Since(start).Microseconds())/1000)
				}
			}
			sort.Float64s(times)
			t.Logf("%s lightweight=%v median %.3f ms", mode, light, times[1])
		}
		pool.Close()
	}
}
