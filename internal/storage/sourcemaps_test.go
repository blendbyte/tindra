package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func setupProjectForSourcemaps(t *testing.T) *storage.Project {
	t.Helper()
	truncateProjects(t)
	p, err := storage.CreateProject(context.Background(), testPool, "sm-test", "SM Test")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p
}

func truncateSourcemaps(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE sourcemaps CASCADE"); err != nil {
		t.Fatalf("truncate sourcemaps: %v", err)
	}
}

func TestUpsertSourcemap_insert(t *testing.T) {
	p := setupProjectForSourcemaps(t)

	sm, err := storage.UpsertSourcemap(context.Background(), testPool, &storage.Sourcemap{
		ProjectID:   p.ID,
		Release:     "v1.0.0",
		URL:         "~/dist/main.js",
		ContentHash: "abc123",
		SizeBytes:   1024,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if sm.ID == "" {
		t.Error("expected non-empty ID")
	}
	if sm.ContentHash != "abc123" {
		t.Errorf("content_hash: got %q", sm.ContentHash)
	}
	if sm.SizeBytes != 1024 {
		t.Errorf("size_bytes: got %d, want 1024", sm.SizeBytes)
	}
}

func TestUpsertSourcemap_replace(t *testing.T) {
	p := setupProjectForSourcemaps(t)
	truncateSourcemaps(t)

	storage.UpsertSourcemap(context.Background(), testPool, &storage.Sourcemap{
		ProjectID: p.ID, Release: "v1", URL: "~/app.js", ContentHash: "old-hash", SizeBytes: 100,
	})

	updated, err := storage.UpsertSourcemap(context.Background(), testPool, &storage.Sourcemap{
		ProjectID: p.ID, Release: "v1", URL: "~/app.js", ContentHash: "new-hash", SizeBytes: 200,
	})
	if err != nil {
		t.Fatalf("upsert replace: %v", err)
	}
	if updated.ContentHash != "new-hash" {
		t.Errorf("expected new-hash, got %q", updated.ContentHash)
	}
	if updated.SizeBytes != 200 {
		t.Errorf("size_bytes: got %d, want 200", updated.SizeBytes)
	}
}

func TestGetSourcemap_found(t *testing.T) {
	p := setupProjectForSourcemaps(t)
	truncateSourcemaps(t)

	storage.UpsertSourcemap(context.Background(), testPool, &storage.Sourcemap{
		ProjectID: p.ID, Release: "v2", URL: "~/chunk.js", ContentHash: "hash-xyz", SizeBytes: 512,
	})

	got, err := storage.GetSourcemap(context.Background(), testPool, p.ID, "v2", "~/chunk.js")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	require.NotNil(t, got, "expected sourcemap, got nil")
	if got.ContentHash != "hash-xyz" {
		t.Errorf("content_hash: got %q", got.ContentHash)
	}
}

func TestGetSourcemap_notFound(t *testing.T) {
	p := setupProjectForSourcemaps(t)

	got, err := storage.GetSourcemap(context.Background(), testPool, p.ID, "v99", "~/nope.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestListSourcemaps_all(t *testing.T) {
	p := setupProjectForSourcemaps(t)
	truncateSourcemaps(t)

	for i, url := range []string{"~/a.js", "~/b.js", "~/c.js"} {
		storage.UpsertSourcemap(context.Background(), testPool, &storage.Sourcemap{
			ProjectID: p.ID, Release: "v1", URL: url,
			ContentHash: string(rune('a' + i)), SizeBytes: 1,
		})
	}

	maps, err := storage.ListSourcemaps(context.Background(), testPool, p.ID, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(maps) != 3 {
		t.Errorf("expected 3, got %d", len(maps))
	}
}

func TestListSourcemaps_filterByRelease(t *testing.T) {
	p := setupProjectForSourcemaps(t)
	truncateSourcemaps(t)

	storage.UpsertSourcemap(context.Background(), testPool, &storage.Sourcemap{
		ProjectID: p.ID, Release: "v1", URL: "~/x.js", ContentHash: "h1", SizeBytes: 1,
	})
	storage.UpsertSourcemap(context.Background(), testPool, &storage.Sourcemap{
		ProjectID: p.ID, Release: "v2", URL: "~/y.js", ContentHash: "h2", SizeBytes: 1,
	})

	maps, err := storage.ListSourcemaps(context.Background(), testPool, p.ID, "v1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(maps) != 1 {
		t.Errorf("expected 1, got %d", len(maps))
	}
	if maps[0].Release != "v1" {
		t.Errorf("release: got %q", maps[0].Release)
	}
}

func TestListSourcemaps_empty(t *testing.T) {
	p := setupProjectForSourcemaps(t)
	truncateSourcemaps(t)

	maps, err := storage.ListSourcemaps(context.Background(), testPool, p.ID, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if maps != nil {
		t.Errorf("expected nil slice, got %v", maps)
	}
}

func TestDeleteSourcemap_found(t *testing.T) {
	p := setupProjectForSourcemaps(t)
	truncateSourcemaps(t)

	sm, _ := storage.UpsertSourcemap(context.Background(), testPool, &storage.Sourcemap{
		ProjectID: p.ID, Release: "v1", URL: "~/del.js", ContentHash: "hd", SizeBytes: 1,
	})

	contentHash, err := storage.DeleteSourcemap(context.Background(), testPool, sm.ID, p.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if contentHash == "" {
		t.Error("expected non-empty content hash for deleted record")
	}

	got, _ := storage.GetSourcemap(context.Background(), testPool, p.ID, "v1", "~/del.js")
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestDeleteSourcemap_notFound(t *testing.T) {
	p := setupProjectForSourcemaps(t)

	contentHash, err := storage.DeleteSourcemap(context.Background(), testPool, "00000000-0000-0000-0000-000000000000", p.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if contentHash != "" {
		t.Error("expected empty content hash for non-existent ID")
	}
}

func TestCountSourcemapsByHash(t *testing.T) {
	truncateProjects(t)
	p, err := storage.CreateProject(context.Background(), testPool, "sm-count", "SM Count")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	ctx := context.Background()

	// No sourcemaps yet
	n, err := storage.CountSourcemapsByHash(ctx, testPool, p.ID, "abc123")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}

	// Add a sourcemap
	storage.UpsertSourcemap(ctx, testPool, &storage.Sourcemap{
		ProjectID: p.ID, Release: "v1", URL: "app.js", ContentHash: "abc123", SizeBytes: 13,
	})

	n, err = storage.CountSourcemapsByHash(ctx, testPool, p.ID, "abc123")
	if err != nil {
		t.Fatalf("count after insert: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1, got %d", n)
	}
}
