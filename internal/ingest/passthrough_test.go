package ingest_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/ingest"
)

// buildDSN returns a Sentry-style DSN pointing at srv: scheme://publicKey@host/path.
func buildDSN(srv *httptest.Server, publicKey, path string) string {
	srv.Client() // NewTestServer starts lazily; srv.URL is empty until first use.
	u, _ := url.Parse(srv.URL)
	return fmt.Sprintf("%s://%s@%s/%s", u.Scheme, publicKey, u.Host, path)
}

type upstreamRequest struct {
	method, path, auth, contentType string
	body                            []byte
}

// recordingUpstream captures every forwarded request, gunzipping the body.
func recordingUpstream(t *testing.T, status int) (*httptest.Server, func() []upstreamRequest) {
	t.Helper()
	var mu sync.Mutex
	var got []upstreamRequest
	srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := io.Reader(r.Body)
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Errorf("forwarded body is not gzip: %v", err)
				return
			}
			body = gz
		}
		b, _ := io.ReadAll(body)
		mu.Lock()
		got = append(got, upstreamRequest{r.Method, r.URL.Path, r.Header.Get("X-Sentry-Auth"), r.Header.Get("Content-Type"), b})
		mu.Unlock()
		w.WriteHeader(status)
	}))
	return srv, func() []upstreamRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]upstreamRequest(nil), got...)
	}
}

func forwardAndDrain(t *testing.T, client *http.Client, dsn string, envelope []byte) {
	t.Helper()
	f := ingest.NewForwarder(client, 1, 4)
	if !f.Enqueue(dsn, envelope) {
		t.Fatal("enqueue refused")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestForwarder_sendsEnvelopeWithCorrectHeaders(t *testing.T) {
	// Envelope header without a dsn field; items follow unchanged.
	raw := []byte("{\"event_id\":\"abc\"}\n{\"type\":\"event\",\"length\":2}\n{}\n")
	srv, requests := recordingUpstream(t, http.StatusOK)
	dsn := buildDSN(srv, "mykey", "proj1")
	forwardAndDrain(t, srv.Client(), dsn, raw)

	got := requests()
	if len(got) != 1 {
		t.Fatalf("expected 1 upstream request, got %d", len(got))
	}
	req := got[0]
	if req.method != http.MethodPost || req.path != "/api/proj1/envelope/" {
		t.Errorf("request: %s %s", req.method, req.path)
	}
	if req.auth != "Sentry sentry_version=7, sentry_key=mykey" {
		t.Errorf("X-Sentry-Auth: got %q", req.auth)
	}
	if req.contentType != "application/x-sentry-envelope" {
		t.Errorf("Content-Type: got %q", req.contentType)
	}

	// The envelope header must have the upstream DSN injected; items are unchanged.
	header, items, ok := strings.Cut(string(req.body), "\n")
	if !ok {
		t.Fatalf("forwarded body has fewer than 2 lines: %q", req.body)
	}
	var hdr map[string]json.RawMessage
	if err := json.Unmarshal([]byte(header), &hdr); err != nil {
		t.Fatalf("envelope header is not valid JSON: %v", err)
	}
	var gotDSN string
	if err := json.Unmarshal(hdr["dsn"], &gotDSN); err != nil || gotDSN != dsn {
		t.Errorf("envelope header dsn: got %q, want %q", gotDSN, dsn)
	}
	if want := strings.SplitN(string(raw), "\n", 2)[1]; items != want {
		t.Errorf("envelope items changed:\ngot  %q\nwant %q", items, want)
	}
}

func TestForwarder_dsnPathPrefix(t *testing.T) {
	// Sentry served under a sub-path: the prefix goes before /api/.
	srv, requests := recordingUpstream(t, http.StatusOK)
	forwardAndDrain(t, srv.Client(), buildDSN(srv, "k", "sentry/42"), []byte("{}\n"))
	if got := requests(); len(got) != 1 || got[0].path != "/sentry/api/42/envelope/" {
		t.Errorf("expected /sentry/api/42/envelope/, got %+v", got)
	}
}

func TestForwarder_upstreamErrorAndBadDSNsAreContained(t *testing.T) {
	srv, requests := recordingUpstream(t, http.StatusInternalServerError)
	forwardAndDrain(t, srv.Client(), buildDSN(srv, "k", "123"), []byte("{}\n"))
	if len(requests()) != 1 {
		t.Error("upstream should have been called once despite returning 500")
	}
	client := &http.Client{Timeout: time.Second}
	for _, dsn := range []string{"not a url ://garbage", "http://localhost:19999/123", "http://key@localhost:19999/"} {
		forwardAndDrain(t, client, dsn, []byte("{}\n")) // logs and returns, no panic
	}
}

func TestForwarder_dropsWhenQueueFull(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	dsn := buildDSN(srv, "k", "1")
	f := ingest.NewForwarder(srv.Client(), 1, 1)

	// One envelope in flight, one queued, the rest dropped.
	accepted := 0
	for range 5 {
		if f.Enqueue(dsn, []byte("{}\n")) {
			accepted++
		}
		time.Sleep(20 * time.Millisecond) // let the worker pick up the first one
	}
	if accepted != 2 || f.Dropped() != 3 {
		t.Errorf("accepted=%d dropped=%d, want 2 and 3", accepted, f.Dropped())
	}

	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	if f.Enqueue(dsn, []byte("{}\n")) {
		t.Error("a closed forwarder must refuse envelopes")
	}
}
