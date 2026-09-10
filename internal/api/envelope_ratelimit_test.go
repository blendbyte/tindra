package api_test

import (
	"fmt"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

type envelopeReadTracker struct {
	io.Reader
	read bool
}

func (r *envelopeReadTracker) Read(p []byte) (int, error) { r.read = true; return r.Reader.Read(p) }

func TestEnvelopeLimitUsesAuthenticatedProject(t *testing.T) {
	project, err := storage.CreateProject(t.Context(), testPool, uuid.NewString(), "Rate-limited project")
	require.NoError(t, err)
	other, err := storage.CreateProject(t.Context(), testPool, uuid.NewString(), "Independent project")
	require.NoError(t, err)
	h := api.NewRouter(testPool, ingest.NewBuffer(10), nil, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 1, nil, false, false, nil)
	request := func(hint, key, transport string) (*httptest.ResponseRecorder, bool) {
		body := &envelopeReadTracker{Reader: strings.NewReader(eventEnvelope(uuid.NewString(), `{"message":"test"}`))}
		path := "/api/" + hint + "/envelope/"
		if transport == "query" {
			path += "?sentry_key=" + key
		}
		req := httptest.NewRequest("POST", path, body)
		if transport != "query" && key != "" {
			req.Header.Set(transport, sentryAuthHeader(key))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec, body.read
	}
	// A truncated/non-UUID SDK routing hint still resolves using its key.
	rec, read := request("123", project.PublicKey, "X-Sentry-Auth")
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.True(t, read)
	// A different project gets its own quota even when the hint is identical.
	rec, _ = request("123", other.PublicKey, "X-Sentry-Auth")
	require.Equal(t, 200, rec.Code, rec.Body.String())
	for i, transport := range []string{"X-Sentry-Auth", "Authorization", "query"} {
		rec, read = request(fmt.Sprintf("rotated-%d", i), project.PublicKey, transport)
		require.Equal(t, 429, rec.Code, rec.Body.String())
		require.False(t, read, "rejection must happen before envelope parsing")
		retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
		require.NoError(t, err)
		require.Greater(t, retry, 0)
		require.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	// Rotating the project's public key must not reset its quota either.
	newKey := strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = testPool.Exec(t.Context(), "UPDATE projects SET public_key=$1 WHERE id=$2", newKey, project.ID)
	require.NoError(t, err)
	rec, _ = request(project.ID, newKey, "X-Sentry-Auth")
	require.Equal(t, 429, rec.Code)
}

func TestEnvelopeLimitIgnoresUnauthenticatedRequests(t *testing.T) {
	project, err := storage.CreateProject(t.Context(), testPool, uuid.NewString(), "Authenticated quota")
	require.NoError(t, err)
	h := api.NewRouter(testPool, ingest.NewBuffer(10), nil, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 1, nil, false, false, nil)
	for _, key := range []string{"", "invalid-key", project.PublicKey} {
		req := httptest.NewRequest("POST", "/api/123/envelope/", strings.NewReader(eventEnvelope(uuid.NewString(), `{}`)))
		req.Header.Set("X-Sentry-Auth", sentryAuthHeader(key))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		status := 401
		if key == project.PublicKey {
			status = 200
		}
		require.Equal(t, status, rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest("OPTIONS", "/api/123/envelope/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)
	require.Empty(t, rec.Header().Get("Retry-After"))
}

func TestEnvelopeLimitCanBeDisabled(t *testing.T) {
	h := api.NewRouter(testPool, ingest.NewBuffer(10), nil, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, false, nil)
	for range 3 {
		req := httptest.NewRequest("POST", "/api/123/envelope/", strings.NewReader(eventEnvelope(uuid.NewString(), `{}`)))
		req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, 200, rec.Code, rec.Body.String())
	}
}
