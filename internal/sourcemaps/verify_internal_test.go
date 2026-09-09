package sourcemaps

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
)

func TestVerificationRespectsBusyParserDeadline(t *testing.T) {
	pool, cleanup := testutil.SetupDB(t.Context())
	defer cleanup()
	p, err := storage.CreateProject(t.Context(), pool, "verify-timeout", "Setup")
	require.NoError(t, err)
	store := NewStore(t.TempDir(), pool)
	_, err = store.Upload(t.Context(), p.ID, "v1", "~/app.js", strings.NewReader(`{"version":3,"sources":["app.ts"],"mappings":"AAAA"}`))
	require.NoError(t, err)
	store.parseGate <- struct{}{}
	defer func() { <-store.parseGate }()
	result, err := store.VerifyEvent(t.Context(), p.ID, json.RawMessage(`{"release":"v1","exception":{"values":[{"stacktrace":{"frames":[{"filename":"~/app.js","lineno":1}]}}]}}`))
	require.Error(t, err)
	require.Nil(t, result)
}
