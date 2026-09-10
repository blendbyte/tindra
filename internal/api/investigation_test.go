package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
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
