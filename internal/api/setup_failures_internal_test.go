package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/sourcemaps"
	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
)

type failSetupQuery struct {
	match string
	skip  int
	hits  int
}

func (f *failSetupQuery) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, f.match) {
		f.hits++
		if f.hits > f.skip {
			ctx, cancel := context.WithCancel(ctx)
			cancel()
			return ctx
		}
	}
	return ctx
}
func (*failSetupQuery) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestSetupEndpointsReportPartialDatabaseFailures(t *testing.T) {
	ctx := t.Context()
	pool, cleanup := testutil.SetupDB(ctx)
	defer cleanup()
	p, err := storage.CreateProject(ctx, pool, "setup-failures", "Setup")
	require.NoError(t, err)
	check, err := storage.StartSetupCheck(ctx, pool, p.ID)
	require.NoError(t, err)
	var eventID string
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO events(project_id,timestamp,payload) VALUES($1,now(),jsonb_build_object('tags',jsonb_build_object('tindra_setup',$2::text),'release','v1','platform','javascript','exception','{"values":[{"stacktrace":{"frames":[{"filename":"app.js","lineno":1}]}}]}'::jsonb)) RETURNING id`, p.ID, check.ID).Scan(&eventID))
	for _, tc := range []struct {
		name, match string
		skip        int
		handler     string
	}{
		{"project list", "SELECT p.id, EXISTS", 0, "list"},
		{"new check", "INSERT INTO project_setup_checks", 0, "start"},
		{"receipts", "SELECT kind, first_received_at", 0, "status"},
		{"observations", "SELECT kind, outcome", 0, "status"},
		{"check lookup", "SELECT id, created_at, expires_at", 0, "status"},
		{"recent example", "SELECT id, issue_id, received_at", 0, "status"},
		{"test example", "SELECT id, issue_id, received_at", 1, "status"},
		{"profile link", "SELECT t.id FROM", 0, "status"},
		{"map count", "SELECT count(*) FROM sourcemaps", 0, "status"},
		{"event payload", "SELECT payload FROM events", 0, "verify"},
		{"map lookup", "FROM sourcemaps", 0, "verify"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trace := &failSetupQuery{match: tc.match, skip: tc.skip}
			cfg := pool.Config()
			cfg.ConnConfig.Tracer = trace
			failing, err := pgxpool.NewWithConfig(ctx, cfg)
			require.NoError(t, err)
			defer failing.Close()
			ro := &router{pool: failing, smStore: sourcemaps.NewStore(t.TempDir(), failing)}
			req := httptest.NewRequest("GET", "/?check_id="+check.ID+"&event_id="+eventID, nil)
			rc := chi.NewRouteContext()
			rc.URLParams.Add("projectSlug", p.Slug)
			req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rc))
			rec := httptest.NewRecorder()
			switch tc.handler {
			case "list":
				ro.handleSetupProjects(rec, req)
			case "start":
				ro.handleStartSetupCheck(rec, req)
			case "status":
				ro.handleSetupStatus(rec, req)
			case "verify":
				ro.handleVerifySetupSourcemaps(rec, req)
			}
			require.Greater(t, trace.hits, tc.skip)
			require.Equal(t, 500, rec.Code, rec.Body.String())
			require.Equal(t, "could not check project setup\n", rec.Body.String())
		})
	}
	// A missing project must stop every handler before a project-scoped query.
	ro := &router{pool: pool}
	for _, h := range []http.HandlerFunc{ro.handleStartSetupCheck, ro.handleSetupStatus, ro.handleVerifySetupSourcemaps} {
		req := httptest.NewRequest("GET", "/", nil)
		rc := chi.NewRouteContext()
		rc.URLParams.Add("projectSlug", "missing")
		req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rc))
		rec := httptest.NewRecorder()
		h(rec, req)
		require.Equal(t, 404, rec.Code)
	}
	for _, tc := range []struct {
		configured bool
		id         string
		want       int
	}{{false, eventID, 503}, {true, "invalid", 400}, {true, uuid.NewString(), 404}} {
		ro := &router{pool: pool}
		if tc.configured {
			ro.smStore = sourcemaps.NewStore(t.TempDir(), pool)
		}
		req := httptest.NewRequest("GET", "/?event_id="+tc.id, nil)
		rc := chi.NewRouteContext()
		rc.URLParams.Add("projectSlug", p.Slug)
		req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rc))
		rec := httptest.NewRecorder()
		ro.handleVerifySetupSourcemaps(rec, req)
		require.Equal(t, tc.want, rec.Code, rec.Body.String())
	}
	// A schema or decoding mismatch must fail the response instead of
	// presenting a partial list as complete.
	_, err = pool.Exec(ctx, `ALTER TABLE projects RENAME TO setup_real_projects; CREATE VIEW projects AS SELECT NULL::uuid AS id`)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	ro.handleSetupProjects(rec, httptest.NewRequest("GET", "/", nil))
	require.Equal(t, 500, rec.Code)
	require.Equal(t, "could not check project setup\n", rec.Body.String())

}
