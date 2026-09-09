package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestInvestigationBoundsAcrossTelemetry(t *testing.T) {
	p := setupProjectForTxns(t)
	from := time.Date(2026, 3, 8, 6, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Hour)
	ctx := storage.WithTimeRange(context.Background(), storage.TimeRange{From: from, To: to})
	timestamps := []time.Time{from.Add(-time.Microsecond), from, from.Add(time.Hour), to, to.Add(time.Microsecond)}
	for i, ts := range timestamps {
		tx := seedTransaction(t, p.ID, "GET /boundary", 10, ts)
		_, err := testPool.Exec(ctx, "UPDATE transactions SET environment='preview' WHERE id=$1", tx.ID)
		require.NoError(t, err)
		seedSpan(t, tx.ID, string(rune('a'+i))+"000000000000000", "db.query", "SELECT boundary", 10, ts)
		seedLog(t, p.ID, "info", "boundary", ts, "preview")
	}
	rows, err := storage.ListAllTransactions(ctx, testPool, storage.TransactionFilter{ProjectIDs: []string{p.ID}, Environment: "preview"})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	summaries, err := storage.ListTransactionSummaries(ctx, testPool, []string{p.ID}, 24, 0, "preview", "", "", "", "")
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	require.EqualValues(t, 2, summaries[0].SampleCount)
	chart, err := storage.GetTransactionTimeseries(ctx, testPool, []string{p.ID}, 24, "preview", "", "", "")
	require.NoError(t, err)
	total := 0
	for _, bucket := range chart.Buckets {
		total += int(bucket.Count)
	}
	require.Equal(t, 2, total)
	logs, _, err := storage.ListLogs(ctx, testPool, storage.LogFilter{ProjectIDs: []string{p.ID}, Environment: "preview"})
	require.NoError(t, err)
	require.Len(t, logs, 2)
	spans, err := storage.GetSpanSummaries(ctx, testPool, "db", []string{p.ID}, 24, "", "")
	require.NoError(t, err)
	require.Len(t, spans, 1)
	require.EqualValues(t, 2, spans[0].SampleCount)
	samples, err := storage.GetSpanSamples(ctx, testPool, "db.query", "SELECT boundary", []string{p.ID}, 24, "", "")
	require.NoError(t, err)
	require.Len(t, samples, 2)
}

func TestInvestigationIssuesUseMatchingEvents(t *testing.T) {
	p := setupProjectForTxns(t)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	ctx := storage.WithTimeRange(context.Background(), storage.TimeRange{From: from, To: to})
	issue, _, _, err := storage.UpsertIssue(ctx, testPool, p.ID, "bounded", "Bounded issue", "error", "error", "production", "", to.Add(time.Hour))
	require.NoError(t, err)
	for _, row := range []struct {
		at  time.Time
		env string
	}{{from.Add(-time.Microsecond), "preview"}, {from, "preview"}, {from.Add(time.Hour), "preview"}, {from.Add(time.Hour), "production"}, {to, "preview"}, {to.Add(time.Hour), "production"}} {
		_, err = testPool.Exec(ctx, `INSERT INTO events(project_id,issue_id,timestamp,payload) VALUES($1,$2,$3,jsonb_build_object('environment',$4::text,'user',jsonb_build_object('id','user-1')))`, p.ID, issue.ID, row.at, row.env)
		require.NoError(t, err)
	}
	filter := storage.IssueFilter{ProjectIDs: []string{p.ID}, Environment: "preview"}
	count, err := storage.CountAllIssues(ctx, testPool, filter)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	issues, err := storage.ListAllIssues(ctx, testPool, filter)
	require.NoError(t, err)
	require.Len(t, issues, 1)
	require.EqualValues(t, 2, issues[0].EventCount)
	require.EqualValues(t, 1, issues[0].UserCount)
	require.Equal(t, "preview", *issues[0].Environment)
	require.True(t, from.Add(time.Hour).Equal(issues[0].LastSeen))
	total := 0
	for _, n := range issues[0].Sparkline {
		total += n
	}
	require.Equal(t, 2, total)
	require.Len(t, issues[0].Sparkline, 24)
	filter.CursorTime = &issues[0].LastSeen
	filter.CursorID = &issues[0].ID
	next, err := storage.ListAllIssues(ctx, testPool, filter)
	require.NoError(t, err)
	require.Empty(t, next)
	filter.CursorTime, filter.CursorID = nil, nil
	allCtx := storage.WithTimeRange(context.Background(), storage.TimeRange{To: to.Add(2 * time.Hour), AllTime: true})
	all, err := storage.ListAllIssues(allCtx, testPool, filter)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.EqualValues(t, 4, all[0].EventCount)
	total = 0
	for _, n := range all[0].Sparkline {
		total += n
	}
	require.Equal(t, 4, total)

}

func TestInvestigationLogCountsAndReleaseCharts(t *testing.T) {
	p := setupProjectForTxns(t)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ctx := storage.WithTimeRange(context.Background(), storage.TimeRange{From: from, To: from.Add(time.Hour)})
	seedLog(t, p.ID, "info", "inside", from.Add(time.Minute), "preview")
	seedLog(t, p.ID, "info", "outside", from.Add(-time.Minute), "preview")
	count, err := storage.CountLogs(ctx, testPool, storage.LogFilter{ProjectIDs: []string{p.ID}})
	require.NoError(t, err)
	require.Equal(t, 1, count)
	hit, err := storage.LogsReachThreshold(ctx, testPool, storage.LogFilter{ProjectIDs: []string{p.ID}}, 2)
	require.NoError(t, err)
	require.False(t, hit)
	tx := seedTransaction(t, p.ID, "release-scoped", 10, from.Add(time.Minute))
	_, err = testPool.Exec(ctx, "UPDATE transactions SET release='v2' WHERE id=$1", tx.ID)
	require.NoError(t, err)
	for _, release := range []string{"v1", "v2"} {
		chart, err := storage.GetTransactionTimeseries(ctx, testPool, []string{p.ID}, 1, "", "", "", "", release)
		require.NoError(t, err)
		if release == "v1" {
			require.Empty(t, chart.Buckets)
		} else {
			require.Len(t, chart.Buckets, 1)
			require.EqualValues(t, 1, chart.Buckets[0].Count)
		}
	}
}
