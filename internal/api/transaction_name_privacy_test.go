package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func TestEnvelopePersistsScrubbedTransactionName(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fields   []string
		patterns json.RawMessage
		want     string
	}{
		{name: "email pattern", fields: []string{}, patterns: json.RawMessage(`[{"name":"email","builtin":true,"enabled":true}]`), want: "GET /users/[Filtered]"},
		{name: "field rule", fields: []string{"TRANSACTION"}, patterns: json.RawMessage(`[]`), want: "[Filtered]"},
		{name: "custom pattern", fields: []string{}, patterns: json.RawMessage(`[{"name":"private-user","pattern":"private@[^/]+","enabled":true}]`), want: "GET /users/[Filtered]"},
		{name: "disabled rule", fields: []string{}, patterns: json.RawMessage(`[{"name":"email","builtin":true,"enabled":false}]`), want: "GET /users/private@example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := storage.CreateProject(t.Context(), testPool, uuid.NewString(), "Transaction privacy")
			require.NoError(t, err)
			_, err = storage.UpdateProjectScrubbing(t.Context(), testPool, p.ID, tc.fields, tc.patterns)
			require.NoError(t, err)
			txBuf := ingest.NewTransactionBuffer(10)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() { defer close(done); txBuf.Run(ctx, testPool) }()
			defer func() { cancel(); <-done }()
			h := api.NewRouter(testPool, nil, txBuf, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, false, nil)
			body := "{}\n{\"type\":\"transaction\"}\n" + `{"transaction":"GET /users/private@example.com","contexts":{"trace":{"trace_id":"1234567890abcdef1234567890abcdef","span_id":"1234567890abcdef"}},"start_timestamp":1704067200,"timestamp":1704067201}` + "\n"
			req := httptest.NewRequest(http.MethodPost, "/api/"+p.ID+"/envelope/", strings.NewReader(body))
			req.Header.Set("X-Sentry-Auth", sentryAuthHeader(p.PublicKey))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			cancel()
			<-done
			var name, traceID string
			require.NoError(t, testPool.QueryRow(t.Context(), `SELECT transaction, trace_id FROM transactions WHERE project_id=$1`, p.ID).Scan(&name, &traceID))
			require.Equal(t, tc.want, name)
			require.Equal(t, "1234567890abcdef1234567890abcdef", traceID)
			require.EqualValues(t, 1, txBuf.Stats().Persisted)
		})
	}
}
