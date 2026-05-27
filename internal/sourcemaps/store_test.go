package sourcemaps_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/sourcemaps"
	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
)

var (
	testPool    *pgxpool.Pool
	testProject *storage.Project
	testDataDir string
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	pool, cleanup := testutil.SetupDB(ctx)

	project, err := storage.CreateProject(ctx, pool, "sm-store-test", "SM Store Test")
	if err != nil {
		log.Fatalf("create project: %v", err)
	}

	dir, err := os.MkdirTemp("", "tindra-sm-test-*")
	if err != nil {
		log.Fatalf("temp dir: %v", err)
	}

	testPool = pool
	testProject = project
	testDataDir = dir

	code := m.Run()
	cleanup()
	os.RemoveAll(dir)
	os.Exit(code)
}

// --- NormalizeURL ---

func TestNormalizeURL_absolute(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://example.com/dist/main.js", "~/dist/main.js"},
		{"http://cdn.example.com/app.js", "~/app.js"},
		{"~/dist/main.js", "~/dist/main.js"},
		{"/dist/main.js", "~/dist/main.js"},
		{"dist/main.js", "~dist/main.js"}, // no leading slash → no slash added
	}
	for _, tc := range cases {
		got := sourcemaps.NormalizeURL(tc.in)
		if got != tc.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- Store.Upload ---

func TestStore_Upload_and_retrieve(t *testing.T) {
	// Truncate sourcemaps
	testPool.Exec(context.Background(), "TRUNCATE sourcemaps CASCADE")

	store := sourcemaps.NewStore(testDataDir, testPool)

	content := []byte(`{"version":3,"sources":["src/app.js"],"mappings":"AAAA"}`)
	r := strings.NewReader(string(content))

	sm, err := store.Upload(context.Background(), testProject.ID, "v1.0.0", "https://example.com/dist/app.js", r)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if sm.ID == "" {
		t.Error("expected non-empty ID")
	}
	if sm.URL != "~/dist/app.js" {
		t.Errorf("URL: got %q, want ~/dist/app.js", sm.URL)
	}
	if sm.SizeBytes != len(content) {
		t.Errorf("size_bytes: got %d, want %d", sm.SizeBytes, len(content))
	}

	// File should exist on disk
	path := testDataDir + "/sourcemaps/" + testProject.ID + "/" + sm.ContentHash + ".map"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(data) != string(content) {
		t.Error("file content mismatch")
	}
}

func TestStore_Upload_replaces(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE sourcemaps CASCADE")
	store := sourcemaps.NewStore(testDataDir, testPool)

	old := `{"version":3,"sources":["a.js"],"mappings":"AAAA"}`
	store.Upload(context.Background(), testProject.ID, "v1", "~/app.js", strings.NewReader(old))

	fresh := `{"version":3,"sources":["b.js"],"mappings":"AAAA"}`
	sm, err := store.Upload(context.Background(), testProject.ID, "v1", "~/app.js", strings.NewReader(fresh))
	if err != nil {
		t.Fatalf("replace upload: %v", err)
	}
	if sm.SizeBytes != len(fresh) {
		t.Errorf("size after replace: got %d, want %d", sm.SizeBytes, len(fresh))
	}
}

// --- ResolveEventPayload ---

func minimalSourceMapContent() string {
	sm := map[string]any{
		"version":        3,
		"sources":        []string{"src/original.js"},
		"sourcesContent": []string{"var a = 1;\nvar b = 2;"},
		"mappings":       "AAAA,IAAI;AACJ",
	}
	b, _ := json.Marshal(sm)
	return string(b)
}

func TestStore_ResolveEventPayload_noRelease(t *testing.T) {
	store := sourcemaps.NewStore(testDataDir, testPool)
	payload := json.RawMessage(`{"level":"error"}`)
	got := store.ResolveEventPayload(context.Background(), testProject.ID, "", payload)
	if string(got) != string(payload) {
		t.Error("expected unchanged payload when release is empty")
	}
}

func TestStore_ResolveEventPayload_noException(t *testing.T) {
	store := sourcemaps.NewStore(testDataDir, testPool)
	payload := json.RawMessage(`{"level":"error","message":"plain"}`)
	got := store.ResolveEventPayload(context.Background(), testProject.ID, "v1", payload)
	if string(got) != string(payload) {
		t.Error("expected unchanged payload without exception block")
	}
}

func TestStore_ResolveEventPayload_withSourcemap(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE sourcemaps CASCADE")
	store := sourcemaps.NewStore(testDataDir, testPool)

	// Upload a source map for ~/dist/app.js
	store.Upload(context.Background(), testProject.ID, "v2.0.0", "~/dist/app.js",
		strings.NewReader(minimalSourceMapContent()))

	payload := json.RawMessage(`{
		"exception": {
			"values": [{
				"type": "TypeError",
				"stacktrace": {
					"frames": [{
						"abs_path": "https://cdn.example.com/dist/app.js",
						"lineno": 1,
						"colno": 0
					}]
				}
			}]
		}
	}`)

	got := store.ResolveEventPayload(context.Background(), testProject.ID, "v2.0.0", payload)

	var out map[string]any
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	frames := out["exception"].(map[string]any)["values"].([]any)[0].(map[string]any)["stacktrace"].(map[string]any)["frames"].([]any)
	frame := frames[0].(map[string]any)
	if _, ok := frame["orig_filename"]; !ok {
		t.Error("expected orig_filename to be set after resolution")
	}
}

func TestStore_ResolveEventPayload_frameWithFilename(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE sourcemaps CASCADE")
	store := sourcemaps.NewStore(testDataDir, testPool)

	// Upload sourcemap for ~/dist/app.js
	store.Upload(context.Background(), testProject.ID, "v4", "~/dist/app.js",
		strings.NewReader(minimalSourceMapContent()))

	// Frame uses "filename" instead of "abs_path"
	payload := json.RawMessage(`{
		"exception": {
			"values": [{
				"stacktrace": {
					"frames": [{
						"filename": "~/dist/app.js",
						"lineno": 1,
						"colno": 0
					}]
				}
			}]
		}
	}`)

	got := store.ResolveEventPayload(context.Background(), testProject.ID, "v4", payload)
	var out map[string]any
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	frames := out["exception"].(map[string]any)["values"].([]any)[0].(map[string]any)["stacktrace"].(map[string]any)["frames"].([]any)
	frame := frames[0].(map[string]any)
	if _, ok := frame["orig_filename"]; !ok {
		t.Error("expected orig_filename when using filename field")
	}
}

func TestStore_ResolveEventPayload_frameNoLineno(t *testing.T) {
	store := sourcemaps.NewStore(testDataDir, testPool)

	// lineno missing → early return, frame unchanged
	payload := json.RawMessage(`{
		"exception": {
			"values": [{
				"stacktrace": {
					"frames": [{
						"abs_path": "~/dist/app.js",
						"lineno": 0,
						"colno": 0
					}]
				}
			}]
		}
	}`)

	got := store.ResolveEventPayload(context.Background(), testProject.ID, "v4", payload)
	var out map[string]any
	json.Unmarshal(got, &out)
	frames := out["exception"].(map[string]any)["values"].([]any)[0].(map[string]any)["stacktrace"].(map[string]any)["frames"].([]any)
	frame := frames[0].(map[string]any)
	if _, ok := frame["orig_filename"]; ok {
		t.Error("expected no orig_filename when lineno=0")
	}
}

func TestStore_ResolveEventPayload_frameNoURL(t *testing.T) {
	store := sourcemaps.NewStore(testDataDir, testPool)

	// Neither abs_path nor filename → early return
	payload := json.RawMessage(`{
		"exception": {
			"values": [{
				"stacktrace": {
					"frames": [{"lineno": 1, "colno": 0}]
				}
			}]
		}
	}`)

	got := store.ResolveEventPayload(context.Background(), testProject.ID, "v4", payload)
	var out map[string]any
	json.Unmarshal(got, &out)
	frames := out["exception"].(map[string]any)["values"].([]any)[0].(map[string]any)["stacktrace"].(map[string]any)["frames"].([]any)
	frame := frames[0].(map[string]any)
	if _, ok := frame["orig_filename"]; ok {
		t.Error("expected no orig_filename when no URL field")
	}
}

func TestStore_ResolveEventPayload_invalidJSON(t *testing.T) {
	store := sourcemaps.NewStore(testDataDir, testPool)
	payload := json.RawMessage(`not json`)
	got := store.ResolveEventPayload(context.Background(), testProject.ID, "v1", payload)
	if string(got) != string(payload) {
		t.Error("expected unchanged payload for invalid JSON")
	}
}

// --- Store.Delete ---

func TestStore_Delete_notFound(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE sourcemaps CASCADE")
	store := sourcemaps.NewStore(testDataDir, testPool)

	ok, err := store.Delete(context.Background(), "00000000-0000-0000-0000-000000000000", testProject.ID)
	if err != nil {
		t.Fatalf("delete non-existent: %v", err)
	}
	if ok {
		t.Error("expected ok=false for non-existent record")
	}
}

func TestStore_Delete_removesRecord(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE sourcemaps CASCADE")
	store := sourcemaps.NewStore(testDataDir, testPool)

	content := `{"version":3,"sources":["src/del.js"],"mappings":"AAAA"}`
	sm, err := store.Upload(context.Background(), testProject.ID, "v3.0.0", "~/dist/del.js", strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	ok, err := store.Delete(context.Background(), sm.ID, testProject.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !ok {
		t.Error("expected ok=true after deleting existing record")
	}

	// The file on disk should also be removed (no other record shares the hash).
	path := testDataDir + "/sourcemaps/" + testProject.ID + "/" + sm.ContentHash + ".map"
	if _, err := os.Stat(path); err == nil {
		t.Error("expected file to be removed from disk after delete")
	}
}

func TestStore_Delete_keepsFileWhenHashShared(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE sourcemaps CASCADE")
	store := sourcemaps.NewStore(testDataDir, testPool)

	// Upload identical content under two different releases — same hash, two records.
	content := `{"version":3,"sources":["src/shared.js"],"mappings":"AAAA"}`
	sm1, err := store.Upload(context.Background(), testProject.ID, "vA", "~/dist/shared.js", strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload v1: %v", err)
	}
	_, err = store.Upload(context.Background(), testProject.ID, "vB", "~/dist/shared.js", strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload v2: %v", err)
	}

	// Delete the first record — file must stay because vB still references it.
	ok, err := store.Delete(context.Background(), sm1.ID, testProject.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !ok {
		t.Error("expected ok=true")
	}

	path := testDataDir + "/sourcemaps/" + testProject.ID + "/" + sm1.ContentHash + ".map"
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file should remain on disk while another record shares the hash: %v", err)
	}
}

func TestStore_ResolveEventPayload_noSourcemapInDB(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE sourcemaps CASCADE")
	store := sourcemaps.NewStore(testDataDir, testPool)

	// Use a non-HTTP URL so the HTTP-fetch fallback is skipped immediately.
	payload := json.RawMessage(`{
		"exception": {
			"values": [{
				"stacktrace": {
					"frames": [{
						"abs_path": "~/dist/missing.js",
						"lineno": 1,
						"colno": 0
					}]
				}
			}]
		}
	}`)

	got := store.ResolveEventPayload(context.Background(), testProject.ID, "v3", payload)
	// Frame should be unchanged: no sourcemap in DB, non-HTTP URL skips fetch fallback.
	var outFrames, inFrames []any
	inMap := map[string]any{}
	outMap := map[string]any{}
	json.Unmarshal(payload, &inMap)
	json.Unmarshal(got, &outMap)
	inFrames = inMap["exception"].(map[string]any)["values"].([]any)[0].(map[string]any)["stacktrace"].(map[string]any)["frames"].([]any)
	outFrames = outMap["exception"].(map[string]any)["values"].([]any)[0].(map[string]any)["stacktrace"].(map[string]any)["frames"].([]any)

	inFrame := inFrames[0].(map[string]any)
	outFrame := outFrames[0].(map[string]any)
	if _, ok := outFrame["orig_filename"]; ok {
		t.Error("expected no orig_filename when sourcemap missing")
	}
	if outFrame["abs_path"] != inFrame["abs_path"] {
		t.Error("abs_path should be unchanged when no sourcemap")
	}
}

func TestStore_fetchContextLine_httpFallback(t *testing.T) {
	jsContent := "const x = 1;\nconst y = 2;\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, jsContent)
	}))
	defer srv.Close()

	testPool.Exec(context.Background(), "TRUNCATE sourcemaps CASCADE")
	store := sourcemaps.NewStore(testDataDir, testPool)

	payload := json.RawMessage(fmt.Sprintf(`{"exception":{"values":[{"stacktrace":{"frames":[{"abs_path":"%s/app.js","lineno":1,"colno":0}]}}]}}`, srv.URL))

	// First call: fetches from HTTP server, populates cache
	got := store.ResolveEventPayload(context.Background(), testProject.ID, "", payload)
	var out map[string]any
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	frames := out["exception"].(map[string]any)["values"].([]any)[0].(map[string]any)["stacktrace"].(map[string]any)["frames"].([]any)
	frame := frames[0].(map[string]any)
	if _, ok := frame["context_line"]; !ok {
		t.Error("expected context_line to be set from HTTP fallback")
	}

	// Second call: same URL → cache hit (getCachedLines returns non-nil)
	got2 := store.ResolveEventPayload(context.Background(), testProject.ID, "", payload)
	var out2 map[string]any
	if err := json.Unmarshal(got2, &out2); err != nil {
		t.Fatalf("unmarshal cached: %v", err)
	}
	frames2 := out2["exception"].(map[string]any)["values"].([]any)[0].(map[string]any)["stacktrace"].(map[string]any)["frames"].([]any)
	frame2 := frames2[0].(map[string]any)
	if _, ok := frame2["context_line"]; !ok {
		t.Error("expected context_line on second call (cache hit)")
	}
}

func TestStore_fetchContextLine_longLine(t *testing.T) {
	// Line longer than ctxLineMax (140) triggers {snip} truncation
	longLine := strings.Repeat("a", 200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, longLine)
	}))
	defer srv.Close()

	store := sourcemaps.NewStore(testDataDir, testPool)
	// colno=100: start=30, end=170; both start>0 and end<200 → leading and trailing {snip}
	payload := json.RawMessage(fmt.Sprintf(`{"exception":{"values":[{"stacktrace":{"frames":[{"abs_path":"%s/long.js","lineno":1,"colno":100}]}}]}}`, srv.URL))
	got := store.ResolveEventPayload(context.Background(), testProject.ID, "", payload)

	var out map[string]any
	json.Unmarshal(got, &out)
	frames := out["exception"].(map[string]any)["values"].([]any)[0].(map[string]any)["stacktrace"].(map[string]any)["frames"].([]any)
	frame := frames[0].(map[string]any)
	cl, _ := frame["context_line"].(string)
	if !strings.Contains(cl, "{snip}") {
		t.Errorf("expected {snip} in context_line for long line, got %q", cl)
	}
}

func TestStore_fetchContextLine_nonHTTPURL(t *testing.T) {
	store := sourcemaps.NewStore(testDataDir, testPool)
	// ~/dist/app.js is not an http:// URL → fetchContextLine returns "" immediately
	payload := json.RawMessage(`{"exception":{"values":[{"stacktrace":{"frames":[{"abs_path":"~/dist/app.js","lineno":1,"colno":0}]}}]}}`)
	got := store.ResolveEventPayload(context.Background(), testProject.ID, "", payload)

	var out map[string]any
	json.Unmarshal(got, &out)
	frames := out["exception"].(map[string]any)["values"].([]any)[0].(map[string]any)["stacktrace"].(map[string]any)["frames"].([]any)
	frame := frames[0].(map[string]any)
	if _, ok := frame["context_line"]; ok {
		t.Error("expected no context_line for non-HTTP abs_path")
	}
}
