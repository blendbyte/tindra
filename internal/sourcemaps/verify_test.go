package sourcemaps_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/sourcemaps"
)

func TestVerifySourceMapsExplainsMatchingFailures(t *testing.T) {
	store := sourcemaps.NewStore(t.TempDir(), testPool)
	_, err := store.Upload(t.Context(), testProject.ID, "verify-release", "~/verify.js", strings.NewReader(`{"version":3,"sources":["original.ts"],"mappings":"AAAA"}`))
	require.NoError(t, err)
	cases := []struct {
		name, release, url, platform, status, frameStatus string
		line                                              int
	}{
		{"matched", "verify-release", "https://cdn.test/verify.js", "javascript", "verified", "verified", 1},
		{"missing release", "", "https://cdn.test/verify.js", "javascript", "needs_attention", "missing_release", 1},
		{"wrong release", "other-release", "https://cdn.test/verify.js", "javascript", "needs_attention", "no_matching_map", 1},
		{"wrong url", "verify-release", "https://cdn.test/other.js", "javascript", "needs_attention", "no_matching_map", 1},
		{"no mapping", "verify-release", "https://cdn.test/verify.js", "javascript", "needs_attention", "no_mapping", 99},
		{"native platform", "verify-release", "app.py", "python", "not_applicable", "", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			payload, _ := json.Marshal(map[string]any{"release": c.release, "platform": c.platform, "exception": map[string]any{"values": []any{map[string]any{"stacktrace": map[string]any{"frames": []any{map[string]any{"abs_path": c.url, "lineno": c.line, "colno": 0}}}}}}})
			result, err := store.VerifyEvent(t.Context(), testProject.ID, payload)
			require.NoError(t, err)
			require.Equal(t, c.status, result.Status)
			if c.frameStatus != "" {
				require.Equal(t, c.frameStatus, result.Frames[0].Status)
			}
		})
	}
}

func TestVerifySourceMapsDoesNotVerifyMissingOriginalSource(t *testing.T) {
	store := sourcemaps.NewStore(t.TempDir(), testPool)
	_, err := store.Upload(t.Context(), testProject.ID, "missing-source", "~/broken.js", strings.NewReader(`{"version":3,"sources":[],"mappings":"AAAA"}`))
	require.NoError(t, err)
	result, err := store.VerifyEvent(t.Context(), testProject.ID, json.RawMessage(`{"release":"missing-source","platform":"javascript","exception":{"values":[{"stacktrace":{"frames":[{"filename":"~/broken.js","lineno":1,"colno":0}]}}]}}`))
	require.NoError(t, err)
	require.Equal(t, "needs_attention", result.Status)
	require.Equal(t, "no_mapping", result.Frames[0].Status)
}

func TestVerifySourceMapsHandlesIncompleteAndMalformedStacks(t *testing.T) {
	store := sourcemaps.NewStore(t.TempDir(), testPool)
	_, err := store.VerifyEvent(t.Context(), testProject.ID, json.RawMessage(`{`))
	require.Error(t, err)
	frames := []map[string]any{{"lineno": 1}, {"filename": "app.js", "lineno": 0}}
	for range 41 {
		frames = append(frames, map[string]any{"filename": "app.js", "lineno": 1})
	}
	payload, err := json.Marshal(map[string]any{"exception": map[string]any{"values": []any{map[string]any{"stacktrace": map[string]any{"frames": frames}}}}})
	require.NoError(t, err)
	result, err := store.VerifyEvent(t.Context(), testProject.ID, payload)
	require.NoError(t, err)
	require.True(t, result.Truncated)
	require.Len(t, result.Frames, 40)
	require.Equal(t, "needs_attention", result.Status)
}

func TestVerificationReturnsDatabaseFailureWithoutClaimingMissingMap(t *testing.T) {
	pool, err := pgxpool.NewWithConfig(t.Context(), testPool.Config())
	require.NoError(t, err)
	pool.Close()
	store := sourcemaps.NewStore(t.TempDir(), pool)
	result, err := store.VerifyEvent(t.Context(), testProject.ID, json.RawMessage(`{"release":"v1","platform":"javascript","exception":{"values":[{"stacktrace":{"frames":[{"filename":"~/app.js","lineno":1}]}}]}}`))
	require.Error(t, err)
	require.Nil(t, result)
}
