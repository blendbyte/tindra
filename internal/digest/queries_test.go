package digest

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	testcontainers "github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/migrations"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:18",
		tcpostgres.WithDatabase("tindra_test"),
		tcpostgres.WithUsername("tindra"),
		tcpostgres.WithPassword("tindra"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		log.Fatalf("start postgres container: %v", err)
	}

	connStr, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatalf("get connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		log.Fatalf("create pool: %v", err)
	}

	names, err := migrations.Files()
	if err != nil {
		log.Fatalf("list migrations: %v", err)
	}
	for _, name := range names {
		sql, err := migrations.FS.ReadFile(name)
		if err != nil {
			log.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			log.Fatalf("apply migration %s: %v", name, err)
		}
	}

	testPool = pool

	code := m.Run()

	pool.Close()
	_ = ctr.Terminate(ctx)
	os.Exit(code)
}

func truncateAll(t *testing.T) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), "TRUNCATE projects CASCADE")
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func seedProject(t *testing.T, slug, name string) *storage.Project {
	t.Helper()
	p, err := storage.CreateProject(context.Background(), testPool, slug, name)
	if err != nil {
		t.Fatalf("create project %s: %v", slug, err)
	}
	return p
}

func seedEvent(t *testing.T, projectID string, receivedAt time.Time) string {
	t.Helper()
	payload := json.RawMessage(`{"level":"error","message":"test error"}`)
	var id string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO events (project_id, timestamp, received_at, payload)
		VALUES ($1, $2, $2, $3) RETURNING id
	`, projectID, receivedAt, payload).Scan(&id)
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return id
}

func seedEventForIssue(t *testing.T, projectID, issueID, fingerprint string, receivedAt time.Time) string {
	t.Helper()
	payload := json.RawMessage(`{"level":"error","message":"test error"}`)
	var id string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, $2, $2, $3, $4, $5) RETURNING id
	`, projectID, receivedAt, payload, fingerprint, issueID).Scan(&id)
	if err != nil {
		t.Fatalf("seed event for issue: %v", err)
	}
	return id
}

func seedTransaction(t *testing.T, projectID, name string, durationMs int, start time.Time) {
	t.Helper()
	end := start.Add(time.Duration(durationMs) * time.Millisecond)
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO transactions
			(project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
		VALUES ($1, $2, 'http.server', 'ok', $3, $4, $5)
	`, projectID, name, durationMs, start, end)
	if err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
}

func seedIssue(t *testing.T, projectID, fp, title, status string, firstSeen time.Time) string {
	t.Helper()
	iss, _, _, err := storage.UpsertIssue(context.Background(), testPool, projectID, fp, title, "error", "error", "", "", firstSeen)
	if err != nil {
		t.Fatalf("seed issue %s: %v", fp, err)
	}
	if status != "open" {
		_, err = storage.UpdateIssueStatus(context.Background(), testPool, projectID, iss.ID, status, nil)
		if err != nil {
			t.Fatalf("set issue status %s: %v", status, err)
		}
	}
	return iss.ID
}

// --- dailyErrorCounts ---

func TestDailyErrorCounts_empty(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "ec-empty", "EC Empty")
	from := time.Now().UTC().AddDate(0, 0, -7)
	to := time.Now().UTC()

	stats, err := dailyErrorCounts(context.Background(), testPool, []string{p.ID}, from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("expected 0 stats for empty project, got %d", len(stats))
	}
}

func TestDailyErrorCounts_countsEvents(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "ec-count", "EC Count")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	day1 := now.AddDate(0, 0, -3)
	day2 := now.AddDate(0, 0, -2)

	seedEvent(t, p.ID, day1)
	seedEvent(t, p.ID, day1)
	seedEvent(t, p.ID, day2)

	stats, err := dailyErrorCounts(context.Background(), testPool, []string{p.ID}, from, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) == 0 {
		t.Fatal("expected non-empty stats")
	}

	total := int64(0)
	for _, s := range stats {
		total += s.Count
	}
	if total != 3 {
		t.Errorf("total event count: got %d, want 3", total)
	}
}

func TestDailyErrorCounts_excludesOutOfRange(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "ec-range", "EC Range")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	oldEvent := now.AddDate(0, 0, -10) // outside the window

	seedEvent(t, p.ID, oldEvent)

	stats, err := dailyErrorCounts(context.Background(), testPool, []string{p.ID}, from, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range stats {
		if s.Count > 0 {
			t.Errorf("old event should be outside range, but got count %d", s.Count)
		}
	}
}

func TestDailyErrorCounts_multipleProjects(t *testing.T) {
	truncateAll(t)
	p1 := seedProject(t, "ec-mp1", "MP1")
	p2 := seedProject(t, "ec-mp2", "MP2")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	day := now.AddDate(0, 0, -1)

	seedEvent(t, p1.ID, day)
	seedEvent(t, p2.ID, day)

	stats, err := dailyErrorCounts(context.Background(), testPool, []string{p1.ID, p2.ID}, from, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	total := int64(0)
	for _, s := range stats {
		total += s.Count
	}
	if total != 2 {
		t.Errorf("expected 2 events across projects, got %d", total)
	}
}

// --- dailyTxCounts ---

func TestDailyTxCounts_empty(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "tc-empty", "TC Empty")
	from := time.Now().UTC().AddDate(0, 0, -7)
	to := time.Now().UTC()

	stats, err := dailyTxCounts(context.Background(), testPool, []string{p.ID}, from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("expected 0 tx stats for empty project, got %d", len(stats))
	}
}

func TestDailyTxCounts_countsTx(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "tc-count", "TC Count")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	day1 := now.AddDate(0, 0, -3)
	day2 := now.AddDate(0, 0, -2)

	seedTransaction(t, p.ID, "/api/a", 10, day1)
	seedTransaction(t, p.ID, "/api/b", 20, day1)
	seedTransaction(t, p.ID, "/api/c", 30, day2)

	stats, err := dailyTxCounts(context.Background(), testPool, []string{p.ID}, from, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	total := int64(0)
	for _, s := range stats {
		total += s.Count
	}
	if total != 3 {
		t.Errorf("total tx count: got %d, want 3", total)
	}
}

func TestDailyTxCounts_excludesOutOfRange(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "tc-range", "TC Range")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	old := now.AddDate(0, 0, -10)

	seedTransaction(t, p.ID, "/api/old", 10, old)

	stats, err := dailyTxCounts(context.Background(), testPool, []string{p.ID}, from, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range stats {
		if s.Count > 0 {
			t.Errorf("out-of-range tx should be excluded, got count %d", s.Count)
		}
	}
}

// --- projectBreakdown ---

func TestProjectBreakdown_empty(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "pb-empty", "PB Empty")
	from := time.Now().UTC().AddDate(0, 0, -7)
	to := time.Now().UTC()

	stats, err := projectBreakdown(context.Background(), testPool, []string{p.ID}, from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 project stat entry, got %d", len(stats))
	}
	if stats[0].Errors != 0 || stats[0].Transactions != 0 {
		t.Errorf("expected zero counts for empty project, got errors=%d txns=%d", stats[0].Errors, stats[0].Transactions)
	}
}

func TestProjectBreakdown_countsEventsAndTx(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "pb-count", "PB Count")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	day := now.AddDate(0, 0, -1)

	seedEvent(t, p.ID, day)
	seedEvent(t, p.ID, day)
	seedTransaction(t, p.ID, "/api/x", 10, day)

	stats, err := projectBreakdown(context.Background(), testPool, []string{p.ID}, from, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 stat, got %d", len(stats))
	}
	if stats[0].Errors != 2 {
		t.Errorf("errors: got %d, want 2", stats[0].Errors)
	}
	if stats[0].Transactions != 1 {
		t.Errorf("transactions: got %d, want 1", stats[0].Transactions)
	}
	if stats[0].ProjectName != "PB Count" {
		t.Errorf("project name: got %q, want %q", stats[0].ProjectName, "PB Count")
	}
}

func TestProjectBreakdown_multipleProjects(t *testing.T) {
	truncateAll(t)
	p1 := seedProject(t, "pb-multi-1", "Multi 1")
	p2 := seedProject(t, "pb-multi-2", "Multi 2")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	day := now.AddDate(0, 0, -1)

	seedEvent(t, p1.ID, day)
	seedEvent(t, p1.ID, day)
	seedEvent(t, p2.ID, day)

	stats, err := projectBreakdown(context.Background(), testPool, []string{p1.ID, p2.ID}, from, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 project stats, got %d", len(stats))
	}
	total := int64(0)
	for _, s := range stats {
		total += s.Errors
	}
	if total != 3 {
		t.Errorf("total errors across projects: got %d, want 3", total)
	}
}

func TestProjectBreakdown_orderedByErrorsDesc(t *testing.T) {
	truncateAll(t)
	p1 := seedProject(t, "pb-ord-1", "Low Errors")
	p2 := seedProject(t, "pb-ord-2", "High Errors")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	day := now.AddDate(0, 0, -1)

	seedEvent(t, p1.ID, day)
	for i := 0; i < 5; i++ {
		seedEvent(t, p2.ID, day)
	}

	stats, err := projectBreakdown(context.Background(), testPool, []string{p1.ID, p2.ID}, from, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) < 2 {
		t.Fatalf("expected 2 stats, got %d", len(stats))
	}
	if stats[0].ProjectName != "High Errors" {
		t.Errorf("expected 'High Errors' first (order by errors DESC), got %q", stats[0].ProjectName)
	}
}

// --- issuesSummary ---

func TestIssuesSummary_empty(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "is-empty", "IS Empty")
	from := time.Now().UTC().AddDate(0, 0, -7)
	to := time.Now().UTC()

	s, err := issuesSummary(context.Background(), testPool, []string{p.ID}, from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.New != 0 || s.Regressed != 0 || s.Ongoing != 0 {
		t.Errorf("expected all zeros for empty project, got %+v", s)
	}
}

func TestIssuesSummary_newIssues(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "is-new", "IS New")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)

	seedIssue(t, p.ID, "fp-new-1", "New Issue 1", "open", now.AddDate(0, 0, -3))
	seedIssue(t, p.ID, "fp-new-2", "New Issue 2", "open", now.AddDate(0, 0, -1))

	s, err := issuesSummary(context.Background(), testPool, []string{p.ID}, from, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.New != 2 {
		t.Errorf("new count: got %d, want 2", s.New)
	}
}

func TestIssuesSummary_ongoingIssues(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "is-ongoing", "IS Ongoing")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)

	// Ongoing: open, first_seen before the window
	seedIssue(t, p.ID, "fp-old-1", "Old Issue 1", "open", now.AddDate(0, 0, -30))
	seedIssue(t, p.ID, "fp-old-2", "Old Issue 2", "open", now.AddDate(0, 0, -14))

	s, err := issuesSummary(context.Background(), testPool, []string{p.ID}, from, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Ongoing != 2 {
		t.Errorf("ongoing count: got %d, want 2", s.Ongoing)
	}
}

func TestIssuesSummary_regressedIssues(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "is-reg", "IS Regressed")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)

	// Create, resolve, then re-upsert to trigger regression
	iss, _, _, err := storage.UpsertIssue(context.Background(), testPool, p.ID, "fp-reg", "Regressed Issue", "error", "error", "", "", now.AddDate(0, 0, -10))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	_, err = storage.UpdateIssueStatus(context.Background(), testPool, p.ID, iss.ID, "resolved", nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	storage.UpsertIssue(context.Background(), testPool, p.ID, "fp-reg", "Regressed Issue", "error", "error", "", "", now.AddDate(0, 0, -1))

	s, err := issuesSummary(context.Background(), testPool, []string{p.ID}, from, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Regressed != 1 {
		t.Errorf("regressed count: got %d, want 1", s.Regressed)
	}
}

func TestIssuesSummary_excludesResolved(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "is-resolved", "IS Resolved")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)

	issID := seedIssue(t, p.ID, "fp-res", "Resolved Issue", "open", now.AddDate(0, 0, -3))
	_, err := storage.UpdateIssueStatus(context.Background(), testPool, p.ID, issID, "resolved", nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	s, err := issuesSummary(context.Background(), testPool, []string{p.ID}, from, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.New+s.Regressed+s.Ongoing != 0 {
		t.Errorf("resolved issue should not appear in summary, got %+v", s)
	}
}

// --- topIssues ---

func TestTopIssues_empty(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "ti-empty", "TI Empty")
	from := time.Now().UTC().AddDate(0, 0, -7)
	to := time.Now().UTC()

	issues, err := topIssues(context.Background(), testPool, []string{p.ID}, from, to, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues, got %d", len(issues))
	}
}

func TestTopIssues_returnsOrderedByCount(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "ti-order", "TI Order")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	day := now.AddDate(0, 0, -1)

	iss1ID := seedIssue(t, p.ID, "fp-ti-1", "Issue One", "open", day)
	iss2ID := seedIssue(t, p.ID, "fp-ti-2", "Issue Two", "open", day)

	// Issue 2 gets more events
	seedEventForIssue(t, p.ID, iss1ID, "fp-ti-1", day)
	seedEventForIssue(t, p.ID, iss2ID, "fp-ti-2", day)
	seedEventForIssue(t, p.ID, iss2ID, "fp-ti-2", day)
	seedEventForIssue(t, p.ID, iss2ID, "fp-ti-2", day)

	issues, err := topIssues(context.Background(), testPool, []string{p.ID}, from, now, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}
	if issues[0].Title != "Issue Two" {
		t.Errorf("expected 'Issue Two' first (most events), got %q", issues[0].Title)
	}
	if issues[0].Count != 3 {
		t.Errorf("Issue Two count: got %d, want 3", issues[0].Count)
	}
}

func TestTopIssues_respectsLimit(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "ti-limit", "TI Limit")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	day := now.AddDate(0, 0, -1)

	for i := 0; i < 5; i++ {
		fp := "fp-lim-" + string(rune('a'+i))
		title := "Issue " + string(rune('A'+i))
		issID := seedIssue(t, p.ID, fp, title, "open", day)
		seedEventForIssue(t, p.ID, issID, fp, day)
	}

	issues, err := topIssues(context.Background(), testPool, []string{p.ID}, from, now, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 3 {
		t.Errorf("expected 3 issues (limit), got %d", len(issues))
	}
}

func TestTopIssues_populatesFields(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "ti-fields", "TI Fields")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	day := now.AddDate(0, 0, -1)

	issID := seedIssue(t, p.ID, "fp-tf", "My Error Title", "open", day)
	seedEventForIssue(t, p.ID, issID, "fp-tf", day)

	issues, err := topIssues(context.Background(), testPool, []string{p.ID}, from, now, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("expected at least 1 issue")
	}
	iss := issues[0]
	if iss.Title != "My Error Title" {
		t.Errorf("title: got %q, want %q", iss.Title, "My Error Title")
	}
	if iss.ProjectName != "TI Fields" {
		t.Errorf("project name: got %q, want %q", iss.ProjectName, "TI Fields")
	}
	if iss.IssueID == "" {
		t.Error("expected non-empty IssueID")
	}
	if iss.Status != "open" {
		t.Errorf("status: got %q, want %q", iss.Status, "open")
	}
}

// --- topTransactions ---

func TestTopTransactions_empty(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "tt-empty", "TT Empty")
	from := time.Now().UTC().AddDate(0, 0, -7)
	to := time.Now().UTC()

	txns, err := topTransactions(context.Background(), testPool, []string{p.ID}, from, to, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) != 0 {
		t.Errorf("expected 0 transactions, got %d", len(txns))
	}
}

func TestTopTransactions_returnsOrderedByCount(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "tt-order", "TT Order")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	day := now.AddDate(0, 0, -1)

	seedTransaction(t, p.ID, "/api/rare", 10, day)
	seedTransaction(t, p.ID, "/api/common", 20, day)
	seedTransaction(t, p.ID, "/api/common", 30, day.Add(time.Second))
	seedTransaction(t, p.ID, "/api/common", 40, day.Add(2*time.Second))

	txns, err := topTransactions(context.Background(), testPool, []string{p.ID}, from, now, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) < 2 {
		t.Fatalf("expected at least 2 transaction groups, got %d", len(txns))
	}
	if txns[0].Transaction != "/api/common" {
		t.Errorf("expected /api/common first (most frequent), got %q", txns[0].Transaction)
	}
	if txns[0].Count != 3 {
		t.Errorf("/api/common count: got %d, want 3", txns[0].Count)
	}
}

func TestTopTransactions_respectsLimit(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "tt-limit", "TT Limit")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	day := now.AddDate(0, 0, -1)

	for i := 0; i < 6; i++ {
		name := "/api/route-" + string(rune('a'+i))
		seedTransaction(t, p.ID, name, 10, day.Add(time.Duration(i)*time.Second))
	}

	txns, err := topTransactions(context.Background(), testPool, []string{p.ID}, from, now, 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) != 4 {
		t.Errorf("expected 4 transactions (limit), got %d", len(txns))
	}
}

func TestTopTransactions_populatesPercentiles(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "tt-pctile", "TT Percentile")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	day := now.AddDate(0, 0, -1)

	for i, dur := range []int{10, 20, 50, 100, 200} {
		seedTransaction(t, p.ID, "/api/perf", dur, day.Add(time.Duration(i)*time.Second))
	}

	txns, err := topTransactions(context.Background(), testPool, []string{p.ID}, from, now, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) != 1 {
		t.Fatalf("expected 1 transaction group, got %d", len(txns))
	}
	tx := txns[0]
	if tx.P50Ms <= 0 {
		t.Errorf("expected positive P50, got %d", tx.P50Ms)
	}
	if tx.P95Ms < tx.P50Ms {
		t.Errorf("P95 (%d) should be >= P50 (%d)", tx.P95Ms, tx.P50Ms)
	}
	if tx.Count != 5 {
		t.Errorf("count: got %d, want 5", tx.Count)
	}
}

func TestTopTransactions_includesPrevP50(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "tt-prev", "TT Prev")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	day := now.AddDate(0, 0, -1)
	prevDay := now.AddDate(0, 0, -8) // in the previous window

	// Seed in the current window
	seedTransaction(t, p.ID, "/api/stable", 100, day)

	// Seed in the previous window (prevFrom = from - 7 days)
	seedTransaction(t, p.ID, "/api/stable", 80, prevDay)

	txns, err := topTransactions(context.Background(), testPool, []string{p.ID}, from, now, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txns))
	}
	if txns[0].P50PrevMs != 80 {
		t.Errorf("P50PrevMs: got %d, want 80", txns[0].P50PrevMs)
	}
}

func TestTopTransactions_prevP50ZeroWhenNoPrevData(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "tt-noprev", "TT No Prev")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	day := now.AddDate(0, 0, -1)

	seedTransaction(t, p.ID, "/api/new", 50, day)

	txns, err := topTransactions(context.Background(), testPool, []string{p.ID}, from, now, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txns) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txns))
	}
	if txns[0].P50PrevMs != 0 {
		t.Errorf("P50PrevMs should be 0 with no prior data, got %d", txns[0].P50PrevMs)
	}
}
