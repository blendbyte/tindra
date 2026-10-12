package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every variable the server reads should be documented in .env.example, which
// is the reference operators copy from.
func TestEnvExampleDocumentsEveryVariable(t *testing.T) {
	read := regexp.MustCompile(`(?:Getenv|LookupEnv|envInt|envBool)\("([A-Z][A-Z0-9_]+)"`)
	used := map[string]string{}
	for _, root := range []string{".", "../../internal"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// testutil only runs under go test; its variables are for contributors.
			if d.IsDir() && d.Name() == "testutil" {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range read.FindAllStringSubmatch(string(src), -1) {
				used[m[1]] = path
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(used) < 20 {
		t.Fatalf("found only %d variables; the pattern no longer matches how config is read", len(used))
	}

	example, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	for name, path := range used {
		documented := regexp.MustCompile(`(?m)^#? ?` + name + `=`)
		if !documented.Match(example) {
			t.Errorf("%s (read in %s) is missing from .env.example", name, path)
		}
	}
}
