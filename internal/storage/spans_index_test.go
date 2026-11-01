package storage_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

// spanQueryRecorder keeps the last statement that read from spans, so a test
// can EXPLAIN exactly what a storage function sent.
type spanQueryRecorder struct {
	mu   sync.Mutex
	sql  string
	args []any
}

func (r *spanQueryRecorder) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "FROM spans") {
		r.mu.Lock()
		r.sql, r.args = data.SQL, data.Args
		r.mu.Unlock()
	}
	return ctx
}

func (r *spanQueryRecorder) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (r *spanQueryRecorder) last() (string, []any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sql, r.args
}

// explainWithoutSeqScan plans the statement with sequential scans disabled. If
// the planner can prove the partial index predicate it picks the index;
// otherwise the plan names some other access path.
func explainWithoutSeqScan(t *testing.T, sql string, args []any) string {
	t.Helper()
	ctx := context.Background()
	conn, err := testPool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()
	_, err = conn.Exec(ctx, "SET enable_seqscan = off")
	require.NoError(t, err)
	defer conn.Exec(ctx, "RESET enable_seqscan") //nolint:errcheck
	rows, err := conn.Query(ctx, "EXPLAIN "+sql, args...)
	require.NoError(t, err)
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan.WriteString(line + "\n")
	}
	require.NoError(t, rows.Err())
	return plan.String()
}

// Every span page query (summaries, chart, samples), with and without a
// project filter, must be able to use its category's partial index. The index
// predicate has to be implied by the query text, so a reworded filter would
// silently fall back to reading every span.
func TestSpanPageQueriesUseCategoryIndexes(t *testing.T) {
	p := setupProjectForSpans(t)
	ctx := context.Background()
	now := time.Now().UTC()
	tx := seedTransaction(t, p.ID, "/plan", 100, now)
	seedSpan(t, tx.ID, "plan-db", "db.query", "SELECT 1", 5, now)

	rec := &spanQueryRecorder{}
	cfg := testPool.Config()
	cfg.ConnConfig.Tracer = rec
	traced, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer traced.Close()

	summaries := func(category string) func([]string) error {
		return func(ps []string) error {
			_, err := storage.GetSpanSummaries(ctx, traced, category, ps, 24, "production", "1.0")
			return err
		}
	}
	samples := func(op string) func([]string) error {
		return func(ps []string) error {
			_, err := storage.GetSpanSamples(ctx, traced, op, "SELECT 1", ps, 24, "", "")
			return err
		}
	}
	cases := []struct {
		name, index string
		run         func([]string) error
	}{
		{"summaries db", "spans_db_start", summaries("db")},
		{"summaries cache", "spans_cache_start", summaries("cache")},
		{"summaries job", "spans_job_start", summaries("job")},
		{"timeseries cache", "spans_cache_start", func(ps []string) error {
			_, err := storage.GetSpanTimeseries(ctx, traced, "cache", ps, 24, "", "")
			return err
		}},
		{"samples db op", "spans_db_start", samples("db.query")},
		{"samples cache op", "spans_cache_start", samples("cache.get")},
		{"samples queue op", "spans_job_start", samples("queue.process")},
		{"samples celery op", "spans_job_start", samples("celery.task")},
	}
	for _, c := range cases {
		for _, projects := range [][]string{{p.ID}, nil} {
			require.NoError(t, c.run(projects), c.name)
			sql, args := rec.last()
			require.NotEmpty(t, sql, c.name)
			plan := explainWithoutSeqScan(t, sql, args)
			require.Contains(t, plan, c.index, "%s (projects %v) plan:\n%s", c.name, projects, plan)
		}
	}
}

// Ops outside the span pages are not in the partial index, but samples for
// them still come back, and so do samples across all projects.
func TestGetSpanSamplesOutsideCategoryIndex(t *testing.T) {
	p := setupProjectForSpans(t)
	ctx := context.Background()
	now := time.Now().UTC()
	tx := seedTransaction(t, p.ID, "/views", 100, now)
	seedSpan(t, tx.ID, "view-1", "view.render", "home.blade.php", 7, now)
	seedSpan(t, tx.ID, "db-1", "db.query", "SELECT 1", 3, now)

	views, err := storage.GetSpanSamples(ctx, testPool, "view.render", "home.blade.php", nil, 24, "", "")
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.Equal(t, "view-1", views[0].SpanID)

	dbAll, err := storage.GetSpanSamples(ctx, testPool, "db.query", "SELECT 1", nil, 24, "", "")
	require.NoError(t, err)
	require.Len(t, dbAll, 1)
	dbOne, err := storage.GetSpanSamples(ctx, testPool, "db.query", "SELECT 1", []string{p.ID}, 24, "", "")
	require.NoError(t, err)
	require.Len(t, dbOne, 1)
	none, err := storage.GetSpanSamples(ctx, testPool, "db.query", "SELECT 1", []string{"00000000-0000-0000-0000-000000000000"}, 24, "", "")
	require.NoError(t, err)
	require.Empty(t, none)
}

// Without a project filter the summaries cover every project.
func TestGetSpanSummariesAcrossAllProjects(t *testing.T) {
	p := setupProjectForSpans(t)
	ctx := context.Background()
	other, err := storage.CreateProject(ctx, testPool, "spans-other", "Spans Other")
	require.NoError(t, err)
	now := time.Now().UTC()
	seedSpan(t, seedTransaction(t, p.ID, "/a", 100, now).ID, "a-db", "db.query", "SELECT a", 5, now)
	seedSpan(t, seedTransaction(t, other.ID, "/b", 100, now).ID, "b-db", "db.query", "SELECT b", 5, now)

	all, err := storage.GetSpanSummaries(ctx, testPool, "db", nil, 24, "", "")
	require.NoError(t, err)
	descriptions := []string{}
	for _, s := range all {
		descriptions = append(descriptions, s.Description)
	}
	require.ElementsMatch(t, []string{"SELECT a", "SELECT b"}, descriptions)
}
