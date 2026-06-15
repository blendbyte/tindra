package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/storage"
)

func projectsCfg() config {
	return config{databaseURL: testUserDSN}
}

func truncateProjects(t *testing.T) {
	t.Helper()
	_, err := testUserPool.Exec(context.Background(), "TRUNCATE projects CASCADE")
	if err != nil {
		t.Fatalf("truncate projects: %v", err)
	}
}

func TestProjectsCreateCmd_missingFlags(t *testing.T) {
	cmd := projectsCreateCmd(projectsCfg())
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --name and --slug are missing")
	}
}

func TestProjectsCreateCmd_success(t *testing.T) {
	truncateProjects(t)

	cmd := projectsCreateCmd(projectsCfg())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--name", "My Project", "--slug", "my-project"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

func TestProjectsCreateCmd_limitReached(t *testing.T) {
	truncateProjects(t)

	if _, err := storage.CreateProject(context.Background(), testUserPool, "first-project", "First Project"); err != nil {
		t.Fatalf("create first project: %v", err)
	}

	cfg := projectsCfg()
	cfg.projectLimit = 1

	cmd := projectsCreateCmd(cfg)
	cmd.SetArgs([]string{"--name", "Second Project", "--slug", "second-project"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when project limit is reached")
	}
	if !strings.Contains(err.Error(), "project limit") {
		t.Errorf("expected project limit error, got: %v", err)
	}
}

func TestProjectsListCmd_empty(t *testing.T) {
	truncateProjects(t)

	cmd := projectsListCmd(projectsCfg())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

func TestProjectsListCmd_withProject(t *testing.T) {
	truncateProjects(t)

	if _, err := storage.CreateProject(context.Background(), testUserPool, "listed-project", "Listed Project"); err != nil {
		t.Fatalf("create project: %v", err)
	}

	cmd := projectsListCmd(projectsCfg())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	projects, err := storage.ListProjects(context.Background(), testUserPool)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	if projects[0].Slug != "listed-project" {
		t.Errorf("slug: got %q, want listed-project", projects[0].Slug)
	}
}

func TestProjectsDeleteCmd_notFound(t *testing.T) {
	truncateProjects(t)

	cmd := projectsDeleteCmd(projectsCfg())
	cmd.SetArgs([]string{"nonexistent-slug"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when deleting non-existent project")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestProjectsDeleteCmd_success(t *testing.T) {
	truncateProjects(t)

	if _, err := storage.CreateProject(context.Background(), testUserPool, "to-delete", "To Delete"); err != nil {
		t.Fatalf("create project: %v", err)
	}

	cmd := projectsDeleteCmd(projectsCfg())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"to-delete"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}
