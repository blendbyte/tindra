package api

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
)

func TestSetupReportsEnvelopeRejections(t *testing.T) {
	ctx := t.Context()
	pool, cleanup := testutil.SetupDB(ctx)
	defer cleanup()
	p, err := storage.CreateProject(ctx, pool, "setup-rejected", "Setup")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO events(project_id,timestamp,payload) VALUES($1,now(),'{}')`, p.ID)
	require.NoError(t, err)
	profile, err := os.ReadFile("../ingest/testdata/profiles/v1_php_laravel.json")
	require.NoError(t, err)
	for _, tc := range []struct {
		name, kind, payload, reason string
		status                      int
	}{
		{"monthly limit", "event", "{}", "event_limit", 429},
		{"transactions unavailable", "transaction", "{}", "unavailable", 200},
		{"invalid transaction", "transaction", `{"transaction":{}}`, "invalid_transaction", 200},
		{"transaction backpressure", "transaction", `{"transaction":"setup"}`, "buffer_full", 429},
		{"encoding failure", "profile", string(profile), "profile_encoding_failed", 200},
		{"diagnostics unavailable", "event", "{}", "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, `DELETE FROM project_setup_observations`)
			require.NoError(t, err)
			ro := &router{pool: pool, buf: ingest.NewBuffer(5), txBuf: ingest.NewTransactionBuffer(1), profBuf: ingest.NewProfileBuffer(1), encodeProfile: ingest.NewBufferedProfile}
			if tc.name == "transactions unavailable" {
				ro.txBuf = nil
			}
			if tc.name == "monthly limit" {
				ro.eventLimit.Store(1)
			}
			if tc.name == "transaction backpressure" {
				require.True(t, ro.txBuf.Push(ingest.BufferedTransaction{ProjectID: p.ID}))
			}
			if tc.name == "encoding failure" {
				ro.encodeProfile = func(id string, profile *ingest.Profile) (ingest.BufferedProfile, error) {
					require.Equal(t, p.ID, id)
					require.NotEmpty(t, profile.Samples)
					return ingest.BufferedProfile{}, errors.New("encoder unavailable")
				}
			}
			var trace *failSetupQuery
			if tc.name == "diagnostics unavailable" {
				trace = &failSetupQuery{match: "INSERT INTO project_setup_observations"}
				cfg := pool.Config()
				cfg.ConnConfig.Tracer = trace
				failing, err := pgxpool.NewWithConfig(ctx, cfg)
				require.NoError(t, err)
				defer failing.Close()
				ro.pool = failing
			}
			body := fmt.Sprintf("{}\n{\"type\":%q,\"length\":%d}\n%s\n", tc.kind, len(tc.payload), tc.payload)
			req := httptest.NewRequest("POST", "/", strings.NewReader(body))
			req.Header.Set("X-Sentry-Auth", "Sentry sentry_version=7, sentry_key="+p.PublicKey)
			rec := httptest.NewRecorder()
			ro.handleEnvelope(rec, req)
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			obs, err := storage.ListSetupObservations(ctx, pool, p.ID)
			require.NoError(t, err)
			if trace != nil {
				require.Greater(t, trace.hits, 0)
				require.Empty(t, obs)
			} else {
				require.Len(t, obs, 1)
				require.Equal(t, tc.reason, obs[0].Reason)
				require.WithinDuration(t, time.Now(), obs[0].ObservedAt, time.Second)
			}
		})
	}
}
