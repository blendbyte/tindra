package main

import (
	"os"
	"testing"
)

func TestLoadConfig_defaults(t *testing.T) {
	// Clear relevant env vars so we test pure defaults.
	clearEnv(t, "BIND_ADDR", "DATA_DIR", "LOG_LEVEL", "RETENTION_DAYS",
		"PROJECT_LIMIT", "EVENT_LIMIT", "USER_LIMIT",
		"RATE_LIMIT_LOGIN", "RATE_LIMIT_ENVELOPE",
		"INGEST_BUFFER_SIZE", "TRUSTED_PROXIES",
		"COOKIE_SECURE", "SKIP_AUTO_MIGRATE", "WEBHOOK_ALLOW_PRIVATE_IPS")

	cfg := loadConfig()

	if cfg.bindAddr != ":8080" {
		t.Errorf("bindAddr: got %q, want :8080", cfg.bindAddr)
	}
	if cfg.dataDir != "/data" {
		t.Errorf("dataDir: got %q, want /data", cfg.dataDir)
	}
	if cfg.logLevel != "info" {
		t.Errorf("logLevel: got %q, want info", cfg.logLevel)
	}
	if cfg.retentionDays != 90 {
		t.Errorf("retentionDays: got %d, want 90", cfg.retentionDays)
	}
	if cfg.projectLimit != 0 {
		t.Errorf("projectLimit: got %d, want 0", cfg.projectLimit)
	}
	if cfg.eventLimit != 0 {
		t.Errorf("eventLimit: got %d, want 0", cfg.eventLimit)
	}
	if cfg.userLimit != 0 {
		t.Errorf("userLimit: got %d, want 0", cfg.userLimit)
	}
	if cfg.rateLimitLogin != 10 {
		t.Errorf("rateLimitLogin: got %d, want 10", cfg.rateLimitLogin)
	}
	if cfg.rateLimitEnvelope != 300 {
		t.Errorf("rateLimitEnvelope: got %d, want 300", cfg.rateLimitEnvelope)
	}
	if cfg.ingestBufferSize != 10000 {
		t.Errorf("ingestBufferSize: got %d, want 10000", cfg.ingestBufferSize)
	}
	if cfg.cookieSecure {
		t.Error("cookieSecure: expected false by default")
	}
	if cfg.webhookAllowPrivateIPs {
		t.Error("webhookAllowPrivateIPs: expected false by default")
	}
	if cfg.skipAutoMigrate {
		t.Error("skipAutoMigrate: expected false by default")
	}
	if len(cfg.trustedProxies) != 0 {
		t.Errorf("trustedProxies: expected empty, got %d entries", len(cfg.trustedProxies))
	}
}

func TestLoadConfig_envOverrides(t *testing.T) {
	clearEnv(t, "BIND_ADDR", "DATA_DIR", "LOG_LEVEL", "RETENTION_DAYS",
		"PROJECT_LIMIT", "EVENT_LIMIT", "USER_LIMIT",
		"RATE_LIMIT_LOGIN", "RATE_LIMIT_ENVELOPE", "INGEST_BUFFER_SIZE",
		"COOKIE_SECURE", "SKIP_AUTO_MIGRATE", "WEBHOOK_ALLOW_PRIVATE_IPS",
		"TRUSTED_PROXIES")

	t.Setenv("BIND_ADDR", ":9090")
	t.Setenv("DATA_DIR", "/tmp/data")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("RETENTION_DAYS", "30")
	t.Setenv("PROJECT_LIMIT", "5")
	t.Setenv("EVENT_LIMIT", "1000")
	t.Setenv("USER_LIMIT", "10")
	t.Setenv("RATE_LIMIT_LOGIN", "5")
	t.Setenv("RATE_LIMIT_ENVELOPE", "100")
	t.Setenv("INGEST_BUFFER_SIZE", "5000")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("SKIP_AUTO_MIGRATE", "true")
	t.Setenv("WEBHOOK_ALLOW_PRIVATE_IPS", "true")

	cfg := loadConfig()

	if cfg.bindAddr != ":9090" {
		t.Errorf("bindAddr: got %q, want :9090", cfg.bindAddr)
	}
	if cfg.dataDir != "/tmp/data" {
		t.Errorf("dataDir: got %q, want /tmp/data", cfg.dataDir)
	}
	if cfg.logLevel != "debug" {
		t.Errorf("logLevel: got %q, want debug", cfg.logLevel)
	}
	if cfg.retentionDays != 30 {
		t.Errorf("retentionDays: got %d, want 30", cfg.retentionDays)
	}
	if cfg.projectLimit != 5 {
		t.Errorf("projectLimit: got %d, want 5", cfg.projectLimit)
	}
	if cfg.eventLimit != 1000 {
		t.Errorf("eventLimit: got %d, want 1000", cfg.eventLimit)
	}
	if cfg.userLimit != 10 {
		t.Errorf("userLimit: got %d, want 10", cfg.userLimit)
	}
	if cfg.rateLimitLogin != 5 {
		t.Errorf("rateLimitLogin: got %d, want 5", cfg.rateLimitLogin)
	}
	if cfg.rateLimitEnvelope != 100 {
		t.Errorf("rateLimitEnvelope: got %d, want 100", cfg.rateLimitEnvelope)
	}
	if cfg.ingestBufferSize != 5000 {
		t.Errorf("ingestBufferSize: got %d, want 5000", cfg.ingestBufferSize)
	}
	if !cfg.cookieSecure {
		t.Error("cookieSecure: expected true")
	}
	if !cfg.skipAutoMigrate {
		t.Error("skipAutoMigrate: expected true")
	}
	if !cfg.webhookAllowPrivateIPs {
		t.Error("webhookAllowPrivateIPs: expected true")
	}
}

func TestLoadConfig_invalidNumbers_useDefaults(t *testing.T) {
	clearEnv(t, "RETENTION_DAYS", "INGEST_BUFFER_SIZE", "PROJECT_LIMIT",
		"EVENT_LIMIT", "USER_LIMIT", "RATE_LIMIT_LOGIN", "RATE_LIMIT_ENVELOPE")

	t.Setenv("RETENTION_DAYS", "not-a-number")
	t.Setenv("INGEST_BUFFER_SIZE", "abc")
	t.Setenv("RATE_LIMIT_LOGIN", "bad")
	t.Setenv("RATE_LIMIT_ENVELOPE", "nope")

	cfg := loadConfig()

	if cfg.retentionDays != 90 {
		t.Errorf("retentionDays: expected default 90, got %d", cfg.retentionDays)
	}
	if cfg.ingestBufferSize != 10000 {
		t.Errorf("ingestBufferSize: expected default 10000, got %d", cfg.ingestBufferSize)
	}
	if cfg.rateLimitLogin != 10 {
		t.Errorf("rateLimitLogin: expected default 10, got %d", cfg.rateLimitLogin)
	}
	if cfg.rateLimitEnvelope != 300 {
		t.Errorf("rateLimitEnvelope: expected default 300, got %d", cfg.rateLimitEnvelope)
	}
}

func TestLoadConfig_trustedProxies_CIDR(t *testing.T) {
	clearEnv(t, "TRUSTED_PROXIES")
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.0/24")

	cfg := loadConfig()

	if len(cfg.trustedProxies) != 2 {
		t.Fatalf("expected 2 trusted proxies, got %d", len(cfg.trustedProxies))
	}
	if cfg.trustedProxies[0].String() != "10.0.0.0/8" {
		t.Errorf("first proxy: got %q", cfg.trustedProxies[0])
	}
}

func TestLoadConfig_trustedProxies_bareIP(t *testing.T) {
	clearEnv(t, "TRUSTED_PROXIES")
	t.Setenv("TRUSTED_PROXIES", "1.2.3.4")

	cfg := loadConfig()

	if len(cfg.trustedProxies) != 1 {
		t.Fatalf("expected 1 trusted proxy, got %d", len(cfg.trustedProxies))
	}
	ones, bits := cfg.trustedProxies[0].Mask.Size()
	if ones != 32 || bits != 32 {
		t.Errorf("expected /32 mask for bare IPv4, got /%d", ones)
	}
}

func TestLoadConfig_trustedProxies_bareIPv6(t *testing.T) {
	clearEnv(t, "TRUSTED_PROXIES")
	t.Setenv("TRUSTED_PROXIES", "::1")

	cfg := loadConfig()

	if len(cfg.trustedProxies) != 1 {
		t.Fatalf("expected 1 trusted proxy, got %d", len(cfg.trustedProxies))
	}
	ones, bits := cfg.trustedProxies[0].Mask.Size()
	if ones != 128 || bits != 128 {
		t.Errorf("expected /128 mask for bare IPv6, got /%d", ones)
	}
}

func TestLoadConfig_trustedProxies_invalid(t *testing.T) {
	clearEnv(t, "TRUSTED_PROXIES")
	t.Setenv("TRUSTED_PROXIES", "notanip")

	cfg := loadConfig()

	// Invalid entry is skipped (warning printed to stderr).
	if len(cfg.trustedProxies) != 0 {
		t.Errorf("expected 0 proxies for invalid entry, got %d", len(cfg.trustedProxies))
	}
}

func TestLoadConfig_retentionDaysZero(t *testing.T) {
	clearEnv(t, "RETENTION_DAYS")
	t.Setenv("RETENTION_DAYS", "0")

	cfg := loadConfig()

	if cfg.retentionDays != 0 {
		t.Errorf("expected 0 retention days, got %d", cfg.retentionDays)
	}
}

func TestSetupLogger_levels(t *testing.T) {
	// setupLogger should not panic for any documented level.
	for _, level := range []string{"debug", "info", "warn", "warning", "error", "unknown", ""} {
		// Should not panic.
		setupLogger(level, "text")
		setupLogger(level, "json")
	}
}

func TestSetupLogger_formats(t *testing.T) {
	setupLogger("info", "json")
	setupLogger("info", "text")
	setupLogger("info", "")
}

// clearEnv unsets env vars for the duration of the test, restoring them after.
func clearEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		old, had := os.LookupEnv(k)
		if had {
			t.Cleanup(func() { os.Setenv(k, old) })
		} else {
			t.Cleanup(func() { os.Unsetenv(k) })
		}
		os.Unsetenv(k)
	}
}

