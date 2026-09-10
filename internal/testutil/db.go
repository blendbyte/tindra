// Package testutil provides shared database setup helpers for integration tests.
package testutil

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	testcontainers "github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"

	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/migrations"
)

const templateReady = "tindra test template ready"

// advisory lock key used to serialize template creation across parallel test
// binaries sharing the same postgres instance.
const templateLockKey = int64(0x74696e6472615f74) // "tindra_t"

// SetupDB returns a pool connected to a migrated test database and a cleanup
// function that must be called when the test binary exits.
//
// If TINDRA_TEST_DSN is set (e.g. "postgres://tindra:tindra@localhost:5432/postgres?sslmode=disable")
// a short-lived database is created on that server instead of starting a container.
// This makes repeated local runs near-instant — just keep `make db` running.
func SetupDB(ctx context.Context) (*pgxpool.Pool, func()) {
	storage.BcryptCost = bcrypt.MinCost
	if dsn := os.Getenv("TINDRA_TEST_DSN"); dsn != "" {
		return setupFromDSN(ctx, dsn)
	}
	return setupContainer(ctx)
}

// templateName changes whenever a migration is added, renamed, or edited.
// Separate names let test binaries from different revisions safely share a server.
func templateName(files fs.FS) (string, error) {
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return "", fmt.Errorf("list migrations: %w", err)
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no migrations found")
	}
	hash := sha256.New()
	for _, name := range names {
		sql, err := fs.ReadFile(files, name)
		if err != nil {
			return "", fmt.Errorf("read migration %s: %w", name, err)
		}
		fmt.Fprintf(hash, "%d:%s%d:%s", len(name), name, len(sql), sql)
	}
	return "tindra_test_template_" + hex.EncodeToString(hash.Sum(nil)[:16]), nil
}

// ensureTemplateDB publishes a reusable template only after every migration
// succeeds. Incomplete builds are rebuilt under a session-scoped advisory lock.
func ensureTemplateDB(ctx context.Context, adminDSN string, files fs.FS) (string, error) {
	name, err := templateName(files)
	if err != nil {
		return "", err
	}
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		return "", fmt.Errorf("connect admin for template: %w", err)
	}
	defer admin.Close()
	conn, err := admin.Acquire(ctx)
	if err != nil {
		return "", fmt.Errorf("acquire template connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", templateLockKey); err != nil {
		return "", fmt.Errorf("lock template: %w", err)
	}
	// Closing the admin pool also releases this session lock if cancellation
	// prevents explicit unlock. No other operation borrows this connection.
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", templateLockKey)
	}()
	var exists, ready bool
	err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1),
  EXISTS(SELECT 1 FROM pg_database WHERE datname=$1 AND shobj_description(oid,'pg_database')=$2)`, name, templateReady).Scan(&exists, &ready)
	if err != nil {
		return "", fmt.Errorf("check template: %w", err)
	}
	if ready {
		return name, nil
	}
	if exists {
		if _, err := conn.Exec(ctx, "DROP DATABASE "+name); err != nil {
			return "", fmt.Errorf("drop incomplete template: %w", err)
		}
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		return "", fmt.Errorf("create template: %w", err)
	}
	cfg := admin.Config()
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("connect template: %w", err)
	}
	err = applyMigrations(ctx, pool, files)
	pool.Close() // Cloning requires the source database to have no open sessions.
	if err != nil {
		return "", err
	}
	if _, err := conn.Exec(ctx, "COMMENT ON DATABASE "+name+" IS '"+templateReady+"'"); err != nil {
		return "", fmt.Errorf("mark template ready: %w", err)
	}
	return name, nil
}

func setupFromDSN(ctx context.Context, adminDSN string) (*pgxpool.Pool, func()) {
	templateDB, err := ensureTemplateDB(ctx, adminDSN, migrations.FS)
	if err != nil {
		log.Fatalf("testutil: prepare template: %v", err)
	}

	suffix := randomHex(4)
	dbName := "tindra_test_" + suffix

	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		log.Fatalf("testutil: connect to admin db: %v", err)
	}
	// Clone the pre-migrated template — much faster than running migrations again.
	if _, err := adminPool.Exec(ctx, fmt.Sprintf(
		"CREATE DATABASE %s TEMPLATE %s", dbName, templateDB,
	)); err != nil {
		log.Fatalf("testutil: create test db %s: %v", dbName, err)
	}
	adminPool.Close()

	cfg, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		log.Fatalf("testutil: parse DSN: %v", err)
	}
	cfg.ConnConfig.Database = dbName

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("testutil: connect to test db: %v", err)
	}

	cleanup := func() {
		pool.Close()
		a, err := pgxpool.New(ctx, adminDSN)
		if err == nil {
			_, _ = a.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", dbName))
			a.Close()
		}
	}
	return pool, cleanup
}

var startPostgres = tcpostgres.Run

func setupContainer(ctx context.Context) (*pgxpool.Pool, func()) {
	ctr, err := startPostgres(ctx, "postgres:18",
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
		log.Fatalf("testutil: postgres container is required for database tests: %v", err)
	}

	connStr, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatalf("testutil: get connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		log.Fatalf("testutil: create pool: %v", err)
	}
	runMigrations(ctx, pool)

	cleanup := func() {
		pool.Close()
		_ = ctr.Terminate(ctx)
	}
	return pool, cleanup
}

func runMigrations(ctx context.Context, pool *pgxpool.Pool) {
	if err := applyMigrations(ctx, pool, migrations.FS); err != nil {
		log.Fatalf("testutil: %v", err)
	}
}

func applyMigrations(ctx context.Context, pool *pgxpool.Pool, files fs.FS) error {
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	if len(names) == 0 {
		return fmt.Errorf("no migrations found")
	}
	for _, name := range names {
		sql, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		for i, batch := range migrations.Batches(string(sql)) {
			if _, err := pool.Exec(ctx, batch); err != nil {
				return fmt.Errorf("apply migration %s batch %d: %w", name, i+1, err)
			}
		}
	}
	return nil
}

// SetupDBWithDSN is like SetupDB but also returns the connection string for the
// test database, which is needed by CLI commands that open their own connection.
func SetupDBWithDSN(ctx context.Context) (*pgxpool.Pool, string, func()) {
	pool, cleanup := SetupDB(ctx)
	cc := pool.Config().ConnConfig
	var dsn string
	if cc.Password != "" {
		dsn = fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
			cc.User, cc.Password, cc.Host, cc.Port, cc.Database)
	} else {
		dsn = fmt.Sprintf("postgres://%s@%s:%d/%s?sslmode=disable",
			cc.User, cc.Host, cc.Port, cc.Database)
	}
	return pool, dsn, cleanup
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
