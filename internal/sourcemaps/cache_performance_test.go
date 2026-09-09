package sourcemaps

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestParsedCachePerformance(t *testing.T) {
	if os.Getenv("TINDRA_PERF_SOURCEMAPS") != "1" {
		t.Skip("set TINDRA_PERF_SOURCEMAPS=1")
	}
	store := NewStore(t.TempDir(), nil)
	dir := filepath.Join(store.dataDir, "sourcemaps", "project")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"version": 3, "sources": []string{"src/app.js"}, "sourcesContent": []string{strings.Repeat("const x = 1;\n", 100000)}, "mappings": strings.Repeat("AAAA;", 100000)})
	file := filepath.Join(dir, "hash.map")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, cached := range []bool{false, true} {
		var samples []time.Duration
		for i := range 7 {
			start := time.Now()
			var sm *SourceMap
			if cached {
				sm = store.parsedSourceMap(context.Background(), "project", "hash")
			} else {
				raw, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				sm, err = Parse(raw)
				if err != nil {
					t.Fatal(err)
				}
			}
			if sm == nil {
				t.Fatal("missing parsed map")
			}
			if _, ok := sm.Resolve(1, 0); !ok {
				t.Fatal("missing mapping")
			}
			if i > 0 {
				samples = append(samples, time.Since(start))
			}
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		t.Logf("map bytes=%d cached=%v median=%s", len(data), cached, samples[len(samples)/2])
	}
}
