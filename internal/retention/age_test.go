package retention_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/retention"
)

func seedAgeRows(t *testing.T, table string, expired, fresh int) {
	t.Helper()
	seedCapRows(t, table, expired+fresh)
	ctx := context.Background()
	// Old SDK timestamps on fresh rows must not cause eviction. Expired rows
	// have future SDK timestamps, which must not protect them from eviction.
	_, err := testPool.Exec(ctx, "UPDATE "+table+" SET received_at=NOW()-interval '91 days', timestamp=NOW()+interval '1 year' WHERE id <= lpad(to_hex($1::int),32,'0')::uuid", expired)
	require.NoError(t, err)
	if table == "transactions" {
		_, err = testPool.Exec(ctx, `UPDATE transactions SET start_timestamp=timestamp WHERE received_at < NOW()-interval '90 days'`)
		require.NoError(t, err)
		_, err = testPool.Exec(ctx, `INSERT INTO spans(transaction_id,project_id,span_id,start_timestamp,timestamp,duration_ms) SELECT id,project_id,'span',NOW(),NOW(),1 FROM transactions`)
		require.NoError(t, err)
		var issueID string
		require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO issues(project_id,fingerprint,title,first_seen,last_seen) VALUES ($1,'age-cascade','Age cascade',NOW(),NOW()) RETURNING id`, testProject.ID).Scan(&issueID))
		t.Cleanup(func() { _, e := testPool.Exec(ctx, "DELETE FROM issues WHERE id=$1", issueID); require.NoError(t, e) })
		_, err = testPool.Exec(ctx, `INSERT INTO perf_events(issue_id,transaction_id,span_count,total_ms) SELECT $1,id,1,1 FROM transactions`, issueID)
		require.NoError(t, err)
	}
}
func TestWorkerAgeBatchesReceiptTimeAndCascades(t *testing.T) {
	for _, table := range []string{"logs", "transactions"} {
		t.Run(table, func(t *testing.T) {
			seedAgeRows(t, table, 12000, 7)
			trace := &capTrace{matchSQL: "DELETE FROM " + table}
			retention.NewWorker(capPool(t, trace), 90).RunOnce(context.Background())
			require.Equal(t, []int64{5000, 5000, 2000}, trace.batches)
			require.Equal(t, 7, capCount(t, table))
			var fresh int
			require.NoError(t, testPool.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE received_at > NOW()-interval '1 day'").Scan(&fresh))
			require.Equal(t, 7, fresh)
			if table == "transactions" {
				require.Equal(t, 7, capCount(t, "spans"))
				require.Equal(t, 7, capCount(t, "perf_events"))
			}
		})
	}
}
func TestWorkerAgeCancellationAndArrivals(t *testing.T) {
	for _, table := range []string{"logs", "transactions"} {
		t.Run(table, func(t *testing.T) {
			seedAgeRows(t, table, 12000, 7)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			trace := &capTrace{matchSQL: "DELETE FROM " + table, cancel: cancel}
			trace.afterBatch = func() {
				query := `INSERT INTO logs(project_id,timestamp,body) VALUES ($1,'2000-01-01','late arrival')`
				if table == "transactions" {
					query = `INSERT INTO transactions(project_id,start_timestamp,timestamp,transaction,duration_ms) VALUES ($1,'2000-01-01','2000-01-01','/late-arrival',1)`
				}
				_, err := testPool.Exec(context.Background(), query, testProject.ID)
				require.NoError(t, err)
			}
			retention.NewWorker(capPool(t, trace), 90).RunOnce(ctx)
			require.Equal(t, []int64{5000}, trace.batches)
			require.Equal(t, 7008, capCount(t, table))
			retention.NewWorker(testPool, 90).RunOnce(context.Background())
			require.Equal(t, 8, capCount(t, table))
		})
	}
}
func TestWorkerAgePassBudget(t *testing.T) {
	seedAgeRows(t, "logs", 205000, 7)
	trace := &capTrace{matchSQL: "DELETE FROM logs"}
	worker := retention.NewWorker(capPool(t, trace), 90)
	require.True(t, worker.RunOnce(context.Background()))
	require.Len(t, trace.batches, 40)
	for _, n := range trace.batches {
		require.EqualValues(t, 5000, n)
	}
	require.Equal(t, 5007, capCount(t, "logs"))
	require.False(t, worker.RunOnce(context.Background()))
	require.Equal(t, 7, capCount(t, "logs"))
}
