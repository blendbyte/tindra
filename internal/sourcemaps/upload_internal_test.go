package sourcemaps

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type failingUploadFile struct {
	*os.File
	writeErr error
	closeErr error
	closed   bool
}

func (f *failingUploadFile) Write(data []byte) (int, error) {
	if f.writeErr != nil {
		// Model a disk filling after a partial write.
		n, _ := f.File.Write(data[:1])
		return n, f.writeErr
	}
	return f.File.Write(data)
}

func (f *failingUploadFile) Close() error {
	f.closed = true
	err := f.File.Close()
	if f.closeErr != nil {
		return f.closeErr
	}
	return err
}

func TestPublishUploadIOFailures(t *testing.T) {
	for _, stage := range []string{"write", "close"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "existing.map")
			require.NoError(t, os.WriteFile(target, []byte("original"), 0o600))
			file, err := os.CreateTemp(dir, ".upload-*")
			require.NoError(t, err)
			failure := errors.New("disk failure")
			f := &failingUploadFile{File: file}
			if stage == "write" {
				f.writeErr = failure
			} else {
				f.closeErr = failure
			}
			require.ErrorIs(t, publishUpload(f, target, []byte("replacement")), failure)
			require.True(t, f.closed)
			_, err = os.Stat(file.Name())
			require.ErrorIs(t, err, os.ErrNotExist)
			data, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, "original", string(data))
		})
	}
}
