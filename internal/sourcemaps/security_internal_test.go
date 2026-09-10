package sourcemaps

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type sourceRoundTripFunc func(*http.Request) (*http.Response, error)

func (f sourceRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSourceEnrichmentBlocksInternalDestinations(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "private-secret")
	}))
	defer server.Close()
	for _, target := range []string{server.URL, strings.Replace(server.URL, "127.0.0.1", "localhost", 1), "http://169.254.169.254/", "http://10.0.0.1/", "http://[::1]/", "http://[64:ff9b::169.254.169.254]/"} {
		t.Run(target, func(t *testing.T) {
			store := NewStore(t.TempDir(), nil)
			payload := json.RawMessage(fmt.Sprintf(`{"exception":{"values":[{"stacktrace":{"frames":[{"abs_path":%q,"lineno":1}]}}]}}`, target))
			enriched := store.ResolveEventPayload(t.Context(), "", "", payload)
			require.NotContains(t, string(enriched), "context_line")
			require.NotContains(t, string(enriched), "private-secret")
			require.Zero(t, hits.Load())
			// Check the actual client failure rather than accepting any failed fetch.
			resp, err := store.httpClient.Get(target)
			if resp != nil {
				resp.Body.Close()
			}
			require.ErrorContains(t, err, "private address")
		})
	}
}

func TestSourceEnrichmentRechecksRedirectDestination(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "private-secret")
	}))
	defer server.Close()
	store := NewStore(t.TempDir(), nil)
	protected := store.httpClient.Transport
	// Simulate a public server response without depending on an external host.
	// Redirected requests still go through the real production transport.
	store.httpClient.Transport = sourceRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "public.example" {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{server.URL}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return protected.RoundTrip(r)
	})
	lines := store.fetchLines(t.Context(), "https://public.example/app.js")
	require.Nil(t, lines)
	require.Zero(t, hits.Load())
}
