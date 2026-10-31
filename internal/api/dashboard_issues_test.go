package api_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

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

func TestAttentionIssuesSplitsRegressedAndNew(t *testing.T) {
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "dashboard-attention", "Attention")
	require.NoError(t, err)
	t.Cleanup(func() { testPool.Exec(ctx, "DELETE FROM projects WHERE id=$1", p.ID) })
	var assignee string
	require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO users(email,name,password_hash) VALUES ('attention@example.com','Ada','x') RETURNING id`).Scan(&assignee))
	t.Cleanup(func() { testPool.Exec(ctx, "DELETE FROM users WHERE id=$1", assignee) })
	insertSeen := func(title, status, firstSeen, lastSeen string, regressedAt any, assigneeID any) {
		_, err := testPool.Exec(ctx, `INSERT INTO issues(project_id,fingerprint,title,first_seen,last_seen,event_count,status,regressed_at,assignee_id,first_release)
			VALUES ($1,$2,$2,NOW()-$3::interval,NOW()-$4::interval,3,$5,$6,$7,'2.0.0')`, p.ID, title, firstSeen, lastSeen, status, regressedAt, assigneeID)
		require.NoError(t, err)
	}
	insert := func(title, status, firstSeen string, regressedAt any, assigneeID any) {
		insertSeen(title, status, firstSeen, "0 seconds", regressedAt, assigneeID)
	}
	insert("regressed-old", "regressed", "10 days", time.Now().Add(-2*time.Hour), nil)
	insertSeen("regressed-quiet", "regressed", "10 days", "3 days", time.Now().Add(-4*24*time.Hour), nil)
	insert("regressed-recent", "regressed", "10 days", time.Now().Add(-time.Hour), assignee)
	insert("new-today", "open", "2 hours", nil, nil)
	insert("new-this-week", "open", "3 days", nil, nil)
	insert("resolved-today", "resolved", "1 hour", nil, nil)
	token := bearerToken(t, p.ID)

	get := func(query string) storage.AttentionIssues {
		req := httptest.NewRequest("GET", "/api/issues/attention?"+query, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		globalHandler().ServeHTTP(rec, req)
		require.Equal(t, 200, rec.Code, rec.Body.String())
		var body storage.AttentionIssues
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		return body
	}

	day := get("project_id=" + p.ID)
	require.Equal(t, 2, day.RegressedTotal)
	require.Equal(t, []string{"regressed-recent", "regressed-old"}, []string{day.Regressed[0].Title, day.Regressed[1].Title})
	require.Equal(t, "Ada", *day.Regressed[0].Assignee)
	require.Nil(t, day.Regressed[1].Assignee)
	require.Equal(t, 1, day.NewTotal)
	require.Equal(t, "new-today", day.New[0].Title)
	require.Equal(t, "2.0.0", *day.New[0].FirstRelease)
	require.Len(t, day.New[0].Sparkline, 14)

	week := get("project_id=" + p.ID + "&hours=168")
	require.Equal(t, 2, week.NewTotal)
	require.Equal(t, 3, week.RegressedTotal, "a regression counts once it was seen in the window")
	require.Equal(t, "regressed-quiet", week.Regressed[2].Title)

	from := time.Now().Add(-4 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	to := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano)
	custom := get("project_id=" + p.ID + "&from=" + from + "&to=" + to)
	require.Equal(t, 1, custom.NewTotal)
	require.Equal(t, "new-this-week", custom.New[0].Title)

	visit := time.Now().Add(-90 * time.Minute).UTC().Format(time.RFC3339Nano)
	seen := get("project_id=" + p.ID + "&last_visit=" + visit)
	require.NotNil(t, seen.UnseenRegressed)
	require.Equal(t, 1, *seen.UnseenRegressed, "only regressed-recent regressed after the visit")
	require.Equal(t, 0, *seen.UnseenNew, "new-today was first seen before the visit")
	require.Nil(t, day.UnseenNew, "unseen counts are omitted without a last visit")

	nobody := get("project_id=" + p.ID + "&user=nobody@example.com")
	require.Zero(t, nobody.RegressedTotal)
	require.Zero(t, nobody.NewTotal)
	require.Empty(t, nobody.New)

	// The issue list's New status shows the same issues the briefing calls new.
	_, err = testPool.Exec(ctx, `INSERT INTO events(project_id,issue_id,timestamp,payload)
		SELECT project_id,id,NOW()-INTERVAL '1 hour','{}'::jsonb FROM issues WHERE project_id=$1`, p.ID)
	require.NoError(t, err)
	dayFrom := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano)
	dayTo := time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	req := httptest.NewRequest("GET", "/api/issues?status=new&project_id="+p.ID+"&from="+dayFrom+"&to="+dayTo, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var list struct {
		Issues []storage.Issue `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	titles := []string{}
	for _, issue := range list.Issues {
		titles = append(titles, issue.Title)
	}
	require.Equal(t, []string{"new-today"}, titles)
}
