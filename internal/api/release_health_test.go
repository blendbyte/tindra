package api_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestRecentReleaseHealthScopeAndShape(t *testing.T) {
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "release-health-scope", "Health")
	require.NoError(t, err)
	t.Cleanup(func() { testPool.Exec(ctx, "DELETE FROM projects WHERE id=$1", p.ID) })
	_, err = testPool.Exec(ctx, `INSERT INTO releases(project_id,version,deployed_at) SELECT $1,'v'||i,NOW()-i*interval '1 hour' FROM generate_series(1,8) i`, p.ID)
	require.NoError(t, err)
	token := bearerToken(t, p.ID)
	for _, auth := range []bool{false, true} {
		req := httptest.NewRequest("GET", "/api/releases/health?project_id="+testProject.ID, nil)
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
			Releases []map[string]any `json:"releases"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Len(t, body.Releases, 5)
		for _, r := range body.Releases {
			require.Equal(t, p.ID, r["project_id"])
			require.Contains(t, r, "new_issues")
			require.Contains(t, r, "regressed_issues")
			require.NotContains(t, r, "tx_count")
			require.NotContains(t, r, "tx_p50")
			require.NotContains(t, r, "tx_error_rate")
		}
	}
}
