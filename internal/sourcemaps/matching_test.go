package sourcemaps_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/sourcemaps"
	"github.com/blendbyte/tindra/migrations"
)

// mapTo returns a source map whose first generated column maps to source.
func mapTo(source string) string {
	return `{"version":3,"sources":["` + source + `"],"sourcesContent":["original"],"mappings":"AAAA"}`
}

func resolvedSource(t *testing.T, store *sourcemaps.Store, release, frame string) any {
	t.Helper()
	payload := json.RawMessage(`{"exception":{"values":[{"type":"Error","stacktrace":{"frames":[` + frame + `]}}]}}`)
	var out map[string]any
	require.NoError(t, json.Unmarshal(store.ResolveEventPayload(t.Context(), testProject.ID, release, payload), &out))
	f := out["exception"].(map[string]any)["values"].([]any)[0].(map[string]any)["stacktrace"].(map[string]any)["frames"].([]any)[0].(map[string]any)
	return f["orig_filename"]
}

func TestResolveEventPayload_matchesFilenameWhenAbsPathMisses(t *testing.T) {
	store := sourcemaps.NewStore(t.TempDir(), testPool)
	_, err := store.Upload(t.Context(), testProject.ID, "match-fallback", "index.js", strings.NewReader(mapTo("src/worker.ts")))
	require.NoError(t, err)

	got := resolvedSource(t, store, "match-fallback", `{"abs_path":"/var/task/bundle/abc123.js","filename":"index.js","lineno":1,"colno":1}`)
	require.Equal(t, "src/worker.ts", got)
}

func TestResolveEventPayload_absPathMapWinsOverFilenameMap(t *testing.T) {
	store := sourcemaps.NewStore(t.TempDir(), testPool)
	_, err := store.Upload(t.Context(), testProject.ID, "match-order", "~/dist/app.js", strings.NewReader(mapTo("from-abs-path.ts")))
	require.NoError(t, err)
	_, err = store.Upload(t.Context(), testProject.ID, "match-order", "~/app.js", strings.NewReader(mapTo("from-filename.ts")))
	require.NoError(t, err)

	got := resolvedSource(t, store, "match-order", `{"abs_path":"https://cdn.test/dist/app.js","filename":"app.js","lineno":1,"colno":1}`)
	require.Equal(t, "from-abs-path.ts", got)
}

func TestResolveEventPayload_matchesCustomSchemes(t *testing.T) {
	store := sourcemaps.NewStore(t.TempDir(), testPool)
	_, err := store.Upload(t.Context(), testProject.ID, "match-scheme", "https://example.com/dist/app.js", strings.NewReader(mapTo("src/app.ts")))
	require.NoError(t, err)

	for _, frame := range []string{
		`{"abs_path":"app:///dist/app.js","lineno":1,"colno":1}`,
		`{"filename":"dist/app.js","lineno":1,"colno":1}`,
	} {
		require.Equal(t, "src/app.ts", resolvedSource(t, store, "match-scheme", frame), frame)
	}
}

func TestVerifyEvent_matchesFilenameWhenAbsPathMisses(t *testing.T) {
	store := sourcemaps.NewStore(t.TempDir(), testPool)
	_, err := store.Upload(t.Context(), testProject.ID, "verify-fallback", "index.js", strings.NewReader(mapTo("src/worker.ts")))
	require.NoError(t, err)

	result, err := store.VerifyEvent(t.Context(), testProject.ID, json.RawMessage(`{"release":"verify-fallback","platform":"javascript","exception":{"values":[{"stacktrace":{"frames":[{"abs_path":"/var/task/bundle/abc123.js","filename":"index.js","lineno":1,"colno":1}]}}]}}`))
	require.NoError(t, err)
	require.Equal(t, "verified", result.Status)
	require.Equal(t, "~/index.js", result.Frames[0].NormalizedURL)
}

// Migration 0035 must rewrite URLs stored under the old rules exactly as
// NormalizeURL does now, and must not fail when two old URLs collapse into one.
func TestMigration0035RenormalizesStoredURLs(t *testing.T) {
	ctx := context.Background()
	sql, err := migrations.FS.ReadFile("0035_sourcemap_url_normalization.up.sql")
	require.NoError(t, err)

	insert := func(release, url, age string) {
		_, err := testPool.Exec(ctx, `
			INSERT INTO sourcemaps (project_id, release, url, content_hash, size_bytes, created_at)
			VALUES ($1, $2, $3, 'h', 1, NOW() - $4::interval)`, testProject.ID, release, url, age)
		require.NoError(t, err)
	}
	urlOf := func(release, url string) bool {
		var n int
		require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM sourcemaps WHERE project_id=$1 AND release=$2 AND url=$3`, testProject.ID, release, url).Scan(&n))
		return n == 1
	}

	legacy := []string{"~index.js", "~app:///dist/a.js", "~webpack-internal:///./src/b.js", "~", "~/already/fine.js"}
	for _, u := range legacy {
		insert("mig-plain", u, "1 hour")
	}
	// Two old URLs for one new URL: the newest wins, the older is left alone.
	insert("mig-collide", "~app:///c.js", "2 hours")
	insert("mig-collide", "~c.js", "1 hour")
	// An existing current URL always wins.
	insert("mig-current", "~/d.js", "2 hours")
	insert("mig-current", "~d.js", "1 hour")

	_, err = testPool.Exec(ctx, string(sql))
	require.NoError(t, err)

	for _, u := range legacy {
		require.True(t, urlOf("mig-plain", sourcemaps.NormalizeURL(u)), "%s should become %s", u, sourcemaps.NormalizeURL(u))
	}
	require.True(t, urlOf("mig-collide", "~/c.js"))
	require.True(t, urlOf("mig-collide", "~app:///c.js"), "the older duplicate stays as it was")
	require.True(t, urlOf("mig-current", "~/d.js"))
	require.True(t, urlOf("mig-current", "~d.js"))

	// Running it again changes nothing.
	tag, err := testPool.Exec(ctx, string(sql))
	require.NoError(t, err)
	require.Zero(t, tag.RowsAffected())
}

func TestResolveEventPayload_indexMap(t *testing.T) {
	store := sourcemaps.NewStore(t.TempDir(), testPool)
	_, err := store.Upload(t.Context(), testProject.ID, "match-index", "~/_next/static/chunks/app.js", strings.NewReader(`{"version":3,"sections":[
		{"offset":{"line":0,"column":0},"map":{"version":3,"sources":["src/page.tsx"],"mappings":"AAAA"}},
		{"offset":{"line":5,"column":0},"map":{"version":3,"sources":["src/layout.tsx"],"mappings":"AAAA"}}
	]}`))
	require.NoError(t, err)

	got := resolvedSource(t, store, "match-index", `{"abs_path":"https://example.com/_next/static/chunks/app.js","lineno":6,"colno":1}`)
	require.Equal(t, "src/layout.tsx", got)
}
