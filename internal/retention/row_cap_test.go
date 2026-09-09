package retention_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/retention"
)

type capTrace struct {
	matchSQL         string
	boundaryDuration time.Duration
	afterBatch       func()
	batches          []int64
	durations        []time.Duration
	cancel           context.CancelFunc
}
type capTraceKey struct{}
type capBoundaryKey struct{}

func (c *capTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if (c.matchSQL == "" && strings.Contains(d.SQL, "DELETE FROM") && strings.Contains(d.SQL, "LIMIT $3")) || (c.matchSQL != "" && strings.Contains(d.SQL, c.matchSQL) && strings.Contains(d.SQL, "LIMIT $2")) {
		return context.WithValue(ctx, capTraceKey{}, time.Now())
	}
	if strings.Contains(d.SQL, "OFFSET $1 LIMIT 1") {
		return context.WithValue(ctx, capBoundaryKey{}, time.Now())
	}
	return ctx
}
func (c *capTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	if start, ok := ctx.Value(capBoundaryKey{}).(time.Time); ok {
		c.boundaryDuration += time.Since(start)
	}
	if start, ok := ctx.Value(capTraceKey{}).(time.Time); ok && d.Err == nil {
		c.batches = append(c.batches, d.CommandTag.RowsAffected())
		c.durations = append(c.durations, time.Since(start))
		if c.afterBatch != nil {
			c.afterBatch()
		}
		if c.cancel != nil {
			c.cancel()
		}
	}
}
func capPool(t *testing.T, trace *capTrace) *pgxpool.Pool {
	t.Helper()
	cfg := testPool.Config()
	cfg.ConnConfig.Tracer = trace
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(p.Close)
	return p
}
func seedCapRows(t *testing.T, table string, n int) {
	t.Helper()
	ctx := context.Background()
	_, err := testPool.Exec(ctx, "TRUNCATE logs, transactions CASCADE")
	require.NoError(t, err)
	t.Cleanup(func() { _, e := testPool.Exec(ctx, "TRUNCATE logs, transactions CASCADE"); require.NoError(t, e) })
	query := `INSERT INTO logs(id,project_id,timestamp,body) SELECT lpad(to_hex(i),32,'0')::uuid,$1,'2020-01-01'::timestamptz,'cap-test' FROM generate_series(1,$2::int) i`
	if table == "transactions" {
		query = `INSERT INTO transactions(id,project_id,start_timestamp,timestamp,transaction,duration_ms) SELECT lpad(to_hex(i),32,'0')::uuid,$1,'2020-01-01'::timestamptz,NOW(),'/cap-test',10 FROM generate_series(1,$2::int) i`
	}
	_, err = testPool.Exec(ctx, query, testProject.ID, n)
	require.NoError(t, err)
}
func runCap(ctx context.Context, pool *pgxpool.Pool, table string, limit int) {
	logs, tx := 0, 0
	if table == "logs" {
		logs = limit
	} else {
		tx = limit
	}
	retention.NewWorker(pool, 90).WithRowLimits(logs, tx).RunOnce(ctx)
}
func capCount(t *testing.T, table string) int {
	t.Helper()
	var n int
	require.NoError(t, testPool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n))
	return n
}
func TestWorkerRowCapBatchesAndTies(t *testing.T) {
	for _, table := range []string{"logs", "transactions"} {
		t.Run(table, func(t *testing.T) {
			seedCapRows(t, table, 12007)
			ctx := context.Background()
			if table == "transactions" {
				_, err := testPool.Exec(ctx, `INSERT INTO spans(transaction_id,project_id,span_id,start_timestamp,timestamp,duration_ms) SELECT id,project_id,'span',NOW(),NOW(),1 FROM transactions`)
				require.NoError(t, err)
				var issueID string
				require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO issues(project_id,fingerprint,title,first_seen,last_seen) VALUES ($1,'cap-cascade','Cap cascade',NOW(),NOW()) RETURNING id`, testProject.ID).Scan(&issueID))
				t.Cleanup(func() { _, e := testPool.Exec(ctx, "DELETE FROM issues WHERE id=$1", issueID); require.NoError(t, e) })
				_, err = testPool.Exec(ctx, `INSERT INTO perf_events(issue_id,transaction_id,span_count,total_ms) SELECT $1,id,1,1 FROM transactions`, issueID)
				require.NoError(t, err)
			}
			trace := &capTrace{}
			runCap(ctx, capPool(t, trace), table, 7)
			require.Equal(t, []int64{5000, 5000, 2000}, trace.batches)
			var ids []string
			rows, err := testPool.Query(ctx, "SELECT id::text FROM "+table+" ORDER BY id")
			require.NoError(t, err)
			for rows.Next() {
				var id string
				require.NoError(t, rows.Scan(&id))
				ids = append(ids, id)
			}
			require.NoError(t, rows.Err())
			rows.Close()
			var want []string
			for i := 12001; i <= 12007; i++ {
				h := fmt.Sprintf("%032x", i)
				want = append(want, h[:8]+"-"+h[8:12]+"-"+h[12:16]+"-"+h[16:20]+"-"+h[20:])
			}
			require.Equal(t, want, ids)
			if table == "transactions" {
				require.Equal(t, 7, capCount(t, "spans"))
				require.Equal(t, 7, capCount(t, "perf_events"))
			}
		})
	}
}
func TestWorkerRowCapCancellationKeepsCommittedBatch(t *testing.T) {
	for _, table := range []string{"logs", "transactions"} {
		t.Run(table, func(t *testing.T) {
			seedCapRows(t, table, 12007)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			trace := &capTrace{cancel: cancel}
			runCap(ctx, capPool(t, trace), table, 7)
			require.Equal(t, []int64{5000}, trace.batches)
			require.Equal(t, 7007, capCount(t, table))
			runCap(context.Background(), testPool, table, 7)
			require.Equal(t, 7, capCount(t, table))
		})
	}
}
func TestWorkerRowCapPassBudget(t *testing.T) {
	seedCapRows(t, "logs", 205007)
	trace := &capTrace{}
	p := capPool(t, trace)
	runCap(context.Background(), p, "logs", 7)
	require.Len(t, trace.batches, 40)
	for _, n := range trace.batches {
		require.EqualValues(t, 5000, n)
	}
	require.Equal(t, 5007, capCount(t, "logs"))
	runCap(context.Background(), p, "logs", 7)
	require.Equal(t, 7, capCount(t, "logs"))
}

func TestWorkerRowCapPreservesNewerArrivalsBetweenBatches(t *testing.T) {
	for _, table := range []string{"logs", "transactions"} {
		t.Run(table, func(t *testing.T) {
			seedCapRows(t, table, 12007)
			ctx := context.Background()
			trace := &capTrace{}
			trace.afterBatch = func() {
				trace.afterBatch = nil
				query := `INSERT INTO logs(project_id,timestamp,body) SELECT $1,NOW(),'arriving' FROM generate_series(1,20)`
				if table == "transactions" {
					query = `INSERT INTO transactions(project_id,start_timestamp,timestamp,transaction,duration_ms) SELECT $1,NOW(),NOW(),'/arriving',1 FROM generate_series(1,20)`
				}
				_, err := testPool.Exec(ctx, query, testProject.ID)
				require.NoError(t, err)
			}
			runCap(ctx, capPool(t, trace), table, 7)
			require.Equal(t, []int64{5000, 5000, 2000}, trace.batches)
			require.Equal(t, 27, capCount(t, table))
			column := "timestamp"
			if table == "transactions" {
				column = "start_timestamp"
			}
			var arriving int
			require.NoError(t, testPool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE "+column+" > '2020-01-02'::timestamptz").Scan(&arriving))
			require.Equal(t, 20, arriving)
			runCap(ctx, testPool, table, 7)
			require.Equal(t, 7, capCount(t, table))
		})
	}
}
