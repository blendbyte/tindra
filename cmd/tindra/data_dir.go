package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// checkDataDir exercises the directory and file writes required by source-map
// uploads before the server accepts traffic. Never change existing ownership.
func checkDataDir(dataDir string) error {
	return checkDataDirIO(dataDir, os.WriteFile, os.RemoveAll)
}

// Keep write and cleanup failures testable without filling a real disk or
// racing filesystem permissions while the process is starting.
func checkDataDirIO(dataDir string, writeFile func(string, []byte, os.FileMode) error, removeAll func(string) error) error {
	dir := filepath.Join(dataDir, "sourcemaps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("prepare DATA_DIR %q: %w", dataDir, err)
	}
	probe, err := os.MkdirTemp(dir, ".write-check-")
	if err != nil {
		return fmt.Errorf("DATA_DIR %q must be writable by the application user: %w", dataDir, err)
	}
	defer removeAll(probe) //nolint:errcheck // Best-effort cleanup if writing fails.
	if err := writeFile(filepath.Join(probe, "check"), []byte("ok"), 0o600); err != nil {
		return fmt.Errorf("write to DATA_DIR %q: %w", dataDir, err)
	}
	if err := removeAll(probe); err != nil {
		return fmt.Errorf("clean up DATA_DIR %q write check: %w", dataDir, err)
	}
	return nil
}
