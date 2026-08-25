package sourcemaps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/storage"
)

const (
	fileCacheMax = 64              // max cached JS files (LRU eviction)
	fileCacheTTL = 5 * time.Minute // re-fetch after this duration
	fetchTimeout = 5 * time.Second
	fetchMaxSize = 5 << 20 // 5 MB per file
	ctxLineMax   = 140     // chars visible in context_line window
)

type cachedFile struct {
	lines []string
	at    time.Time
}

// Store manages sourcemap files on the filesystem with metadata in Postgres.
type Store struct {
	dataDir    string
	pool       *pgxpool.Pool
	httpClient *http.Client

	fileMu    sync.Mutex
	fileCache map[string]cachedFile // URL → cached line split; evict LRU on fileCacheMax
	fileOrder []string              // insertion order for simple LRU
}

func NewStore(dataDir string, pool *pgxpool.Pool) *Store {
	return &Store{
		dataDir:    dataDir,
		pool:       pool,
		httpClient: &http.Client{Timeout: fetchTimeout},
		fileCache:  make(map[string]cachedFile, fileCacheMax),
	}
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
//
// When a source map is available for a frame it is used to translate to original
// source coordinates and populate context_line from sourcesContent. When no
// source map is uploaded, the raw JS file is fetched via HTTP (if the frame has
// an abs_path https:// URL) and context_line is extracted around the error column
// with {snip} markers, matching Sentry's fallback behaviour.
func (s *Store) ResolveEventPayload(ctx context.Context, projectID, release string, payload json.RawMessage) json.RawMessage {
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

	// Try uploaded source map first (requires a known release).
	if release != "" {
		normURL := NormalizeURL(url)
		sm, ok := cache[normURL]
		if !ok {
			sm = s.loadSourceMap(ctx, projectID, release, normURL)
			cache[normURL] = sm // cache nil too to avoid repeated DB misses
		}
		if sm != nil {
			resolved, found := sm.Resolve(int(lineno), int(colno))
			if found {
				frame["orig_filename"] = resolved.Source
				frame["orig_lineno"] = resolved.Line
				frame["orig_colno"] = resolved.Col
				if resolved.ContextLine != "" {
					frame["context_line"] = resolved.ContextLine
					return
				}
			}
		}
	}

	// Fallback: fetch the raw JS file and extract a line window around colno.
	// This replicates Sentry's behaviour when no source map is uploaded.
	if _, already := frame["context_line"]; !already {
		if cl := s.fetchContextLine(ctx, url, int(lineno), int(colno)); cl != "" {
			frame["context_line"] = cl
		}
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

// fetchContextLine fetches the JS file at rawURL (must be https?://) and
// returns a window of ctxLineMax characters centred on colno from line lineno,
// with {snip} markers at either end if the line was truncated.
func (s *Store) fetchContextLine(ctx context.Context, rawURL string, lineno, colno int) string {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return ""
	}

	lines := s.getCachedLines(rawURL)
	if lines == nil {
		lines = s.fetchLines(ctx, rawURL)
		if lines == nil {
			return ""
		}
		s.setCachedLines(rawURL, lines)
	}

	if lineno < 1 || lineno > len(lines) {
		return ""
	}
	line := lines[lineno-1]
	if line == "" {
		return ""
	}

	if len(line) <= ctxLineMax {
		return line
	}

	// Centre the window around colno, clamped to line bounds.
	half := ctxLineMax / 2
	start := max(colno-half, 0)
	end := start + ctxLineMax
	if end > len(line) {
		end = len(line)
		start = max(end-ctxLineMax, 0)
	}

	snippet := line[start:end]
	if start > 0 {
		snippet = "{snip} " + snippet
	}
	if end < len(line) {
		snippet += " {snip}"
	}
	return snippet
}

func (s *Store) fetchLines(ctx context.Context, rawURL string) []string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Tindra/1.0 (sourcemap-resolver)")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, fetchMaxSize))
	if err != nil {
		return nil
	}
	return strings.Split(string(data), "\n")
}

func (s *Store) getCachedLines(url string) []string {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	entry, ok := s.fileCache[url]
	if !ok || time.Since(entry.at) > fileCacheTTL {
		return nil
	}
	return entry.lines
}

func (s *Store) setCachedLines(url string, lines []string) {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	// Simple LRU: evict oldest entry when at capacity.
	if _, exists := s.fileCache[url]; !exists {
		if len(s.fileOrder) >= fileCacheMax {
			oldest := s.fileOrder[0]
			s.fileOrder = s.fileOrder[1:]
			delete(s.fileCache, oldest)
		}
		s.fileOrder = append(s.fileOrder, url)
	}
	s.fileCache[url] = cachedFile{lines: lines, at: time.Now()}
}
