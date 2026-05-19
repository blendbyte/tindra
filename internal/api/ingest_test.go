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
	testcontainers "github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/migrations"
)

var (
	testPool    *pgxpool.Pool
	testProject *storage.Project
	testUser    *storage.User
	testSession *storage.Session
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:18",
		tcpostgres.WithDatabase("tindra_test"),
		tcpostgres.WithUsername("tindra"),
		tcpostgres.WithPassword("tindra"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		log.Fatalf("start postgres container: %v", err)
	}

	connStr, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatalf("get connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		log.Fatalf("create pool: %v", err)
	}

	names, err := migrations.Files()
	if err != nil {
		log.Fatalf("list migrations: %v", err)
	}
	for _, name := range names {
		sql, err := migrations.FS.ReadFile(name)
		if err != nil {
			log.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			log.Fatalf("apply migration %s: %v", name, err)
		}
	}

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

	pool.Close()
	_ = ctr.Terminate(ctx)
	os.Exit(code)
}

func truncateEvents(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE events CASCADE"); err != nil {
		t.Fatalf("truncate events: %v", err)
	}
}

func newHandler(buf *ingest.Buffer) http.Handler {
	return api.NewRouter(testPool, buf, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, nil)
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

	time.Sleep(350 * time.Millisecond)

	var count int
	testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM events WHERE project_id = $1", testProject.ID,
	).Scan(&count)
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
