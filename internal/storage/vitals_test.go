package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

func setupProjectForVitals(t *testing.T) *storage.Project {
	t.Helper()
	truncateProjects(t)
	p, err := storage.CreateProject(context.Background(), testPool, "vitals-proj", "Vitals Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p
}

// seedPageloadTx inserts a pageload transaction with a measurements JSONB blob.
// measurements is a raw JSON string, e.g. `{"lcp":{"value":1200},"fcp":{"value":900}}`.
func seedPageloadTx(t *testing.T, projectID, name, measurements string, start time.Time) {
	t.Helper()
	end := start.Add(500 * time.Millisecond)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO transactions
			(project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, measurements)
		VALUES ($1, $2, 'pageload', 'ok', 500, $3, $4, $5::jsonb)
	`, projectID, name, start, end, measurements); err != nil {
		t.Fatalf("seed pageload tx: %v", err)
	}
}

// TestGetWebVitalsSummary_noData verifies that all counts are zero and pass-rates are
// zero when there are no matching transactions.
func TestGetWebVitalsSummary_noData(t *testing.T) {
	p := setupProjectForVitals(t)
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	to := now.Add(time.Hour)

	summary, err := storage.GetWebVitalsSummary(context.Background(), testPool, []string{p.ID}, from, to, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.LCP.Count != 0 {
		t.Errorf("LCP.Count: expected 0, got %d", summary.LCP.Count)
	}
	if summary.FCP.Count != 0 {
		t.Errorf("FCP.Count: expected 0, got %d", summary.FCP.Count)
	}
	if summary.CLS.Count != 0 {
		t.Errorf("CLS.Count: expected 0, got %d", summary.CLS.Count)
	}
	if summary.INP.Count != 0 {
		t.Errorf("INP.Count: expected 0, got %d", summary.INP.Count)
	}
	if summary.TTFB.Count != 0 {
		t.Errorf("TTFB.Count: expected 0, got %d", summary.TTFB.Count)
	}
}

// TestGetWebVitalsSummary_withData verifies that p75 and pass-rate are computed correctly
// for rows that carry measurements.
func TestGetWebVitalsSummary_withData(t *testing.T) {
	p := setupProjectForVitals(t)
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	to := now.Add(time.Hour)

	// LCP=1200ms (good, threshold=2500), FCP=900ms (good, threshold=1800),
	// CLS=0.05 (good, threshold=0.1), INP=150ms (good, threshold=200), TTFB=400ms (good, threshold=800).
	measurements := `{"lcp":{"value":1200},"fcp":{"value":900},"cls":{"value":0.05},"inp":{"value":150},"ttfb":{"value":400}}`
	seedPageloadTx(t, p.ID, "/home", measurements, now.Add(-1*time.Hour))

	summary, err := storage.GetWebVitalsSummary(context.Background(), testPool, []string{p.ID}, from, to, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.LCP.Count != 1 {
		t.Errorf("LCP.Count: expected 1, got %d", summary.LCP.Count)
	}
	if summary.LCP.P75 <= 0 {
		t.Errorf("LCP.P75: expected > 0, got %f", summary.LCP.P75)
	}
	// All values are within the "good" thresholds so pass-rate should be 1.0.
	if summary.LCP.PassRate != 1.0 {
		t.Errorf("LCP.PassRate: expected 1.0, got %f", summary.LCP.PassRate)
	}
	if summary.INP.Count != 1 {
		t.Errorf("INP.Count: expected 1, got %d", summary.INP.Count)
	}
	if summary.TTFB.Count != 1 {
		t.Errorf("TTFB.Count: expected 1, got %d", summary.TTFB.Count)
	}
}

// TestGetWebVitalsSummary_environmentFilter verifies that the env filter restricts results.
func TestGetWebVitalsSummary_environmentFilter(t *testing.T) {
	p := setupProjectForVitals(t)
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	to := now.Add(time.Hour)

	prodMeasurements := `{"lcp":{"value":1000}}`
	end := now.Add(-1 * time.Hour).Add(500 * time.Millisecond)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO transactions
			(project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, measurements, environment)
		VALUES ($1, '/prod', 'pageload', 'ok', 500, $2, $3, $4::jsonb, 'production')
	`, p.ID, now.Add(-1*time.Hour), end, prodMeasurements); err != nil {
		t.Fatalf("insert prod tx: %v", err)
	}

	// Query for staging should yield count=0.
	staging, err := storage.GetWebVitalsSummary(context.Background(), testPool, []string{p.ID}, from, to, "staging")
	if err != nil {
		t.Fatalf("staging query: %v", err)
	}
	if staging.LCP.Count != 0 {
		t.Errorf("expected 0 staging LCPs, got %d", staging.LCP.Count)
	}

	// Query for production should find the row.
	prod, err := storage.GetWebVitalsSummary(context.Background(), testPool, []string{p.ID}, from, to, "production")
	if err != nil {
		t.Fatalf("production query: %v", err)
	}
	if prod.LCP.Count != 1 {
		t.Errorf("expected 1 production LCP, got %d", prod.LCP.Count)
	}
}

// TestGetWebVitalsSummary_nonPageloadExcluded verifies that transactions with op != pageload/navigation
// are not included in the results.
func TestGetWebVitalsSummary_nonPageloadExcluded(t *testing.T) {
	p := setupProjectForVitals(t)
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	to := now.Add(time.Hour)

	// http.server transaction - must not appear in vitals.
	measurements := `{"lcp":{"value":999}}`
	end := now.Add(-1 * time.Hour).Add(100 * time.Millisecond)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO transactions
			(project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, measurements)
		VALUES ($1, '/api/not-pageload', 'http.server', 'ok', 100, $2, $3, $4::jsonb)
	`, p.ID, now.Add(-1*time.Hour), end, measurements); err != nil {
		t.Fatalf("insert server tx: %v", err)
	}

	summary, err := storage.GetWebVitalsSummary(context.Background(), testPool, []string{p.ID}, from, to, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.LCP.Count != 0 {
		t.Errorf("expected 0 LCPs (http.server excluded), got %d", summary.LCP.Count)
	}
}

// TestGetWebVitalsByPage_noData verifies an empty slice is returned when there are no pageload transactions.
func TestGetWebVitalsByPage_noData(t *testing.T) {
	p := setupProjectForVitals(t)
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	to := now.Add(time.Hour)

	pages, err := storage.GetWebVitalsByPage(context.Background(), testPool, []string{p.ID}, from, to, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pages) != 0 {
		t.Errorf("expected 0 pages, got %d", len(pages))
	}
}

// TestGetWebVitalsByPage_groupsByTransaction verifies that each unique transaction name
// produces one row with correct session count and vitals.
func TestGetWebVitalsByPage_groupsByTransaction(t *testing.T) {
	p := setupProjectForVitals(t)
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	to := now.Add(time.Hour)

	m := `{"lcp":{"value":1200},"inp":{"value":100},"cls":{"value":0.02}}`
	seedPageloadTx(t, p.ID, "/home", m, now.Add(-2*time.Hour))
	seedPageloadTx(t, p.ID, "/home", m, now.Add(-1*time.Hour))
	seedPageloadTx(t, p.ID, "/about", m, now.Add(-30*time.Minute))

	pages, err := storage.GetWebVitalsByPage(context.Background(), testPool, []string{p.ID}, from, to, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pages) < 2 {
		t.Fatalf("expected at least 2 pages, got %d", len(pages))
	}

	byName := map[string]storage.WebVitalsPage{}
	for _, pg := range pages {
		byName[pg.Transaction] = pg
	}

	home, ok := byName["/home"]
	if !ok {
		t.Fatal("expected /home page")
	}
	if home.Sessions != 2 {
		t.Errorf("/home sessions: expected 2, got %d", home.Sessions)
	}
	if home.LCPP75 <= 0 {
		t.Errorf("/home LCP P75: expected > 0, got %f", home.LCPP75)
	}
	// All vitals are within good thresholds so pass rate should be 1.0.
	if home.PassRate != 1.0 {
		t.Errorf("/home PassRate: expected 1.0, got %f", home.PassRate)
	}

	about, ok := byName["/about"]
	if !ok {
		t.Fatal("expected /about page")
	}
	if about.Sessions != 1 {
		t.Errorf("/about sessions: expected 1, got %d", about.Sessions)
	}
}

// TestGetWebVitalsByPage_timeRange verifies the from/to window excludes out-of-range rows.
func TestGetWebVitalsByPage_timeRange(t *testing.T) {
	p := setupProjectForVitals(t)
	now := time.Now().UTC()

	m := `{"lcp":{"value":1000}}`
	// Seed a transaction well outside the query window.
	seedPageloadTx(t, p.ID, "/old", m, now.Add(-100*time.Hour))

	from := now.Add(-24 * time.Hour)
	to := now.Add(time.Hour)

	pages, err := storage.GetWebVitalsByPage(context.Background(), testPool, []string{p.ID}, from, to, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, pg := range pages {
		if pg.Transaction == "/old" {
			t.Error("out-of-range transaction appeared in results")
		}
	}
}

// TestGetWebVitalsByPage_allProjectsWhenNoIDs verifies that passing an empty projectIDs
// returns results from all projects.
func TestGetWebVitalsByPage_allProjectsWhenNoIDs(t *testing.T) {
	truncateProjects(t)
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	to := now.Add(time.Hour)

	p1, _ := storage.CreateProject(context.Background(), testPool, "vit-p1", "P1")
	p2, _ := storage.CreateProject(context.Background(), testPool, "vit-p2", "P2")

	m := `{"lcp":{"value":1500}}`
	seedPageloadTx(t, p1.ID, "/p1-page", m, now.Add(-1*time.Hour))
	seedPageloadTx(t, p2.ID, "/p2-page", m, now.Add(-1*time.Hour))

	// Passing nil projectIDs should include both projects.
	pages, err := storage.GetWebVitalsByPage(context.Background(), testPool, nil, from, to, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := map[string]bool{}
	for _, pg := range pages {
		found[pg.Transaction] = true
	}
	if !found["/p1-page"] || !found["/p2-page"] {
		t.Errorf("expected both /p1-page and /p2-page, got %v", found)
	}
}
