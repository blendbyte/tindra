package issues_test

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

	project, err := storage.CreateProject(ctx, pool, "issues-test", "Issues Test")
	if err != nil {
		log.Fatalf("create test project: %v", err)
	}

	testPool = pool
	testProject = project

	code := m.Run()

	pool.Close()
	_ = ctr.Terminate(ctx)
	os.Exit(code)
}
