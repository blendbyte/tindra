// Package testutil provides shared database setup helpers for integration tests.
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
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

const templateDB = "tindra_test_template"

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

// ensureTemplateDB creates tindra_test_template with all migrations applied if
// it does not already exist. An advisory lock serializes concurrent calls from
// parallel test binaries so only the first one does the work; the rest return
// immediately once the lock is released and the template already exists.
func ensureTemplateDB(ctx context.Context, adminDSN string) {
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		log.Fatalf("testutil: connect admin for template: %v", err)
	}
	defer admin.Close()

	if _, err := admin.Exec(ctx, "SELECT pg_advisory_lock($1)", templateLockKey); err != nil {
		log.Fatalf("testutil: advisory lock: %v", err)
	}
	defer admin.Exec(ctx, "SELECT pg_advisory_unlock($1)", templateLockKey) //nolint:errcheck

	var exists bool
	if err := admin.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", templateDB,
	).Scan(&exists); err != nil {
		log.Fatalf("testutil: check template existence: %v", err)
	}
	if exists {
		return
	}

	if _, err := admin.Exec(ctx, "CREATE DATABASE "+templateDB); err != nil {
		log.Fatalf("testutil: create template DB: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		log.Fatalf("testutil: parse DSN: %v", err)
	}
	cfg.ConnConfig.Database = templateDB

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("testutil: connect template DB: %v", err)
	}
	runMigrations(ctx, pool)
	pool.Close()
}

func setupFromDSN(ctx context.Context, adminDSN string) (*pgxpool.Pool, func()) {
	ensureTemplateDB(ctx, adminDSN)

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

func setupContainer(ctx context.Context) (*pgxpool.Pool, func()) {
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
		log.Printf("testutil: no postgres container available, skipping db tests: %v", err)
		os.Exit(0)
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
	names, err := migrations.Files()
	if err != nil {
		log.Fatalf("testutil: list migrations: %v", err)
	}
	for _, name := range names {
		sql, err := migrations.FS.ReadFile(name)
		if err != nil {
			log.Fatalf("testutil: read migration %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			log.Fatalf("testutil: apply migration %s: %v", name, err)
		}
	}
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
