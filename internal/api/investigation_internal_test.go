package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

type environmentRows struct {
	pgx.Rows
	values                []string
	scanErr, iterationErr error
	closed                bool
	index                 int
}

func (r *environmentRows) Next() bool { r.index++; return r.index <= len(r.values) }
func (r *environmentRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	*dest[0].(*string) = r.values[r.index-1]
	return nil
}
func (r *environmentRows) Err() error { return r.iterationErr }
func (r *environmentRows) Close()     { r.closed = true }

type environmentDB struct {
	rows pgx.Rows
	err  error
	args []any
}

func (db *environmentDB) Query(_ context.Context, _ string, args ...any) (pgx.Rows, error) {
	db.args = args
	return db.rows, db.err
}

func TestEnvironmentQueryFailuresAndEmptyScope(t *testing.T) {
	failure := errors.New("private database detail")
	for _, phase := range []string{"query", "scan", "iteration", "success"} {
		t.Run(phase, func(t *testing.T) {
			rows := &environmentRows{values: []string{"preview", "production"}}
			db := &environmentDB{rows: rows}
			switch phase {
			case "query":
				db.err = failure
			case "scan":
				rows.scanErr = failure
			case "iteration":
				rows.iterationErr = failure
			}
			rec := httptest.NewRecorder()
			serveEnvironments(rec, httptest.NewRequest("GET", "/api/environments", nil), db)
			require.Equal(t, []string{}, db.args[0])
			if phase == "success" {
				require.Equal(t, 200, rec.Code)
				require.JSONEq(t, `["preview","production"]`, rec.Body.String())
			} else {
				require.Equal(t, 500, rec.Code)
				require.NotContains(t, rec.Body.String(), failure.Error())
			}
			if phase != "query" {
				require.True(t, rows.closed)
			}
		})
	}
}

func TestInvestigationAllTimeAndAliases(t *testing.T) {
	for _, tc := range []struct {
		path            string
		status          int
		scoped, allTime bool
	}{
		{"/api/issues?all_time=1&as_of=2026-01-01T00:00:00Z", 204, true, true},
		{"/api/issues/export?all_time=1&as_of=2026-01-01T00:00:00Z", 204, true, true},
		{"/api/issues?all_time=1", 400, false, false},
		{"/api/issues?all_time=1&as_of=0001-01-01T00:00:00Z", 400, false, false},
		{"/api/issues?all_time=1&as_of=2026-01-01T00:00:00Z&from=2025-12-01T00:00:00Z", 400, false, false},
		{"/api/issues?from=2026-01-01T00:00:00Z&to=2026-03-01T00:00:00Z", 204, true, false},
		{"/api/issues/export?from=2026-01-01T00:00:00Z&to=2026-03-01T00:00:00Z", 204, true, false},
		{"/api/logs?env=preview", 204, false, false},
		{"/api/logs?environment=preview", 204, false, false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			called := false
			handler := investigationContext(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				bounds, ok := storage.InvestigationRange(r.Context())
				require.Equal(t, tc.scoped, ok)
				require.Equal(t, tc.allTime, bounds.AllTime)
				if ok {
					require.Equal(t, time.UTC, bounds.To.Location())
				}
				if r.URL.Path == "/api/logs" {
					require.Equal(t, "preview", r.URL.Query().Get("env"))
					require.Equal(t, "preview", r.URL.Query().Get("environment"))
				}
				w.WriteHeader(204)
			}))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
			require.Equal(t, tc.status, rec.Code)
			require.Equal(t, tc.status == 204, called)
		})
	}
}
