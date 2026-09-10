package retention

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/testutil"
)

type cancelRetentionQuery struct {
	match string
	hit   atomic.Bool
}

func (c *cancelRetentionQuery) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, c.match) {
		c.hit.Store(true)
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx
	}
	return ctx
}
func (*cancelRetentionQuery) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestEventRetentionCancellationRollsBackEveryStage(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := testutil.SetupDB(ctx)
	defer cleanup()
	var project, issue, event string
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO projects(slug,name,public_key) VALUES('cancel','Cancel','cancel') RETURNING id`).Scan(&project))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO issues(project_id,fingerprint,title,status,event_count,first_seen,last_seen) VALUES($1,'cancel','Cancel','resolved',1,now(),now()) RETURNING id`, project).Scan(&issue))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO events(project_id,issue_id,timestamp,received_at,payload) VALUES($1,$2,now(),now()-interval '100 days','{}') RETURNING id`, project, issue).Scan(&event))
	for _, stage := range []string{"pg_advisory_xact_lock_shared", "DELETE FROM events", "SELECT id FROM issues", "UPDATE issues", "DELETE FROM issues", "commit"} {
		t.Run(stage, func(t *testing.T) {
			trace := &cancelRetentionQuery{match: stage}
			cfg := pool.Config()
			cfg.ConnConfig.Tracer = trace
			failing, err := pgxpool.NewWithConfig(ctx, cfg)
			require.NoError(t, err)
			defer failing.Close()
			events, issues, err := NewWorker(failing, 90).purgeEventBatch(ctx, time.Now(), 5000)
			require.Error(t, err)
			require.True(t, trace.hit.Load())
			require.Zero(t, events)
			require.Zero(t, issues)
			var count int
			require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM events WHERE id=$1", event).Scan(&count))
			require.Equal(t, 1, count)
			require.NoError(t, pool.QueryRow(ctx, "SELECT event_count FROM issues WHERE id=$1", issue).Scan(&count))
			require.Equal(t, 1, count)
		})
	}
	events, issues, err := NewWorker(pool, 90).purgeEventBatch(ctx, time.Now(), 5000)
	require.NoError(t, err)
	require.EqualValues(t, 1, events)
	require.EqualValues(t, 1, issues)
}

func TestRetentionUnavailableDatabaseStopsWithoutReportingDeletes(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, "postgres://unused@localhost:1/unused?sslmode=disable")
	require.NoError(t, err)
	pool.Close()
	w := NewWorker(pool, 90).WithRowLimits(1, 1).WithProfileLimits(1, 1)
	require.Zero(t, w.purgeProfiles(ctx, time.Now()))
	require.Zero(t, w.purgeProfilesStorageCap(ctx))
	e, i := w.purgeEvents(ctx, time.Now())
	require.Zero(t, e)
	require.Zero(t, i)
	require.Zero(t, w.purgeTransactions(ctx, time.Now()))
	require.Zero(t, w.purgeCronCheckins(ctx, time.Now()))
	require.Zero(t, w.purgeAlertFirings(ctx))
	require.Zero(t, w.purgeLogsRowCap(ctx))
	w.purgeExpiredAuthTokens(ctx)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.Zero(t, w.purgeAlertFirings(cancelled))
	require.Zero(t, w.purgeLogsRowCap(cancelled))
	require.Zero(t, w.purgeCronCheckins(cancelled, time.Now()))
}

func TestTokenRetentionDeleteFailurePreservesTokenForRetry(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := testutil.SetupDB(ctx)
	defer cleanup()
	_, err := pool.Exec(ctx, `INSERT INTO oauth_states(token_hash,provider,verifier,expires_at) VALUES('expired','github','test',now()-interval '1 day')`)
	require.NoError(t, err)
	trace := &cancelRetentionQuery{match: "DELETE FROM oauth_states"}
	cfg := pool.Config()
	cfg.ConnConfig.Tracer = trace
	failing, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer failing.Close()
	NewWorker(failing, 90).purgeExpiredTokenTable(ctx, "oauth_states", time.Now())
	require.True(t, trace.hit.Load())
	var n int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM oauth_states").Scan(&n))
	require.Equal(t, 1, n)
	NewWorker(pool, 90).purgeExpiredTokenTable(ctx, "oauth_states", time.Now())
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM oauth_states").Scan(&n))
	require.Zero(t, n)
}
