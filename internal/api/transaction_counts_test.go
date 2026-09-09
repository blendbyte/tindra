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

func TestTransactionCountsAuthAndScope(t *testing.T) {
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "api-counts-scope", "Counts scope")
	require.NoError(t, err)
	t.Cleanup(func() { testPool.Exec(ctx, "DELETE FROM projects WHERE id=$1", p.ID) })
	_, err = testPool.Exec(ctx, `INSERT INTO transactions(project_id,transaction,duration_ms,start_timestamp,timestamp) VALUES ($1,'/counts',123,NOW(),NOW())`, p.ID)
	require.NoError(t, err)
	token := bearerToken(t, p.ID)
	for _, auth := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodGet, "/api/transactions/counts?hours=168&project_id="+testProject.ID, nil)
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
		var result struct {
			Buckets    []map[string]any `json:"buckets"`
			BucketSize string           `json:"bucket_size"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
		require.Equal(t, "hour", result.BucketSize)
		require.Len(t, result.Buckets, 1)
		require.Equal(t, float64(1), result.Buckets[0]["count"])
		require.Len(t, result.Buckets[0], 2)
		require.Contains(t, result.Buckets[0], "time")
	}
}
