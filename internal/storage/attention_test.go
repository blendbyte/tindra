package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestListAttentionIssuesGroupsCountsAndLimits(t *testing.T) {
	p := setupProjectForTxns(t)
	ctx := context.Background()
	now := time.Now().UTC()
	insert := func(title, status string, firstSeen, lastSeen time.Time, regressedAt *time.Time, events int) {
		_, err := testPool.Exec(ctx, `INSERT INTO issues(project_id,fingerprint,title,first_seen,last_seen,event_count,status,regressed_at,first_release)
			VALUES ($1,$2,$2,$3,$4,$5,$6,$7,'3.8.0')`, p.ID, title, firstSeen, lastSeen, events, status, regressedAt)
		require.NoError(t, err)
	}
	at := func(d time.Duration) *time.Time { v := now.Add(-d); return &v }
	insert("back-recent", "regressed", now.Add(-72*time.Hour), now.Add(-time.Minute), at(30*time.Minute), 4)
	insert("back-earlier", "regressed", now.Add(-72*time.Hour), now.Add(-2*time.Hour), at(3*time.Hour), 9)
	insert("back-quiet", "regressed", now.Add(-72*time.Hour), now.Add(-48*time.Hour), at(50*time.Hour), 2)
	insert("new-busy", "open", now.Add(-4*time.Hour), now.Add(-time.Minute), nil, 40)
	insert("new-small", "open", now.Add(-20*time.Minute), now.Add(-time.Minute), nil, 3)
	insert("new-other", "open", now.Add(-2*time.Hour), now.Add(-time.Minute), nil, 12)
	insert("old-open", "open", now.Add(-72*time.Hour), now.Add(-time.Minute), nil, 80)
	insert("new-resolved", "resolved", now.Add(-time.Hour), now.Add(-time.Minute), nil, 5)

	filter := storage.IssueFilter{ProjectIDs: []string{p.ID}, Status: "resolved"}
	from, to := now.Add(-24*time.Hour), now.Add(time.Minute)
	got, err := storage.ListAttentionIssues(ctx, testPool, filter, from, to, nil, 2)
	require.NoError(t, err)

	// The caller's status filter is ignored: each group selects its own state.
	require.Equal(t, 2, got.RegressedTotal, "the quiet regression was not seen in the window")
	require.Equal(t, []string{"back-recent", "back-earlier"}, []string{got.Regressed[0].Title, got.Regressed[1].Title})
	require.Equal(t, 3, got.NewTotal)
	require.Len(t, got.New, 2, "rows stop at the limit while totals count every match")
	require.Equal(t, []string{"new-busy", "new-other"}, []string{got.New[0].Title, got.New[1].Title})
	require.Equal(t, "3.8.0", *got.New[0].FirstRelease)
	require.Nil(t, got.UnseenNew)
	require.Nil(t, got.UnseenRegressed)

	visit := now.Add(-time.Hour)
	seen, err := storage.ListAttentionIssues(ctx, testPool, filter, from, to, &visit, 2)
	require.NoError(t, err)
	require.Equal(t, 1, *seen.UnseenRegressed, "only back-recent regressed after the visit")
	require.Equal(t, 1, *seen.UnseenNew, "only new-small appeared after the visit")

	empty, err := storage.ListAttentionIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{p.ID}}, now.Add(-time.Minute), to, &visit, 5)
	require.NoError(t, err)
	require.Empty(t, empty.New)
	require.Zero(t, empty.NewTotal)
	require.Equal(t, 0, *empty.UnseenNew, "an empty group still reports zero unseen")
}

func TestPriorityRank(t *testing.T) {
	since := time.Now().Add(-time.Hour)
	require.Equal(t, 0, storage.PriorityRank("regressed", time.Now(), &since))
	require.Equal(t, 1, storage.PriorityRank("open", time.Now(), &since))
	require.Equal(t, 1, storage.PriorityRank("open", since, &since), "first seen at the window start is new")
	require.Equal(t, 2, storage.PriorityRank("open", since.Add(-time.Second), &since))
	require.Equal(t, 2, storage.PriorityRank("open", time.Now(), nil), "without a window nothing is new")
	require.Equal(t, 2, storage.PriorityRank("resolved", time.Now(), &since))
}

func TestListIssuesStatusNewWithoutWindow(t *testing.T) {
	p := setupProjectForTxns(t)
	ctx := context.Background()
	_, _, _, err := storage.UpsertIssue(ctx, testPool, p.ID, "fresh", "Fresh", "error", "error", "", "", time.Now())
	require.NoError(t, err)
	_, _, _, err = storage.UpsertIssue(ctx, testPool, p.ID, "old", "Old", "error", "error", "", "", time.Now().Add(-72*time.Hour))
	require.NoError(t, err)
	since := time.Now().Add(-time.Hour)
	issues, err := storage.ListAllIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{p.ID}, Status: "new", NewSince: &since})
	require.NoError(t, err)
	require.Len(t, issues, 1)
	require.Equal(t, "Fresh", issues[0].Title)
	n, err := storage.CountAllIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{p.ID}, Status: "new"})
	require.NoError(t, err)
	require.Equal(t, 2, n, "without a lower bound every open issue counts as new")
}

func TestGetIssueCarriesFirstReleaseAndRegression(t *testing.T) {
	p := setupProjectForTxns(t)
	ctx := context.Background()
	issue, _, _, err := storage.UpsertIssue(ctx, testPool, p.ID, "came-back", "Came back", "error", "error", "production", "3.7.0", time.Now().Add(-48*time.Hour))
	require.NoError(t, err)
	detail, err := storage.GetIssue(ctx, testPool, issue.ID)
	require.NoError(t, err)
	require.Equal(t, "3.7.0", *detail.FirstRelease)
	require.Nil(t, detail.RegressedAt)

	_, err = testPool.Exec(ctx, `UPDATE issues SET status='regressed', regressed_at=NOW()-INTERVAL '1 hour' WHERE id=$1`, issue.ID)
	require.NoError(t, err)
	detail, err = storage.GetIssue(ctx, testPool, issue.ID)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().Add(-time.Hour), *detail.RegressedAt, time.Minute)
}
