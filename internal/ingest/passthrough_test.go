package ingest_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/ingest"
)

// buildDSN returns a Sentry-style DSN pointing at srv: scheme://publicKey@host/projectID.
func buildDSN(srv *httptest.Server, publicKey, projectID string) string {
	u, _ := url.Parse(srv.URL)
	return fmt.Sprintf("%s://%s@%s/%s", u.Scheme, publicKey, u.Host, projectID)
}

func TestForwardEnvelope_sendsRawBytesWithCorrectHeaders(t *testing.T) {
	raw := []byte("{\"event_id\":\"abc\"}\n{\"type\":\"event\",\"length\":2}\n{}\n")

	var (
		gotMethod      string
		gotAuth        string
		gotContentType string
		gotBody        []byte
	)
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("X-Sentry-Auth")
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		close(done)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ingest.ForwardEnvelope(&http.Client{Timeout: 5 * time.Second}, buildDSN(srv, "mykey", "proj1"), raw)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive request within 2s")
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q, want POST", gotMethod)
	}
	if gotAuth != "Sentry sentry_version=7, sentry_key=mykey" {
		t.Errorf("X-Sentry-Auth: got %q", gotAuth)
	}
	if gotContentType != "application/x-sentry-envelope" {
		t.Errorf("Content-Type: got %q", gotContentType)
	}
	if string(gotBody) != string(raw) {
		t.Errorf("body mismatch:\ngot  %q\nwant %q", string(gotBody), string(raw))
	}
}

func TestForwardEnvelope_postsToCorrectEndpoint(t *testing.T) {
	var gotPath string
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		close(done)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ingest.ForwardEnvelope(&http.Client{Timeout: 5 * time.Second}, buildDSN(srv, "k", "42"), []byte("{}"))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive request within 2s")
	}

	if gotPath != "/api/42/envelope/" {
		t.Errorf("path: got %q, want /api/42/envelope/", gotPath)
	}
}

func TestForwardEnvelope_upstreamErrorDoesNotPanic(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(done)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ingest.ForwardEnvelope(&http.Client{Timeout: 5 * time.Second}, buildDSN(srv, "k", "123"), []byte("{}"))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive request within 2s")
	}
	// passes if we reach here without panicking
}

func TestForwardEnvelope_badDSNDoesNotPanic(t *testing.T) {
	ingest.ForwardEnvelope(&http.Client{Timeout: 5 * time.Second}, "not a url ://garbage", []byte("{}"))
	time.Sleep(100 * time.Millisecond) // let goroutine finish
}

func TestForwardEnvelope_missingPublicKeyDoesNotPanic(t *testing.T) {
	// No userinfo in DSN - should log and return, not panic.
	ingest.ForwardEnvelope(&http.Client{Timeout: 5 * time.Second}, "http://localhost:19999/123", []byte("{}"))
	time.Sleep(100 * time.Millisecond)
}

func TestForwardEnvelope_missingProjectIDDoesNotPanic(t *testing.T) {
	// Empty path after trimming - should log and return, not panic.
	ingest.ForwardEnvelope(&http.Client{Timeout: 5 * time.Second}, "http://key@localhost:19999/", []byte("{}"))
	time.Sleep(100 * time.Millisecond)
}
