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
	require.True(t, to.Add(time.Hour).Equal(issues[0].FirstSeen), "first seen is the issue's own, not the window's first event")
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
	newFilter := filter
	newFilter.Status = "new"
	newFilter.NewSince = &from
	fresh, err := storage.ListAllIssues(ctx, testPool, newFilter)
	require.NoError(t, err)
	require.Len(t, fresh, 1, "an open issue first seen after the window start is new")
	later := to.Add(2 * time.Hour)
	newFilter.NewSince = &later
	fresh, err = storage.ListAllIssues(ctx, testPool, newFilter)
	require.NoError(t, err)
	require.Empty(t, fresh)
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

func TestNinetyDayTelemetryWindow(t *testing.T) {
	p := setupProjectForTxns(t)
	to := time.Now().UTC()
	from := to.Add(-90 * 24 * time.Hour)
	at := to.Add(-60 * 24 * time.Hour)
	tx := seedTransaction(t, p.ID, "historical", 10, at)
	seedSpan(t, tx.ID, "abcdef0123456789", "db.query", "SELECT historical", 10, at)
	seedLog(t, p.ID, "info", "historical", at, "preview")
	ctx := storage.WithTimeRange(context.Background(), storage.TimeRange{From: from, To: to})
	for _, queryContext := range []context.Context{context.Background(), ctx} {
		summaries, err := storage.ListTransactionSummaries(queryContext, testPool, []string{p.ID}, 2160, 0, "", "", "", "", "")
		require.NoError(t, err)
		require.Len(t, summaries, 1)
		chart, err := storage.GetTransactionTimeseries(queryContext, testPool, []string{p.ID}, 2160, "", "", "", "")
		require.NoError(t, err)
		require.Equal(t, "day", chart.BucketSize)
		require.Len(t, chart.Buckets, 1)
		spans, err := storage.GetSpanTimeseries(queryContext, testPool, "db", []string{p.ID}, 2160, "", "")
		require.NoError(t, err)
		require.Equal(t, "day", spans.BucketSize)
		require.Len(t, spans.Buckets, 1)
		samples, err := storage.GetSpanSamples(queryContext, testPool, "db.query", "SELECT historical", []string{p.ID}, 2160, "", "")
		require.NoError(t, err)
		require.Len(t, samples, 1)
	}
	logs, _, err := storage.ListLogs(ctx, testPool, storage.LogFilter{ProjectIDs: []string{p.ID}})
	require.NoError(t, err)
	require.Len(t, logs, 1)
}

func TestSpanInvestigationUserFilter(t *testing.T) {
	p := setupProjectForTxns(t)
	now := time.Now().Add(-time.Minute)
	for i, identity := range []string{"alice", "bob"} {
		tx := seedTransaction(t, p.ID, "users", 10, now)
		_, err := testPool.Exec(context.Background(), "UPDATE transactions SET user_identity=$1 WHERE id=$2", identity, tx.ID)
		require.NoError(t, err)
		seedSpan(t, tx.ID, string(rune('a'+i))+"123456789abcdef", "db.query", "SELECT user", 10, now)
	}
	summaries, err := storage.GetSpanSummaries(context.Background(), testPool, "db", []string{p.ID}, 24, "", "", "alice")
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	require.EqualValues(t, 1, summaries[0].SampleCount)
	chart, err := storage.GetSpanTimeseries(context.Background(), testPool, "db", []string{p.ID}, 24, "", "", "alice")
	require.NoError(t, err)
	require.Len(t, chart.Buckets, 1)
	require.EqualValues(t, 1, chart.Buckets[0].Count)
	samples, err := storage.GetSpanSamples(context.Background(), testPool, "db.query", "SELECT user", []string{p.ID}, 24, "", "", "nobody")
	require.NoError(t, err)
	require.Empty(t, samples)
}

func TestPriorityListsRegressedThenNewThenTheRest(t *testing.T) {
	p := setupProjectForTxns(t)
	to := time.Now().UTC().Truncate(time.Second)
	from := to.Add(-24 * time.Hour)
	ctx := storage.WithTimeRange(context.Background(), storage.TimeRange{From: from, To: to})
	old := to.Add(-72 * time.Hour)
	issue := func(title string, firstSeen, lastEvent time.Time, status string) {
		iss, _, _, err := storage.UpsertIssue(ctx, testPool, p.ID, title, title, "error", "error", "production", "", firstSeen)
		require.NoError(t, err)
		_, err = testPool.Exec(ctx, `UPDATE issues SET status=$2 WHERE id=$1`, iss.ID, status)
		require.NoError(t, err)
		_, err = testPool.Exec(ctx, `INSERT INTO events(project_id,issue_id,timestamp,payload) VALUES($1,$2,$3,'{}')`, p.ID, iss.ID, lastEvent)
		require.NoError(t, err)
	}
	issue("busy-old", old, to.Add(-time.Minute), "open")
	issue("new", to.Add(-2*time.Hour), to.Add(-30*time.Minute), "open")
	issue("quiet-old", old, to.Add(-5*time.Hour), "open")
	issue("regressed", old, to.Add(-10*time.Hour), "regressed")
	issue("new-latest", to.Add(-3*time.Hour), to.Add(-20*time.Minute), "open")
	issue("resolved-new", to.Add(-time.Hour), to.Add(-2*time.Minute), "resolved")

	// Page two at a time, continuing from each page's last issue the way the
	// API's cursor does.
	filter := storage.IssueFilter{ProjectIDs: []string{p.ID}, Priority: true, Limit: 2}
	var titles []string
	for range 4 {
		page, err := storage.ListAllIssues(ctx, testPool, filter)
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		for _, iss := range page {
			titles = append(titles, iss.Title)
		}
		last := page[len(page)-1]
		rank := storage.PriorityRank(last.Status, last.FirstSeen, &from)
		filter.CursorTime, filter.CursorID, filter.CursorRank = &last.LastSeen, &last.ID, &rank
	}
	require.Equal(t, []string{"regressed", "new-latest", "new", "busy-old", "resolved-new", "quiet-old"}, titles,
		"regressed first, then open issues first seen in the window, then the rest by last seen")

	// Without a window only regressions lead.
	all, err := storage.ListAllIssues(context.Background(), testPool, storage.IssueFilter{ProjectIDs: []string{p.ID}, Priority: true})
	require.NoError(t, err)
	require.Equal(t, "regressed", all[0].Title)
}
