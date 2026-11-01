package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/migrations"
)

// releaseCounts returns new and regressed counts per version, plus each
// version's GetReleaseIssues categories.
func releaseCounts(t *testing.T, projectID string) (map[string][2]int, map[string][]string) {
	t.Helper()
	ctx := context.Background()
	list, err := storage.ListReleases(ctx, testPool, storage.ReleaseFilter{ProjectIDs: []string{projectID}, Limit: 50})
	require.NoError(t, err)
	counts := map[string][2]int{}
	cats := map[string][]string{}
	for _, r := range list {
		counts[r.Version] = [2]int{r.NewIssues, r.RegressedIssues}
		issues, err := storage.GetReleaseIssues(ctx, testPool, r.ID)
		require.NoError(t, err)
		for _, i := range issues {
			cats[r.Version] = append(cats[r.Version], i.Category)
		}
	}
	return counts, cats
}

func seedReleases(t *testing.T, projectID string, versions ...string) {
	t.Helper()
	for i, v := range versions {
		_, err := testPool.Exec(context.Background(),
			`INSERT INTO releases(project_id,version,deployed_at) VALUES ($1,$2,NOW()-make_interval(hours => $3))`,
			projectID, v, len(versions)-i)
		require.NoError(t, err)
	}
}

func seedReleaseEvent(t *testing.T, projectID, issueID, release string, ts time.Time) {
	t.Helper()
	_, err := testPool.Exec(context.Background(),
		`INSERT INTO events(project_id,issue_id,timestamp,payload) VALUES ($1,$2,$3,jsonb_build_object('release',$4::text))`,
		projectID, issueID, ts, release)
	require.NoError(t, err)
}

// An issue seen in v1 and v2, resolved, then back in v3 was brought back by
// v3 only. v1 still introduced it and v2 merely saw it.
func TestRegressionCountsOnlyInTheReleaseItCameBackIn(t *testing.T) {
	p := setupProjectForTxns(t)
	ctx := context.Background()
	seedReleases(t, p.ID, "v1", "v2", "v3")
	now := time.Now()

	issue, _, _, err := storage.UpsertIssue(ctx, testPool, p.ID, "back", "Back", "error", "error", "", "v1", now.Add(-3*time.Hour))
	require.NoError(t, err)
	seedReleaseEvent(t, p.ID, issue.ID, "v1", now.Add(-3*time.Hour))
	_, _, _, err = storage.UpsertIssue(ctx, testPool, p.ID, "back", "Back", "error", "error", "", "v2", now.Add(-2*time.Hour))
	require.NoError(t, err)
	seedReleaseEvent(t, p.ID, issue.ID, "v2", now.Add(-2*time.Hour))
	_, err = storage.UpdateIssueStatus(ctx, testPool, p.ID, issue.ID, "resolved", nil)
	require.NoError(t, err)

	_, _, regressed, err := storage.UpsertIssue(ctx, testPool, p.ID, "back", "Back", "error", "error", "", "v3", now.Add(-time.Hour))
	require.NoError(t, err)
	require.True(t, regressed)
	seedReleaseEvent(t, p.ID, issue.ID, "v3", now.Add(-time.Hour))

	counts, cats := releaseCounts(t, p.ID)
	require.Equal(t, [2]int{1, 0}, counts["v1"], "v1 introduced it and did not bring it back")
	require.Equal(t, [2]int{0, 0}, counts["v2"])
	require.Equal(t, [2]int{0, 1}, counts["v3"])
	require.Equal(t, []string{"new"}, cats["v1"])
	require.Equal(t, []string{"ongoing"}, cats["v2"])
	require.Equal(t, []string{"regressed"}, cats["v3"])

	// Later events from another release don't move the regression.
	_, _, _, err = storage.UpsertIssue(ctx, testPool, p.ID, "back", "Back", "error", "error", "", "v2", now)
	require.NoError(t, err)
	counts, _ = releaseCounts(t, p.ID)
	require.Equal(t, [2]int{0, 1}, counts["v3"])
	require.Equal(t, [2]int{0, 0}, counts["v2"])
}

// An ignore that runs out regresses the issue too, in the release of the
// event that ended it.
func TestIgnoreCountRegressionRecordsItsRelease(t *testing.T) {
	p := setupProjectForTxns(t)
	ctx := context.Background()
	seedReleases(t, p.ID, "v1", "v2")
	issue, _, _, err := storage.UpsertIssue(ctx, testPool, p.ID, "loud", "Loud", "error", "error", "", "v1", time.Now().Add(-time.Hour))
	require.NoError(t, err)
	limit := 1
	_, err = storage.UpdateIssueStatus(ctx, testPool, p.ID, issue.ID, "ignored", &storage.IgnoreOptions{CountLimit: &limit})
	require.NoError(t, err)

	_, _, regressed, err := storage.UpsertIssue(ctx, testPool, p.ID, "loud", "Loud", "error", "error", "", "v2", time.Now())
	require.NoError(t, err)
	require.True(t, regressed)
	counts, _ := releaseCounts(t, p.ID)
	require.Equal(t, [2]int{0, 1}, counts["v2"])
	require.Equal(t, [2]int{1, 0}, counts["v1"])
}

// Issues that regressed before the column existed get the release of the
// last event at or before the regression, not of later events.
func TestRegressedReleaseBackfill(t *testing.T) {
	p := setupProjectForTxns(t)
	ctx := context.Background()
	seedReleases(t, p.ID, "v1", "v2", "v3")
	now := time.Now()
	issue, _, _, err := storage.UpsertIssue(ctx, testPool, p.ID, "old", "Old", "error", "error", "", "v1", now.Add(-4*time.Hour))
	require.NoError(t, err)
	seedReleaseEvent(t, p.ID, issue.ID, "v1", now.Add(-4*time.Hour))
	seedReleaseEvent(t, p.ID, issue.ID, "v2", now.Add(-2*time.Hour))
	seedReleaseEvent(t, p.ID, issue.ID, "v3", now.Add(-time.Hour))
	_, err = testPool.Exec(ctx, `UPDATE issues SET status='regressed', regressed_at=$2, regressed_release=NULL WHERE id=$1`,
		issue.ID, now.Add(-90*time.Minute))
	require.NoError(t, err)
	// An issue that is not regressed gets no regression release.
	other, _, _, err := storage.UpsertIssue(ctx, testPool, p.ID, "fine", "Fine", "error", "error", "", "v1", now.Add(-time.Hour))
	require.NoError(t, err)
	seedReleaseEvent(t, p.ID, other.ID, "v1", now.Add(-time.Hour))

	sql, err := migrations.FS.ReadFile("0036_issue_regressed_release.up.sql")
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, string(sql))
	require.NoError(t, err)

	var got, otherGot *string
	require.NoError(t, testPool.QueryRow(ctx, `SELECT regressed_release FROM issues WHERE id=$1`, issue.ID).Scan(&got))
	require.NoError(t, testPool.QueryRow(ctx, `SELECT regressed_release FROM issues WHERE id=$1`, other.ID).Scan(&otherGot))
	require.NotNil(t, got)
	require.Equal(t, "v2", *got)
	require.Nil(t, otherGot)

	counts, _ := releaseCounts(t, p.ID)
	require.Equal(t, [2]int{0, 1}, counts["v2"])
	require.Equal(t, [2]int{0, 0}, counts["v3"])
}
