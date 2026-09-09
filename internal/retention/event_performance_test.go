package retention_test

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/retention"
)

type eventBatchTiming struct {
	start     time.Time
	durations []time.Duration
}
type eventBatchOperation struct{}

func (b *eventBatchTiming) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.EqualFold(d.SQL, "begin") {
		b.start = time.Now()
	}
	return context.WithValue(ctx, eventBatchOperation{}, d.SQL)
}
func (b *eventBatchTiming) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	if sql, _ := ctx.Value(eventBatchOperation{}).(string); strings.EqualFold(sql, "commit") && d.Err == nil {
		b.durations = append(b.durations, time.Since(b.start))
	}
}

// Opt in: TINDRA_EVENT_RETENTION_PERF=1 go test -v ./internal/retention
// -run '^TestEventRetentionPerformance$' -count=1 -timeout=15m
func TestEventRetentionPerformance(t *testing.T) {
	if os.Getenv("TINDRA_EVENT_RETENTION_PERF") != "1" {
		t.Skip("set TINDRA_EVENT_RETENTION_PERF=1")
	}
	ctx := context.Background()
	exec := func(q string, args ...any) { _, err := testPool.Exec(ctx, q, args...); require.NoError(t, err) }
	exec("DROP INDEX events_received_id")
	t.Cleanup(func() {
		_, err := testPool.Exec(ctx, "CREATE INDEX IF NOT EXISTS events_received_id ON events(received_at,id)")
		require.NoError(t, err)
	})
	for _, batched := range []bool{false, true} {
		if batched {
			exec("CREATE INDEX CONCURRENTLY events_received_id ON events(received_at,id)")
		}
		var totals, longest []float64
		for run := 0; run < 4; run++ {
			truncate(t)
			issue := seedRetainedIssue(t, "open", 10000, 90000)
			exec(`INSERT INTO event_tags(event_id,issue_id,project_id,key,value) SELECT id,issue_id,project_id,'test','value' FROM events`)
			exec("VACUUM ANALYZE events")
			exec("VACUUM ANALYZE event_tags")
			exec("ANALYZE issues")
			var total, maxTransaction time.Duration
			if batched {
				tracer := &eventBatchTiming{}
				cfg := testPool.Config()
				cfg.ConnConfig.Tracer = tracer
				pool, err := pgxpool.NewWithConfig(ctx, cfg)
				require.NoError(t, err)
				retention.NewWorker(pool, 90).RunOnce(ctx)
				pool.Close()
				require.Len(t, tracer.durations, 3)
				for _, d := range tracer.durations {
					total += d
					maxTransaction = max(maxTransaction, d)
				}
			} else {
				start := time.Now()
				statement := time.Now()
				rows, err := testPool.Query(ctx, "DELETE FROM events WHERE received_at < $1 RETURNING issue_id", time.Now().AddDate(0, 0, -90))
				require.NoError(t, err)
				ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
				require.NoError(t, err)
				require.Len(t, ids, 10000)
				maxTransaction = time.Since(statement)
				statement = time.Now()
				exec(`UPDATE issues SET event_count=(SELECT COUNT(*) FROM events WHERE events.issue_id=issues.id) WHERE id=ANY($1::uuid[])`, []string{issue})
				maxTransaction = max(maxTransaction, time.Since(statement))
				statement = time.Now()
				exec(`DELETE FROM issues WHERE event_count=0 AND status IN ('resolved','ignored') AND id=ANY($1::uuid[])`, []string{issue})
				maxTransaction = max(maxTransaction, time.Since(statement))
				total = time.Since(start)
			}
			require.Equal(t, 90000, capCount(t, "events"))
			require.Equal(t, 90000, capCount(t, "event_tags"))
			var count int
			require.NoError(t, testPool.QueryRow(ctx, "SELECT event_count FROM issues WHERE id=$1", issue).Scan(&count))
			require.Equal(t, 90000, count)
			if run > 0 {
				totals = append(totals, float64(total.Microseconds())/1000)
				longest = append(longest, float64(maxTransaction.Microseconds())/1000)
			}
		}
		sort.Float64s(totals)
		sort.Float64s(longest)
		t.Logf("batched=%v total %.3f ms longest transaction %.3f ms", batched, totals[1], longest[1])
	}
	var size int64
	require.NoError(t, testPool.QueryRow(ctx, "SELECT pg_relation_size('events_received_id')").Scan(&size))
	t.Logf("index bytes: %d", size)
}
