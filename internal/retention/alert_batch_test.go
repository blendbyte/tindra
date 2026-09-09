package retention_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/retention"
)

type alertCleanupTrace struct {
	capTrace
	rankings int
}

func (a *alertCleanupTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(d.SQL, "DELETE FROM alert_firings WHERE id = ANY") {
		return context.WithValue(ctx, capTraceKey{}, time.Now())
	}
	if strings.Contains(d.SQL, "ROW_NUMBER()") && strings.Contains(d.SQL, "alert_firings") {
		a.rankings++
		return context.WithValue(ctx, capBoundaryKey{}, time.Now())
	}
	return ctx
}
func alertPool(t *testing.T, trace *alertCleanupTrace) *pgxpool.Pool {
	t.Helper()
	cfg := testPool.Config()
	cfg.ConnConfig.Tracer = trace
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(p.Close)
	return p
}
func seedAlertBacklog(t *testing.T, counts ...int) []string {
	t.Helper()
	ctx := context.Background()
	_, err := testPool.Exec(ctx, "TRUNCATE alert_rules CASCADE")
	require.NoError(t, err)
	t.Cleanup(func() { _, e := testPool.Exec(ctx, "TRUNCATE alert_rules CASCADE"); require.NoError(t, e) })
	var ids []string
	offset := 0
	for _, n := range counts {
		var id string
		require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO alert_rules(name,trigger,channel) VALUES ('retention','new_issue','webhook') RETURNING id`).Scan(&id))
		ids = append(ids, id)
		_, err := testPool.Exec(ctx, `INSERT INTO alert_firings(id,rule_id,fired_at,trigger,channel,status)
 SELECT lpad(to_hex(i+$3),32,'0')::uuid,$1,'2020-01-01','new_issue','webhook',
 CASE i%3 WHEN 0 THEN 'pending' WHEN 1 THEN 'failed' ELSE 'success' END
 FROM generate_series(1,$2::int) i`, id, n, offset)
		require.NoError(t, err)
		offset += n
	}
	return ids
}
func TestWorkerAlertBatchesTiesAndRuleScope(t *testing.T) {
	ids := seedAlertBacklog(t, 13000, 7)
	ctx := context.Background()
	trace := &alertCleanupTrace{}
	trace.afterBatch = func() {
		trace.afterBatch = nil
		_, err := testPool.Exec(ctx, `INSERT INTO alert_firings(rule_id,trigger,channel,status) VALUES ($1,'new_issue','webhook','success')`, ids[0])
		require.NoError(t, err)
	}
	retention.NewWorker(alertPool(t, trace), 90).RunOnce(ctx)
	require.Equal(t, 1, trace.rankings)
	require.Equal(t, []int64{5000, 5000, 2000}, trace.batches)
	require.Equal(t, 1008, capCount(t, "alert_firings"))
	require.Equal(t, 2, capCount(t, "alert_rules"))
	var small, old int
	require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM alert_firings WHERE rule_id=$1`, ids[1]).Scan(&small))
	require.Equal(t, 7, small)
	require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM alert_firings WHERE id <= lpad(to_hex(12000),32,'0')::uuid`).Scan(&old))
	require.Zero(t, old)
	retention.NewWorker(testPool, 90).RunOnce(ctx)
	require.Equal(t, 1007, capCount(t, "alert_firings"))
}
func TestWorkerAlertCancellationAndBudget(t *testing.T) {
	seedAlertBacklog(t, 206000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	trace := &alertCleanupTrace{}
	trace.cancel = cancel
	retention.NewWorker(alertPool(t, trace), 90).RunOnce(ctx)
	require.Equal(t, []int64{5000}, trace.batches)
	require.Equal(t, 201000, capCount(t, "alert_firings"))
	// Add a backlog again so the following pass must stop at its work budget.
	seedAlertBacklog(t, 206000)
	trace = &alertCleanupTrace{}
	worker := retention.NewWorker(alertPool(t, trace), 90)
	worker.RunOnce(context.Background())
	require.Equal(t, 1, trace.rankings)
	require.Len(t, trace.batches, 40)
	for _, n := range trace.batches {
		require.EqualValues(t, 5000, n)
	}
	require.Equal(t, 6000, capCount(t, "alert_firings"))
	worker.RunOnce(context.Background())
	require.Equal(t, 1000, capCount(t, "alert_firings"))
}
func TestWorkerAlertRankingCancellationDoesNotDelete(t *testing.T) {
	seedAlertBacklog(t, 12000)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	trace := &alertCleanupTrace{}
	retention.NewWorker(alertPool(t, trace), 90).RunOnce(ctx)
	require.Empty(t, trace.batches)
	require.Zero(t, trace.rankings)
	require.Equal(t, 12000, capCount(t, "alert_firings"))
}
