package storage_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestOptimizedReadsRejectUnavailableDatabase(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, testPool.Config())
	require.NoError(t, err)
	pool.Close()
	t.Run("project metadata", func(t *testing.T) {
		data, err := storage.ListProjectMetadata(ctx, pool)
		require.Error(t, err)
		require.Nil(t, data)
	})
	t.Run("release metadata", func(t *testing.T) {
		data, err := storage.ListReleaseMetadata(ctx, pool, storage.ReleaseFilter{})
		require.Error(t, err)
		require.Nil(t, data)
	})
	t.Run("release health", func(t *testing.T) {
		data, err := storage.ListRecentReleaseHealth(ctx, pool, nil)
		require.Error(t, err)
		require.Nil(t, data)
	})
	t.Run("dashboard", func(t *testing.T) {
		data, err := storage.ListDashboardIssues(ctx, pool, nil)
		require.Error(t, err)
		require.Nil(t, data)
	})
	t.Run("issue metadata", func(t *testing.T) {
		data, err := storage.GetIssueMetadata(ctx, pool, "missing")
		require.Error(t, err)
		require.Nil(t, data)
	})
	t.Run("counts", func(t *testing.T) {
		data, err := storage.GetTransactionCounts(ctx, pool, nil, 24, "", "", "", "")
		require.Error(t, err)
		require.Nil(t, data)
	})
	t.Run("percentiles", func(t *testing.T) {
		data, err := storage.GetTransactionTimeseries(ctx, pool, nil, 24, "", "", "", "")
		require.Error(t, err)
		require.Nil(t, data)
	})
	t.Run("grouping", func(t *testing.T) {
		data, created, regressed, err := storage.GroupEvent(ctx, pool, "missing", "fp", "Title", "error", "", "")
		require.ErrorContains(t, err, "begin grouping")
		require.Nil(t, data)
		require.False(t, created)
		require.False(t, regressed)
	})
}

type cancelGroupingQuery struct {
	match string
	hit   atomic.Bool
}

func (c *cancelGroupingQuery) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, c.match) {
		c.hit.Store(true)
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx
	}
	return ctx
}
func (*cancelGroupingQuery) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestGroupingCancellationLeavesEventRetryable(t *testing.T) {
	for _, stage := range []string{"pg_advisory_xact_lock_shared", "SELECT project_id, timestamp", "INSERT INTO issues", "commit"} {
		t.Run(stage, func(t *testing.T) {
			ids := groupEventFixture(t, 1)
			ctx := context.Background()
			trace := &cancelGroupingQuery{match: stage}
			cfg := testPool.Config()
			cfg.ConnConfig.Tracer = trace
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			require.NoError(t, err)
			defer pool.Close()
			issue, created, regressed, err := groupOne(ctx, pool, ids[0])
			require.Error(t, err)
			require.Nil(t, issue)
			require.False(t, created)
			require.False(t, regressed)
			require.True(t, trace.hit.Load())
			var grouped bool
			require.NoError(t, testPool.QueryRow(ctx, "SELECT issue_id IS NOT NULL FROM events WHERE id=$1", ids[0]).Scan(&grouped))
			require.False(t, grouped)
			var count int
			require.NoError(t, testPool.QueryRow(ctx, "SELECT count(*) FROM issues").Scan(&count))
			require.Zero(t, count)
			issue, created, _, err = groupOne(ctx, testPool, ids[0])
			require.NoError(t, err)
			require.True(t, created)
			require.EqualValues(t, 1, issue.EventCount)
		})
	}
}

func TestMembershipMovesRespectCancelledLocks(t *testing.T) {
	ids := groupEventFixture(t, 1)
	ctx := context.Background()
	issue, _, _, err := groupOne(ctx, testPool, ids[0])
	require.NoError(t, err)
	trace := &cancelGroupingQuery{match: "pg_advisory_xact_lock("}
	cfg := testPool.Config()
	cfg.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()
	_, err = storage.MergeIssues(ctx, pool, issue.ID, []string{issue.ID})
	require.ErrorContains(t, err, "lock issue membership")
	_, err = storage.UnmergeFingerprints(ctx, pool, issue.ID, []string{"same-fingerprint"})
	require.ErrorContains(t, err, "lock issue membership")
	require.True(t, trace.hit.Load())
	var count int
	require.NoError(t, testPool.QueryRow(ctx, "SELECT event_count FROM issues WHERE id=$1", issue.ID).Scan(&count))
	require.Equal(t, 1, count)
}
