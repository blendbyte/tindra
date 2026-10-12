package main

import (
	"os"
	"path/filepath"
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
	env := loadDotenv(path)
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

func TestDotenvMissingFile(t *testing.T) {
	env := loadDotenv(filepath.Join(t.TempDir(), "missing.env"))
	if err := env.reload(); err != nil {
		t.Fatalf("reload without a file = %v, want nil", err)
	}
}
