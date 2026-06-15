package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func newHandlerAllowPrivate(buf *ingest.Buffer) http.Handler {
	return api.NewRouter(testPool, buf, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, true, true, nil)
}

// apiPassthroughDSN builds a DSN that the forwarder will translate into
// POST {srv.URL}/api/{projectID}/envelope/.
func apiPassthroughDSN(srv *httptest.Server, publicKey, projectID string) string {
	u, _ := url.Parse(srv.URL)
	return fmt.Sprintf("%s://%s@%s/%s", u.Scheme, publicKey, u.Host, projectID)
}

func setPassthroughDSN(t *testing.T, dsn *string) {
	t.Helper()
	_, err := storage.UpdateProject(context.Background(), testPool,
		testProject.ID, testProject.Name, testProject.Slug, dsn)
	if err != nil {
		t.Fatalf("set passthrough DSN: %v", err)
	}
}

// TestHandleEnvelope_passthroughForwards verifies that the raw envelope bytes
// are forwarded to the configured upstream DSN with the correct auth header.
func TestHandleEnvelope_passthroughForwards(t *testing.T) {
	truncateEvents(t)

	var (
		gotAuth string
		gotBody []byte
	)
	done := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("X-Sentry-Auth")
		gotBody, _ = io.ReadAll(r.Body)
		close(done)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	dsn := apiPassthroughDSN(upstream, "upstreamkey", "upstream-proj")
	setPassthroughDSN(t, &dsn)
	t.Cleanup(func() { setPassthroughDSN(t, nil) })

	buf := ingest.NewBuffer(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Run(ctx, testPool)

	payload := `{"timestamp":"2024-01-01T00:00:00Z","level":"error","message":"pt-test"}`
	body := eventEnvelope("pt-fwd-event-001", payload)

	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	newHandlerAllowPrivate(buf).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ingest returned %d: %s", rec.Code, rec.Body.String())
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive passthrough request within 2s")
	}

	// The envelope header must have the upstream DSN injected; items are unchanged.
	lines := strings.SplitN(string(gotBody), "\n", 2)
	if len(lines) < 2 {
		t.Fatalf("forwarded body has fewer than 2 lines: %q", string(gotBody))
	}
	var hdr map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[0]), &hdr); err != nil {
		t.Fatalf("envelope header is not valid JSON: %v", err)
	}
	var gotDSN string
	if err := json.Unmarshal(hdr["dsn"], &gotDSN); err != nil || gotDSN != dsn {
		t.Errorf("envelope header dsn: got %q, want %q", gotDSN, dsn)
	}
	wantItems := strings.SplitN(body, "\n", 2)[1]
	if lines[1] != wantItems {
		t.Errorf("envelope items changed:\ngot  %q\nwant %q", lines[1], wantItems)
	}
	if want := "Sentry sentry_version=7, sentry_key=upstreamkey"; gotAuth != want {
		t.Errorf("X-Sentry-Auth: got %q, want %q", gotAuth, want)
	}
}

// TestHandleEnvelope_passthroughNotCalledWhenUnset verifies that with no
// passthrough DSN configured the ingest endpoint still returns 200.
func TestHandleEnvelope_passthroughNotCalledWhenUnset(t *testing.T) {
	setPassthroughDSN(t, nil)

	buf := ingest.NewBuffer(100)
	payload := `{"timestamp":"2024-01-01T00:00:00Z","level":"error"}`
	body := eventEnvelope("no-pt-event-001", payload)

	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	newHandler(buf).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with no passthrough DSN, got %d", rec.Code)
	}
}

// TestHandleEnvelope_passthroughUpstreamFailDoesNotAffectIngest verifies that
// a 500 from the passthrough upstream does not prevent the event from being
// stored or cause a non-200 response to the SDK.
func TestHandleEnvelope_passthroughUpstreamFailDoesNotAffectIngest(t *testing.T) {
	truncateEvents(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	dsn := apiPassthroughDSN(upstream, "failkey", "fail-proj")
	setPassthroughDSN(t, &dsn)
	t.Cleanup(func() { setPassthroughDSN(t, nil) })

	buf := ingest.NewBuffer(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Run(ctx, testPool)

	payload := `{"timestamp":"2024-01-01T00:00:00Z","level":"error"}`
	body := eventEnvelope("pt-fail-event-001", payload)

	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	newHandlerAllowPrivate(buf).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("ingest should return 200 even when passthrough fails, got %d", rec.Code)
	}

	time.Sleep(350 * time.Millisecond) // wait for buffer flush

	var count int
	testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM events WHERE project_id = $1", testProject.ID,
	).Scan(&count)
	if count < 1 {
		t.Error("event should be stored in DB even when passthrough upstream fails")
	}
}
