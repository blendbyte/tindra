package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/blendbyte/tindra/migrations"
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
	want := map[string]bool{
		"create":              false,
		"list":                false,
		"send-password-reset": false,
		"send-invite":         false,
		"disable-mfa":         false,
	}
	for _, sub := range cmd.Commands() {
		if _, ok := want[sub.Name()]; !ok {
			t.Errorf("unexpected subcommand %q", sub.Name())
			continue
		}
		want[sub.Name()] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing subcommand %q", name)
		}
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

// latestMigrationVersion opens the embedded migration source and walks it to
// find the highest available version number. This is the version to seed into
// schema_migrations so that m.Up() sees no pending work and returns ErrNoChange.
func latestMigrationVersion(t *testing.T) int {
	t.Helper()
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("open migrations source: %v", err)
	}
	defer src.Close()
	v, err := src.First()
	if err != nil {
		t.Fatalf("migrations source has no files: %v", err)
	}
	for {
		next, err := src.Next(v)
		if err != nil {
			break
		}
		v = next
	}
	return int(v)
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
	// Seed schema_migrations with the actual latest migration version so that
	// m.Up() finds no pending migrations and returns ErrNoChange, which the
	// command treats as success.
	setupSchemaMigrations(t, latestMigrationVersion(t))
	cmd := migrateCmd(inviteCfg())
	cmd.SetOut(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

func TestMigrateForceCmd_success(t *testing.T) {
	// Ensure schema_migrations exists with a known version, then force it to 1.
	// Force() sets the recorded version without running any SQL migrations,
	// so it works regardless of the DB schema state.
	setupSchemaMigrations(t, latestMigrationVersion(t))
	cmd := migrateForceCmd(inviteCfg())
	cmd.SetOut(io.Discard)
	cmd.SetArgs([]string{"1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}
