package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// A failed short ID lookup answers 500 and never reaches the handler.
func TestIssueRefParam_lookupFailure(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://unused@localhost:1/unused?sslmode=disable")
	require.NoError(t, err)
	pool.Close()
	ro := &router{pool: pool}
	reached := false
	h := ro.issueRefParam(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	rc := chi.NewRouteContext()
	rc.URLParams.Add("issueID", "BACKEND-42")
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(context.WithValue(t.Context(), chi.RouteCtxKey, rc))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.False(t, reached)
}
