package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestUserTransactionAnalytics(t *testing.T) {
	p := setupProjectForTxns(t)
	ctx := context.Background()
	now := time.Now().UTC()
	since := now.Add(-24 * time.Hour)
	for _, row := range []struct {
		name, op, user, env string
		duration, lcp       int
		age                 time.Duration
	}{
		{"/home", "pageload", "alice", "production", 100, 1000, time.Hour},
		{"/other", "navigation", "alice", "production", 300, 3000, time.Hour},
		{"/api", "http.server", "alice", "production", 500, 9000, time.Hour},
		{"/staging", "pageload", "alice", "staging", 1000, 9000, time.Hour},
		{"/bob", "pageload", "bob", "production", 5000, 10000, time.Hour},
		{"/old", "pageload", "alice", "production", 9000, 9000, 40 * 24 * time.Hour},
	} {
		_, err := testPool.Exec(ctx, `INSERT INTO transactions(project_id,transaction,op,user_identity,environment,status,duration_ms,start_timestamp,timestamp,measurements)
  VALUES($1,$2,$3,$4,$5,'ok',$6,$7,$7,jsonb_build_object('lcp',jsonb_build_object('value',$8::int)))`, p.ID, row.name, row.op, row.user, row.env, row.duration, now.Add(-row.age), row.lcp)
		require.NoError(t, err)
	}
	for _, ids := range [][]string{nil, {p.ID}} {
		summaries, err := storage.ListTransactionSummaries(ctx, testPool, ids, 24, 0, "production", "", "", "", "alice")
		require.NoError(t, err)
		require.Len(t, summaries, 3)
		var count int64
		for _, s := range summaries {
			count += s.SampleCount
		}
		require.EqualValues(t, 3, count)
		series, err := storage.GetTransactionTimeseries(ctx, testPool, ids, 24, "production", "", "", "alice")
		require.NoError(t, err)
		require.Len(t, series.Buckets, 1)
		require.EqualValues(t, 3, series.Buckets[0].Count)
		require.Equal(t, 300.0, series.Buckets[0].P50)
		vitals, err := storage.GetWebVitalsSummary(ctx, testPool, ids, since, now, "production", "alice")
		require.NoError(t, err)
		require.EqualValues(t, 2, vitals.LCP.Count)
		require.Equal(t, 2500.0, vitals.LCP.P75)
		require.Equal(t, 0.5, vitals.LCP.PassRate)
		pages, err := storage.GetWebVitalsByPage(ctx, testPool, ids, since, now, "production", "alice")
		require.NoError(t, err)
		require.Len(t, pages, 2)
		names := []string{}
		for _, page := range pages {
			names = append(names, page.Transaction)
		}
		require.ElementsMatch(t, []string{"/home", "/other"}, names)
	}
	// Exercise the project-specific listing with the same combined constraints.
	txns, err := storage.ListTransactions(ctx, testPool, p.ID, storage.TransactionFilter{UserIdentity: "alice", Since: &since, Ops: []string{"pageload", "navigation"}, Environment: "production"})
	require.NoError(t, err)
	require.Len(t, txns, 2)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = storage.GetWebVitalsSummary(cancelled, testPool, nil, since, now, "", "alice")
	require.ErrorIs(t, err, context.Canceled)
	_, err = storage.GetWebVitalsByPage(cancelled, testPool, nil, since, now, "", "alice")
	require.ErrorIs(t, err, context.Canceled)
}
