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
	"path/filepath"
	"strings"
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
	return api.NewRouter(testPool, buf, nil, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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

	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, logBuf, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)

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

// A profile item rides in the same envelope as its transaction and dwarfs an
// error event. Before the decompressed cap was raised, gzipped envelopes like
// this were truncated mid-item and rejected as malformed, silently taking the
// transaction down with the profile.
func TestHandleEnvelope_gzipLargeProfileItem(t *testing.T) {
	buf := ingest.NewBuffer(100)
	txBuf := ingest.NewTransactionBuffer(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go txBuf.Run(ctx, testPool)

	const txName = "/profiling/large-envelope"
	txPayload := fmt.Sprintf(`{"transaction":%q,"start_timestamp":"2024-01-01T00:00:00Z","timestamp":"2024-01-01T00:00:01Z","contexts":{"trace":{"trace_id":"aa","span_id":"bb"}}}`, txName)

	// ~4 MB, comfortably past the old 1 MB decompressed cap.
	profilePayload := fmt.Sprintf(`{"version":"1","platform":"php","padding":%q}`,
		strings.Repeat("frame", 800_000))

	body := `{"event_id":"770e8400e29b41d4a716446655440000"}` + "\n" +
		fmt.Sprintf(`{"type":"transaction","length":%d}`, len(txPayload)) + "\n" +
		txPayload + "\n" +
		fmt.Sprintf(`{"type":"profile","length":%d}`, len(profilePayload)) + "\n" +
		profilePayload + "\n"

	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write([]byte(body))
	w.Close()

	h := api.NewRouter(testPool, buf, txBuf, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/", &gz)
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	time.Sleep(350 * time.Millisecond)

	// The profile itself is still discarded, but the transaction must survive.
	var count int
	testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM transactions WHERE project_id = $1 AND transaction = $2",
		testProject.ID, txName,
	).Scan(&count)
	if count < 1 {
		t.Errorf("expected transaction to be stored alongside the oversized profile item, got %d", count)
	}
}

func TestHandleEnvelope_tooLarge(t *testing.T) {
	buf := ingest.NewBuffer(100)

	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	// Highly compressible, so the raw body stays small while the decompressed
	// stream runs past maxGzipBodyBytes.
	_, _ = w.Write(bytes.Repeat([]byte("a"), 21*1024*1024))
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/", &gz)
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	newHandler(buf).ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 for oversized envelope, got %d", rec.Code)
	}
}

// --- profiling ---

func profileEnvelope(t *testing.T, itemType, fixtureFile string) string {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "ingest", "testdata", "profiles", fixtureFile))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	compact := &bytes.Buffer{}
	if err := json.Compact(compact, payload); err != nil {
		t.Fatalf("compact fixture: %v", err)
	}
	body := compact.String()
	return `{"event_id":"990e8400e29b41d4a716446655440000"}` + "\n" +
		fmt.Sprintf(`{"type":%q,"length":%d}`, itemType, len(body)) + "\n" +
		body + "\n"
}

func newProfileHandler(profBuf *ingest.ProfileBuffer) http.Handler {
	return api.NewRouter(testPool, ingest.NewBuffer(10), nil, nil, profBuf, nil, nil,
		false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
}

func postEnvelope(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandleEnvelope_profileItems(t *testing.T) {
	tests := []struct {
		name     string
		itemType string
		fixture  string
		format   int16
	}{
		{"v1 transaction profile", "profile", "v1_php_laravel.json", 1},
		{"v2 continuous chunk", "profile_chunk", "v2_python_chunk.json", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := testPool.Exec(context.Background(),
				"DELETE FROM profile_chunks WHERE project_id = $1", testProject.ID); err != nil {
				t.Fatalf("clear profiles: %v", err)
			}

			profBuf := ingest.NewProfileBuffer(10)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go profBuf.Run(ctx, testPool)

			rec := postEnvelope(t, newProfileHandler(profBuf), profileEnvelope(t, tt.itemType, tt.fixture))
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}

			time.Sleep(400 * time.Millisecond)

			var format int16
			var sampleCount int
			err := testPool.QueryRow(context.Background(),
				"SELECT format, sample_count FROM profile_chunks WHERE project_id = $1",
				testProject.ID).Scan(&format, &sampleCount)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if format != tt.format {
				t.Errorf("format = %d, want %d", format, tt.format)
			}
			if sampleCount == 0 {
				t.Error("sample_count should be non-zero")
			}
		})
	}
}

// A profile that fails validation must not take its envelope down with it.
func TestHandleEnvelope_malformedProfileIsDroppedAlone(t *testing.T) {
	if _, err := testPool.Exec(context.Background(),
		"DELETE FROM profile_chunks WHERE project_id = $1", testProject.ID); err != nil {
		t.Fatalf("clear profiles: %v", err)
	}

	profBuf := ingest.NewProfileBuffer(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go profBuf.Run(ctx, testPool)

	// Stack id points nowhere, so the parser rejects it.
	bad := `{"version":"1","platform":"php","timestamp":"2026-08-24T10:00:00Z",` +
		`"event_id":"aa","transaction":{"id":"bb","name":"GET /","trace_id":"cc"},` +
		`"profile":{"frames":[],"stacks":[],"samples":[]}}`
	body := `{"event_id":"aa0e8400e29b41d4a716446655440000"}` + "\n" +
		fmt.Sprintf(`{"type":"profile","length":%d}`, len(bad)) + "\n" + bad + "\n"

	rec := postEnvelope(t, newProfileHandler(profBuf), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for a malformed profile, got %d: %s", rec.Code, rec.Body.String())
	}

	time.Sleep(300 * time.Millisecond)

	var count int
	if err := testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM profile_chunks WHERE project_id = $1", testProject.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("stored %d profiles, want the malformed one discarded", count)
	}
}

func TestHandleEnvelope_profilingDisabledForProject(t *testing.T) {
	if _, err := testPool.Exec(context.Background(),
		"DELETE FROM profile_chunks WHERE project_id = $1", testProject.ID); err != nil {
		t.Fatalf("clear profiles: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		"UPDATE projects SET profiling_enabled = FALSE WHERE id = $1", testProject.ID); err != nil {
		t.Fatalf("disable profiling: %v", err)
	}
	defer func() {
		_, _ = testPool.Exec(context.Background(),
			"UPDATE projects SET profiling_enabled = TRUE WHERE id = $1", testProject.ID)
	}()

	profBuf := ingest.NewProfileBuffer(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go profBuf.Run(ctx, testPool)

	rec := postEnvelope(t, newProfileHandler(profBuf),
		profileEnvelope(t, "profile", "v1_php_laravel.json"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	time.Sleep(300 * time.Millisecond)

	var count int
	if err := testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM profile_chunks WHERE project_id = $1", testProject.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("stored %d profiles, want none while the project has profiling off", count)
	}
}

// The v2 link lives on the transaction, so those three columns have to survive
// parsing or a chunk can never be found again.
func TestHandleEnvelope_transactionCarriesProfileLink(t *testing.T) {
	buf := ingest.NewBuffer(10)
	txBuf := ingest.NewTransactionBuffer(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go txBuf.Run(ctx, testPool)

	payload, err := os.ReadFile(filepath.Join("..", "ingest", "testdata", "profiles",
		"v2_transaction_link.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	compact := &bytes.Buffer{}
	if err := json.Compact(compact, payload); err != nil {
		t.Fatalf("compact: %v", err)
	}
	body := `{"event_id":"bb0e8400e29b41d4a716446655440000"}` + "\n" +
		fmt.Sprintf(`{"type":"transaction","length":%d}`, compact.Len()) + "\n" +
		compact.String() + "\n"

	h := api.NewRouter(testPool, buf, txBuf, nil, nil, nil, nil, false, "", "", "", "",
		0, 0, 0, 0, 0, 0, nil, false, true, nil)
	rec := postEnvelope(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	time.Sleep(400 * time.Millisecond)

	var eventID, profilerID, threadID string
	err = testPool.QueryRow(context.Background(), `
		SELECT event_id, profiler_id, thread_id FROM transactions
		WHERE project_id = $1 AND transaction = 'GET /api/orders'
		ORDER BY received_at DESC LIMIT 1`, testProject.ID,
	).Scan(&eventID, &profilerID, &threadID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	if eventID != "5e6f70819a2b3c4d5e6f70819a2b3c4d" {
		t.Errorf("event_id = %q", eventID)
	}
	if profilerID != "4d229f1d3807421ba62a5f8bc295d836" {
		t.Errorf("profiler_id = %q, want the value from contexts.profile", profilerID)
	}
	if threadID != "8412331008" {
		t.Errorf(`thread_id = %q, want the value from contexts.trace.data["thread.id"]`, threadID)
	}
}

// --- flame graph endpoint ---

// The endpoint has to fold a stored profile into a tree the UI can draw, and
// distinguish "no profile" from a failure: most transactions have no profile,
// either sampled out by the SDK or turned off for the project.
func TestHandleGetTransactionFlameGraph(t *testing.T) {
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, "TRUNCATE profile_chunks CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	profBuf := ingest.NewProfileBuffer(10)
	txBuf := ingest.NewTransactionBuffer(10)
	bufCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go profBuf.Run(bufCtx, testPool)
	go txBuf.Run(bufCtx, testPool)

	h := api.NewRouter(testPool, ingest.NewBuffer(10), txBuf, nil, profBuf, nil, nil,
		false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)

	// Ingest the profile and a transaction naming it, exactly as an SDK would.
	rec := postEnvelope(t, h, profileEnvelope(t, "profile", "v1_php_laravel.json"))
	if rec.Code != http.StatusOK {
		t.Fatalf("profile ingest: %d %s", rec.Code, rec.Body.String())
	}

	txPayload := `{"event_id":"3c2b1a09f8e7d6c5b4a39281706f5e4d","transaction":"GET /api/orders",` +
		`"start_timestamp":"2026-08-24T10:15:32.123Z","timestamp":"2026-08-24T10:15:32.223Z",` +
		`"contexts":{"trace":{"trace_id":"9b8a7c6d5e4f3a2b1c0d9e8f7a6b5c4d","span_id":"aabbccdd",` +
		`"op":"http.server","status":"ok"}}}`
	body := `{"event_id":"cc0e8400e29b41d4a716446655440000"}` + "\n" +
		fmt.Sprintf(`{"type":"transaction","length":%d}`, len(txPayload)) + "\n" + txPayload + "\n"
	if rec := postEnvelope(t, h, body); rec.Code != http.StatusOK {
		t.Fatalf("transaction ingest: %d %s", rec.Code, rec.Body.String())
	}

	time.Sleep(500 * time.Millisecond)

	var txID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM transactions
		WHERE project_id = $1 AND event_id = '3c2b1a09f8e7d6c5b4a39281706f5e4d'
		ORDER BY received_at DESC LIMIT 1`, testProject.ID).Scan(&txID); err != nil {
		t.Fatalf("find transaction: %v", err)
	}

	t.Run("returns the folded tree", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/transactions/"+txID+"/flamegraph", nil)
		req.AddCookie(authCookie())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var got struct {
			SampleCount      int   `json:"sample_count"`
			SampleIntervalNs int64 `json:"sample_interval_ns"`
			Root             struct {
				Children []struct {
					Function     string `json:"function"`
					TotalSamples int    `json:"total_samples"`
				} `json:"children"`
			} `json:"root"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.SampleCount != 5 {
			t.Errorf("sample_count = %d, want 5", got.SampleCount)
		}
		if got.SampleIntervalNs == 0 {
			t.Error("sample_interval_ns should be measured from the samples")
		}
		if len(got.Root.Children) != 1 {
			t.Fatalf("root has %d children, want 1", len(got.Root.Children))
		}
		if got.Root.Children[0].Function != "/var/www/app/public/index.php" {
			t.Errorf("entry point = %q", got.Root.Children[0].Function)
		}
	})

	t.Run("404 for a transaction with no profile", func(t *testing.T) {
		var bareID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO transactions
				(project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
			VALUES ($1, 'GET /bare', 'http.server', 'ok', 10, NOW(), NOW())
			RETURNING id`, testProject.ID).Scan(&bareID); err != nil {
			t.Fatalf("insert bare transaction: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/transactions/"+bareID+"/flamegraph", nil)
		req.AddCookie(authCookie())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("requires authentication", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/transactions/"+txID+"/flamegraph", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 without a session, got %d", rec.Code)
		}
	})
}

// The endpoint must distinguish "no profile", which is ordinary, from a real
// failure. Both used to be untested, and they differ by status code alone.
func TestHandleGetTransactionFlameGraph_errorPaths(t *testing.T) {
	ctx := context.Background()
	h := api.NewRouter(testPool, ingest.NewBuffer(10), nil, nil, nil, nil, nil,
		false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)

	get := func(t *testing.T, id string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/transactions/"+id+"/flamegraph", nil)
		req.AddCookie(authCookie())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	t.Run("malformed id is a server error, not a panic", func(t *testing.T) {
		if code := get(t, "not-a-uuid"); code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", code)
		}
	})

	t.Run("unknown transaction is a 404", func(t *testing.T) {
		if code := get(t, "00000000-0000-0000-0000-000000000000"); code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", code)
		}
	})

	// A blob that will not decode is a fault on our side, so it must not be
	// dressed up as a missing profile.
	t.Run("corrupt stored blob is a server error", func(t *testing.T) {
		var txID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO transactions
				(project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, event_id)
			VALUES ($1, 'GET /corrupt', 'http.server', 'ok', 10, NOW(), NOW(), 'corrupt-api-event')
			RETURNING id`, testProject.ID).Scan(&txID); err != nil {
			t.Fatalf("insert transaction: %v", err)
		}
		if _, err := testPool.Exec(ctx, `
			INSERT INTO profile_chunks
				(project_id, format, transaction_event_id, start_ts, end_ts,
				 sample_count, size_bytes, encoding, data)
			VALUES ($1, 1, 'corrupt-api-event', NOW(), NOW(), 10, 8, 1, $2)`,
			testProject.ID, []byte("not zstd")); err != nil {
			t.Fatalf("insert corrupt profile: %v", err)
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(),
				"DELETE FROM profile_chunks WHERE transaction_event_id = 'corrupt-api-event'")
		})

		if code := get(t, txID); code != http.StatusInternalServerError {
			t.Errorf("expected 500 for a corrupt blob, got %d", code)
		}
	})
}

// contexts.trace.data is a free-form bag: SDKs put numbers and booleans in it
// alongside thread.id. Decoding it into a typed map fails on the first number
// and parseTransaction drops the whole transaction, spans and all, so a
// profiling change would silently delete ordinary performance data.
func TestHandleEnvelope_transactionWithMixedTypeTraceData(t *testing.T) {
	buf := ingest.NewBuffer(10)
	txBuf := ingest.NewTransactionBuffer(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go txBuf.Run(ctx, testPool)

	const txName = "/mixed-trace-data"
	payload := `{"event_id":"dd0e8400e29b41d4a716446655440000","transaction":"` + txName + `",` +
		`"start_timestamp":"2026-08-24T10:00:00Z","timestamp":"2026-08-24T10:00:01Z",` +
		`"contexts":{"trace":{"trace_id":"aa","span_id":"bb","op":"http.server","status":"ok",` +
		`"data":{"thread.id":"8412331008","sentry.sample_rate":1.0,` +
		`"http.response.status_code":200,"sentry.parentIsRemote":false}}}}`
	body := `{"event_id":"ee0e8400e29b41d4a716446655440000"}` + "\n" +
		fmt.Sprintf(`{"type":"transaction","length":%d}`, len(payload)) + "\n" + payload + "\n"

	h := api.NewRouter(testPool, buf, txBuf, nil, nil, nil, nil, false, "", "", "", "",
		0, 0, 0, 0, 0, 0, nil, false, true, nil)
	if rec := postEnvelope(t, h, body); rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	time.Sleep(400 * time.Millisecond)

	var threadID *string
	err := testPool.QueryRow(ctx, `
		SELECT thread_id FROM transactions
		WHERE project_id = $1 AND transaction = $2`, testProject.ID, txName).Scan(&threadID)
	if err != nil {
		t.Fatalf("the transaction was dropped: %v", err)
	}
	if threadID == nil || *threadID != "8412331008" {
		t.Errorf("thread_id = %v, want the string value alongside the numeric keys", threadID)
	}
}

// ingest.flexString stringifies a numeric thread id on the profile side, so
// dropping one on the transaction side left the two unable to match and the
// chunk got folded across every thread instead of the one that ran.
func TestHandleEnvelope_numericThreadIDInTraceData(t *testing.T) {
	buf := ingest.NewBuffer(10)
	txBuf := ingest.NewTransactionBuffer(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go txBuf.Run(ctx, testPool)

	const txName = "/numeric-thread-id"
	payload := `{"event_id":"ff0e8400e29b41d4a716446655440000","transaction":"` + txName + `",` +
		`"start_timestamp":"2026-08-24T10:00:00Z","timestamp":"2026-08-24T10:00:01Z",` +
		`"contexts":{"trace":{"trace_id":"aa","span_id":"bb","op":"http.server","status":"ok",` +
		`"data":{"thread.id":8412331008}}}}`
	body := `{"event_id":"110e8400e29b41d4a716446655440000"}` + "\n" +
		fmt.Sprintf(`{"type":"transaction","length":%d}`, len(payload)) + "\n" + payload + "\n"

	h := api.NewRouter(testPool, buf, txBuf, nil, nil, nil, nil, false, "", "", "", "",
		0, 0, 0, 0, 0, 0, nil, false, true, nil)
	if rec := postEnvelope(t, h, body); rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	time.Sleep(400 * time.Millisecond)

	var threadID *string
	if err := testPool.QueryRow(ctx, `
		SELECT thread_id FROM transactions WHERE project_id = $1 AND transaction = $2`,
		testProject.ID, txName).Scan(&threadID); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if threadID == nil || *threadID != "8412331008" {
		t.Errorf("thread_id = %v, want the number formatted as the profile side would", threadID)
	}
}

// Backpressure has to reach the SDK. Silently dropping profiles when the writer
// is behind would look like profiling simply not working.
func TestHandleEnvelope_profileBufferFull(t *testing.T) {
	profBuf := ingest.NewProfileBuffer(1)
	// Fill it, with no writer draining.
	p, err := ingest.ParseProfile(fixtureBytes(t, "v1_php_laravel.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	first, err := ingest.NewBufferedProfile(testProject.ID, p)
	if err != nil {
		t.Fatalf("buffer: %v", err)
	}
	if !profBuf.Push(first) {
		t.Fatal("expected the first push to fit")
	}

	rec := postEnvelope(t, newProfileHandler(profBuf),
		profileEnvelope(t, "profile", "v1_php_laravel.json"))

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 when the profile buffer is full, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected a Retry-After header so the SDK backs off")
	}
}

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "ingest", "testdata", "profiles", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

// A project-scoped API token must not read another project's flame graph. The
// endpoint takes a transaction id with no project in the path, so this check is
// the only thing keeping the two apart.
func TestHandleGetTransactionFlameGraph_tokenScopedToAnotherProject(t *testing.T) {
	ctx := context.Background()

	other, err := storage.CreateProject(ctx, testPool, "flame-scope-other", "Flame Scope Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", other.ID) })

	_, plaintext, err := storage.CreateAPIToken(ctx, testPool, other.ID, "scoped", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	// A transaction belonging to the *other* project than the token.
	var txID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO transactions
			(project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
		VALUES ($1, 'GET /scoped', 'http.server', 'ok', 10, NOW(), NOW())
		RETURNING id`, testProject.ID).Scan(&txID); err != nil {
		t.Fatalf("insert transaction: %v", err)
	}

	h := api.NewRouter(testPool, ingest.NewBuffer(10), nil, nil, nil, nil, nil,
		false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/"+txID+"/flamegraph", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for a token scoped to another project, got %d", rec.Code)
	}
}
