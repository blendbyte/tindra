package retention_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

func workerTestHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

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

func TestWorker_purgesLogsRowCap(t *testing.T) {
	ctx := context.Background()

	testPool.Exec(ctx, "DELETE FROM logs")
	t.Cleanup(func() { testPool.Exec(context.Background(), "DELETE FROM logs") })

	// Insert 15 logs for the test project.
	for i := range 15 {
		testPool.Exec(ctx, `
			INSERT INTO logs (project_id, timestamp, received_at, level, body)
			VALUES ($1, NOW() - ($2 * INTERVAL '1 second'), NOW(), 'info', 'row-cap-test')
		`, testProject.ID, i)
	}

	var before int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM logs WHERE project_id = $1", testProject.ID).Scan(&before)
	if before != 15 {
		t.Fatalf("expected 15 logs before purge, got %d", before)
	}

	// Cap at 10; expect 5 oldest to be removed.
	retention.NewWorker(testPool, 90).WithRowLimits(10, 0).RunOnce(ctx)

	var after int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM logs WHERE project_id = $1", testProject.ID).Scan(&after)
	if after != 10 {
		t.Errorf("expected 10 logs after row cap purge, got %d", after)
	}
}

func TestWorker_logsRowCapDoesNotPurgeWhenUnderLimit(t *testing.T) {
	ctx := context.Background()

	testPool.Exec(ctx, "DELETE FROM logs")
	t.Cleanup(func() { testPool.Exec(context.Background(), "DELETE FROM logs") })

	for range 5 {
		testPool.Exec(ctx, `
			INSERT INTO logs (project_id, timestamp, received_at, level, body)
			VALUES ($1, NOW(), NOW(), 'info', 'under-cap')
		`, testProject.ID)
	}

	retention.NewWorker(testPool, 90).WithRowLimits(10, 0).RunOnce(ctx)

	var after int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM logs WHERE project_id = $1", testProject.ID).Scan(&after)
	if after != 5 {
		t.Errorf("expected all 5 logs to survive when under cap, got %d", after)
	}
}

func TestWorker_logsRowCapZeroMeansNoCap(t *testing.T) {
	ctx := context.Background()

	testPool.Exec(ctx, "DELETE FROM logs")
	t.Cleanup(func() { testPool.Exec(context.Background(), "DELETE FROM logs") })

	for range 20 {
		testPool.Exec(ctx, `
			INSERT INTO logs (project_id, timestamp, received_at, level, body)
			VALUES ($1, NOW(), NOW(), 'info', 'no-cap')
		`, testProject.ID)
	}

	retention.NewWorker(testPool, 90).WithRowLimits(0, 0).RunOnce(ctx)

	var after int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM logs WHERE project_id = $1", testProject.ID).Scan(&after)
	if after != 20 {
		t.Errorf("expected 20 logs to survive when cap=0, got %d", after)
	}
}

func TestWorker_purgesTransactionsRowCap(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	// Insert 15 transactions.
	for i := range 15 {
		testPool.Exec(ctx, `
			INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
			VALUES ($1, '/tx-cap', 'http.server', 'ok', 10, NOW() - ($2 * INTERVAL '1 second'), NOW())
		`, testProject.ID, i)
	}

	var before int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM transactions WHERE project_id = $1", testProject.ID).Scan(&before)
	if before != 15 {
		t.Fatalf("expected 15 transactions before purge, got %d", before)
	}

	// Cap at 10; expect 5 to be removed.
	retention.NewWorker(testPool, 90).WithRowLimits(0, 10).RunOnce(ctx)

	var after int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM transactions WHERE project_id = $1", testProject.ID).Scan(&after)
	if after != 10 {
		t.Errorf("expected 10 transactions after row cap purge, got %d", after)
	}
}

func TestWorker_txRowCapIsolatesProjects(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	// Create a second project.
	otherProject, err := storage.CreateProject(ctx, testPool, "ret-other", "Retention Other")
	if err != nil {
		t.Fatalf("create other project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", otherProject.ID)
	})

	// 12 transactions in testProject, 8 in otherProject.
	for i := range 12 {
		testPool.Exec(ctx, `
			INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
			VALUES ($1, '/tx-cap-isolation', 'http.server', 'ok', 10, NOW() - ($2 * INTERVAL '1 second'), NOW())
		`, testProject.ID, i)
	}
	for i := range 8 {
		testPool.Exec(ctx, `
			INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
			VALUES ($1, '/tx-cap-isolation', 'http.server', 'ok', 10, NOW() - ($2 * INTERVAL '1 second'), NOW())
		`, otherProject.ID, i)
	}

	// Cap at 10 per project: testProject goes from 12→10, otherProject stays at 8.
	retention.NewWorker(testPool, 90).WithRowLimits(0, 10).RunOnce(ctx)

	var countA, countB int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM transactions WHERE project_id = $1", testProject.ID).Scan(&countA)
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM transactions WHERE project_id = $1", otherProject.ID).Scan(&countB)

	if countA != 10 {
		t.Errorf("testProject: expected 10 transactions after cap, got %d", countA)
	}
	if countB != 8 {
		t.Errorf("otherProject: expected 8 transactions (under cap, no deletions), got %d", countB)
	}
}

func TestWorker_logsRowCapKeepsNewest(t *testing.T) {
	ctx := context.Background()

	testPool.Exec(ctx, "DELETE FROM logs")
	t.Cleanup(func() { testPool.Exec(context.Background(), "DELETE FROM logs") })

	// Insert 5 logs spaced 1 hour apart, oldest first. Body encodes age so we
	// can assert which rows survive.
	for i := range 5 {
		testPool.Exec(ctx, `
			INSERT INTO logs (project_id, timestamp, received_at, level, body)
			VALUES ($1, NOW() - ($2 * INTERVAL '1 hour'), NOW(), 'info', $3)
		`, testProject.ID, 4-i, fmt.Sprintf("log-%d-hours-old", 4-i))
	}

	// Cap at 3: only the 3 newest (0h, 1h, 2h old) should survive.
	retention.NewWorker(testPool, 90).WithRowLimits(3, 0).RunOnce(ctx)

	rows, err := testPool.Query(ctx, `
		SELECT body FROM logs WHERE project_id = $1 ORDER BY timestamp DESC
	`, testProject.ID)
	if err != nil {
		t.Fatalf("query logs: %v", err)
	}
	defer rows.Close()

	var bodies []string
	for rows.Next() {
		var body string
		rows.Scan(&body)
		bodies = append(bodies, body)
	}

	if len(bodies) != 3 {
		t.Fatalf("expected 3 logs after cap, got %d: %v", len(bodies), bodies)
	}
	want := []string{"log-0-hours-old", "log-1-hours-old", "log-2-hours-old"}
	for i, w := range want {
		if bodies[i] != w {
			t.Errorf("row %d: got %q, want %q", i, bodies[i], w)
		}
	}
}

func TestWorker_logsRowCapIsolatesProjects(t *testing.T) {
	ctx := context.Background()

	testPool.Exec(ctx, "DELETE FROM logs")
	t.Cleanup(func() { testPool.Exec(context.Background(), "DELETE FROM logs") })

	otherProject, err := storage.CreateProject(ctx, testPool, "ret-log-other", "Retention Log Other")
	if err != nil {
		t.Fatalf("create other project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", otherProject.ID)
	})

	// 12 logs in testProject, 8 in otherProject.
	for i := range 12 {
		testPool.Exec(ctx, `
			INSERT INTO logs (project_id, timestamp, received_at, level, body)
			VALUES ($1, NOW() - ($2 * INTERVAL '1 second'), NOW(), 'info', 'log-isolation')
		`, testProject.ID, i)
	}
	for i := range 8 {
		testPool.Exec(ctx, `
			INSERT INTO logs (project_id, timestamp, received_at, level, body)
			VALUES ($1, NOW() - ($2 * INTERVAL '1 second'), NOW(), 'info', 'log-isolation')
		`, otherProject.ID, i)
	}

	// Cap at 10 per project: testProject 12→10, otherProject stays at 8.
	retention.NewWorker(testPool, 90).WithRowLimits(10, 0).RunOnce(ctx)

	var countA, countB int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM logs WHERE project_id = $1", testProject.ID).Scan(&countA)
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM logs WHERE project_id = $1", otherProject.ID).Scan(&countB)

	if countA != 10 {
		t.Errorf("testProject: expected 10 logs after cap, got %d", countA)
	}
	if countB != 8 {
		t.Errorf("otherProject: expected 8 logs (under cap, no deletions), got %d", countB)
	}
}

func TestWorker_txRowCapDoesNotPurgeWhenUnderLimit(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	for range 5 {
		testPool.Exec(ctx, `
			INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
			VALUES ($1, '/tx-under-cap', 'http.server', 'ok', 10, NOW(), NOW())
		`, testProject.ID)
	}

	retention.NewWorker(testPool, 90).WithRowLimits(0, 10).RunOnce(ctx)

	var after int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM transactions WHERE project_id = $1", testProject.ID).Scan(&after)
	if after != 5 {
		t.Errorf("expected all 5 transactions to survive when under cap, got %d", after)
	}
}

func TestWorker_txRowCapZeroMeansNoCap(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	for range 20 {
		testPool.Exec(ctx, `
			INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
			VALUES ($1, '/tx-no-cap', 'http.server', 'ok', 10, NOW(), NOW())
		`, testProject.ID)
	}

	retention.NewWorker(testPool, 90).WithRowLimits(0, 0).RunOnce(ctx)

	var after int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM transactions WHERE project_id = $1", testProject.ID).Scan(&after)
	if after != 20 {
		t.Errorf("expected 20 transactions to survive when cap=0, got %d", after)
	}
}

func TestWorker_txRowCapKeepsNewest(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	// Insert 5 transactions spaced 1 hour apart. Transaction name encodes age.
	for i := range 5 {
		testPool.Exec(ctx, `
			INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
			VALUES ($1, $2, 'http.server', 'ok', 10, NOW() - ($3 * INTERVAL '1 hour'), NOW())
		`, testProject.ID, fmt.Sprintf("/tx-%d-hours-old", 4-i), 4-i)
	}

	// Cap at 3: only the 3 newest (0h, 1h, 2h old) should survive.
	retention.NewWorker(testPool, 90).WithRowLimits(0, 3).RunOnce(ctx)

	rows, err := testPool.Query(ctx, `
		SELECT transaction FROM transactions WHERE project_id = $1 ORDER BY start_timestamp DESC
	`, testProject.ID)
	if err != nil {
		t.Fatalf("query transactions: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		names = append(names, name)
	}

	if len(names) != 3 {
		t.Fatalf("expected 3 transactions after cap, got %d: %v", len(names), names)
	}
	want := []string{"/tx-0-hours-old", "/tx-1-hours-old", "/tx-2-hours-old"}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("row %d: got %q, want %q", i, names[i], w)
		}
	}
}

func TestWorker_txRowCapCascadesToSpans(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	// Insert 5 transactions each with one span. The oldest 2 should be purged by the cap.
	var txIDs [5]string
	for i := range 5 {
		err := testPool.QueryRow(ctx, `
			INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
			VALUES ($1, '/tx-cascade', 'http.server', 'ok', 10, NOW() - ($2 * INTERVAL '1 hour'), NOW())
			RETURNING id
		`, testProject.ID, 4-i).Scan(&txIDs[i])
		if err != nil {
			t.Fatalf("insert transaction %d: %v", i, err)
		}
		testPool.Exec(ctx, `
			INSERT INTO spans (transaction_id, span_id, op, description, start_timestamp, timestamp, duration_ms, status, project_id)
			VALUES ($1, $2, 'db.query', 'SELECT 1', NOW(), NOW(), 5, 'ok', $3)
		`, txIDs[i], fmt.Sprintf("span-%d", i), testProject.ID)
	}

	var spansBefore int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM spans WHERE project_id = $1", testProject.ID).Scan(&spansBefore)
	if spansBefore != 5 {
		t.Fatalf("expected 5 spans before purge, got %d", spansBefore)
	}

	// Cap at 3: 2 oldest transactions (and their spans) should be removed.
	retention.NewWorker(testPool, 90).WithRowLimits(0, 3).RunOnce(ctx)

	var txAfter, spansAfter int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM transactions WHERE project_id = $1", testProject.ID).Scan(&txAfter)
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM spans WHERE project_id = $1", testProject.ID).Scan(&spansAfter)

	if txAfter != 3 {
		t.Errorf("expected 3 transactions after cap, got %d", txAfter)
	}
	if spansAfter != 3 {
		t.Errorf("expected 3 spans after cascade delete, got %d", spansAfter)
	}
}

func TestWorker_purgesExpiredOAuthStates(t *testing.T) {
	ctx := context.Background()

	testPool.Exec(ctx, "DELETE FROM oauth_states")
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM oauth_states")
	})

	testPool.Exec(ctx, `
		INSERT INTO oauth_states (token_hash, provider, verifier, expires_at)
		VALUES ($1, 'github', 'verifier1', NOW() - INTERVAL '1 minute')
	`, workerTestHash("expired-oauth-state"))
	testPool.Exec(ctx, `
		INSERT INTO oauth_states (token_hash, provider, verifier, expires_at)
		VALUES ($1, 'github', 'verifier2', NOW() + INTERVAL '10 minutes')
	`, workerTestHash("valid-oauth-state"))

	retention.NewWorker(testPool, 90).RunOnce(ctx)

	var expiredCount int
	testPool.QueryRow(ctx, `SELECT COUNT(*) FROM oauth_states WHERE token_hash = $1`, workerTestHash("expired-oauth-state")).Scan(&expiredCount)
	if expiredCount != 0 {
		t.Errorf("expected expired oauth state to be deleted, got count=%d", expiredCount)
	}

	var validCount int
	testPool.QueryRow(ctx, `SELECT COUNT(*) FROM oauth_states WHERE token_hash = $1`, workerTestHash("valid-oauth-state")).Scan(&validCount)
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
		INSERT INTO mfa_challenges (token_hash, user_id, expires_at)
		VALUES ($1, $2, NOW() - INTERVAL '1 minute')
	`, workerTestHash("expired-token-retention"), userID)
	testPool.Exec(ctx, `
		INSERT INTO mfa_challenges (token_hash, user_id, expires_at)
		VALUES ($1, $2, NOW() + INTERVAL '10 minutes')
	`, workerTestHash("valid-token-retention"), userID)

	retention.NewWorker(testPool, 90).RunOnce(ctx)

	var expiredCount int
	testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mfa_challenges WHERE token_hash = $1
	`, workerTestHash("expired-token-retention")).Scan(&expiredCount)
	if expiredCount != 0 {
		t.Errorf("expected expired MFA challenge to be deleted, got count=%d", expiredCount)
	}

	var validCount int
	testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mfa_challenges WHERE token_hash = $1
	`, workerTestHash("valid-token-retention")).Scan(&validCount)
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

func TestWorker_purgesOldAlertFirings(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	// Create an alert rule to attach firings to
	url := "https://example.com/hook"
	rule, err := storage.CreateAlertRule(ctx, testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "retention test", Enabled: true,
		Trigger: "new_issue", Channel: "webhook", WebhookURL: &url, CooldownMins: 60,
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}

	// Insert 1005 firings
	for i := 0; i < 1005; i++ {
		testPool.Exec(ctx, `
			INSERT INTO alert_firings (rule_id, trigger, channel, status, attempt)
			VALUES ($1, 'new_issue', 'webhook', 'success', 1)`, rule.ID)
	}

	var before int
	testPool.QueryRow(ctx, `SELECT COUNT(*) FROM alert_firings WHERE rule_id = $1`, rule.ID).Scan(&before)
	if before != 1005 {
		t.Fatalf("expected 1005 firings before purge, got %d", before)
	}

	retention.NewWorker(testPool, 90).RunOnce(ctx)

	var after int
	testPool.QueryRow(ctx, `SELECT COUNT(*) FROM alert_firings WHERE rule_id = $1`, rule.ID).Scan(&after)
	if after != 1000 {
		t.Errorf("expected 1000 firings after purge, got %d", after)
	}
}

func TestWorker_doesNotPurgeWhenUnder1000(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	url := "https://example.com/hook"
	rule, _ := storage.CreateAlertRule(ctx, testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "retention small", Enabled: true,
		Trigger: "new_issue", Channel: "webhook", WebhookURL: &url, CooldownMins: 60,
	})

	for i := 0; i < 5; i++ {
		testPool.Exec(ctx, `
			INSERT INTO alert_firings (rule_id, trigger, channel, status, attempt)
			VALUES ($1, 'new_issue', 'webhook', 'success', 1)`, rule.ID)
	}

	retention.NewWorker(testPool, 90).RunOnce(ctx)

	var after int
	testPool.QueryRow(ctx, `SELECT COUNT(*) FROM alert_firings WHERE rule_id = $1`, rule.ID).Scan(&after)
	if after != 5 {
		t.Errorf("expected 5 firings to survive, got %d", after)
	}
}
