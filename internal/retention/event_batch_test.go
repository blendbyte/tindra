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
	"github.com/blendbyte/tindra/internal/storage"
)

func seedRetainedIssue(t *testing.T, status string, expired, fresh int) string {
	t.Helper()
	ctx := context.Background()
	var id string
	require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO issues(project_id,fingerprint,title,first_seen,last_seen,status,event_count) VALUES ($1,$2,'Retention',NOW(),NOW(),$2,$3) RETURNING id`, testProject.ID, status, expired+fresh).Scan(&id))
	_, err := testPool.Exec(ctx, `INSERT INTO issue_fingerprints(project_id,fingerprint,issue_id) VALUES ($1,$2,$3)`, testProject.ID, status, id)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `INSERT INTO events(project_id,issue_id,fingerprint,timestamp,received_at,payload)
 SELECT $1,$2,$3,NOW()+interval '1 year',CASE WHEN i <= $4 THEN NOW()-interval '91 days' ELSE NOW() END,'{}'
 FROM generate_series(1,$5::int) i`, testProject.ID, id, status, expired, expired+fresh)
	require.NoError(t, err)
	return id
}
func TestWorkerEventBatchesReconcileAndPreserveShells(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	open := seedRetainedIssue(t, "open", 12000, 7)
	regressed := seedRetainedIssue(t, "regressed", 1, 0)
	seedRetainedIssue(t, "resolved", 1, 0)
	seedRetainedIssue(t, "ignored", 1, 0)
	_, err := testPool.Exec(ctx, `INSERT INTO events(project_id,timestamp,received_at,payload) VALUES ($1,NOW(),NOW()-interval '91 days','{}')`, testProject.ID)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `INSERT INTO event_tags(event_id,issue_id,project_id,key,value) SELECT id,issue_id,project_id,'test','value' FROM events`)
	require.NoError(t, err)
	// Retention must still repair drift for affected issues, not merely subtract.
	_, err = testPool.Exec(ctx, "UPDATE issues SET event_count=999999 WHERE id=$1", open)
	require.NoError(t, err)
	trace := &capTrace{matchSQL: "DELETE FROM events"}
	retention.NewWorker(capPool(t, trace), 90).RunOnce(ctx)
	require.Equal(t, []int64{5000, 5000, 2004}, trace.batches)
	require.Equal(t, 7, capCount(t, "events"))
	require.Equal(t, 7, capCount(t, "event_tags"))
	require.Equal(t, 2, capCount(t, "issues"))
	var count int
	require.NoError(t, testPool.QueryRow(ctx, "SELECT event_count FROM issues WHERE id=$1", open).Scan(&count))
	require.Equal(t, 7, count)
	require.NoError(t, testPool.QueryRow(ctx, "SELECT event_count FROM issues WHERE id=$1", regressed).Scan(&count))
	require.Zero(t, count)
}
func TestWorkerEventBatchFailureRollsBack(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	id := seedRetainedIssue(t, "open", 3, 1)
	_, err := testPool.Exec(ctx, `CREATE FUNCTION reject_retention_count() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected count failure'; END $$;
 CREATE TRIGGER reject_retention_count BEFORE UPDATE OF event_count ON issues FOR EACH ROW EXECUTE FUNCTION reject_retention_count()`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := testPool.Exec(ctx, "DROP TRIGGER reject_retention_count ON issues; DROP FUNCTION reject_retention_count()")
		require.NoError(t, e)
	})
	retention.NewWorker(testPool, 90).RunOnce(ctx)
	require.Equal(t, 4, capCount(t, "events"))
	var count int
	require.NoError(t, testPool.QueryRow(ctx, "SELECT event_count FROM issues WHERE id=$1", id).Scan(&count))
	require.Equal(t, 4, count)
}
func TestWorkerEventCancellationRollsBackCurrentBatch(t *testing.T) {
	truncate(t)
	id := seedRetainedIssue(t, "open", 12000, 7)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel after the second DELETE returns, while its transaction is open.
	trace := &capTrace{matchSQL: "DELETE FROM events"}
	trace.afterBatch = func() {
		if len(trace.batches) == 2 {
			cancel()
		}
	}
	retention.NewWorker(capPool(t, trace), 90).RunOnce(ctx)
	require.Equal(t, []int64{5000, 5000}, trace.batches)
	require.Equal(t, 7007, capCount(t, "events"))
	var count int
	require.NoError(t, testPool.QueryRow(context.Background(), "SELECT event_count FROM issues WHERE id=$1", id).Scan(&count))
	require.Equal(t, 7007, count)
	retention.NewWorker(testPool, 90).RunOnce(context.Background())
	require.Equal(t, 7, capCount(t, "events"))
}

type beforeIssueLock struct{ hook func() }

func (b *beforeIssueLock) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, "ORDER BY id FOR UPDATE") && b.hook != nil {
		hook := b.hook
		b.hook = nil
		hook()
	}
	return ctx
}
func (*beforeIssueLock) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func TestWorkerEventRecountIncludesConcurrentGrouping(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	issue := seedRetainedIssue(t, "open", 1, 0)
	var event string
	require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO events(project_id,timestamp,payload) VALUES ($1,NOW(),'{}') RETURNING id`, testProject.ID).Scan(&event))
	tracer := &beforeIssueLock{hook: func() {
		// No partial deletion is visible while the retention batch is uncommitted.
		require.Equal(t, 2, capCount(t, "events"))
		grouped, _, _, err := storage.GroupEvent(ctx, testPool, event, "open", "Retention", "error", "", "")
		require.NoError(t, err)
		require.Equal(t, issue, grouped.ID)
		// A membership move cannot overlap the active retention batch.
		tx, err := testPool.Begin(ctx)
		require.NoError(t, err)
		defer tx.Rollback(ctx)
		var acquired bool
		require.NoError(t, tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(1953066596, 1)").Scan(&acquired))
		require.False(t, acquired)
	}}
	cfg := testPool.Config()
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()
	retention.NewWorker(pool, 90).RunOnce(ctx)
	var count int
	require.NoError(t, testPool.QueryRow(ctx, "SELECT event_count FROM issues WHERE id=$1", issue).Scan(&count))
	require.Equal(t, 1, count)
	require.Equal(t, 1, capCount(t, "events"))
}
func TestWorkerEventPassBudget(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	id := seedRetainedIssue(t, "open", 200001, 0)
	retention.NewWorker(testPool, 90).RunOnce(ctx)
	require.Equal(t, 1, capCount(t, "events"))
	var count int
	require.NoError(t, testPool.QueryRow(ctx, "SELECT event_count FROM issues WHERE id=$1", id).Scan(&count))
	require.Equal(t, 1, count)
	retention.NewWorker(testPool, 90).RunOnce(ctx)
	require.Zero(t, capCount(t, "events"))
}
func TestMembershipMovesWaitForRetention(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	a := seedRetainedIssue(t, "open", 1, 0)
	b := seedRetainedIssue(t, "resolved", 1, 0)
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	require.NoError(t, storage.LockIssueMembership(ctx, tx, false))
	for _, move := range []string{"merge", "unmerge"} {
		t.Run(move, func(t *testing.T) {
			timed, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			if move == "merge" {
				_, err = storage.MergeIssues(timed, testPool, a, []string{b})
			} else {
				_, err = storage.UnmergeFingerprints(timed, testPool, a, []string{"open"})
			}
			require.Error(t, err)
			require.ErrorIs(t, timed.Err(), context.DeadlineExceeded)
		})
	}
}

func TestWorkerEventEmptyIssueFailureRollsBack(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	id := seedRetainedIssue(t, "resolved", 3, 0)
	_, err := testPool.Exec(ctx, `CREATE FUNCTION reject_empty_issue() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected issue cleanup failure'; END $$;
 CREATE TRIGGER reject_empty_issue BEFORE DELETE ON issues FOR EACH ROW EXECUTE FUNCTION reject_empty_issue()`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := testPool.Exec(ctx, "DROP TRIGGER reject_empty_issue ON issues; DROP FUNCTION reject_empty_issue()")
		require.NoError(t, e)
	})
	retention.NewWorker(testPool, 90).RunOnce(ctx)
	require.Equal(t, 3, capCount(t, "events"))
	var count int
	require.NoError(t, testPool.QueryRow(ctx, "SELECT event_count FROM issues WHERE id=$1", id).Scan(&count))
	require.Equal(t, 3, count)
}
