package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestInvestigationValidatesBounds(t *testing.T) {
	handler := globalHandler()
	for _, query := range []string{"from=bad&to=2026-01-02T00:00:00Z", "from=2026-01-01T00:00:00Z", "from=2026-01-02T00:00:00Z&to=2026-01-01T00:00:00Z", "from=2026-01-01T00:00:00Z&to=2026-05-01T00:00:00Z"} {
		req := httptest.NewRequest(http.MethodGet, "/api/logs?"+query, nil)
		req.AddCookie(authCookie())
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/logs?environment=preview&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}
func TestInvestigationEnvironmentMetadata(t *testing.T) {
	handler := globalHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/environments?project_id="+testProject.ID, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestNinetyDayInvestigationEndpoints(t *testing.T) {
	for _, path := range []string{"/api/logs", "/api/transactions/summaries", "/api/transactions/timeseries", "/api/vitals", "/api/vitals/pages"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path+"?hours=2160&from=2026-01-01T00:00:00Z&to=2026-04-01T00:00:00Z", nil)
			req.AddCookie(authCookie())
			rec := httptest.NewRecorder()
			globalHandler().ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		})
	}
}

func TestEnvironmentsAcrossTablesAndProjects(t *testing.T) {
	ctx := context.Background()
	var ids []string
	for _, slug := range []string{"env-walk-a", "env-walk-b", "env-walk-c"} {
		p, err := storage.CreateProject(ctx, testPool, slug, slug)
		require.NoError(t, err)
		t.Cleanup(func() { testPool.Exec(ctx, "DELETE FROM projects WHERE id=$1", p.ID) })
		ids = append(ids, p.ID)
	}
	a, b, c := ids[0], ids[1], ids[2]
	event := func(project string, env any) {
		t.Helper()
		_, err := testPool.Exec(ctx, `INSERT INTO events(project_id,timestamp,payload) VALUES ($1,NOW(),jsonb_build_object('environment',$2::text))`, project, env)
		require.NoError(t, err)
	}
	transaction := func(project string, env any) {
		t.Helper()
		_, err := testPool.Exec(ctx, `INSERT INTO transactions(project_id,transaction,duration_ms,start_timestamp,timestamp,environment) VALUES ($1,'/env',1,NOW(),NOW(),$2)`, project, env)
		require.NoError(t, err)
	}
	logLine := func(project string, env any) {
		t.Helper()
		_, err := testPool.Exec(ctx, `INSERT INTO logs(project_id,timestamp,body,environment) VALUES ($1,NOW(),'env',$2)`, project, env)
		require.NoError(t, err)
	}
	for range 3 {
		event(a, "envwalk-prod")
		transaction(a, "envwalk-prod")
		logLine(a, "envwalk-prod")
	}
	event(a, "envwalk-staging")
	event(a, nil)
	event(a, "")
	transaction(a, nil)
	transaction(a, "")
	logLine(a, nil)
	logLine(a, "")
	transaction(b, "envwalk-preview")
	transaction(b, "envwalk-prod")
	logLine(b, "envwalk-b-logs")
	event(b, "envwalk-Upper")
	event(b, "envwalk-älpha")
	logLine(c, nil)
	logLine(c, "")

	// The previous full-index DISTINCT is the reference for every scope.
	reference := func(scope []string) []string {
		t.Helper()
		rows, err := testPool.Query(ctx, `SELECT DISTINCT environment FROM (
 SELECT environment FROM events WHERE (cardinality($1::uuid[]) = 0 OR project_id = ANY($1::uuid[]))
 UNION SELECT environment FROM transactions WHERE (cardinality($1::uuid[]) = 0 OR project_id = ANY($1::uuid[]))
 UNION SELECT environment FROM logs WHERE (cardinality($1::uuid[]) = 0 OR project_id = ANY($1::uuid[]))
 ) environments WHERE environment IS NOT NULL AND environment <> '' ORDER BY environment`, scope)
		require.NoError(t, err)
		values, err := pgx.CollectRows(rows, pgx.RowTo[string])
		require.NoError(t, err)
		return values
	}
	fetch := func(query, token string) []string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/environments"+query, nil)
		if token == "" {
			req.AddCookie(authCookie())
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		globalHandler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var values []string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &values))
		require.NotNil(t, values)
		return values
	}

	got := fetch("?project_id="+a, "")
	require.Equal(t, []string{"envwalk-prod", "envwalk-staging"}, got)
	require.Equal(t, reference([]string{a}), got)
	got = fetch("?project_id="+a+"&project_id="+b, "")
	// Order follows the database collation, as it always has.
	require.ElementsMatch(t, []string{"envwalk-Upper", "envwalk-b-logs", "envwalk-preview", "envwalk-prod", "envwalk-staging", "envwalk-älpha"}, got)
	require.Equal(t, reference([]string{a, b}), got)
	require.Equal(t, []string{}, fetch("?project_id="+c, ""))
	require.Equal(t, []string{}, fetch("?project_id=00000000-0000-0000-0000-000000000001", ""))
	all := fetch("", "")
	require.Equal(t, reference([]string{}), all)
	require.Subset(t, all, []string{"envwalk-Upper", "envwalk-b-logs", "envwalk-preview", "envwalk-prod", "envwalk-staging", "envwalk-älpha"})
	require.NotContains(t, all, "")

	// A project token only ever sees its own project, whatever is requested.
	token := bearerToken(t, b)
	require.Equal(t, reference([]string{b}), fetch("?project_id="+a, token))
	require.Equal(t, reference([]string{b}), fetch("", token))
}
