package main

import (
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
