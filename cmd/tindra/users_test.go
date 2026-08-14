package main

import (
	"bytes"
	"context"
	"os"
	"strconv"
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

// --- create ---

func TestCreateUserCmd_missingEmail(t *testing.T) {
	cmd := usersCreateCmd(inviteCfg())
	cmd.SetArgs([]string{"--password", "password1234"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error when --email flag is missing")
	}
}

func TestCreateUserCmd_missingPassword(t *testing.T) {
	cmd := usersCreateCmd(inviteCfg())
	cmd.SetArgs([]string{"--email", "admin@example.com"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error when --password flag is missing")
	}
}

func TestCreateUserCmd_success(t *testing.T) {
	truncateUsersAndInvites(t)

	cmd := usersCreateCmd(inviteCfg())
	cmd.SetArgs([]string{"--email", "admin@example.com", "--password", "password1234"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	count, err := storage.CountUsers(context.Background(), testUserPool)
	if err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 user, got %d", count)
	}
}

func TestCreateUserCmd_limitReached(t *testing.T) {
	truncateUsersAndInvites(t)

	if _, err := storage.CreateUser(context.Background(), testUserPool, "first@example.com", "password1234"); err != nil {
		t.Fatalf("create initial user: %v", err)
	}

	cfg := inviteCfg()
	cfg.userLimit = 1

	cmd := usersCreateCmd(cfg)
	cmd.SetArgs([]string{"--email", "second@example.com", "--password", "password1234"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when user limit is reached")
	}
	if !strings.Contains(err.Error(), "user limit") {
		t.Errorf("expected user limit error, got: %v", err)
	}
}

// --- list ---

func TestListUsersCmd_empty(t *testing.T) {
	truncateUsersAndInvites(t)

	cmd := usersListCmd(inviteCfg())
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

func TestListUsersCmd_withUser(t *testing.T) {
	truncateUsersAndInvites(t)

	if _, err := storage.CreateUser(context.Background(), testUserPool, "listme@example.com", "password1234"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	cmd := usersListCmd(inviteCfg())
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

// --- send-password-reset ---

func TestSendPasswordResetCmd_missingArg(t *testing.T) {
	cmd := usersSendPasswordResetCmd(inviteCfg())
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error when email argument is missing")
	}
}

func TestSendPasswordResetCmd_userNotFound(t *testing.T) {
	truncateUsersAndInvites(t)

	cmd := usersSendPasswordResetCmd(inviteCfg())
	cmd.SetArgs([]string{"nobody@example.com"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for unknown email")
	}
	if !strings.Contains(err.Error(), "no user found") {
		t.Errorf("expected 'no user found' error, got: %v", err)
	}
}

func TestSendPasswordResetCmd_noEmailSender(t *testing.T) {
	truncateUsersAndInvites(t)

	if _, err := storage.CreateUser(context.Background(), testUserPool, "reset@example.com", "password1234"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	cmd := usersSendPasswordResetCmd(inviteCfg())
	cmd.SetArgs([]string{"reset@example.com"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

func TestSendPasswordResetCmd_sendsEmail(t *testing.T) {
	truncateUsersAndInvites(t)

	if _, err := storage.CreateUser(context.Background(), testUserPool, "reset-smtp@example.com", "password1234"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	srv := testutil.NewFakeSMTP(t, false)
	t.Setenv("EMAIL_PROVIDER", "smtp")
	t.Setenv("EMAIL_FROM", "noreply@example.com")
	t.Setenv("SMTP_HOST", "127.0.0.1")
	t.Setenv("SMTP_PORT", strconv.Itoa(srv.Port()))
	t.Setenv("SMTP_USERNAME", "")
	t.Setenv("SMTP_PASSWORD", "")

	cmd := usersSendPasswordResetCmd(inviteCfg())
	cmd.SetArgs([]string{"reset-smtp@example.com"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if msgs := srv.Received(); len(msgs) != 1 {
		t.Errorf("expected 1 email sent, got %d", len(msgs))
	}
}

func TestSendInviteCmd_sendsEmail(t *testing.T) {
	truncateUsersAndInvites(t)

	srv := testutil.NewFakeSMTP(t, false)
	t.Setenv("EMAIL_PROVIDER", "smtp")
	t.Setenv("EMAIL_FROM", "noreply@example.com")
	t.Setenv("SMTP_HOST", "127.0.0.1")
	t.Setenv("SMTP_PORT", strconv.Itoa(srv.Port()))
	t.Setenv("SMTP_USERNAME", "")
	t.Setenv("SMTP_PASSWORD", "")

	cmd := usersSendInviteCmd(inviteCfg())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--email", "invited-smtp@example.com"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if msgs := srv.Received(); len(msgs) != 1 {
		t.Errorf("expected 1 invite email sent, got %d", len(msgs))
	}
	if !strings.Contains(out.String(), "invited-smtp@example.com") {
		t.Errorf("expected success output mentioning recipient, got: %q", out.String())
	}
}

// --- disable-mfa ---

func TestDisableMFACmd_missingArg(t *testing.T) {
	cmd := usersDisableMFACmd(inviteCfg())
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error when email argument is missing")
	}
}

func TestDisableMFACmd_userNotFound(t *testing.T) {
	truncateUsersAndInvites(t)

	cmd := usersDisableMFACmd(inviteCfg())
	cmd.SetArgs([]string{"nobody@example.com"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown email")
	}
	if !strings.Contains(err.Error(), "no user found") {
		t.Errorf("expected 'no user found' error, got: %v", err)
	}
}

func TestDisableMFACmd_clearsSecretAndFlag(t *testing.T) {
	truncateUsersAndInvites(t)
	ctx := context.Background()

	u, err := storage.CreateUser(ctx, testUserPool, "locked-out@example.com", "password1234")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := storage.StoreMFASecret(ctx, testUserPool, u.ID, "JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatalf("store mfa secret: %v", err)
	}
	if err := storage.EnableMFA(ctx, testUserPool, u.ID); err != nil {
		t.Fatalf("enable mfa: %v", err)
	}

	cmd := usersDisableMFACmd(inviteCfg())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"locked-out@example.com"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "locked-out@example.com") {
		t.Errorf("expected output naming the user, got: %q", out.String())
	}

	after, err := storage.GetUserByEmail(ctx, testUserPool, "locked-out@example.com")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if after.MFAEnabled {
		t.Error("expected mfa_enabled to be false after disable-mfa")
	}
	secret, err := storage.GetMFASecret(ctx, testUserPool, u.ID)
	if err != nil {
		t.Fatalf("get mfa secret: %v", err)
	}
	if secret != nil {
		t.Errorf("expected mfa_secret to be cleared, got %q", *secret)
	}
}

// The email is uppercased here to confirm the lookup lowercases it the same way
// send-password-reset does.
func TestDisableMFACmd_mixedCaseEmail(t *testing.T) {
	truncateUsersAndInvites(t)
	ctx := context.Background()

	u, err := storage.CreateUser(ctx, testUserPool, "mixed-case@example.com", "password1234")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := storage.StoreMFASecret(ctx, testUserPool, u.ID, "JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatalf("store mfa secret: %v", err)
	}
	if err := storage.EnableMFA(ctx, testUserPool, u.ID); err != nil {
		t.Fatalf("enable mfa: %v", err)
	}

	cmd := usersDisableMFACmd(inviteCfg())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"  Mixed-Case@Example.COM  "})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	after, err := storage.GetUserByEmail(ctx, testUserPool, "mixed-case@example.com")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if after.MFAEnabled {
		t.Error("expected mfa_enabled to be false after disable-mfa")
	}
}

func TestDisableMFACmd_mfaNotEnabled(t *testing.T) {
	truncateUsersAndInvites(t)
	ctx := context.Background()

	if _, err := storage.CreateUser(ctx, testUserPool, "no-mfa@example.com", "password1234"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	cmd := usersDisableMFACmd(inviteCfg())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"no-mfa@example.com"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected no error when mfa is already off, got: %v", err)
	}
	if !strings.Contains(out.String(), "does not have an authenticator enabled") {
		t.Errorf("expected a no-op message, got: %q", out.String())
	}
}
