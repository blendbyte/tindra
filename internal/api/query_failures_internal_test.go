package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/blendbyte/tindra/internal/testutil"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestOptimizedEndpointsReportDatabaseFailure(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://unused@localhost:1/unused?sslmode=disable")
	require.NoError(t, err)
	pool.Close()
	ro := &router{pool: pool}
	for name, handler := range map[string]http.HandlerFunc{
		"project metadata": ro.handleListProjectMetadata,
		"release metadata": ro.handleListReleaseMetadata,
		"release health":   ro.handleRecentReleaseHealth,
		"dashboard":        ro.handleDashboardIssues,
		"instance health":  ro.handleGetInstanceHealth,
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			require.Equal(t, http.StatusInternalServerError, rec.Code)
			require.Equal(t, "internal error\n", rec.Body.String())
			require.NotContains(t, rec.Body.String(), "postgres")
		})
	}
}

type cancelDashboardQuery struct {
	match string
	hit   atomic.Bool
}

func (c *cancelDashboardQuery) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, c.match) {
		c.hit.Store(true)
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx
	}
	return ctx
}
func (*cancelDashboardQuery) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestDashboardPartialFailures(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := testutil.SetupDB(ctx)
	defer cleanup()
	var project string
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO projects(slug,name,public_key) VALUES('partial','Partial','partial') RETURNING id`).Scan(&project))
	_, err := pool.Exec(ctx, `INSERT INTO issues(project_id,fingerprint,title,status,event_count,first_seen,last_seen) VALUES($1,'partial','Useful issue','open',1,now(),now())`, project)
	require.NoError(t, err)
	for _, tc := range []struct {
		name, match string
		status      int
	}{{"issue listing", "WITH recent AS", 500}, {"sparklines", "SELECT issue_id::text", 200}} {
		t.Run(tc.name, func(t *testing.T) {
			trace := &cancelDashboardQuery{match: tc.match}
			cfg := pool.Config()
			cfg.ConnConfig.Tracer = trace
			failing, err := pgxpool.NewWithConfig(ctx, cfg)
			require.NoError(t, err)
			defer failing.Close()
			ro := &router{pool: failing}
			rec := httptest.NewRecorder()
			ro.handleDashboardIssues(rec, httptest.NewRequest("GET", "/", nil))
			require.True(t, trace.hit.Load())
			require.Equal(t, tc.status, rec.Code)
			if tc.status == 200 {
				require.Contains(t, rec.Body.String(), "Useful issue")
				require.Contains(t, rec.Body.String(), `"total":1`)
			} else {
				require.Equal(t, "internal error\n", rec.Body.String())
			}
		})
	}
}
