package issues_test

import (
	"context"
	"log"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

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

	project, err := storage.CreateProject(ctx, pool, "issues-test", "Issues Test")
	if err != nil {
		log.Fatalf("create test project: %v", err)
	}

	testPool = pool
	testProject = project

	code := m.Run()
	cleanup()
	os.Exit(code)
}
