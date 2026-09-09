package storage_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestDashboardIssuesPreserveRecentPageSelection(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()
	var projects []string
	for _, slug := range []string{"dashboard-a", "dashboard-b"} {
		p, err := storage.CreateProject(ctx, testPool, slug, slug)
		require.NoError(t, err)
		projects = append(projects, p.ID)
		_, err = testPool.Exec(ctx, `INSERT INTO issues(project_id,fingerprint,title,level,first_seen,last_seen,event_count,status)
 SELECT $1,'fp'||i,'Issue '||i,'warning',NOW(),NOW()-i*interval '1 hour',i/3,
 CASE WHEN i%2=0 THEN 'regressed' ELSE 'open' END FROM generate_series(1,60) i`, p.ID)
		require.NoError(t, err)
		_, err = testPool.Exec(ctx, `INSERT INTO issues(project_id,fingerprint,title,first_seen,last_seen,event_count,status) VALUES ($1,'closed','Closed',NOW(),NOW(),999999,'resolved')`, p.ID)
		require.NoError(t, err)
	}
	for _, ids := range [][]string{nil, {projects[0]}, projects} {
		full, err := storage.ListAllIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: ids, Status: "open", Limit: 50})
		require.NoError(t, err)
		require.Len(t, full, 50)
		sort.SliceStable(full, func(i, j int) bool { return full[i].EventCount > full[j].EventCount })
		lock, err := testPool.Begin(ctx)
		require.NoError(t, err)
		_, err = lock.Exec(ctx, "LOCK TABLE events IN ACCESS EXCLUSIVE MODE")
		require.NoError(t, err)
		timed, cancel := context.WithTimeout(ctx, 2*time.Second)
		small, err := storage.ListDashboardIssues(timed, testPool, ids)
		cancel()
		require.NoError(t, lock.Rollback(ctx))
		require.NoError(t, err)
		require.Len(t, small, 5)
		for i, issue := range small {
			require.Equal(t, full[i].ID, issue.ID)
			require.Equal(t, full[i].ProjectID, issue.ProjectID)
			require.Equal(t, full[i].Title, issue.Title)
			require.Equal(t, full[i].Level, issue.Level)
			require.Equal(t, full[i].EventCount, issue.EventCount)
		}
	}
	empty, err := storage.ListDashboardIssues(ctx, testPool, []string{"00000000-0000-0000-0000-000000000000"})
	require.NoError(t, err)
	require.NotNil(t, empty)
	require.Empty(t, empty)
}
