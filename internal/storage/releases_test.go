package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func insertRelease(t *testing.T, projectID, version string) string {
	t.Helper()
	var id string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO releases (project_id, version, deployed_at)
		VALUES ($1, $2, $3)
		RETURNING id
	`, projectID, version, time.Now()).Scan(&id)
	if err != nil {
		t.Fatalf("insert release %q: %v", version, err)
	}
	return id
}

func TestListReleases_empty(t *testing.T) {
	truncateProjects(t)
	p, _ := storage.CreateProject(context.Background(), testPool, "rel-empty", "Rel Empty")

	releases, err := storage.ListReleases(context.Background(), testPool, storage.ReleaseFilter{ProjectIDs: []string{p.ID}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(releases) != 0 {
		t.Errorf("expected 0 releases, got %d", len(releases))
	}
}

func TestListReleases_filteredByProject(t *testing.T) {
	truncateProjects(t)
	p1, _ := storage.CreateProject(context.Background(), testPool, "rel-p1", "Project 1")
	p2, _ := storage.CreateProject(context.Background(), testPool, "rel-p2", "Project 2")

	insertRelease(t, p1.ID, "v1.0.0")
	insertRelease(t, p1.ID, "v1.1.0")
	insertRelease(t, p2.ID, "v2.0.0")

	releases, err := storage.ListReleases(context.Background(), testPool, storage.ReleaseFilter{ProjectIDs: []string{p1.ID}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("expected 2 releases for p1, got %d", len(releases))
	}
	for _, r := range releases {
		if r.ProjectID != p1.ID {
			t.Errorf("unexpected project_id %q in results", r.ProjectID)
		}
	}
}

func TestListReleases_allProjects(t *testing.T) {
	truncateProjects(t)
	p1, _ := storage.CreateProject(context.Background(), testPool, "rel-all1", "All 1")
	p2, _ := storage.CreateProject(context.Background(), testPool, "rel-all2", "All 2")

	insertRelease(t, p1.ID, "v1.0.0")
	insertRelease(t, p2.ID, "v2.0.0")

	releases, err := storage.ListReleases(context.Background(), testPool, storage.ReleaseFilter{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(releases) < 2 {
		t.Fatalf("expected at least 2 releases across both projects, got %d", len(releases))
	}
}

func TestListReleases_fields(t *testing.T) {
	truncateProjects(t)
	p, _ := storage.CreateProject(context.Background(), testPool, "rel-fields", "Fields")

	insertRelease(t, p.ID, "v1.2.3")

	releases, err := storage.ListReleases(context.Background(), testPool, storage.ReleaseFilter{ProjectIDs: []string{p.ID}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("expected 1 release, got %d", len(releases))
	}
	r := releases[0]
	if r.ID == "" {
		t.Error("expected non-empty ID")
	}
	if r.ProjectID != p.ID {
		t.Errorf("project_id: got %q", r.ProjectID)
	}
	if r.Version != "v1.2.3" {
		t.Errorf("version: got %q", r.Version)
	}
	if r.CreatedAt.IsZero() {
		t.Error("expected non-zero created_at")
	}
}

func TestGetRelease_found(t *testing.T) {
	truncateProjects(t)
	p, _ := storage.CreateProject(context.Background(), testPool, "rel-get", "Get Release")
	id := insertRelease(t, p.ID, "v3.0.0")

	r, err := storage.GetRelease(context.Background(), testPool, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	require.NotNil(t, r, "expected release, got nil")
	if r.ID != id {
		t.Errorf("ID: got %q, want %q", r.ID, id)
	}
	if r.Version != "v3.0.0" {
		t.Errorf("version: got %q", r.Version)
	}
}

func TestGetRelease_notFound(t *testing.T) {
	r, err := storage.GetRelease(context.Background(), testPool, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if r != nil {
		t.Errorf("expected nil, got %+v", r)
	}
}
