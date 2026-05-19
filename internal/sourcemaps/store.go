package sourcemaps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/storage"
)

// Store manages sourcemap files on the filesystem with metadata in Postgres.
type Store struct {
	dataDir string
	pool    *pgxpool.Pool
}

func NewStore(dataDir string, pool *pgxpool.Pool) *Store {
	return &Store{dataDir: dataDir, pool: pool}
}

var absURLRe = regexp.MustCompile(`^https?://[^/]+`)

// NormalizeURL converts any form of JS URL to the ~/path convention used for storage.
//
//	https://example.com/dist/main.js → ~/dist/main.js
//	/dist/main.js                    → ~/dist/main.js
//	~/dist/main.js                   → ~/dist/main.js  (unchanged)
func NormalizeURL(u string) string {
	if strings.HasPrefix(u, "~/") {
		return u
	}
	u = absURLRe.ReplaceAllString(u, "~")
	if !strings.HasPrefix(u, "~") {
		u = "~" + u
	}
	return u
}

// Upload stores a sourcemap on disk and records its metadata in Postgres.
// If a record already exists for (project, release, url) it is replaced.
func (s *Store) Upload(ctx context.Context, projectID, release, url string, r io.Reader) (*storage.Sourcemap, error) {
	data, err := io.ReadAll(io.LimitReader(r, 10<<20)) // 10 MB cap
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	sum := sha256.Sum256(data)
	hexHash := hex.EncodeToString(sum[:])

	dir := filepath.Join(s.dataDir, "sourcemaps", projectID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, hexHash+".map"), data, 0o644); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	sm, err := storage.UpsertSourcemap(ctx, s.pool, &storage.Sourcemap{
		ProjectID:   projectID,
		Release:     release,
		URL:         NormalizeURL(url),
		ContentHash: hexHash,
		SizeBytes:   len(data),
	})
	if err != nil {
		return nil, fmt.Errorf("db: %w", err)
	}
	return sm, nil
}

// Delete removes a sourcemap record from the DB and, if no other record in the
// same project references the same file, deletes the file from disk.
func (s *Store) Delete(ctx context.Context, id, projectID string) (bool, error) {
	contentHash, err := storage.DeleteSourcemap(ctx, s.pool, id, projectID)
	if err != nil {
		return false, err
	}
	if contentHash == "" {
		return false, nil // not found
	}
	remaining, err := storage.CountSourcemapsByHash(ctx, s.pool, projectID, contentHash)
	if err != nil {
		return true, nil // DB record gone; log nothing, file cleanup is best-effort
	}
	if remaining == 0 {
		path := filepath.Join(s.dataDir, "sourcemaps", projectID, contentHash+".map")
		_ = os.Remove(path) // best-effort; stale files are non-critical
	}
	return true, nil
}

// ResolveEventPayload applies sourcemap resolution to all JS stack frames in a
// Sentry-format event payload and returns the augmented JSON. Frames that cannot
// be resolved are left unchanged. Never returns an error - on any problem the
// original payload is returned.
func (s *Store) ResolveEventPayload(ctx context.Context, projectID, release string, payload json.RawMessage) json.RawMessage {
	if release == "" {
		return payload
	}

	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		return payload
	}

	exc, _ := data["exception"].(map[string]any)
	if exc == nil {
		return payload
	}
	values, _ := exc["values"].([]any)

	// Cache parsed source maps per URL so each .map file is read+parsed once.
	smCache := map[string]*SourceMap{}

	for _, v := range values {
		val, _ := v.(map[string]any)
		if val == nil {
			continue
		}
		st, _ := val["stacktrace"].(map[string]any)
		if st == nil {
			continue
		}
		frames, _ := st["frames"].([]any)
		for _, f := range frames {
			frame, _ := f.(map[string]any)
			if frame == nil {
				continue
			}
			s.resolveFrame(ctx, projectID, release, frame, smCache)
		}
	}

	out, err := json.Marshal(data)
	if err != nil {
		return payload
	}
	return out
}

func (s *Store) resolveFrame(ctx context.Context, projectID, release string, frame map[string]any, cache map[string]*SourceMap) {
	url := ""
	if v, _ := frame["abs_path"].(string); v != "" {
		url = v
	} else if v, _ := frame["filename"].(string); v != "" {
		url = v
	}
	if url == "" {
		return
	}

	lineno, _ := frame["lineno"].(float64)
	colno, _ := frame["colno"].(float64)
	if lineno == 0 {
		return
	}

	normURL := NormalizeURL(url)

	sm, ok := cache[normURL]
	if !ok {
		sm = s.loadSourceMap(ctx, projectID, release, normURL)
		cache[normURL] = sm // cache nil too to avoid repeated DB misses
	}
	if sm == nil {
		return
	}

	resolved, found := sm.Resolve(int(lineno), int(colno))
	if !found {
		return
	}

	frame["orig_filename"] = resolved.Source
	frame["orig_lineno"] = resolved.Line
	frame["orig_colno"] = resolved.Col
	if resolved.ContextLine != "" {
		frame["context_line"] = resolved.ContextLine
	}
}

func (s *Store) loadSourceMap(ctx context.Context, projectID, release, normURL string) *SourceMap {
	meta, err := storage.GetSourcemap(ctx, s.pool, projectID, release, normURL)
	if err != nil || meta == nil {
		return nil
	}
	path := filepath.Join(s.dataDir, "sourcemaps", projectID, meta.ContentHash+".map")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	sm, err := Parse(data)
	if err != nil {
		return nil
	}
	return sm
}
