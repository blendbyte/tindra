package storage_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

// Opt in: TINDRA_PERF_ROWS=1000000 go test -v ./internal/storage
// -run '^TestWindowPerformanceIssuesPlan$' -count=1 -timeout=15m
func TestWindowPerformanceIssuesPlan(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("TINDRA_PERF_ROWS"))
	if err != nil || n < 10000 {
		t.Skip("set TINDRA_PERF_ROWS >= 10000")
	}
	truncateProjects(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "window-perf", "Window performance")
	require.NoError(t, err)
	now := time.Now()
	_, err = testPool.Exec(ctx, `INSERT INTO issues(id,project_id,fingerprint,title,level,kind,first_seen,last_seen,event_count)
 SELECT lpad(to_hex(i),32,'0')::uuid,$1,'fp'||i,'Issue '||i,CASE WHEN i<=20 THEN 'performance' ELSE 'error' END,
 CASE WHEN i<=20 THEN 'n1_query' ELSE 'error' END,$2::timestamptz-interval '30 days',$2::timestamptz,1 FROM generate_series(1,2000) i`, p.ID, now)
	require.NoError(t, err)
	// Thirty days of occurrences, so one day is a small slice of perf_events.
	_, err = testPool.Exec(ctx, `INSERT INTO transactions(id,project_id,transaction,op,status,duration_ms,start_timestamp,timestamp,environment)
 SELECT lpad(to_hex(i),32,'0')::uuid,$1,'App\Jobs\Job','queue','ok',10,ts,ts,'production'
 FROM (SELECT i,$2::timestamptz-(i::float8/$3)*interval '30 days' AS ts FROM generate_series(1,$3::int) i) s`, p.ID, now, n)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `INSERT INTO perf_events(issue_id,transaction_id,span_count,total_ms,created_at)
 SELECT lpad(to_hex(1+i%20),32,'0')::uuid,lpad(to_hex(i),32,'0')::uuid,6,60,$1::timestamptz-(i::float8/$2)*interval '30 days'
 FROM generate_series(1,$2::int) i`, now, n)
	require.NoError(t, err)
	for _, table := range []string{"issues", "transactions", "perf_events", "events"} {
		_, err = testPool.Exec(ctx, "VACUUM ANALYZE "+table)
		require.NoError(t, err)
	}

	bounds := storage.TimeRange{From: now.Add(-24 * time.Hour), To: now}
	window := storage.WithTimeRange(ctx, bounds)
	start := time.Now()
	issues, err := storage.ListAllIssues(window, testPool, storage.IssueFilter{Status: "open", ProjectIDs: []string{p.ID}, Priority: true})
	require.NoError(t, err)
	t.Logf("listed %d issues over %d perf events in %s", len(issues), n, time.Since(start))
	require.Len(t, issues, 20)

	plan := explainWindowPerfOccurrences(t, p.ID, bounds)
	t.Log(plan)
	require.Contains(t, plan, "perf_events_issue")
	require.NotContains(t, plan, "Seq Scan on perf_events")
}

func explainWindowPerfOccurrences(t *testing.T, projectID string, bounds storage.TimeRange) string {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `EXPLAIN SELECT pe.issue_id, pe.created_at, t.environment
 FROM issues n JOIN perf_events pe ON pe.issue_id = n.id JOIN transactions t ON t.id = pe.transaction_id
 WHERE n.kind <> 'error' AND pe.created_at >= $1 AND pe.created_at < $2 AND n.project_id = ANY($3::uuid[])`,
		bounds.From, bounds.To, []string{projectID})
	require.NoError(t, err)
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		lines = append(lines, line)
	}
	require.NoError(t, rows.Err())
	return strings.Join(lines, "\n")
}
