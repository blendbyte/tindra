package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestAppUsersHandler_databaseUnavailable(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://unused:unused@127.0.0.1/unused")
	require.NoError(t, err)
	pool.Close()
	ro := &router{pool: pool}
	for _, path := range []string{"/api/app-users", "/api/app-users?identity=alice"} {
		rec := httptest.NewRecorder()
		ro.handleListAppUsers(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		require.Equal(t, "internal error\n", rec.Body.String())
	}
}
