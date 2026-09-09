package api_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestDashboardIssuesScopeCountsAndSparklines(t *testing.T) {
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "dashboard-issue-scope", "Dashboard")
	require.NoError(t, err)
	t.Cleanup(func() { testPool.Exec(ctx, "DELETE FROM projects WHERE id=$1", p.ID) })
	for _, status := range []string{"open", "regressed", "resolved"} {
		var id string
		require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO issues(project_id,fingerprint,title,first_seen,last_seen,event_count,status) VALUES ($1,$2,$2,NOW(),NOW(),2,$2) RETURNING id`, p.ID, status).Scan(&id))
		_, err = testPool.Exec(ctx, `INSERT INTO events(project_id,issue_id,timestamp,payload) SELECT $1,$2,NOW(),'{}' FROM generate_series(1,2)`, p.ID, id)
		require.NoError(t, err)
	}
	token := bearerToken(t, p.ID)
	for _, auth := range []bool{false, true} {
		req := httptest.NewRequest("GET", "/api/issues/overview?project_id="+testProject.ID, nil)
		if auth {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		globalHandler().ServeHTTP(rec, req)
		if !auth {
			require.Equal(t, 401, rec.Code)
			continue
		}
		require.Equal(t, 200, rec.Code)
		var body struct {
			Issues []map[string]any `json:"issues"`
			Total  int              `json:"total"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Equal(t, 2, body.Total)
		require.Len(t, body.Issues, 2)
		for _, issue := range body.Issues {
			require.Equal(t, p.ID, issue["project_id"])
			require.NotContains(t, issue, "user_count")
			line := issue["sparkline"].([]any)
			require.Len(t, line, 14)
			sum := float64(0)
			for _, v := range line {
				sum += v.(float64)
			}
			require.Equal(t, float64(2), sum)
		}
	}
}
