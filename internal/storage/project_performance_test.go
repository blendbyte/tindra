package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func seedPerformanceRow(t *testing.T, projectID string, duration int, status, env, user string, start time.Time) {
	t.Helper()
	tx := seedTransaction(t, projectID, "/route", duration, start)
	_, err := testPool.Exec(context.Background(),
		"UPDATE transactions SET status=$2, environment=$3, user_identity=NULLIF($4,''), duration_ms=$5 WHERE id=$1",
		tx.ID, status, env, user, duration)
	require.NoError(t, err)
}

func performanceRow(t *testing.T, overview *storage.PerformanceOverview, projectID string) *storage.ProjectPerformance {
	t.Helper()
	for _, row := range overview.Projects {
		if row.ProjectID == projectID {
			return row
		}
	}
	require.Failf(t, "no row", "project %s is not in the overview", projectID)
	return nil
}

func TestGetPerformanceOverview(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()
	busy, err := storage.CreateProject(ctx, testPool, "perf-busy", "Busy")
	require.NoError(t, err)
	quiet, err := storage.CreateProject(ctx, testPool, "perf-quiet", "Quiet")
	require.NoError(t, err)
	now := time.Now()

	// Current window: one failed request in four, in a known latency spread.
	seedPerformanceRow(t, busy.ID, 100, "ok", "prod", "alice", now.Add(-1*time.Hour))
	seedPerformanceRow(t, busy.ID, 200, "ok", "prod", "alice", now.Add(-2*time.Hour))
	seedPerformanceRow(t, busy.ID, 1000, "ok", "prod", "", now.Add(-3*time.Hour))
	seedPerformanceRow(t, busy.ID, 3000, "internal_error", "dev", "", now.Add(-4*time.Hour))
	// Previous window: fast and clean.
	seedPerformanceRow(t, busy.ID, 50, "ok", "prod", "", now.Add(-30*time.Hour))
	seedPerformanceRow(t, busy.ID, 50, "ok", "prod", "", now.Add(-31*time.Hour))
	// A project with only one request still gets its own row.
	seedPerformanceRow(t, quiet.ID, 400, "ok", "prod", "", now.Add(-1*time.Hour))
	// Outside both windows, so it counts nowhere.
	seedPerformanceRow(t, busy.ID, 9000, "internal_error", "prod", "", now.Add(-60*time.Hour))

	overview, err := storage.GetPerformanceOverview(ctx, testPool, nil, 24, "", "")
	require.NoError(t, err)
	require.Len(t, overview.Projects, 2)

	row := performanceRow(t, overview, busy.ID)
	require.Equal(t, int64(4), row.Requests)
	require.InDelta(t, 600, row.P50, 0.01)
	require.InDelta(t, 2700, row.P95, 0.01)
	require.InDelta(t, 0.25, row.FailureRate, 0.0001)
	// Two satisfied (<=500ms), one tolerating (<=2s), one frustrated.
	require.InDelta(t, 2.5/4, row.Apdex, 0.0001)
	require.Equal(t, int64(2), row.PrevRequests)
	require.InDelta(t, 50, row.PrevP95, 0.01)
	require.Zero(t, row.PrevFailureRate)

	require.Len(t, row.Trend, storage.PerformanceTrendBuckets)
	var trendTotal int64
	for _, count := range row.Trend {
		trendTotal += count
	}
	require.Equal(t, row.Requests, trendTotal)
	// The oldest request (4h ago) lands in the slice for that hour, newest last.
	require.Equal(t, int64(1), row.Trend[19])
	require.Equal(t, int64(1), row.Trend[22])

	require.Equal(t, int64(1), performanceRow(t, overview, quiet.ID).Requests)

	// The total is computed over the raw rows, not averaged from the projects.
	require.Equal(t, int64(5), overview.Total.Requests)
	require.Equal(t, int64(2), overview.Total.PrevRequests)
	require.InDelta(t, 400, overview.Total.P50, 0.01)
	require.InDelta(t, 0.2, overview.Total.FailureRate, 0.0001)
	require.Empty(t, overview.Total.ProjectID)
	require.Empty(t, overview.Total.Trend)
}

func TestGetPerformanceOverview_filters(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()
	first, err := storage.CreateProject(ctx, testPool, "perf-first", "First")
	require.NoError(t, err)
	second, err := storage.CreateProject(ctx, testPool, "perf-second", "Second")
	require.NoError(t, err)
	now := time.Now()
	seedPerformanceRow(t, first.ID, 100, "ok", "prod", "alice", now.Add(-time.Hour))
	seedPerformanceRow(t, first.ID, 100, "ok", "dev", "bob", now.Add(-time.Hour))
	seedPerformanceRow(t, second.ID, 100, "ok", "prod", "bob", now.Add(-time.Hour))

	scoped, err := storage.GetPerformanceOverview(ctx, testPool, []string{first.ID}, 24, "", "")
	require.NoError(t, err)
	require.Len(t, scoped.Projects, 1)
	require.Equal(t, int64(2), scoped.Total.Requests)

	env, err := storage.GetPerformanceOverview(ctx, testPool, nil, 24, "prod", "")
	require.NoError(t, err)
	require.Len(t, env.Projects, 2)
	require.Equal(t, int64(2), env.Total.Requests)

	user, err := storage.GetPerformanceOverview(ctx, testPool, []string{first.ID, second.ID}, 24, "prod", "bob")
	require.NoError(t, err)
	require.Len(t, user.Projects, 1)
	require.Equal(t, second.ID, user.Projects[0].ProjectID)
	require.Equal(t, int64(1), user.Projects[0].Trend[22])
}

func TestGetPerformanceOverview_investigationRange(t *testing.T) {
	truncateProjects(t)
	p, err := storage.CreateProject(context.Background(), testPool, "perf-range", "Range")
	require.NoError(t, err)
	to := time.Now().Add(-10 * time.Hour)
	from := to.Add(-2 * time.Hour)
	seedPerformanceRow(t, p.ID, 100, "ok", "prod", "", from.Add(time.Hour))
	seedPerformanceRow(t, p.ID, 100, "ok", "prod", "", from.Add(-time.Hour))
	seedPerformanceRow(t, p.ID, 100, "ok", "prod", "", to.Add(time.Hour))

	ctx := storage.WithTimeRange(context.Background(), storage.TimeRange{From: from, To: to})
	overview, err := storage.GetPerformanceOverview(ctx, testPool, nil, 24, "", "")
	require.NoError(t, err)
	require.Equal(t, int64(1), overview.Total.Requests)
	require.Equal(t, int64(1), overview.Total.PrevRequests)
}

func TestGetPerformanceOverview_empty(t *testing.T) {
	truncateProjects(t)
	overview, err := storage.GetPerformanceOverview(context.Background(), testPool, nil, 24, "", "")
	require.NoError(t, err)
	require.NotNil(t, overview.Projects)
	require.Empty(t, overview.Projects)
	require.Zero(t, overview.Total.Requests)
	require.Zero(t, overview.Total.P95)
}
