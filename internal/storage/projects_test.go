package storage_test

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

func truncateProjects(t *testing.T) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), "TRUNCATE projects CASCADE")
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func TestCreateProject(t *testing.T) {
	truncateProjects(t)

	p, err := storage.CreateProject(context.Background(), testPool, "my-app", "My App")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ID == "" {
		t.Error("expected non-empty ID")
	}
	if p.Slug != "my-app" {
		t.Errorf("slug: got %q, want %q", p.Slug, "my-app")
	}
	if p.Name != "My App" {
		t.Errorf("name: got %q, want %q", p.Name, "My App")
	}
	if len(p.PublicKey) != 32 {
		t.Errorf("public_key length: got %d, want 32", len(p.PublicKey))
	}
	if p.CreatedAt.IsZero() {
		t.Error("expected non-zero created_at")
	}
}

func TestCreateProject_duplicateSlug(t *testing.T) {
	truncateProjects(t)

	if _, err := storage.CreateProject(context.Background(), testPool, "dupe", "First"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := storage.CreateProject(context.Background(), testPool, "dupe", "Second")
	if err == nil {
		t.Error("expected error on duplicate slug")
	}
}

func TestListProjects(t *testing.T) {
	truncateProjects(t)

	for _, slug := range []string{"alpha", "beta", "gamma"} {
		if _, err := storage.CreateProject(context.Background(), testPool, slug, slug); err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
	}

	projects, err := storage.ListProjects(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(projects) != 3 {
		t.Fatalf("expected 3 projects, got %d", len(projects))
	}
	// ListProjects returns newest first
	if projects[0].Slug != "gamma" {
		t.Errorf("expected gamma first, got %q", projects[0].Slug)
	}
}

func TestDeleteProject(t *testing.T) {
	truncateProjects(t)

	if _, err := storage.CreateProject(context.Background(), testPool, "to-delete", "To Delete"); err != nil {
		t.Fatalf("create: %v", err)
	}

	found, err := storage.DeleteProject(context.Background(), testPool, "to-delete")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Error("expected found=true")
	}

	found, err = storage.DeleteProject(context.Background(), testPool, "to-delete")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected found=false on second delete")
	}
}

func TestGetByPublicKey_found(t *testing.T) {
	truncateProjects(t)

	created, err := storage.CreateProject(context.Background(), testPool, "lookup-me", "Lookup Me")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	p, err := storage.GetByPublicKey(context.Background(), testPool, created.PublicKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected project, got nil")
	}
	if p.ID != created.ID {
		t.Errorf("ID mismatch: got %q, want %q", p.ID, created.ID)
	}
}

func TestGetByPublicKey_notFound(t *testing.T) {
	p, err := storage.GetByPublicKey(context.Background(), testPool, "nonexistent-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Errorf("expected nil, got %+v", p)
	}
}

func TestGetProjectBySlug_found(t *testing.T) {
	truncateProjects(t)

	created, err := storage.CreateProject(context.Background(), testPool, "slug-lookup", "Slug Lookup")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	p, err := storage.GetProjectBySlug(context.Background(), testPool, "slug-lookup")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected project, got nil")
	}
	if p.ID != created.ID {
		t.Errorf("ID mismatch: got %q, want %q", p.ID, created.ID)
	}
}

func TestGetProjectBySlug_notFound(t *testing.T) {
	p, err := storage.GetProjectBySlug(context.Background(), testPool, "does-not-exist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Errorf("expected nil, got %+v", p)
	}
}

func TestGetByIDAndPublicKey_found(t *testing.T) {
	truncateProjects(t)

	created, err := storage.CreateProject(context.Background(), testPool, "id-and-key", "ID And Key")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	p, err := storage.GetByIDAndPublicKey(context.Background(), testPool, created.ID, created.PublicKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected project, got nil")
	}
	if p.ID != created.ID {
		t.Errorf("ID mismatch: got %q, want %q", p.ID, created.ID)
	}
}

func TestGetByIDAndPublicKey_wrongKey(t *testing.T) {
	truncateProjects(t)

	created, _ := storage.CreateProject(context.Background(), testPool, "id-wrong-key", "Test")

	p, err := storage.GetByIDAndPublicKey(context.Background(), testPool, created.ID, "wrong-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Error("expected nil for wrong key")
	}
}

func TestGetByIDAndPublicKey_wrongID(t *testing.T) {
	truncateProjects(t)

	created, _ := storage.CreateProject(context.Background(), testPool, "wrong-id", "Test")

	p, err := storage.GetByIDAndPublicKey(context.Background(), testPool, "00000000-0000-0000-0000-000000000000", created.PublicKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Error("expected nil for wrong ID")
	}
}

func TestUpdateProject_passthroughDSN(t *testing.T) {
	truncateProjects(t)

	p, err := storage.CreateProject(context.Background(), testPool, "pt-test", "PT Test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.PassthroughDSN != nil {
		t.Errorf("new project should have nil passthrough_dsn, got %q", *p.PassthroughDSN)
	}

	dsn := "https://abc123@sentry.io/456"
	updated, err := storage.UpdateProject(context.Background(), testPool, p.ID, p.Name, p.Slug, &dsn)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated == nil {
		t.Fatal("expected non-nil project")
	}
	if updated.PassthroughDSN == nil || *updated.PassthroughDSN != dsn {
		t.Errorf("passthrough_dsn: got %v, want %q", updated.PassthroughDSN, dsn)
	}

	// Confirm it's persisted.
	fetched, err := storage.GetByPublicKey(context.Background(), testPool, p.PublicKey)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fetched.PassthroughDSN == nil || *fetched.PassthroughDSN != dsn {
		t.Errorf("persisted passthrough_dsn: got %v, want %q", fetched.PassthroughDSN, dsn)
	}
}

func TestUpdateProject_clearPassthroughDSN(t *testing.T) {
	truncateProjects(t)

	p, err := storage.CreateProject(context.Background(), testPool, "pt-clear", "PT Clear")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	dsn := "https://abc123@sentry.io/456"
	p, err = storage.UpdateProject(context.Background(), testPool, p.ID, p.Name, p.Slug, &dsn)
	if err != nil {
		t.Fatalf("set dsn: %v", err)
	}

	cleared, err := storage.UpdateProject(context.Background(), testPool, p.ID, p.Name, p.Slug, nil)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cleared.PassthroughDSN != nil {
		t.Errorf("expected nil passthrough_dsn after clearing, got %q", *cleared.PassthroughDSN)
	}
}

func TestCountProjects_zero(t *testing.T) {
	truncateProjects(t)

	n, err := storage.CountProjects(context.Background(), testPool)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 projects, got %d", n)
	}
}

func TestCountProjects_nonZero(t *testing.T) {
	truncateProjects(t)

	for _, slug := range []string{"count-a", "count-b", "count-c"} {
		if _, err := storage.CreateProject(context.Background(), testPool, slug, slug); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	n, err := storage.CountProjects(context.Background(), testPool)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 projects, got %d", n)
	}
}

func TestDeleteProjectByID_found(t *testing.T) {
	truncateProjects(t)

	p, err := storage.CreateProject(context.Background(), testPool, "del-by-id", "Del By ID")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	deleted, err := storage.DeleteProjectByID(context.Background(), testPool, p.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !deleted {
		t.Error("expected deleted=true")
	}

	// Second delete: not found
	deleted, err = storage.DeleteProjectByID(context.Background(), testPool, p.ID)
	if err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if deleted {
		t.Error("expected deleted=false on second delete")
	}
}

func TestDeleteProjectByID_notFound(t *testing.T) {
	deleted, err := storage.DeleteProjectByID(context.Background(), testPool, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted {
		t.Error("expected deleted=false for nonexistent ID")
	}
}
