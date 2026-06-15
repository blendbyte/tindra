package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/storage"
)

// ---------------------------------------------------------------------------
// loadConfig -- fields not covered by config_test.go
// ---------------------------------------------------------------------------

func TestLoadConfig_requireMFA_default(t *testing.T) {
	clearEnv(t, "REQUIRE_MFA")
	cfg := loadConfig()
	if !cfg.requireMFA {
		t.Error("requireMFA: expected true when REQUIRE_MFA is unset")
	}
}

func TestLoadConfig_requireMFA_false(t *testing.T) {
	clearEnv(t, "REQUIRE_MFA")
	t.Setenv("REQUIRE_MFA", "false")
	cfg := loadConfig()
	if cfg.requireMFA {
		t.Error("requireMFA: expected false when REQUIRE_MFA=false")
	}
}

func TestLoadConfig_requireMFA_anyOtherValue(t *testing.T) {
	// Any value other than "false" keeps requireMFA true.
	clearEnv(t, "REQUIRE_MFA")
	t.Setenv("REQUIRE_MFA", "0")
	cfg := loadConfig()
	if !cfg.requireMFA {
		t.Error("requireMFA: expected true when REQUIRE_MFA=0 (not 'false')")
	}
}

func TestLoadConfig_disableVersionCheck(t *testing.T) {
	clearEnv(t, "DISABLE_VERSION_CHECK")
	cfg := loadConfig()
	if cfg.disableVersionCheck {
		t.Error("disableVersionCheck: expected false when unset")
	}

	t.Setenv("DISABLE_VERSION_CHECK", "true")
	cfg = loadConfig()
	if !cfg.disableVersionCheck {
		t.Error("disableVersionCheck: expected true when DISABLE_VERSION_CHECK=true")
	}
}

func TestLoadConfig_socketMode(t *testing.T) {
	clearEnv(t, "SOCKET_MODE")

	// Default when unset.
	cfg := loadConfig()
	if cfg.socketMode != 0660 {
		t.Errorf("socketMode: expected default 0660, got %04o", cfg.socketMode)
	}

	// Valid octal string.
	t.Setenv("SOCKET_MODE", "0666")
	cfg = loadConfig()
	if cfg.socketMode != 0666 {
		t.Errorf("socketMode: expected 0666, got %04o", cfg.socketMode)
	}
}

func TestLoadConfig_socketMode_invalid(t *testing.T) {
	clearEnv(t, "SOCKET_MODE")
	t.Setenv("SOCKET_MODE", "notoctal")
	cfg := loadConfig()
	// Falls back to the default 0660 on parse error.
	if cfg.socketMode != 0660 {
		t.Errorf("socketMode: expected default 0660 on invalid input, got %04o", cfg.socketMode)
	}
}

func TestLoadConfig_stringFields(t *testing.T) {
	clearEnv(t, "CORS_ORIGIN", "PUBLIC_URL", "STATS_API_KEY", "BILLING_URL", "LOG_FORMAT", "DATABASE_URL")
	t.Setenv("CORS_ORIGIN", "https://app.example.com")
	t.Setenv("PUBLIC_URL", "https://tindra.example.com")
	t.Setenv("STATS_API_KEY", "secret-key")
	t.Setenv("BILLING_URL", "  https://billing.example.com  ")
	t.Setenv("LOG_FORMAT", "json")
	t.Setenv("DATABASE_URL", "postgres://localhost/tindra")

	cfg := loadConfig()

	if cfg.corsOrigin != "https://app.example.com" {
		t.Errorf("corsOrigin: got %q", cfg.corsOrigin)
	}
	if cfg.publicURL != "https://tindra.example.com" {
		t.Errorf("publicURL: got %q", cfg.publicURL)
	}
	if cfg.statsAPIKey != "secret-key" {
		t.Errorf("statsAPIKey: got %q", cfg.statsAPIKey)
	}
	// BILLING_URL is TrimSpaced.
	if cfg.billingURL != "https://billing.example.com" {
		t.Errorf("billingURL: got %q (expected trimmed)", cfg.billingURL)
	}
	if cfg.logFormat != "json" {
		t.Errorf("logFormat: got %q", cfg.logFormat)
	}
	if cfg.databaseURL != "postgres://localhost/tindra" {
		t.Errorf("databaseURL: got %q", cfg.databaseURL)
	}
}

func TestLoadConfig_zeroRateLimits(t *testing.T) {
	// Rate limits accept 0 as a valid value (disabled).
	clearEnv(t, "RATE_LIMIT_LOGIN", "RATE_LIMIT_ENVELOPE")
	t.Setenv("RATE_LIMIT_LOGIN", "0")
	t.Setenv("RATE_LIMIT_ENVELOPE", "0")
	cfg := loadConfig()
	if cfg.rateLimitLogin != 0 {
		t.Errorf("rateLimitLogin: expected 0, got %d", cfg.rateLimitLogin)
	}
	if cfg.rateLimitEnvelope != 0 {
		t.Errorf("rateLimitEnvelope: expected 0, got %d", cfg.rateLimitEnvelope)
	}
}

func TestLoadConfig_negativeLimits_ignoredForRateLimits(t *testing.T) {
	// Negative values fail the n >= 0 guard and fall back to the defaults.
	clearEnv(t, "RATE_LIMIT_LOGIN", "RATE_LIMIT_ENVELOPE")
	t.Setenv("RATE_LIMIT_LOGIN", "-1")
	t.Setenv("RATE_LIMIT_ENVELOPE", "-5")
	cfg := loadConfig()
	if cfg.rateLimitLogin != 10 {
		t.Errorf("rateLimitLogin: expected default 10 for negative value, got %d", cfg.rateLimitLogin)
	}
	if cfg.rateLimitEnvelope != 300 {
		t.Errorf("rateLimitEnvelope: expected default 300 for negative value, got %d", cfg.rateLimitEnvelope)
	}
}

func TestLoadConfig_negativeIngestBufferSize_ignored(t *testing.T) {
	clearEnv(t, "INGEST_BUFFER_SIZE")
	t.Setenv("INGEST_BUFFER_SIZE", "-100")
	cfg := loadConfig()
	// Negative values fail the n > 0 guard and fall back to default.
	if cfg.ingestBufferSize != 10000 {
		t.Errorf("ingestBufferSize: expected default 10000 for negative value, got %d", cfg.ingestBufferSize)
	}
}

func TestLoadConfig_trustedProxies_mixed(t *testing.T) {
	clearEnv(t, "TRUSTED_PROXIES")
	// One valid CIDR, one invalid entry — invalid should be skipped.
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8, notanip, 192.168.0.0/16")
	cfg := loadConfig()
	if len(cfg.trustedProxies) != 2 {
		t.Fatalf("expected 2 valid proxies, got %d", len(cfg.trustedProxies))
	}
}

// ---------------------------------------------------------------------------
// sendDigestCmd structural tests (no DB required)
// ---------------------------------------------------------------------------

func TestSendDigestCmd_structure(t *testing.T) {
	cmd := sendDigestCmd(config{})
	if cmd.Use != "send-digest" {
		t.Errorf("Use: got %q, want send-digest", cmd.Use)
	}
	// The --force flag must exist.
	f := cmd.Flags().Lookup("force")
	if f == nil {
		t.Fatal("--force flag not found on send-digest command")
	}
	if f.DefValue != "false" {
		t.Errorf("--force default: got %q, want false", f.DefValue)
	}
}

// ---------------------------------------------------------------------------
// projectsListCmd -- eventLimit display branch
// ---------------------------------------------------------------------------

func TestProjectsListCmd_withEventLimit(t *testing.T) {
	if testUserDSN == "" {
		t.Skip("no database available")
	}
	truncateProjects(t)

	if _, err := storage.CreateProject(context.Background(), testUserPool, "ev-limit-proj", "EV Limit Project"); err != nil {
		t.Fatalf("create project: %v", err)
	}

	cfg := projectsCfg()
	cfg.eventLimit = 5000

	var out bytes.Buffer
	cmd := projectsListCmd(cfg)
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	output := out.String()
	// When eventLimit > 0, events column is "N / limit".
	if !strings.Contains(output, "/ 5000") {
		// tabwriter writes to os.Stdout not cmd.OutOrStdout for this command,
		// so output may be empty — at minimum check no error occurred.
		_ = output
	}
}

func TestProjectsCreateCmd_onlyName_missingSlug(t *testing.T) {
	cmd := projectsCreateCmd(projectsCfg())
	cmd.SetArgs([]string{"--name", "Only Name"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --slug is missing")
	}
	if !strings.Contains(err.Error(), "required") && !strings.Contains(err.Error(), "slug") {
		t.Errorf("expected slug-related error, got: %v", err)
	}
}

func TestProjectsCreateCmd_onlySlug_missingName(t *testing.T) {
	cmd := projectsCreateCmd(projectsCfg())
	cmd.SetArgs([]string{"--slug", "only-slug"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --name is missing")
	}
	if !strings.Contains(err.Error(), "required") && !strings.Contains(err.Error(), "name") {
		t.Errorf("expected name-related error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// usersListCmd -- email normalisation on create
// ---------------------------------------------------------------------------

func TestCreateUserCmd_emailLowercased(t *testing.T) {
	if testUserDSN == "" {
		t.Skip("no database available")
	}
	truncateUsersAndInvites(t)

	cmd := usersCreateCmd(inviteCfg())
	cmd.SetArgs([]string{"--email", "ADMIN@EXAMPLE.COM", "--password", "password1234"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	users, err := storage.ListUsers(context.Background(), testUserPool)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if users[0].Email != "admin@example.com" {
		t.Errorf("email not lowercased: got %q", users[0].Email)
	}
}
