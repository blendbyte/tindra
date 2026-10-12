package storage_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The fixtures in fixtures.go create users, sessions and MFA state without the
// checks the real flows perform. Production code must not call them.
func TestFixturesAreTestOnly(t *testing.T) {
	fixtures := []string{
		"CreateUser", "CreateSession", "CreateOAuthUser", "CreateMFAChallenge",
		"ConsumeMFAChallenge", "DeleteSession", "EnableMFA", "StoreMFASecret",
	}
	call := regexp.MustCompile(`\b(` + strings.Join(fixtures, "|") + `)\(`)
	for _, root := range []string{"../../cmd", "../../internal"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// testutil is itself only imported by tests.
				if d.Name() == "testutil" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "fixtures.go" {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(src), "\n") {
				if m := call.FindStringSubmatch(line); m != nil && !strings.HasPrefix(strings.TrimSpace(line), "//") {
					t.Errorf("%s:%d calls the test fixture %s", path, i+1, m[1])
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
