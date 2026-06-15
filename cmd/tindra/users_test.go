package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
)

var (
	testUserPool *pgxpool.Pool
	testUserDSN  string
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	pool, dsn, cleanup := testutil.SetupDBWithDSN(ctx)
	testUserPool = pool
	testUserDSN = dsn
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func truncateUsersAndInvites(t *testing.T) {
	t.Helper()
	_, err := testUserPool.Exec(context.Background(), "TRUNCATE users CASCADE")
	if err != nil {
		t.Fatalf("truncate users: %v", err)
	}
	_, err = testUserPool.Exec(context.Background(), "TRUNCATE user_invites")
	if err != nil {
		t.Fatalf("truncate user_invites: %v", err)
	}
}

func inviteCfg() config {
	return config{
		databaseURL: testUserDSN,
		publicURL:   "https://tindra.example.com",
	}
}

// --- send-invite ---

func TestSendInviteCmd_missingEmail(t *testing.T) {
	cmd := usersSendInviteCmd(inviteCfg())
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --email flag is missing")
	}
}

func TestSendInviteCmd_createsInvite(t *testing.T) {
	truncateUsersAndInvites(t)

	cmd := usersSendInviteCmd(inviteCfg())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--email", "invited@example.com"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	invites, err := storage.ListPendingInvites(context.Background(), testUserPool)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	if len(invites) != 1 {
		t.Fatalf("expected 1 pending invite, got %d", len(invites))
	}
	if invites[0].Email != "invited@example.com" {
		t.Errorf("email: got %q, want invited@example.com", invites[0].Email)
	}
}

func TestSendInviteCmd_printsInviteURL(t *testing.T) {
	truncateUsersAndInvites(t)

	cmd := usersSendInviteCmd(inviteCfg())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--email", "url-check@example.com"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "https://tindra.example.com/invite/") {
		t.Errorf("expected invite URL in output, got: %q", output)
	}
}

func TestSendInviteCmd_withName(t *testing.T) {
	truncateUsersAndInvites(t)

	cmd := usersSendInviteCmd(inviteCfg())
	cmd.SetArgs([]string{"--email", "named@example.com", "--name", "Alice"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	invites, err := storage.ListPendingInvites(context.Background(), testUserPool)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	if len(invites) != 1 {
		t.Fatalf("expected 1 invite, got %d", len(invites))
	}
	if invites[0].Name != "Alice" {
		t.Errorf("name: got %q, want Alice", invites[0].Name)
	}
}

func TestSendInviteCmd_userLimitReached(t *testing.T) {
	truncateUsersAndInvites(t)

	// Create one user so count=1, then set limit=1.
	if _, err := storage.CreateUser(context.Background(), testUserPool, "existing@example.com", "password1234"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	cfg := inviteCfg()
	cfg.userLimit = 1

	cmd := usersSendInviteCmd(cfg)
	cmd.SetArgs([]string{"--email", "blocked@example.com"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when user limit is reached")
	}
	if !strings.Contains(err.Error(), "user limit") {
		t.Errorf("expected user limit error, got: %v", err)
	}
}

func TestSendInviteCmd_emailNormalised(t *testing.T) {
	truncateUsersAndInvites(t)

	cmd := usersSendInviteCmd(inviteCfg())
	cmd.SetArgs([]string{"--email", "UPPER@Example.COM"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	invites, err := storage.ListPendingInvites(context.Background(), testUserPool)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	if len(invites) != 1 {
		t.Fatalf("expected 1 invite, got %d", len(invites))
	}
	if invites[0].Email != "upper@example.com" {
		t.Errorf("email not lowercased: got %q", invites[0].Email)
	}
}
