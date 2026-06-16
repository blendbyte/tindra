package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestNewMigrator_success(t *testing.T) {
	m, err := newMigrator(inviteCfg())
	if err != nil {
		t.Fatalf("newMigrator: %v", err)
	}
	defer m.Close()
}

func TestMigrateForceCmd_invalidVersion(t *testing.T) {
	cmd := migrateForceCmd(inviteCfg())
	cmd.SetArgs([]string{"notanumber"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for invalid version")
	}
}

func TestProjectsCmd_hasSubcommands(t *testing.T) {
	cmd := projectsCmd(inviteCfg())
	if cmd == nil {
		t.Fatal("projectsCmd returned nil")
	}
	if cmd.Use != "projects" {
		t.Errorf("Use: got %q, want %q", cmd.Use, "projects")
	}
	if len(cmd.Commands()) != 3 {
		t.Errorf("subcommands: got %d, want 3", len(cmd.Commands()))
	}
}

func TestUsersCmd_hasSubcommands(t *testing.T) {
	cmd := usersCmd(inviteCfg())
	if cmd == nil {
		t.Fatal("usersCmd returned nil")
	}
	if cmd.Use != "users" {
		t.Errorf("Use: got %q, want %q", cmd.Use, "users")
	}
	if len(cmd.Commands()) != 4 {
		t.Errorf("subcommands: got %d, want 4", len(cmd.Commands()))
	}
}

func TestSendDigestCmd_noEmailProvider(t *testing.T) {
	// Unset EMAIL_PROVIDER to ensure NewEmailSenderFromEnv returns nil.
	t.Setenv("EMAIL_PROVIDER", "")
	cmd := sendDigestCmd(inviteCfg())
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when EMAIL_PROVIDER not configured")
	}
	if !strings.Contains(err.Error(), "EMAIL_PROVIDER") {
		t.Errorf("expected EMAIL_PROVIDER error, got: %v", err)
	}
}

// setupSchemaMigrations creates the schema_migrations table (if absent) and
// seeds it with the given version so that m.Up() treats all migrations as
// already applied and returns migrate.ErrNoChange.
func setupSchemaMigrations(t *testing.T, version int) {
	t.Helper()
	ctx := context.Background()
	_, err := testUserPool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version bigint not null primary key,
			dirty   boolean not null
		)`)
	if err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if _, err := testUserPool.Exec(ctx, "DELETE FROM schema_migrations"); err != nil {
		t.Fatalf("clear schema_migrations: %v", err)
	}
	if _, err := testUserPool.Exec(ctx,
		"INSERT INTO schema_migrations(version, dirty) VALUES($1, false)", version); err != nil {
		t.Fatalf("seed schema_migrations version %d: %v", version, err)
	}
}

func TestMigrateCmd_noChange(t *testing.T) {
	// Pre-seed schema_migrations with a version higher than any known migration
	// file. m.Up() finds no pending migrations and returns ErrNoChange, which
	// the command treats as success.
	setupSchemaMigrations(t, 999999)
	cmd := migrateCmd(inviteCfg())
	cmd.SetOut(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

func TestMigrateForceCmd_success(t *testing.T) {
	// schema_migrations must exist; ensureVersionTable creates it if absent.
	// Force(1) sets the recorded version to 1 without running migrations.
	cmd := migrateForceCmd(inviteCfg())
	cmd.SetOut(io.Discard)
	cmd.SetArgs([]string{"1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}
