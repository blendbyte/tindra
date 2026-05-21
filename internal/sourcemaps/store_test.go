package sourcemaps_test

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	testcontainers "github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/blendbyte/tindra/internal/sourcemaps"
	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/migrations"
)

var (
	testPool    *pgxpool.Pool
	testProject *storage.Project
	testDataDir string
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:18",
		tcpostgres.WithDatabase("tindra_test"),
		tcpostgres.WithUsername("tindra"),
		tcpostgres.WithPassword("tindra"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		log.Fatalf("start postgres: %v", err)
	}

	connStr, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatalf("connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		log.Fatalf("create pool: %v", err)
	}

	names, err := migrations.Files()
	if err != nil {
		log.Fatalf("list migrations: %v", err)
	}
	for _, name := range names {
		sql, err := migrations.FS.ReadFile(name)
		if err != nil {
			log.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			log.Fatalf("apply %s: %v", name, err)
		}
	}

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

	pool.Close()
	_ = ctr.Terminate(ctx)
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

	payload := json.RawMessage(`{
		"exception": {
			"values": [{
				"stacktrace": {
					"frames": [{
						"abs_path": "https://example.com/missing.js",
						"lineno": 1,
						"colno": 0
					}]
				}
			}]
		}
	}`)

	got := store.ResolveEventPayload(context.Background(), testProject.ID, "v3", payload)
	// Should return payload unchanged (no sourcemap for this URL)
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
