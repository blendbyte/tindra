package sourcemaps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestSharedFetchAndFailureCache(t *testing.T) {
	var hits atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			close(started)
		}
		<-release
		http.Error(w, "missing", 404)
	}))
	defer server.Close()
	store := NewTestStore("", nil)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); store.sharedLines(context.Background(), server.URL) }()
	}
	<-started
	close(release)
	wg.Wait()
	if hits.Load() != 1 {
		t.Fatalf("20 readers issued %d fetches", hits.Load())
	}
	store.sharedLines(context.Background(), server.URL)
	if hits.Load() != 1 {
		t.Fatal("failed response was not cached")
	}
	store.fileMu.Lock()
	entry := store.fileCache[server.URL]
	entry.at = time.Now().Add(-failureCacheTTL)
	store.fileCache[server.URL] = entry
	store.fileMu.Unlock()
	store.sharedLines(context.Background(), server.URL)
	if hits.Load() != 2 {
		t.Fatal("failure did not expire")
	}
}

func TestEnrichmentTotalBudget(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); <-r.Context().Done() }))
	defer server.Close()
	store := NewTestStore("", nil)
	frames := []map[string]any{}
	for i := range 10 {
		frames = append(frames, map[string]any{"abs_path": fmt.Sprintf("%s/%d.js", server.URL, i), "lineno": 1})
	}
	payload, _ := json.Marshal(map[string]any{"message": "basic data", "exception": map[string]any{"values": []any{map[string]any{"stacktrace": map[string]any{"frames": frames}}}}})
	start := time.Now()
	got := store.ResolveEventPayload(context.Background(), "", "", payload)
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("enrichment waited %s", elapsed)
	}
	if string(got) != string(payload) {
		t.Fatal("basic event changed on budget exhaustion")
	}
	if hits.Load() != 1 {
		t.Fatalf("attempted %d URLs after exhausting shared deadline", hits.Load())
	}
	t.Logf("ten stalled URLs: %s, outbound requests=%d", elapsed, hits.Load())
}

func TestFetchCapacityAndWaiterCancellation(t *testing.T) {
	started := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { started <- struct{}{}; <-r.Context().Done() }))
	defer server.Close()
	store := NewTestStore("", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); store.sharedLines(ctx, fmt.Sprintf("%s/%d", server.URL, i)) }()
	}
	for range 4 {
		<-started
	}
	if got := store.sharedLines(context.Background(), server.URL+"/overflow"); got != nil {
		t.Fatal("overflow did not skip")
	}
	waiter, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { store.sharedLines(waiter, server.URL+"/0"); close(done) }()
	stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waiter stuck")
	}
	cancel()
	wg.Wait()
	if len(store.fileCache) != 0 {
		t.Fatal("caller cancellation was negative-cached")
	}
}

func TestCacheByteBudget(t *testing.T) {
	store := NewTestStore("", nil)
	for i := range 10 {
		store.cacheLines(fmt.Sprint(i), []string{strings.Repeat("x", 5<<20)})
	}
	if store.fileBytes > cacheBytesMax || len(store.fileCache) >= 10 {
		t.Fatal("file cache not bounded")
	}
	if _, ok := store.fileCache["0"]; ok {
		t.Fatal("oldest not evicted")
	}
}

func TestParseContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ParseContext(ctx, []byte(`{"version":3,"mappings":"AAAA"}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestConcurrentParsedCache(t *testing.T) {
	store := NewTestStore(t.TempDir(), nil)
	dir := filepath.Join(store.dataDir, "sourcemaps", "project")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hash.map"), []byte(`{"version":3,"sources":["app.js"],"mappings":"AAAA"}`), 0600); err != nil {
		t.Fatal(err)
	}
	results := make(chan *SourceMap, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- store.parsedSourceMap(context.Background(), "project", "hash") }()
	}
	wg.Wait()
	close(results)
	var first *SourceMap
	for sm := range results {
		if sm == nil {
			t.Fatal("concurrent reader lost map")
		}
		if first == nil {
			first = sm
		}
		if sm != first {
			t.Fatal("same content parsed twice")
		}
	}
	// A busy cold-parse slot does not block warm lookups; cold waiters cancel.
	store.parseGate <- struct{}{}
	defer func() { <-store.parseGate }()
	if store.parsedSourceMap(context.Background(), "project", "hash") != first {
		t.Fatal("warm lookup unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if store.parsedSourceMap(ctx, "project", "cold") != nil || ctx.Err() == nil {
		t.Fatal("cold parse ignored its deadline")
	}
}

func TestEnrichmentBudgetPreservesCompletedFrames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fast.js" {
			fmt.Fprint(w, "const answer = 42;")
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	payload, _ := json.Marshal(map[string]any{"exception": map[string]any{"values": []any{map[string]any{"stacktrace": map[string]any{"frames": []any{
		map[string]any{"abs_path": server.URL + "/fast.js", "lineno": 1},
		map[string]any{"abs_path": server.URL + "/slow.js", "lineno": 1},
		map[string]any{"abs_path": server.URL + "/later.js", "lineno": 1},
	}}}}}})
	got := NewTestStore("", nil).ResolveEventPayload(context.Background(), "", "", payload)
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	frame := decoded["exception"].(map[string]any)["values"].([]any)[0].(map[string]any)["stacktrace"].(map[string]any)["frames"].([]any)[0].(map[string]any)
	if line, _ := frame["context_line"].(string); !strings.Contains(line, "answer") {
		t.Fatalf("completed frame enrichment was discarded: %s", got)
	}
}

func TestOversizedFileDoesNotDisplaceCachedSource(t *testing.T) {
	store := NewTestStore("", nil)
	store.cacheLines("small", []string{"keep"})
	before := store.fileBytes
	store.cacheLines("large", []string{strings.Repeat("x", cacheBytesMax)})
	if store.fileBytes != before || len(store.fileCache) != 1 || store.fileCache["small"].lines[0] != "keep" {
		t.Fatal("oversized source displaced cache")
	}
}

func TestParsedCacheRejectsMissingInvalidAndCancelledReads(t *testing.T) {
	store := NewTestStore(t.TempDir(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if store.parsedSourceMap(ctx, "p", "hash") != nil {
		t.Fatal("cancelled read returned a map")
	}
	if store.parsedSourceMap(context.Background(), "p", "hash") != nil {
		t.Fatal("missing map accepted")
	}
	dir := filepath.Join(store.dataDir, "sourcemaps", "p")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, "hash.map")
	if err := os.WriteFile(name, []byte(`{"version":3,"mappings":"!"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if store.parsedSourceMap(context.Background(), "p", "hash") != nil {
		t.Fatal("invalid map accepted")
	}
	if err := os.WriteFile(name, []byte(`{"version":3,"sources":["app.js"],"mappings":"AAAA"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if store.parsedSourceMap(context.Background(), "p", "hash") == nil {
		t.Fatal("failed read prevented later recovery")
	}
}

func TestParsedCacheEvictsOldestAndReparses(t *testing.T) {
	store := NewTestStore(t.TempDir(), nil)
	dir := filepath.Join(store.dataDir, "sourcemaps", "p")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	var first *SourceMap
	for i := 0; i < 65; i++ {
		hash := fmt.Sprint(i)
		if err := os.WriteFile(filepath.Join(dir, hash+".map"), []byte(`{"version":3,"sources":["app.js"],"mappings":"AAAA"}`), 0600); err != nil {
			t.Fatal(err)
		}
		sm := store.parsedSourceMap(context.Background(), "p", hash)
		if sm == nil {
			t.Fatal("map not parsed")
		}
		if i == 0 {
			first = sm
		}
	}
	if len(store.parsed) != 64 || store.parsed["p/0"] != nil {
		t.Fatal("cache did not evict oldest map")
	}
	if got := store.parsedSourceMap(context.Background(), "p", "0"); got == nil || got == first {
		t.Fatal("evicted map not reparsed")
	}
	size := 0
	for key, sm := range store.parsed {
		size += len(key) + sm.cacheSize()
	}
	if size != store.parsedBytes {
		t.Fatalf("accounted=%d actual=%d", store.parsedBytes, size)
	}
}

func TestOversizedParsedMapRemainsUsableWithoutCaching(t *testing.T) {
	store := NewTestStore(t.TempDir(), nil)
	dir := filepath.Join(store.dataDir, "sourcemaps", "p")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"version": 3, "sources": []string{"app.js"}, "sourcesContent": []string{strings.Repeat("x", cacheBytesMax)}, "mappings": "AAAA"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "large.map"), data, 0600); err != nil {
		t.Fatal(err)
	}
	sm := store.parsedSourceMap(context.Background(), "p", "large")
	if sm == nil {
		t.Fatal("oversized map unavailable")
	}
	if len(store.parsed) != 0 || store.parsedBytes != 0 {
		t.Fatal("oversized map retained in cache")
	}
}

func TestSharedSourceWaiterReceivesOwnersResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := NewTestStore("", nil)
		flight := &fileFlight{done: make(chan struct{})}
		store.flights["source.js"] = flight
		result := make(chan []string, 1)
		go func() { result <- store.sharedLines(context.Background(), "source.js") }()
		synctest.Wait()
		select {
		case <-result:
			t.Fatal("waiter returned before the owner")
		default:
		}
		flight.lines = []string{"const answer = 42"}
		close(flight.done)
		synctest.Wait()
		got := <-result
		if len(got) != 1 || got[0] != "const answer = 42" {
			t.Fatalf("got %v", got)
		}
	})
}
