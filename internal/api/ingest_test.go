package api_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
)

var (
	testPool    *pgxpool.Pool
	testProject *storage.Project
	testUser    *storage.User
	testSession *storage.Session
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	pool, cleanup := testutil.SetupDB(ctx)

	project, err := storage.CreateProject(ctx, pool, "test-project", "Test Project")
	if err != nil {
		log.Fatalf("create test project: %v", err)
	}

	user, err := storage.CreateUser(ctx, pool, "test@example.com", "testpassword")
	if err != nil {
		log.Fatalf("create test user: %v", err)
	}

	session, err := storage.CreateSession(ctx, pool, user.ID)
	if err != nil {
		log.Fatalf("create test session: %v", err)
	}

	testPool = pool
	testProject = project
	testUser = user
	testSession = session

	code := m.Run()
	cleanup()
	os.Exit(code)
}

func truncateEvents(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE events CASCADE"); err != nil {
		t.Fatalf("truncate events: %v", err)
	}
}

func newHandler(buf *ingest.Buffer) http.Handler {
	return api.NewRouter(testPool, buf, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
}

func eventEnvelope(eventID, payload string) string {
	header := fmt.Sprintf(`{"event_id":%q}`, eventID)
	itemHeader := fmt.Sprintf(`{"type":"event","length":%d}`, len(payload))
	return header + "\n" + itemHeader + "\n" + payload + "\n"
}

func sentryAuthHeader(key string) string {
	return "Sentry sentry_version=7, sentry_key=" + key
}

func TestHandleEnvelope_validEvent(t *testing.T) {
	truncateEvents(t)

	buf := ingest.NewBuffer(100)
	ctx := t.Context()
	go buf.Run(ctx, testPool)

	payload := `{"timestamp":"2024-01-01T00:00:00Z","level":"error","message":"test"}`
	body := eventEnvelope("550e8400e29b41d4a716446655440000", payload)

	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	newHandler(buf).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	time.Sleep(350 * time.Millisecond) // wait for 200ms ticker flush

	var count int
	if err := testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM events WHERE project_id = $1", testProject.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 event in DB, got %d", count)
	}
}

func TestHandleEnvelope_deduplicatesOnRetry(t *testing.T) {
	truncateEvents(t)

	buf := ingest.NewBuffer(100)
	ctx := t.Context()
	go buf.Run(ctx, testPool)

	payload := `{"timestamp":"2024-01-01T00:00:00Z","level":"error"}`
	body := eventEnvelope("dedup-event-id-0001", payload)

	handler := newHandler(buf)
	for range 2 { // send same envelope twice (SDK retry scenario)
		req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
			bytes.NewBufferString(body))
		req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	}

	// Poll until the batch is committed or the deadline is exceeded.
	// A fixed sleep is too brittle under -race and -p 8 CI load.
	deadline := time.Now().Add(5 * time.Second)
	var count int
	for time.Now().Before(deadline) {
		testPool.QueryRow(context.Background(),
			"SELECT COUNT(*) FROM events WHERE project_id = $1", testProject.ID,
		).Scan(&count)
		if count > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if count != 1 {
		t.Errorf("expected 1 event after dedup, got %d", count)
	}
}

func TestHandleEnvelope_invalidKey(t *testing.T) {
	buf := ingest.NewBuffer(100)
	body := eventEnvelope("abc", `{"timestamp":"2024-01-01T00:00:00Z"}`)

	// Valid project ID, wrong public key - both must match.
	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader("wrongkey"))
	rec := httptest.NewRecorder()
	newHandler(buf).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleEnvelope_missingKey(t *testing.T) {
	buf := ingest.NewBuffer(100)
	body := eventEnvelope("abc", `{"timestamp":"2024-01-01T00:00:00Z"}`)

	// No X-Sentry-Auth header at all.
	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	newHandler(buf).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleEnvelope_malformedEnvelope(t *testing.T) {
	buf := ingest.NewBuffer(100)

	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString("not json at all\n"))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	newHandler(buf).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleEnvelope_gzip(t *testing.T) {
	truncateEvents(t)

	buf := ingest.NewBuffer(100)
	ctx := t.Context()
	go buf.Run(ctx, testPool)

	payload := `{"timestamp":"2024-01-01T00:00:00Z","level":"info"}`
	envelope := eventEnvelope("660e8400e29b41d4a716446655440000", payload)

	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write([]byte(envelope))
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/", &gz)
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	newHandler(buf).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	time.Sleep(350 * time.Millisecond)

	var count int
	testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM events WHERE project_id = $1", testProject.ID,
	).Scan(&count)
	if count < 1 {
		t.Errorf("expected event in DB after gzip ingest, got %d", count)
	}
}

func TestHandleEnvelope_bufferFull(t *testing.T) {
	buf := ingest.NewBuffer(1)
	buf.Push(ingest.BufferedEvent{ // pre-fill
		ProjectID: testProject.ID,
		Timestamp: time.Now(),
		Payload:   json.RawMessage(`{"level":"error"}`),
	})

	payload := `{"timestamp":"2024-01-01T00:00:00Z","level":"error"}`
	body := eventEnvelope("770e8400e29b41d4a716446655440000", payload)

	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	newHandler(buf).ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "1" {
		t.Errorf("expected Retry-After: 1, got %q", rec.Header().Get("Retry-After"))
	}
}

func TestHandleEnvelope_nonEventItemsIgnored(t *testing.T) {
	truncateEvents(t)

	buf := ingest.NewBuffer(100)

	// Envelope with only a session item - no events should be stored
	body := `{"event_id":"abc"}` + "\n" +
		`{"type":"session"}` + "\n" +
		`{"sid":"123","status":"ok"}` + "\n"

	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	newHandler(buf).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	time.Sleep(350 * time.Millisecond)

	var count int
	testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM events WHERE project_id = $1", testProject.ID,
	).Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 events for session-only envelope, got %d", count)
	}
}

// --- parseLogs via log envelope item ---

func logEnvelope(payload string) string {
	header := `{"event_id":"log-test-event-id-0001"}`
	itemHeader := fmt.Sprintf(`{"type":"log","length":%d}`, len(payload))
	return header + "\n" + itemHeader + "\n" + payload + "\n"
}

// sdkLogEnvelope mirrors what current SDKs put on the wire: item_count and
// content_type headers, no length, payload on a single line.
func sdkLogEnvelope(payload string, count int) string {
	header := `{"event_id":"log-test-event-id-0002"}`
	itemHeader := fmt.Sprintf(
		`{"type":"log","item_count":%d,"content_type":"application/vnd.sentry.items.log+json"}`, count)
	return header + "\n" + itemHeader + "\n" + payload + "\n"
}

// postLogEnvelope sends an envelope through a router wired to a running log
// buffer and returns the response recorder.
func postLogEnvelope(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postLogEnvelopeAs(t, testProject.ID, testProject.PublicKey, body)
}

func postLogEnvelopeAs(t *testing.T, projectID, publicKey, body string) *httptest.ResponseRecorder {
	t.Helper()

	logBuf := ingest.NewLogBuffer(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go logBuf.Run(ctx, testPool)

	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, logBuf, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/"+projectID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(publicKey))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// waitForLog polls for a log row with the given body. The buffer flushes on a
// 200ms ticker, so a short poll beats a fixed sleep.
func waitForLog(t *testing.T, body string) *storage.Log {
	t.Helper()
	return waitForProjectLog(t, testProject.ID, body)
}

func waitForProjectLog(t *testing.T, projectID, body string) *storage.Log {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		logs, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
			ProjectIDs: []string{projectID},
			Search:     body,
		})
		if err != nil {
			t.Fatalf("list logs: %v", err)
		}
		for _, l := range logs {
			if l.Body == body {
				return l
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("log with body %q never landed in the database", body)
	return nil
}

func TestHandleEnvelope_logItem_itemsWrapper(t *testing.T) {
	// The payload shape every current SDK sends: an object wrapping the batch
	// under "items", with typed attribute values.
	payload := `{"version":2,"ingest_settings":{"infer_ip":"auto"},"items":[` +
		`{"timestamp":1700000002.0,"trace_id":"5b8efff798038103d269b633813fc60c",` +
		`"span_id":"b0e6f15b45c36b12","level":"warn","body":"items wrapper log",` +
		`"severity_number":13,"attributes":{` +
		`"sentry.environment":{"value":"production","type":"string"},` +
		`"sentry.release":{"value":"4.5.6","type":"string"},` +
		`"user.id":{"value":42,"type":"integer"}}}]}`

	rec := postLogEnvelope(t, sdkLogEnvelope(payload, 1))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	l := waitForLog(t, "items wrapper log")

	if l.Level != "warning" {
		t.Errorf("level: expected warn to be stored as warning, got %q", l.Level)
	}
	if l.TraceID == nil || *l.TraceID != "5b8efff798038103d269b633813fc60c" {
		t.Errorf("trace id: got %v", l.TraceID)
	}
	if l.SpanID == nil || *l.SpanID != "b0e6f15b45c36b12" {
		t.Errorf("span id: got %v", l.SpanID)
	}
	if l.Environment == nil || *l.Environment != "production" {
		t.Errorf("environment: expected it to come from the typed attribute, got %v", l.Environment)
	}
	if l.Release == nil || *l.Release != "4.5.6" {
		t.Errorf("release: expected it to come from the typed attribute, got %v", l.Release)
	}
	if !l.Timestamp.Equal(time.Unix(1700000002, 0).UTC()) {
		t.Errorf("timestamp: got %v", l.Timestamp)
	}

	var attrs map[string]any
	if err := json.Unmarshal(l.Attributes, &attrs); err != nil {
		t.Fatalf("unmarshal attributes: %v", err)
	}
	if attrs["sentry.environment"] != "production" {
		t.Errorf("attributes: expected flattened value, got %#v", attrs["sentry.environment"])
	}
	if attrs["user.id"] != float64(42) {
		t.Errorf("attributes: expected flattened number, got %#v", attrs["user.id"])
	}
}

func TestHandleEnvelope_logItem_itemsWrapperBatch(t *testing.T) {
	payload := `{"version":2,"items":[` +
		`{"timestamp":1700000003.0,"level":"info","body":"batched log one"},` +
		`{"timestamp":1700000004.0,"level":"error","body":"batched log two"}]}`

	rec := postLogEnvelope(t, sdkLogEnvelope(payload, 2))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if l := waitForLog(t, "batched log one"); l.Level != "info" {
		t.Errorf("level: got %q", l.Level)
	}
	if l := waitForLog(t, "batched log two"); l.Level != "error" {
		t.Errorf("level: got %q", l.Level)
	}
}

// TestHandleEnvelope_logItem_scrubbed verifies that a project's scrub settings
// reach log bodies and attributes, the way they already reach events.
func TestHandleEnvelope_logItem_scrubbed(t *testing.T) {
	ctx := context.Background()

	p, err := storage.CreateProject(ctx, testPool, "log-scrub-proj", "Log Scrub Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	patterns := json.RawMessage(`[{"name":"email","builtin":true,"enabled":true}]`)
	if _, err := storage.UpdateProjectScrubbing(ctx, testPool, p.ID,
		[]string{"user.password"}, patterns); err != nil {
		t.Fatalf("update scrubbing: %v", err)
	}

	payload := `{"version":2,"items":[{"timestamp":1700000005.0,"level":"info",` +
		`"body":"password reset for alice@example.com","attributes":{` +
		`"user.password":{"value":"hunter2","type":"string"},` +
		`"sentry.message.parameter.0":{"value":"alice@example.com","type":"string"},` +
		`"user.id":{"value":"u-1","type":"string"}}}]}`

	rec := postLogEnvelopeAs(t, p.ID, p.PublicKey, sdkLogEnvelope(payload, 1))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	l := waitForProjectLog(t, p.ID, "password reset for [Filtered]")

	var attrs map[string]any
	if err := json.Unmarshal(l.Attributes, &attrs); err != nil {
		t.Fatalf("unmarshal attributes: %v", err)
	}
	if attrs["user.password"] != "[Filtered]" {
		t.Errorf("expected blocked attribute redacted, got %#v", attrs["user.password"])
	}
	if attrs["sentry.message.parameter.0"] != "[Filtered]" {
		t.Errorf("expected email attribute redacted, got %#v", attrs["sentry.message.parameter.0"])
	}
	if attrs["user.id"] != "u-1" {
		t.Errorf("expected unrelated attribute untouched, got %#v", attrs["user.id"])
	}
}

func TestHandleEnvelope_logItem(t *testing.T) {
	payload := `[{"timestamp":1700000000.0,"level":"info","body":"hello from log","trace_id":"abc123","span_id":"def456","attributes":{"sentry.environment":"test"}}]`

	rec := postLogEnvelope(t, logEnvelope(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	l := waitForLog(t, "hello from log")
	if l.Level != "info" {
		t.Errorf("level: got %q", l.Level)
	}
	if l.Environment == nil || *l.Environment != "test" {
		t.Errorf("environment: got %v", l.Environment)
	}
}

func TestHandleEnvelope_logItem_singleObject(t *testing.T) {
	// Single object (not array) format.
	payload := `{"timestamp":1700000001.0,"level":"warn","body":"single log object"}`

	rec := postLogEnvelope(t, logEnvelope(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if l := waitForLog(t, "single log object"); l.Level != "warning" {
		t.Errorf("level: expected warn to be stored as warning, got %q", l.Level)
	}
}

// --- handleEnvelopeCheckin ---

func checkinEnvelope(payload string) string {
	header := `{"event_id":"checkin-test-0001"}`
	itemHeader := fmt.Sprintf(`{"type":"check_in","length":%d}`, len(payload))
	return header + "\n" + itemHeader + "\n" + payload + "\n"
}

func TestHandleEnvelope_checkin_inProgress(t *testing.T) {
	m := createTestMonitor(t, "test-checkin-monitor", "0 * * * *")

	payload := fmt.Sprintf(`{"check_in_id":"ci-001","monitor_slug":%q,"status":"in_progress"}`, m.ID)
	body := checkinEnvelope(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	newHandler(ingest.NewBuffer(1)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleEnvelope_checkin_ok(t *testing.T) {
	m := createTestMonitor(t, "test-checkin-ok", "0 * * * *")
	dur := 1.5

	payload := fmt.Sprintf(`{"check_in_id":"ci-ok-001","monitor_slug":%q,"status":"ok","duration":%v}`, m.ID, dur)
	body := checkinEnvelope(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	newHandler(ingest.NewBuffer(1)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleEnvelope_checkin_unknownMonitor(t *testing.T) {
	payload := `{"check_in_id":"ci-unk","monitor_slug":"00000000-0000-0000-0000-000000000000","status":"ok"}`
	body := checkinEnvelope(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	newHandler(ingest.NewBuffer(1)).ServeHTTP(rec, req)

	// Unknown monitor is silently ignored; the envelope still returns 200.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}
