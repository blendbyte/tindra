package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestReleaseHealthMatchesPageWithoutTransactions(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()
	var projects []string
	for _, slug := range []string{"health-a", "health-b"} {
		p, err := storage.CreateProject(ctx, testPool, slug, slug)
		require.NoError(t, err)
		projects = append(projects, p.ID)
		_, err = testPool.Exec(ctx, `INSERT INTO releases(project_id,version,deployed_at) SELECT $1,'v'||i,NOW()-i*interval '1 hour' FROM generate_series(0,7) i`, p.ID)
		require.NoError(t, err)
		issue, _, _, err := storage.UpsertIssue(ctx, testPool, p.ID, "health", "Health", "error", "error", "", "v0", time.Now())
		require.NoError(t, err)
		_, err = testPool.Exec(ctx, "UPDATE issues SET status='regressed' WHERE id=$1", issue.ID)
		require.NoError(t, err)
		_, err = testPool.Exec(ctx, `INSERT INTO events(project_id,issue_id,timestamp,payload) SELECT $1,$2,NOW(),'{"release":"v0"}' FROM generate_series(1,2)`, p.ID, issue.ID)
		require.NoError(t, err)
	}
	for _, ids := range [][]string{nil, {projects[0]}, {projects[1]}, projects} {
		full, err := storage.ListReleases(ctx, testPool, storage.ReleaseFilter{ProjectIDs: ids, Limit: 50})
		require.NoError(t, err)
		lock, err := testPool.Begin(ctx)
		require.NoError(t, err)
		_, err = lock.Exec(ctx, "LOCK TABLE transactions IN ACCESS EXCLUSIVE MODE")
		require.NoError(t, err)
		timed, cancel := context.WithTimeout(ctx, 2*time.Second)
		health, err := storage.ListRecentReleaseHealth(timed, testPool, ids)
		cancel()
		require.NoError(t, lock.Rollback(ctx))
		require.NoError(t, err)
		require.Len(t, health, 5)
		for i, r := range health {
			f := full[i]
			require.Equal(t, f.ID, r.ID)
			require.Equal(t, f.ProjectID, r.ProjectID)
			require.Equal(t, f.Version, r.Version)
			require.Equal(t, f.DeployedAt, r.DeployedAt)
			require.Equal(t, f.NewIssues, r.NewIssues)
			require.Equal(t, f.RegressedIssues, r.RegressedIssues)
			if r.Version == "v0" {
				require.Equal(t, 1, r.NewIssues)
				require.Equal(t, 1, r.RegressedIssues)
			}
		}
	}
	empty, err := storage.ListRecentReleaseHealth(ctx, testPool, []string{"00000000-0000-0000-0000-000000000000"})
	require.NoError(t, err)
	require.NotNil(t, empty)
	require.Empty(t, empty)
}
