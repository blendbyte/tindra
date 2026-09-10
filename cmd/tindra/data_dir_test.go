package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckDataDir(t *testing.T) {
	t.Run("new and existing directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "data")
		require.NoError(t, checkDataDir(dir))
		preserved := filepath.Join(dir, "sourcemaps", "existing.map")
		require.NoError(t, os.WriteFile(preserved, []byte("preserve"), 0o600))
		require.NoError(t, checkDataDir(dir))
		data, err := os.ReadFile(preserved)
		require.NoError(t, err)
		require.Equal(t, "preserve", string(data))
		entries, err := os.ReadDir(filepath.Dir(preserved))
		require.NoError(t, err)
		require.Len(t, entries, 1)
	})
	t.Run("file instead of directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "data")
		require.NoError(t, os.WriteFile(dir, []byte("preserve"), 0o600))
		require.ErrorContains(t, checkDataDir(dir), "prepare DATA_DIR")
	})
	t.Run("unwritable source-map directory", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("requires POSIX permission enforcement for a non-root user")
		}
		dir := t.TempDir()
		maps := filepath.Join(dir, "sourcemaps")
		require.NoError(t, os.Mkdir(maps, 0o555))
		defer os.Chmod(maps, 0o755) //nolint:errcheck
		require.ErrorContains(t, checkDataDir(dir), "must be writable")
	})
}

func TestServeRejectsUnusableDataDirBeforeDatabase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(dir, nil, 0o600))
	cmd := serveCmd(config{dataDir: dir, databaseURL: "invalid"})
	require.ErrorContains(t, cmd.RunE(cmd, nil), "prepare DATA_DIR")
}

func TestCheckDataDirWriteAndCleanupFailures(t *testing.T) {
	t.Run("disk write failure cleans up probe", func(t *testing.T) {
		dir := t.TempDir()
		diskFull := errors.New("disk full")
		err := checkDataDirIO(dir, func(string, []byte, os.FileMode) error { return diskFull }, os.RemoveAll)
		require.ErrorIs(t, err, diskFull)
		require.ErrorContains(t, err, "write to DATA_DIR")
		entries, err := os.ReadDir(filepath.Join(dir, "sourcemaps"))
		require.NoError(t, err)
		require.Empty(t, entries)
	})
	t.Run("cleanup failure aborts startup and retries cleanup", func(t *testing.T) {
		dir := t.TempDir()
		denied := errors.New("permission denied")
		calls := 0
		err := checkDataDirIO(dir, os.WriteFile, func(path string) error {
			calls++
			if calls == 1 {
				return denied
			}
			return os.RemoveAll(path)
		})
		require.ErrorIs(t, err, denied)
		require.ErrorContains(t, err, "clean up DATA_DIR")
		require.Equal(t, 2, calls)
		entries, err := os.ReadDir(filepath.Join(dir, "sourcemaps"))
		require.NoError(t, err)
		require.Empty(t, entries)
	})
}
