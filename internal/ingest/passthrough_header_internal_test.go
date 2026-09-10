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
				require.Error(t, forwardEnvelope(client, dsn, raw))
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
