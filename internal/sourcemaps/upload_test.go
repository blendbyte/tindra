package sourcemaps_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/sourcemaps"
	"github.com/blendbyte/tindra/internal/storage"
)

func TestUploadValidationPreservesExistingMap(t *testing.T) {
	const valid = `{"version":3,"sources":["app.ts"],"mappings":"AAAA"}`
	readErr := errors.New("broken reader")
	for _, tc := range []struct {
		name  string
		input io.Reader
		want  error
	}{
		{"oversized", strings.NewReader(valid + strings.Repeat(" ", sourcemaps.MaxUploadSize+1-len(valid))), sourcemaps.ErrUploadTooLarge},
		{"invalid JSON", strings.NewReader(`{"version":3`), sourcemaps.ErrInvalidMap},
		{"unsupported version", strings.NewReader(`{"version":2}`), sourcemaps.ErrInvalidMap},
		{"invalid mappings", strings.NewReader(`{"version":3,"mappings":"!"}`), sourcemaps.ErrInvalidMap},
		{"read failure", iotest.ErrReader(readErr), readErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store := sourcemaps.NewStore(dir, testPool)
			release := t.Name()
			original, err := store.Upload(t.Context(), testProject.ID, release, "~/app.js", strings.NewReader(valid))
			require.NoError(t, err)
			got, err := store.Upload(t.Context(), testProject.ID, release, "~/app.js", tc.input)
			require.ErrorIs(t, err, tc.want)
			require.Nil(t, got)
			stored, err := storage.GetSourcemap(t.Context(), testPool, testProject.ID, release, "~/app.js")
			require.NoError(t, err)
			require.Equal(t, original, stored)
			mapDir := filepath.Join(dir, "sourcemaps", testProject.ID)
			files, err := os.ReadDir(mapDir)
			require.NoError(t, err)
			require.Len(t, files, 1)
			data, err := os.ReadFile(filepath.Join(mapDir, original.ContentHash+".map"))
			require.NoError(t, err)
			require.Equal(t, valid, string(data))
		})
	}
}

func TestUploadExactLimit(t *testing.T) {
	const valid = `{"version":3,"sources":["app.ts"],"mappings":"AAAA"}`
	data := valid + strings.Repeat(" ", sourcemaps.MaxUploadSize-len(valid))
	dir := t.TempDir()
	store := sourcemaps.NewStore(dir, testPool)
	sm, err := store.Upload(t.Context(), testProject.ID, t.Name(), "~/app.js", strings.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, sourcemaps.MaxUploadSize, sm.SizeBytes)
	stored, err := os.ReadFile(filepath.Join(dir, "sourcemaps", testProject.ID, sm.ContentHash+".map"))
	require.NoError(t, err)
	require.Equal(t, data, string(stored))
}

func TestUploadPublishFailurePreservesExistingMap(t *testing.T) {
	const original = `{"version":3,"sources":["original.ts"],"mappings":"AAAA"}`
	const replacement = `{"version":3,"sources":["replacement.ts"],"mappings":"AAAA"}`
	dir := t.TempDir()
	store := sourcemaps.NewStore(dir, testPool)
	before, err := store.Upload(t.Context(), testProject.ID, t.Name(), "~/app.js", strings.NewReader(original))
	require.NoError(t, err)
	mapDir := filepath.Join(dir, "sourcemaps", testProject.ID)
	// A directory at the target path forces publication to fail after writing.
	blocked := filepath.Join(mapDir, fmt.Sprintf("%x.map", sha256.Sum256([]byte(replacement))))
	require.NoError(t, os.Mkdir(blocked, 0o755))
	sm, err := store.Upload(t.Context(), testProject.ID, t.Name(), "~/app.js", strings.NewReader(replacement))
	require.ErrorContains(t, err, "publish file")
	require.Nil(t, sm)
	after, err := storage.GetSourcemap(t.Context(), testPool, testProject.ID, t.Name(), "~/app.js")
	require.NoError(t, err)
	require.Equal(t, before, after)
	files, err := os.ReadDir(mapDir)
	require.NoError(t, err)
	require.Len(t, files, 2, "temporary upload must be removed on failure")
	data, err := os.ReadFile(filepath.Join(mapDir, before.ContentHash+".map"))
	require.NoError(t, err)
	require.Equal(t, original, string(data))
}

func TestUploadStorageFailures(t *testing.T) {
	const valid = `{"version":3,"sources":["app.ts"],"mappings":"AAAA"}`
	for _, stage := range []string{"mkdir", "create file", "db"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			pool := testPool
			switch stage {
			case "mkdir":
				require.NoError(t, os.WriteFile(filepath.Join(dir, "sourcemaps"), []byte("block"), 0o600))
			case "create file":
				if os.Geteuid() == 0 {
					t.Skip("root bypasses directory write permissions")
				}
				mapDir := filepath.Join(dir, "sourcemaps", testProject.ID)
				require.NoError(t, os.MkdirAll(mapDir, 0o700))
				require.NoError(t, os.Chmod(mapDir, 0o500))
				t.Cleanup(func() { require.NoError(t, os.Chmod(mapDir, 0o700)) })
			case "db":
				var err error
				pool, err = pgxpool.NewWithConfig(t.Context(), testPool.Config())
				require.NoError(t, err)
				pool.Close()
			}
			store := sourcemaps.NewStore(dir, pool)
			sm, err := store.Upload(t.Context(), testProject.ID, t.Name(), "~/app.js", strings.NewReader(valid))
			require.ErrorContains(t, err, stage)
			require.Nil(t, sm)
			stored, err := storage.GetSourcemap(t.Context(), testPool, testProject.ID, t.Name(), "~/app.js")
			require.NoError(t, err)
			require.Nil(t, stored)
		})
	}
}
