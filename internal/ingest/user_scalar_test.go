package ingest

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJSONScalarString(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`"  alice  "`, "alice"}, {`"line\nbreak"`, "line\nbreak"},
		{`"unterminated`, ""}, {`null`, ""}, {``, ""}, {`42`, "42"},
	} {
		require.Equal(t, tc.want, jsonScalarString(json.RawMessage(tc.raw)), "raw=%q", tc.raw)
	}
}
