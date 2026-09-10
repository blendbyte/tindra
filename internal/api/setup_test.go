package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/sourcemaps"
	"github.com/blendbyte/tindra/internal/storage"
)

func setupProject(t *testing.T) *storage.Project {
	t.Helper()
	p, err := storage.CreateProject(t.Context(), testPool, "setup-"+uuid.NewString(), "Setup app")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, p.ID) })
	return p
}

func setupRequest(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSetupCheckConfirmsOnlyCommittedMatchingEvent(t *testing.T) {
	p := setupProject(t)
	h := authHandler()
	base := "/api/projects/" + p.Slug
	res := setupRequest(t, h, "POST", base+"/setup-checks")
	require.Equal(t, 201, res.Code, res.Body.String())
	var check storage.SetupCheck
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &check))
	status := func() struct {
		Check    *storage.SetupCheck    `json:"check"`
		Receipts []storage.SetupReceipt `json:"receipts"`
	} {
		t.Helper()
		rec := setupRequest(t, h, "GET", base+"/setup-status?check_id="+check.ID)
		require.Equal(t, 200, rec.Code, rec.Body.String())
		var result struct {
			Check    *storage.SetupCheck    `json:"check"`
			Receipts []storage.SetupReceipt `json:"receipts"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
		return result
	}
	require.Nil(t, status().Check.ReceivedAt)
	_, err := testPool.Exec(t.Context(), `INSERT INTO events(project_id,timestamp,payload) VALUES($1,now(),'{}')`, p.ID)
	require.NoError(t, err)
	require.Nil(t, status().Check.ReceivedAt, "unrelated events must not confirm the test")
	payload := fmt.Sprintf(`{"tags":{"tindra_setup":%q},"sdk":{"name":"sentry.javascript.node","version":"10"},"environment":"test"}`, check.ID)
	tx, err := testPool.Begin(t.Context())
	require.NoError(t, err)
	defer tx.Rollback(t.Context())
	var eventID string
	require.NoError(t, tx.QueryRow(t.Context(), `INSERT INTO events(project_id,timestamp,payload) VALUES($1,now(),$2) RETURNING id`, p.ID, payload).Scan(&eventID))
	require.Nil(t, status().Check.ReceivedAt, "uncommitted rows must not confirm the test")
	require.NoError(t, tx.Commit(t.Context()))
	require.NotNil(t, status().Check.ReceivedAt)
	require.Equal(t, eventID, *status().Check.EventID)
	_, err = testPool.Exec(t.Context(), `DELETE FROM events WHERE project_id=$1`, p.ID)
	require.NoError(t, err)
	after := status()
	require.NotNil(t, after.Check.ReceivedAt, "retention must not reset confirmation")
	require.Len(t, after.Receipts, 1)
	res = setupRequest(t, h, "GET", "/api/setup-status")
	require.Equal(t, 200, res.Code)
	var all []struct {
		ID       string `json:"id"`
		Complete bool   `json:"setup_complete"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &all))
	found := false
	for _, item := range all {
		if item.ID == p.ID {
			found = true
			require.True(t, item.Complete)
		}
	}
	require.True(t, found)
}

func TestSetupEnvelopePersistsTheSDKTestMarker(t *testing.T) {
	p := setupProject(t)
	check, err := storage.StartSetupCheck(t.Context(), testPool, p.ID)
	require.NoError(t, err)
	buf := ingest.NewBuffer(5)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); buf.Run(ctx, testPool) }()
	defer func() { cancel(); <-done }()
	h := api.NewRouter(testPool, buf, nil, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, false, nil)
	payload := fmt.Sprintf(`{"platform":"javascript","level":"error","tags":{"tindra_setup":%q},"exception":{"values":[{"type":"Error","value":"Hello, Tindra!"}]}}`, check.ID)
	req := httptest.NewRequest("POST", "/api/"+p.ID+"/envelope/", strings.NewReader(eventEnvelope(strings.ReplaceAll(uuid.NewString(), "-", ""), payload)))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(p.PublicKey))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Eventually(t, func() bool {
		stored, err := storage.GetSetupCheck(t.Context(), testPool, p.ID, check.ID)
		return err == nil && stored.ReceivedAt != nil && stored.EventID != nil
	}, 3*time.Second, 20*time.Millisecond)
}

func TestSetupTransactionAndProfileMilestonesSurviveRetention(t *testing.T) {
	p := setupProject(t)
	_, err := testPool.Exec(t.Context(), `INSERT INTO transactions(project_id,transaction,duration_ms,start_timestamp,timestamp,event_id) VALUES($1,'setup',10,now()-interval '1 second',now(),'sdk-event')`, p.ID)
	require.NoError(t, err)
	_, err = testPool.Exec(t.Context(), `INSERT INTO profile_chunks(project_id,format,transaction_event_id,start_ts,end_ts,sample_count,size_bytes,data) VALUES($1,1,'sdk-event',now()-interval '1 second',now(),1,1,'x')`, p.ID)
	require.NoError(t, err)
	res := setupRequest(t, authHandler(), "GET", "/api/projects/"+p.Slug+"/setup-status")
	require.Equal(t, 200, res.Code, res.Body.String())
	var status struct {
		ProfileTransactionID *string                `json:"profile_transaction_id"`
		Receipts             []storage.SetupReceipt `json:"receipts"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &status))
	require.Len(t, status.Receipts, 2)
	require.NotNil(t, status.ProfileTransactionID)
	_, err = testPool.Exec(t.Context(), `DELETE FROM transactions WHERE project_id=$1`, p.ID)
	require.NoError(t, err)
	_, err = testPool.Exec(t.Context(), `DELETE FROM profile_chunks WHERE project_id=$1`, p.ID)
	require.NoError(t, err)
	receipts, err := storage.ListSetupReceipts(t.Context(), testPool, p.ID)
	require.NoError(t, err)
	require.Len(t, receipts, 2)
}

func TestSetupChecksBoundedExpiredAndProjectScoped(t *testing.T) {
	p, other := setupProject(t), setupProject(t)
	for i := 0; i < 12; i++ {
		_, err := storage.StartSetupCheck(t.Context(), testPool, p.ID)
		require.NoError(t, err)
	}
	var n int
	require.NoError(t, testPool.QueryRow(t.Context(), `SELECT count(*) FROM project_setup_checks WHERE project_id=$1`, p.ID).Scan(&n))
	require.Equal(t, 10, n)
	check, err := storage.StartSetupCheck(t.Context(), testPool, p.ID)
	require.NoError(t, err)
	_, err = testPool.Exec(t.Context(), `UPDATE project_setup_checks SET expires_at=now()-interval '1 minute' WHERE id=$1`, check.ID)
	require.NoError(t, err)
	payload := fmt.Sprintf(`{"tags":{"tindra_setup":%q}}`, check.ID)
	_, err = testPool.Exec(t.Context(), `INSERT INTO events(project_id,timestamp,payload) VALUES($1,now(),$2)`, p.ID, payload)
	require.NoError(t, err)
	check, err = storage.GetSetupCheck(t.Context(), testPool, p.ID, check.ID)
	require.NoError(t, err)
	require.Nil(t, check.ReceivedAt)
	token := bearerToken(t, other.ID)
	h := authHandler()
	for _, path := range []string{"/setup-status", "/setup-sourcemaps?event_id=" + uuid.NewString()} {
		req := httptest.NewRequest("GET", "/api/projects/"+p.Slug+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		require.Equal(t, 403, res.Code)
	}
	res := setupRequest(t, h, "GET", "/api/projects/"+other.Slug+"/setup-status?check_id="+check.ID)
	require.Equal(t, 404, res.Code)
	res = setupRequest(t, h, "GET", "/api/projects/"+p.Slug+"/setup-status?check_id=bad")
	require.Equal(t, 400, res.Code)
	req := httptest.NewRequest("GET", "/api/setup-status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	require.Equal(t, 200, res.Code)
	var rows []struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &rows))
	require.Len(t, rows, 1)
	require.Equal(t, other.ID, rows[0].ID)
}

func TestSetupChecksRequireWritableBearerToken(t *testing.T) {
	p := setupProject(t)
	h := authHandler()
	for _, writable := range []bool{false, true} {
		_, token, err := storage.CreateAPIToken(t.Context(), testPool, p.ID, "setup-write-test", writable)
		require.NoError(t, err)
		req := httptest.NewRequest("POST", "/api/projects/"+p.Slug+"/setup-checks", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		want := http.StatusForbidden
		if writable {
			want = http.StatusCreated
		}
		require.Equal(t, want, rec.Code, rec.Body.String())
	}
	var count int
	require.NoError(t, testPool.QueryRow(t.Context(), `SELECT count(*) FROM project_setup_checks WHERE project_id=$1`, p.ID).Scan(&count))
	require.Equal(t, 1, count)
}

func TestSetupMixedEnvelopeReportsDroppedProfileAndQueuedError(t *testing.T) {
	p := setupProject(t)
	_, err := testPool.Exec(t.Context(), `UPDATE projects SET profiling_enabled=false WHERE id=$1`, p.ID)
	require.NoError(t, err)
	buf := ingest.NewBuffer(5)
	h := api.NewRouter(testPool, buf, nil, nil, ingest.NewProfileBuffer(5), nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, false, nil)
	body := eventEnvelope(uuid.NewString(), `{"message":"test"}`) + "{\"type\":\"profile\"}\n{}\n"
	req := httptest.NewRequest("POST", "/api/"+p.ID+"/envelope/", bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(p.PublicKey))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	obs, err := storage.ListSetupObservations(t.Context(), testPool, p.ID)
	require.NoError(t, err)
	require.Len(t, obs, 2)
	reasons := map[string]string{}
	for _, o := range obs {
		reasons[o.Kind] = o.Reason
	}
	require.Equal(t, "profiling_disabled", reasons["profile_chunks"])
	require.Equal(t, "queued", reasons["events"])
	receipts, err := storage.ListSetupReceipts(t.Context(), testPool, p.ID)
	require.NoError(t, err)
	require.Empty(t, receipts, "queued is not stored")
}

func TestSetupSourceMapsVerifyRealFramesAndExactEventLink(t *testing.T) {
	p, other := setupProject(t), setupProject(t)
	store := sourcemaps.NewStore(t.TempDir(), testPool)
	_, err := store.Upload(t.Context(), p.ID, "release-1", "~/app.js", strings.NewReader(`{"version":3,"sources":["src/app.ts"],"sourcesContent":["throw new Error('test')"],"mappings":"AAAA"}`))
	require.NoError(t, err)
	issue, _, _, err := storage.UpsertIssue(t.Context(), testPool, p.ID, "setup-link", "Setup event", "error", "error", "", "", time.Now())
	require.NoError(t, err)
	var eventID string
	require.NoError(t, testPool.QueryRow(t.Context(), `INSERT INTO events(project_id,issue_id,timestamp,payload) VALUES($1,$2,now(),$3) RETURNING id`, p.ID, issue.ID, `{"release":"release-1","platform":"javascript","exception":{"values":[{"stacktrace":{"frames":[{"abs_path":"https://app.test/app.js","lineno":1,"colno":0}]}}]}}`).Scan(&eventID))
	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, store, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, false, nil)
	res := setupRequest(t, h, "GET", "/api/projects/"+p.Slug+"/setup-sourcemaps?event_id="+eventID)
	require.Equal(t, 200, res.Code, res.Body.String())
	var verification sourcemaps.Verification
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &verification))
	require.Equal(t, "verified", verification.Status)
	require.Equal(t, "src/app.ts", verification.Frames[0].Source)
	res = setupRequest(t, h, "GET", "/api/projects/"+other.Slug+"/setup-sourcemaps?event_id="+eventID)
	require.Equal(t, 404, res.Code)
	_, err = testPool.Exec(t.Context(), `INSERT INTO events(project_id,issue_id,timestamp,payload) VALUES($1,$2,now(),'{}')`, p.ID, issue.ID)
	require.NoError(t, err)
	res = setupRequest(t, h, "GET", "/api/issues/"+issue.ID+"/events/latest?event_id="+eventID)
	require.Equal(t, 200, res.Code, res.Body.String())
	var exact struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &exact))
	require.Equal(t, eventID, exact.ID)
}

func TestSetupExactEventLinksValidateIDsAndPreserveTrace(t *testing.T) {
	p := setupProject(t)
	issue, _, _, err := storage.UpsertIssue(t.Context(), testPool, p.ID, "setup-trace", "Setup trace", "error", "error", "", "", time.Now())
	require.NoError(t, err)
	var eventID string
	require.NoError(t, testPool.QueryRow(t.Context(), `INSERT INTO events(project_id,issue_id,timestamp,trace_id,payload) VALUES($1,$2,now(),'setup-trace','{}') RETURNING id`, p.ID, issue.ID).Scan(&eventID))
	h := authHandler()
	for _, endpoint := range []string{"events/latest", "trace"} {
		base := "/api/issues/" + issue.ID + "/" + endpoint + "?event_id="
		require.Equal(t, 400, setupRequest(t, h, "GET", base+"bad").Code)
		wantMissing := 404
		if endpoint == "trace" {
			wantMissing = 200
		}
		require.Equal(t, wantMissing, setupRequest(t, h, "GET", base+uuid.NewString()).Code)
		require.Equal(t, 200, setupRequest(t, h, "GET", base+eventID).Code)
	}
}
