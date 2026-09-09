package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestProjectListsRespectBearerScope(t *testing.T) {
	other, err := storage.CreateProject(context.Background(), testPool, "scope-review", "Scope review")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", other.ID) })
	token := bearerToken(t, testProject.ID)
	for _, path := range []string{"/api/projects", "/api/projects/metadata"} {
		req := httptest.NewRequest(http.MethodGet, path+"?project_id="+other.ID, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		globalHandler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var projects []struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &projects))
		require.Len(t, projects, 1, path)
		require.Equal(t, testProject.ID, projects[0].ID)
	}
}
