package ingest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvelopeHeaderMustBeObject(t *testing.T) {
	const dsn = "https://public@example.invalid/1"
	for _, header := range []string{"null", " \t null \r", "[]", `"header"`, "42", "true", "false", "", "{"} {
		t.Run(header, func(t *testing.T) {
			raw := []byte(header + "\n")
			require.NotPanics(t, func() {
				_, items, err := Parse(bytes.NewReader(raw))
				require.Error(t, err)
				require.Empty(t, items)
				rewritten, err := rewriteEnvelopeDSN(raw, dsn)
				require.Error(t, err)
				require.Nil(t, rewritten)
				// Run synchronously so a regression is recovered safely by this test.
				// The transport must never be reached for a rejected header.
				client := &http.Client{Transport: rejectedHeaderTransport{t}}
				require.Error(t, forwardEnvelope(t.Context(), client, dsn, raw))
			})
		})
	}
}

type rejectedHeaderTransport struct{ t *testing.T }

func (r rejectedHeaderTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.t.Fatal("invalid header reached outbound transport")
	return nil, nil
}

func TestRewriteEnvelopeObjectHeaders(t *testing.T) {
	const dsn = "https://public@example.invalid/1"
	const items = "{\"type\":\"event\",\"length\":2}\n{}\n"
	for _, header := range []string{"{}", " \t {} \r", `{"event_id":"abc","extra":{"keep":true},"dsn":"old"}`} {
		t.Run(header, func(t *testing.T) {
			raw := []byte(header + "\n" + items)
			_, parsed, err := Parse(bytes.NewReader(raw))
			require.NoError(t, err)
			require.Len(t, parsed, 1)
			rewritten, err := rewriteEnvelopeDSN(raw, dsn)
			require.NoError(t, err)
			line, rest, found := strings.Cut(string(rewritten), "\n")
			require.True(t, found)
			require.Equal(t, items, rest)
			var expected, actual map[string]any
			require.NoError(t, json.Unmarshal([]byte(header), &expected))
			require.NoError(t, json.Unmarshal([]byte(line), &actual))
			expected["dsn"] = dsn
			require.Equal(t, expected, actual)
		})
	}
}

func TestRewriteEnvelopeHeaderWithoutNewline(t *testing.T) {
	const dsn = "https://public@example.invalid/1"
	for _, raw := range []string{"", "null", "{}"} {
		t.Run(raw, func(t *testing.T) {
			require.NotPanics(t, func() {
				rewritten, err := rewriteEnvelopeDSN([]byte(raw), dsn)
				if raw != "{}" {
					require.Error(t, err)
					require.Nil(t, rewritten)
					return
				}
				require.NoError(t, err)
				var header map[string]string
				require.NoError(t, json.Unmarshal(bytes.TrimSpace(rewritten), &header))
				require.Equal(t, map[string]string{"dsn": dsn}, header)
			})
		})
	}
}

func TestUpstreamEndpoint(t *testing.T) {
	for dsn, want := range map[string]string{
		"https://key@o1.ingest.sentry.io/42":      "https://o1.ingest.sentry.io/api/42/envelope/",
		"https://key@sentry.example.com/sentry/7": "https://sentry.example.com/sentry/api/7/envelope/",
		"http://key@host:9000/a/b/9/":             "http://host:9000/a/b/api/9/envelope/",
	} {
		got, key, err := upstreamEndpoint(dsn)
		require.NoError(t, err, dsn)
		require.Equal(t, want, got, dsn)
		require.Equal(t, "key", key)
	}
}

func TestEncodeEnvelopeRewritesLengths(t *testing.T) {
	raw := "{\"event_id\":\"e\"}\n" +
		"{\"type\":\"log\",\"length\":12,\"item_count\":1,\"content_type\":\"application/vnd.sentry.items.log+json\"}\n" +
		"{\"items\":[]}\n"
	header, items, err := Parse(strings.NewReader(raw))
	require.NoError(t, err)
	items[0].Payload = []byte(`{"items":[{"body":"[Filtered]"}]}`)
	out, err := EncodeEnvelope(header.Raw, items)
	require.NoError(t, err)

	_, reparsed, err := Parse(bytes.NewReader(out))
	require.NoError(t, err)
	require.Len(t, reparsed, 1)
	require.Equal(t, `{"items":[{"body":"[Filtered]"}]}`, string(reparsed[0].Payload))
	var fields map[string]any
	require.NoError(t, json.Unmarshal(reparsed[0].Header.Raw, &fields))
	require.Equal(t, "application/vnd.sentry.items.log+json", fields["content_type"], "other item header fields are kept")
	require.EqualValues(t, len(reparsed[0].Payload), fields["length"])
}
