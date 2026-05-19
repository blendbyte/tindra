package retention_test

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	testcontainers "github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/blendbyte/tindra/internal/retention"
	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/migrations"
)

var (
	testPool    *pgxpool.Pool
	testProject *storage.Project
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:18",
		tcpostgres.WithDatabase("tindra_test"),
		tcpostgres.WithUsername("tindra"),
		tcpostgres.WithPassword("tindra"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		log.Fatalf("start postgres: %v", err)
	}

	connStr, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatalf("connection string: %v", err)
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
			log.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			log.Fatalf("apply %s: %v", name, err)
		}
	}

	project, err := storage.CreateProject(ctx, pool, "ret-test", "Retention Test")
	if err != nil {
		log.Fatalf("create project: %v", err)
	}

	testPool = pool
	testProject = project

	code := m.Run()
	pool.Close()
	_ = ctr.Terminate(ctx)
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

	// Insert one fresh event and one stale event (91 days old).
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

	// Run purges once immediately before entering the ticker loop, so a short-lived
	// context is enough to trigger one purge cycle and then exit cleanly.
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { retention.NewWorker(testPool, 90).Run(runCtx); close(done) }()
	<-done

	var count int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM events WHERE project_id = $1", testProject.ID).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 event (fresh only), got %d", count)
	}

	// Stale issue should be gone (its only event was deleted).
	staleIss, _ := storage.GetIssue(ctx, testPool, iss.ID)
	if staleIss != nil {
		t.Error("stale issue should have been deleted")
	}

	// Fresh issue should still exist.
	freshIss, _ := storage.GetIssue(ctx, testPool, fresh.ID)
	if freshIss == nil {
		t.Error("fresh issue should still exist")
	}
}

func TestWorker_disabled(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	// Insert a stale event.
	iss, _, _, _ := storage.UpsertIssue(ctx, testPool, testProject.ID, "fp-dis", "Disabled", "error", "error", "", "", time.Now().AddDate(0, 0, -200))
	testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, $2, $2, '{"level":"error"}'::jsonb, 'fp-dis', $3)
	`, testProject.ID, time.Now().AddDate(0, 0, -200), iss.ID)

	// Worker with retentionDays=0 returns immediately without touching the DB.
	done := make(chan struct{})
	go func() { retention.NewWorker(testPool, 0).Run(ctx); close(done) }()
	<-done

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

	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { retention.NewWorker(testPool, 90).Run(runCtx); close(done) }()
	<-done

	var count int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM transactions WHERE project_id = $1", testProject.ID).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 transaction (fresh only), got %d", count)
	}
}

func TestWorker_deletesOldLogs(t *testing.T) {
	ctx := context.Background()

	// Clean up logs before and after so we don't interfere with other tests.
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

	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { retention.NewWorker(testPool, 90).Run(runCtx); close(done) }()
	<-done

	var count int
	testPool.QueryRow(ctx, "SELECT COUNT(*) FROM logs WHERE project_id = $1", testProject.ID).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 log (fresh only), got %d", count)
	}
}

func TestWorker_purgesExpiredMFAChallenges(t *testing.T) {
	ctx := context.Background()

	// Create a throwaway user to satisfy the FK on mfa_challenges.
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

	// Insert an already-expired challenge and a still-valid one.
	testPool.Exec(ctx, `
		INSERT INTO mfa_challenges (token, user_id, expires_at)
		VALUES ('expired-token-retention', $1, NOW() - INTERVAL '1 minute')
	`, userID)
	testPool.Exec(ctx, `
		INSERT INTO mfa_challenges (token, user_id, expires_at)
		VALUES ('valid-token-retention', $1, NOW() + INTERVAL '10 minutes')
	`, userID)

	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { retention.NewWorker(testPool, 90).Run(runCtx); close(done) }()
	<-done

	// Expired challenge must be gone.
	var expiredCount int
	testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mfa_challenges WHERE token = 'expired-token-retention'
	`).Scan(&expiredCount)
	if expiredCount != 0 {
		t.Errorf("expected expired MFA challenge to be deleted, got count=%d", expiredCount)
	}

	// Valid challenge must survive.
	var validCount int
	testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mfa_challenges WHERE token = 'valid-token-retention'
	`).Scan(&validCount)
	if validCount != 1 {
		t.Errorf("expected valid MFA challenge to survive, got count=%d", validCount)
	}
}
