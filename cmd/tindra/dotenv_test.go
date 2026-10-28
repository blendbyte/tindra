package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDotenvKeepsRealEnvironmentAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// t.Setenv restores the variables after the test; Unsetenv starts them empty.
	t.Setenv("TINDRA_TEST_REAL", "from-environment")
	t.Setenv("TINDRA_TEST_FILE", "")
	t.Setenv("TINDRA_TEST_GONE", "")
	os.Unsetenv("TINDRA_TEST_FILE")
	os.Unsetenv("TINDRA_TEST_GONE")

	write("TINDRA_TEST_REAL=from-file\nTINDRA_TEST_FILE=one\nTINDRA_TEST_GONE=soon\n")
	env, err := loadDotenv(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !env.found {
		t.Error("found = false for an existing file")
	}
	if got := os.Getenv("TINDRA_TEST_REAL"); got != "from-environment" {
		t.Errorf("real variable = %q, the file must not override it", got)
	}
	if got := os.Getenv("TINDRA_TEST_FILE"); got != "one" {
		t.Errorf("file variable = %q, want one", got)
	}

	write("TINDRA_TEST_REAL=changed\nTINDRA_TEST_FILE=two\n")
	if err := env.reload(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("TINDRA_TEST_FILE"); got != "two" {
		t.Errorf("after reload file variable = %q, want two", got)
	}
	if got := os.Getenv("TINDRA_TEST_REAL"); got != "from-environment" {
		t.Errorf("after reload real variable = %q", got)
	}
	if _, ok := os.LookupEnv("TINDRA_TEST_GONE"); ok {
		t.Error("a variable removed from the file is still set")
	}
}

func TestDotenvOptionalFileMayBeMissing(t *testing.T) {
	env, err := loadDotenv(filepath.Join(t.TempDir(), "missing.env"), false)
	if err != nil {
		t.Fatalf("load without a file = %v, want nil", err)
	}
	if env.found {
		t.Error("found = true without a file")
	}
	if err := env.reload(); err != nil {
		t.Fatalf("reload without a file = %v, want nil", err)
	}
}

func TestDotenvRequiredFileMustExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tindra.env")
	if _, err := loadDotenv(path, true); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("load of a missing required file = %v, want an error naming it", err)
	}

	// Deleting it later keeps the values from the last good read.
	t.Setenv("TINDRA_TEST_FILE", "")
	os.Unsetenv("TINDRA_TEST_FILE")
	if err := os.WriteFile(path, []byte("TINDRA_TEST_FILE=kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, err := loadDotenv(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := env.reload(); err == nil {
		t.Error("reload of a deleted required file = nil, want an error")
	}
	if got := os.Getenv("TINDRA_TEST_FILE"); got != "kept" {
		t.Errorf("after failed reload file variable = %q, want kept", got)
	}
}

func TestDotenvRejectsUnusableFile(t *testing.T) {
	t.Run("invalid syntax", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".env")
		if err := os.WriteFile(path, []byte("DATABASE_URL='unterminated\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadDotenv(path, false); err == nil || !strings.Contains(err.Error(), path) {
			t.Fatalf("load = %v, want a parse error naming the file", err)
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("file modes do not deny reads here")
		}
		path := filepath.Join(t.TempDir(), "tindra.env")
		if err := os.WriteFile(path, []byte("DATABASE_URL=postgres://x\n"), 0o000); err != nil {
			t.Fatal(err)
		}
		// An admin without access must not silently fall back to defaults.
		if _, err := loadDotenv(path, false); !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("load = %v, want permission denied", err)
		}
	})
}

func TestEnvFilePath(t *testing.T) {
	system := filepath.Join(t.TempDir(), "tindra.env")
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("TINDRA_ENV_FILE", "")

	if path, required := envFilePath(system); runtime.GOOS != "windows" && (path != system || required) {
		t.Errorf("without ./.env = (%q, %t), want (%q, false)", path, required, system)
	}

	if err := os.WriteFile(filepath.Join(dir, ".env"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if path, required := envFilePath(system); path != ".env" || required {
		t.Errorf("with ./.env = (%q, %t), want (.env, false)", path, required)
	}

	t.Setenv("TINDRA_ENV_FILE", "/srv/tindra/custom.env")
	if path, required := envFilePath(system); path != "/srv/tindra/custom.env" || !required {
		t.Errorf("with TINDRA_ENV_FILE = (%q, %t), want (/srv/tindra/custom.env, true)", path, required)
	}
}
