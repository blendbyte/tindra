package retention_test

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/retention"
	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
)

var (
	testPool    *pgxpool.Pool
	testProject *storage.Project
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	pool, cleanup := testutil.SetupDB(ctx)

	project, err := storage.CreateProject(ctx, pool, "ret-test", "Retention Test")
	if err != nil {
		log.Fatalf("create project: %v", err)
	}

	testPool = pool
	testProject = project

	code := m.Run()
	cleanup()
	os.Exit(code)
}

func truncate(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE events, issues, transactions CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func TestWorker_deletesOldEvents(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	iss, _, _, _ := storage.UpsertIssue(ctx, testPool, testProject.ID, "fp-stale", "Stale Error", "error", "error", "", "", time.Now().AddDate(0, 0, -91))
	staleTS := time.Now().AddDate(0, 0, -91)
	testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, $2, $2, '{"level":"error"}'::jsonb, 'fp-stale', $3)
	`, testProject.ID, staleTS, iss.ID)

	fresh, _, _, _ := storage.UpsertIssue(ctx, testPool, testProject.ID, "fp-fresh", "Fresh Error", "error", "error", "", "", time.Now())
	testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, NOW(), NOW(), '{"level":"error"}'::jsonb, 'fp-fresh', $2)
	`, testProject.ID, fresh.ID)

	retention.NewWorker(testPool, 90).RunOnce(ctx)

	var count int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM events WHERE project_id = $1", testProject.ID).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 event (fresh only), got %d", count)
	}

	staleIss, _ := storage.GetIssue(ctx, testPool, iss.ID)
	if staleIss == nil {
		t.Error("open stale issue shell should be kept after events are purged")
	}
	if staleIss != nil && staleIss.EventCount != 0 {
		t.Errorf("open stale issue event_count: got %d, want 0", staleIss.EventCount)
	}

	freshIss, _ := storage.GetIssue(ctx, testPool, fresh.ID)
	if freshIss == nil {
		t.Error("fresh issue should still exist")
	}
}

func TestWorker_disabled(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	iss, _, _, _ := storage.UpsertIssue(ctx, testPool, testProject.ID, "fp-dis", "Disabled", "error", "error", "", "", time.Now().AddDate(0, 0, -200))
	testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, $2, $2, '{"level":"error"}'::jsonb, 'fp-dis', $3)
	`, testProject.ID, time.Now().AddDate(0, 0, -200), iss.ID)

	retention.NewWorker(testPool, 0).RunOnce(ctx)

	var count int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM events WHERE project_id = $1", testProject.ID).Scan(&count)
	if count != 1 {
		t.Errorf("expected event to survive when retention disabled, got count=%d", count)
	}
}

func TestWorker_deletesOldTransactions(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	staleTS := time.Now().AddDate(0, 0, -91)
	testPool.Exec(ctx, `
		INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, received_at)
		VALUES ($1, '/old', 'http.server', 'ok', 100, $2, $2, $2)
	`, testProject.ID, staleTS)
	testPool.Exec(ctx, `
		INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
		VALUES ($1, '/new', 'http.server', 'ok', 50, NOW(), NOW())
	`, testProject.ID)

	retention.NewWorker(testPool, 90).RunOnce(ctx)

	var count int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM transactions WHERE project_id = $1", testProject.ID).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 transaction (fresh only), got %d", count)
	}
}

func TestWorker_deletesOldLogs(t *testing.T) {
	ctx := context.Background()

	testPool.Exec(ctx, "DELETE FROM logs")
	t.Cleanup(func() { testPool.Exec(context.Background(), "DELETE FROM logs") })

	staleTS := time.Now().AddDate(0, 0, -91)
	testPool.Exec(ctx, `
		INSERT INTO logs (project_id, timestamp, received_at, level, body)
		VALUES ($1, $2, $2, 'error', 'stale log')
	`, testProject.ID, staleTS)
	testPool.Exec(ctx, `
		INSERT INTO logs (project_id, timestamp, received_at, level, body)
		VALUES ($1, NOW(), NOW(), 'info', 'fresh log')
	`, testProject.ID)

	retention.NewWorker(testPool, 90).RunOnce(ctx)

	var count int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM logs WHERE project_id = $1", testProject.ID).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 log (fresh only), got %d", count)
	}
}

func TestWorker_purgesExpiredOAuthStates(t *testing.T) {
	ctx := context.Background()

	testPool.Exec(ctx, "DELETE FROM oauth_states")
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM oauth_states")
	})

	testPool.Exec(ctx, `
		INSERT INTO oauth_states (token, provider, verifier, expires_at)
		VALUES ('expired-oauth-state', 'github', 'verifier1', NOW() - INTERVAL '1 minute')
	`)
	testPool.Exec(ctx, `
		INSERT INTO oauth_states (token, provider, verifier, expires_at)
		VALUES ('valid-oauth-state', 'github', 'verifier2', NOW() + INTERVAL '10 minutes')
	`)

	retention.NewWorker(testPool, 90).RunOnce(ctx)

	var expiredCount int
	testPool.QueryRow(ctx, `SELECT COUNT(*) FROM oauth_states WHERE token = 'expired-oauth-state'`).Scan(&expiredCount)
	if expiredCount != 0 {
		t.Errorf("expected expired oauth state to be deleted, got count=%d", expiredCount)
	}

	var validCount int
	testPool.QueryRow(ctx, `SELECT COUNT(*) FROM oauth_states WHERE token = 'valid-oauth-state'`).Scan(&validCount)
	if validCount != 1 {
		t.Errorf("expected valid oauth state to survive, got count=%d", validCount)
	}
}

func TestWorker_purgesExpiredMFAChallenges(t *testing.T) {
	ctx := context.Background()

	var userID string
	err := testPool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash)
		VALUES ('mfa-test@example.com', 'x')
		ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		RETURNING id
	`).Scan(&userID)
	if err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", userID)
	})

	testPool.Exec(ctx, `
		INSERT INTO mfa_challenges (token, user_id, expires_at)
		VALUES ('expired-token-retention', $1, NOW() - INTERVAL '1 minute')
	`, userID)
	testPool.Exec(ctx, `
		INSERT INTO mfa_challenges (token, user_id, expires_at)
		VALUES ('valid-token-retention', $1, NOW() + INTERVAL '10 minutes')
	`, userID)

	retention.NewWorker(testPool, 90).RunOnce(ctx)

	var expiredCount int
	testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mfa_challenges WHERE token = 'expired-token-retention'
	`).Scan(&expiredCount)
	if expiredCount != 0 {
		t.Errorf("expected expired MFA challenge to be deleted, got count=%d", expiredCount)
	}

	var validCount int
	testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mfa_challenges WHERE token = 'valid-token-retention'
	`).Scan(&validCount)
	if validCount != 1 {
		t.Errorf("expected valid MFA challenge to survive, got count=%d", validCount)
	}
}

func TestWorker_keepsOpenIssueShellAfterEventsPurged(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	iss, _, _, _ := storage.UpsertIssue(ctx, testPool, testProject.ID, "fp-open-shell", "Open Shell", "error", "error", "", "", time.Now().AddDate(0, 0, -91))
	testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, $2, $2, '{"level":"error"}'::jsonb, 'fp-open-shell', $3)
	`, testProject.ID, time.Now().AddDate(0, 0, -91), iss.ID)

	retention.NewWorker(testPool, 90).RunOnce(ctx)

	survived, _ := storage.GetIssue(ctx, testPool, iss.ID)
	require.NotNil(t, survived)
	if survived.EventCount != 0 {
		t.Errorf("event_count: got %d, want 0", survived.EventCount)
	}
}

func TestWorker_deletesResolvedIssueAfterEventsPurged(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	iss, _, _, _ := storage.UpsertIssue(ctx, testPool, testProject.ID, "fp-resolved-stale", "Resolved Stale", "error", "error", "", "", time.Now().AddDate(0, 0, -91))
	testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, $2, $2, '{"level":"error"}'::jsonb, 'fp-resolved-stale', $3)
	`, testProject.ID, time.Now().AddDate(0, 0, -91), iss.ID)
	testPool.Exec(ctx, `UPDATE issues SET status = 'resolved' WHERE id = $1`, iss.ID)

	retention.NewWorker(testPool, 90).RunOnce(ctx)

	gone, _ := storage.GetIssue(ctx, testPool, iss.ID)
	if gone != nil {
		t.Error("resolved issue with no remaining events should be deleted")
	}
}

func TestWorker_deletesIgnoredIssueAfterEventsPurged(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	iss, _, _, _ := storage.UpsertIssue(ctx, testPool, testProject.ID, "fp-ignored-stale", "Ignored Stale", "error", "error", "", "", time.Now().AddDate(0, 0, -91))
	testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, $2, $2, '{"level":"error"}'::jsonb, 'fp-ignored-stale', $3)
	`, testProject.ID, time.Now().AddDate(0, 0, -91), iss.ID)
	testPool.Exec(ctx, `UPDATE issues SET status = 'ignored' WHERE id = $1`, iss.ID)

	retention.NewWorker(testPool, 90).RunOnce(ctx)

	gone, _ := storage.GetIssue(ctx, testPool, iss.ID)
	if gone != nil {
		t.Error("ignored issue with no remaining events should be deleted")
	}
}

func TestWorker_deletesOldUptimeChecks(t *testing.T) {
	ctx := context.Background()

	testPool.Exec(ctx, "TRUNCATE uptime_monitors CASCADE")
	t.Cleanup(func() { testPool.Exec(context.Background(), "TRUNCATE uptime_monitors CASCADE") })

	var monitorID string
	err := testPool.QueryRow(ctx, `
		INSERT INTO uptime_monitors (project_id, name, url, method, interval_secs, timeout_secs, expected_codes)
		VALUES ($1, 'ret-uptime', 'https://example.com', 'GET', 300, 10, '200-299')
		RETURNING id
	`, testProject.ID).Scan(&monitorID)
	if err != nil {
		t.Fatalf("insert monitor: %v", err)
	}

	staleTS := time.Now().AddDate(0, 0, -91)
	testPool.Exec(ctx, `
		INSERT INTO uptime_checks (monitor_id, status, checked_at)
		VALUES ($1, 'up', $2)
	`, monitorID, staleTS)
	testPool.Exec(ctx, `
		INSERT INTO uptime_checks (monitor_id, status, checked_at)
		VALUES ($1, 'up', NOW())
	`, monitorID)

	retention.NewWorker(testPool, 90).RunOnce(ctx)

	var count int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM uptime_checks WHERE monitor_id = $1", monitorID).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 uptime check (fresh only), got %d", count)
	}
}

func TestWorker_Run_disabled(t *testing.T) {
	done := make(chan struct{})
	go func() {
		retention.NewWorker(testPool, 0).Run(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("Run with retentionDays=0 should return immediately")
	}
}

func TestWorker_Run_stopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		retention.NewWorker(testPool, 90).Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("Run did not stop after context cancellation")
	}
}
