package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/sourcemaps"
	"github.com/blendbyte/tindra/internal/storage"
)

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestEmailLogoEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/assets/email-logo.png", nil)
	rec := httptest.NewRecorder()
	api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type: got %q, want \"image/png\"", ct)
	}
	body := rec.Body.Bytes()
	if len(body) == 0 {
		t.Fatal("response body is empty - embedded PNG was not included in the binary")
	}
	// PNG magic bytes: 0x89 P N G
	if len(body) < 4 || body[0] != 0x89 || body[1] != 0x50 || body[2] != 0x4E || body[3] != 0x47 {
		t.Errorf("response is not a valid PNG (got first bytes: % x)", body[:min(8, len(body))])
	}
}

func TestHandleEnvelopeCORS(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/"+testProject.ID+"/envelope/", nil)
	rec := httptest.NewRecorder()
	api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS header")
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("expected Allow-Methods CORS header")
	}
}

// --- handleGetLatestEvent ---

func seedIssueWithEventForAPI(t *testing.T) (*storage.Issue, string) {
	t.Helper()
	ctx := context.Background()
	ts := time.Now().UTC()
	iss, _, _, err := storage.UpsertIssue(ctx, testPool, testProject.ID, "fp-api-ev", "API Error", "error", "error", "", "", ts)
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	var evID string
	err = testPool.QueryRow(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, $2, $2, '{"level":"error","message":"test"}'::jsonb, 'fp-api-ev', $3)
		RETURNING id
	`, testProject.ID, ts, iss.ID).Scan(&evID)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	return iss, evID
}

func TestGetLatestEvent_found(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, evID := seedIssueWithEventForAPI(t)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/projects/test-project/issues/%s/events/latest", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != evID {
		t.Errorf("event ID: got %q, want %q", resp.ID, evID)
	}
}

func TestGetLatestEvent_smStoreResolution(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")

	ctx := context.Background()
	ts := time.Now().UTC()
	iss, _, _, err := storage.UpsertIssue(ctx, testPool, testProject.ID, "fp-sm-resolve", "SM Error", "error", "error", "", "", ts)
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	var evID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, $2, $2, '{"level":"error","message":"test","release":"v1.2.3"}'::jsonb, 'fp-sm-resolve', $3)
		RETURNING id
	`, testProject.ID, ts, iss.ID).Scan(&evID); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	store, _ := newSmStore(t)
	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, store, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, nil)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/projects/test-project/issues/%s/events/latest", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != evID {
		t.Errorf("event ID: got %q, want %q", resp.ID, evID)
	}
}

func TestGetLatestEvent_issueNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/issues/00000000-0000-0000-0000-000000000000/events/latest", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestGetLatestEvent_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/issues/00000000-0000-0000-0000-000000000000/events/latest", nil)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- Sourcemap API handlers ---

func newSmStore(t *testing.T) (*sourcemaps.Store, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "tindra-api-sm-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return sourcemaps.NewStore(dir, testPool), dir
}

func smHandler(t *testing.T) (http.Handler, *sourcemaps.Store) {
	t.Helper()
	store, _ := newSmStore(t)
	return api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, store, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, nil), store
}

func TestListSourcemaps_empty(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE sourcemaps CASCADE")
	h, _ := smHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/sourcemaps", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Sourcemaps []json.RawMessage `json:"sourcemaps"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Sourcemaps) != 0 {
		t.Errorf("expected empty list, got %d", len(resp.Sourcemaps))
	}
}

func TestUploadSourcemap_success(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE sourcemaps CASCADE")
	h, _ := smHandler(t)

	mapContent := `{"version":3,"sources":["src/app.js"],"mappings":"AAAA"}`

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("release", "v1.0.0")
	mw.WriteField("url", "https://example.com/dist/app.js")
	fw, _ := mw.CreateFormFile("file", "app.js.map")
	io.Copy(fw, strings.NewReader(mapContent))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/sourcemaps", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var sm storage.Sourcemap
	if err := json.NewDecoder(rec.Body).Decode(&sm); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sm.ID == "" {
		t.Error("expected non-empty ID")
	}
	if sm.URL != "~/dist/app.js" {
		t.Errorf("URL: got %q", sm.URL)
	}
}

func TestUploadSourcemap_missingRelease(t *testing.T) {
	h, _ := smHandler(t)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("url", "~/app.js")
	fw, _ := mw.CreateFormFile("file", "app.js.map")
	io.Copy(fw, strings.NewReader("{}"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/sourcemaps", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestUploadSourcemap_missingURL(t *testing.T) {
	h, _ := smHandler(t)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("release", "v1")
	fw, _ := mw.CreateFormFile("file", "app.js.map")
	io.Copy(fw, strings.NewReader("{}"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/sourcemaps", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestUploadSourcemap_noStore(t *testing.T) {
	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, nil)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("release", "v1")
	mw.WriteField("url", "~/app.js")
	fw, _ := mw.CreateFormFile("file", "app.js.map")
	io.Copy(fw, strings.NewReader("{}"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/sourcemaps", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestDeleteSourcemap_success(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE sourcemaps CASCADE")
	h, store := smHandler(t)

	// Upload one first
	sm, _ := store.Upload(context.Background(), testProject.ID, "v1", "~/del.js",
		strings.NewReader(`{"version":3,"sources":[],"mappings":""}`))

	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/projects/test-project/sourcemaps/%s", sm.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}
}

func TestDeleteSourcemap_notFound(t *testing.T) {
	h, _ := smHandler(t)

	req := httptest.NewRequest(http.MethodDelete,
		"/api/projects/test-project/sourcemaps/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestListSourcemaps_unknownProject(t *testing.T) {
	h, _ := smHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/no-such-project/sourcemaps", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown project, got %d", rec.Code)
	}
}

func TestUploadSourcemap_unknownProject(t *testing.T) {
	h, _ := smHandler(t)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("release", "v1")
	mw.WriteField("url", "~/app.js")
	mw.CreateFormFile("file", "app.js.map")
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/projects/no-such-project/sourcemaps", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown project, got %d", rec.Code)
	}
}

func TestDeleteSourcemap_unknownProject(t *testing.T) {
	h, _ := smHandler(t)

	req := httptest.NewRequest(http.MethodDelete,
		"/api/projects/no-such-project/sourcemaps/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown project, got %d", rec.Code)
	}
}
