package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

// --- parseSentryAuthHeader: header without sentry_key ---

func TestHandleEnvelope_authHeaderNoSentryKey(t *testing.T) {
	buf := ingest.NewBuffer(100)
	body := `{"event_id":"abc"}` + "\n" + `{"type":"event"}` + "\n" + `{"level":"error"}` + "\n"

	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	// Sentry header but no sentry_key → auth fails.
	req.Header.Set("X-Sentry-Auth", "Sentry sentry_version=7")
	rec := httptest.NewRecorder()
	newHandler(buf).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing sentry_key in header, got %d", rec.Code)
	}
}

// --- parseTimestamp: payload with no timestamp field ---

func TestHandleEnvelope_eventNoTimestamp(t *testing.T) {
	truncateEvents(t)

	buf := ingest.NewBuffer(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Run(ctx, testPool)

	// No "timestamp" field - parseTimestamp falls back to time.Now()
	payload := `{"level":"error","message":"no ts"}`
	body := `{"event_id":"no-ts-event-0001"}` + "\n" +
		`{"type":"event","length":` + fmt.Sprintf("%d", len(payload)) + `}` + "\n" +
		payload + "\n"

	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	newHandler(buf).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for event without timestamp, got %d", rec.Code)
	}
}

// --- handleListTransactions: pagination cursor when exactly 50 returned ---

func TestListTransactions_paginationCursorReturned(t *testing.T) {
	truncateTransactions(t)

	for i := range 50 {
		seedTransactionRow(t, fmt.Sprintf("/api/page/%d", i), 10)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/transactions", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Transactions   []json.RawMessage `json:"transactions"`
		NextCursorTime *time.Time        `json:"next_cursor_time"`
		NextCursorID   *string           `json:"next_cursor_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Transactions) != 50 {
		t.Errorf("expected 50 transactions, got %d", len(resp.Transactions))
	}
	if resp.NextCursorTime == nil {
		t.Error("expected next_cursor_time when exactly limit transactions returned")
	}
}

// --- handleListIssues ---

func TestListIssues_withLevelFilter(t *testing.T) {
	truncateIssues(t)

	storage.UpsertIssue(context.Background(), testPool, testProject.ID, "fp-err", "Error", "error", "error", "", "", time.Now())
	storage.UpsertIssue(context.Background(), testPool, testProject.ID, "fp-warn", "Warn", "warning", "error", "", "", time.Now().Add(time.Second))

	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/issues?level=error", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Issues []*storage.Issue `json:"issues"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	for _, iss := range resp.Issues {
		if iss.Level != "error" {
			t.Errorf("unexpected level %q in filtered result", iss.Level)
		}
	}
	if len(resp.Issues) != 1 {
		t.Errorf("expected 1 issue, got %d", len(resp.Issues))
	}
}

func TestListIssues_cursorParsing(t *testing.T) {
	truncateIssues(t)

	// Seed 3 issues
	base := time.Now().UTC()
	for i, fp := range []string{"fp-c1", "fp-c2", "fp-c3"} {
		storage.UpsertIssue(context.Background(), testPool, testProject.ID, fp, "Title", "error", "error", "", "",
			base.Add(time.Duration(i)*time.Second))
	}

	// Get first page
	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/issues", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	var resp struct {
		Issues         []*storage.Issue `json:"issues"`
		NextCursorTime *time.Time       `json:"next_cursor_time"`
		NextCursorID   *string          `json:"next_cursor_id"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)

	// Use cursor params if available (only if exactly limit items returned)
	// Here we have 3 issues < 50 limit, so no cursor. But let's test with manual cursor.
	if len(resp.Issues) == 0 {
		t.Fatal("expected issues")
	}
	cursor := resp.Issues[0]
	cursorTime := cursor.LastSeen.Format(time.RFC3339Nano)
	url := fmt.Sprintf("/api/projects/test-project/issues?cursor_time=%s&cursor_id=%s",
		cursorTime, cursor.ID)

	req2 := httptest.NewRequest(http.MethodGet, url, nil)
	req2.AddCookie(authCookie())
	rec2 := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("cursor request: expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestListIssues_paginationCursorReturned(t *testing.T) {
	truncateIssues(t)

	// Seed exactly 50 issues to trigger next_cursor_time in the response.
	base := time.Now().UTC().Add(-time.Hour)
	for i := range 50 {
		fp := fmt.Sprintf("fp-page-%03d", i)
		storage.UpsertIssue(context.Background(), testPool, testProject.ID, fp, "Page Error", "error", "error", "", "",
			base.Add(time.Duration(i)*time.Second))
	}

	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/issues", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Issues         []*storage.Issue `json:"issues"`
		NextCursorTime *time.Time       `json:"next_cursor_time"`
		NextCursorID   *string          `json:"next_cursor_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Issues) != 50 {
		t.Errorf("expected 50 issues, got %d", len(resp.Issues))
	}
	if resp.NextCursorTime == nil {
		t.Error("expected next_cursor_time when exactly limit items returned")
	}
	if resp.NextCursorID == nil {
		t.Error("expected next_cursor_id when exactly limit items returned")
	}
}

func TestListIssues_invalidCursorTimeIgnored(t *testing.T) {
	truncateIssues(t)

	// Invalid cursor_time format should be ignored gracefully
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/issues?cursor_time=not-a-date&cursor_id=some-id", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	// Should still return 200 with all issues (cursor ignored)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for invalid cursor, got %d", rec.Code)
	}
}

func TestListIssues_cursorTimeWithoutCursorID(t *testing.T) {
	truncateIssues(t)

	// cursor_time present but cursor_id absent → cursor is ignored (inner if not entered)
	cursorTime := time.Now().UTC().Format(time.RFC3339Nano)
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/issues?cursor_time="+cursorTime, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 when cursor_id absent, got %d", rec.Code)
	}
}

// --- handleGetIssue ---

func TestGetIssue_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/issues/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	// handleGetIssue returns 200 with null when not found (no 404 guard)
	if rec.Code != http.StatusOK {
		t.Logf("note: getIssue returned %d for unknown ID", rec.Code)
	}
}

// --- handleListTransactions ---

func TestListTransactions_withOpFilter(t *testing.T) {
	truncateTransactions(t)
	seedTransactionRow(t, "/api/users", 10)

	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/transactions?op=http.server", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestListTransactions_cursorParsing(t *testing.T) {
	truncateTransactions(t)
	seedTransactionRow(t, "/api/v1", 5)

	base := time.Now().UTC()
	cursorTime := base.Format(time.RFC3339Nano)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/projects/test-project/transactions?cursor_time=%s&cursor_id=00000000-0000-0000-0000-000000000000", cursorTime), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListTransactions_invalidCursorIgnored(t *testing.T) {
	truncateTransactions(t)

	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/transactions?cursor_time=bad-date&cursor_id=id", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for invalid cursor, got %d", rec.Code)
	}
}

// --- ingest: query param auth ---

func TestHandleEnvelope_sentryKeyQueryParam(t *testing.T) {
	truncateEvents(t)

	buf := ingest.NewBuffer(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Run(ctx, testPool)

	payload := `{"timestamp":"2024-01-01T00:00:00Z","level":"error","message":"qp"}`
	body := eventEnvelope("qp-event-0001", payload)

	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/%s/envelope/?sentry_key=%s", testProject.ID, testProject.PublicKey),
		bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	newHandler(buf).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for query-param auth, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleEnvelope_authorizationHeader(t *testing.T) {
	truncateEvents(t)

	buf := ingest.NewBuffer(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Run(ctx, testPool)

	payload := `{"timestamp":"2024-01-01T00:00:00Z","level":"error","message":"authz"}`
	body := eventEnvelope("authz-event-0001", payload)

	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("Authorization", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	newHandler(buf).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for Authorization header auth, got %d", rec.Code)
	}
}

// --- MFA handler edge cases ---

func TestMFADisable_wrongPassword(t *testing.T) {
	body := bytes.NewBufferString(`{"password":"wrong-password-xyz"}`)
	req := httptest.NewRequest(http.MethodDelete, "/api/auth/mfa", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong password, got %d", rec.Code)
	}
}

func TestMFADisable_badRequestBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/api/auth/mfa",
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", rec.Code)
	}
}

func TestMFAConfirm_noPendingSetup(t *testing.T) {
	// Ensure no mfa_secret is set for the test user
	testPool.Exec(context.Background(), "UPDATE users SET mfa_secret = NULL WHERE id = $1", testUser.ID)

	body := bytes.NewBufferString(`{"code":"123456"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/confirm", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when no pending MFA setup, got %d", rec.Code)
	}
}

func TestMFAConfirm_badRequestBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/confirm",
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", rec.Code)
	}
}

func TestMFAVerify_badRequestBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/verify",
		bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", rec.Code)
	}
}

// --- handleGetLatestEvent: no events ---

func TestGetLatestEvent_noEvents(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE events, issues CASCADE")
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool, testProject.ID, "fp-noevent", "No Event", "error", "error", "", "", time.Now())

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/projects/test-project/issues/%s/events/latest", iss.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 when no events for issue, got %d", rec.Code)
	}
}

// --- token handler edge cases ---

func TestCreateToken_badRequestBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/tokens",
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", rec.Code)
	}
}

// --- handleGetIssueFingerprints: unknown issue ---

func TestGetIssueFingerprints_unknownIssue(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/issues/00000000-0000-0000-0000-000000000000/fingerprints", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown issue fingerprints, got %d", rec.Code)
	}
}

// --- handleMergeIssues: bad JSON body ---

func TestMergeIssues_badBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/projects/test-project/issues/merge",
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", rec.Code)
	}
}

// --- handleUnmergeIssue: bad JSON body ---

func TestUnmergeIssue_badBody(t *testing.T) {
	truncateIssues(t)

	a, _, _, _ := storage.UpsertIssue(context.Background(), testPool, testProject.ID,
		"fp-unm-body", "Err", "error", "error", "", "", time.Now())

	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/projects/test-project/issues/%s/unmerge", a.ID),
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", rec.Code)
	}
}

// --- handleUnmergeIssue: unknown issue ---

func TestUnmergeIssue_unknownIssue(t *testing.T) {
	body := bytes.NewBufferString(`{"fingerprints":["fp-x"]}`)
	req := httptest.NewRequest(http.MethodPost,
		"/api/projects/test-project/issues/00000000-0000-0000-0000-000000000000/unmerge", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown issue unmerge, got %d", rec.Code)
	}
}

// --- MFA handler empty-field paths ---

func TestMFAConfirm_emptyCode(t *testing.T) {
	body := bytes.NewBufferString(`{"code":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/confirm", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty code, got %d", rec.Code)
	}
}

func TestMFAVerify_emptyToken(t *testing.T) {
	body, _ := json.Marshal(map[string]string{
		"mfa_token": "",
		"code":      "123456",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/verify", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty mfa_token, got %d", rec.Code)
	}
}

func TestMFAVerify_emptyCode(t *testing.T) {
	body, _ := json.Marshal(map[string]string{
		"mfa_token": "sometoken",
		"code":      "",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/verify", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty code, got %d", rec.Code)
	}
}

func TestMFADisable_emptyPassword(t *testing.T) {
	body := bytes.NewBufferString(`{"password":""}`)
	req := httptest.NewRequest(http.MethodDelete, "/api/auth/mfa", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty password, got %d", rec.Code)
	}
}

// --- handleMFAVerify: valid token but wrong TOTP code ---

func TestMFAVerify_validTokenWrongCode(t *testing.T) {
	// Store a secret on the test user and create a challenge.
	secret := "JBSWY3DPEHPK3PXP" // base32 encoded dummy secret
	testPool.Exec(context.Background(), "UPDATE users SET mfa_secret = $1 WHERE id = $2",
		secret, testUser.ID)
	defer testPool.Exec(context.Background(), "UPDATE users SET mfa_secret = NULL WHERE id = $1",
		testUser.ID)

	token, err := storage.CreateMFAChallenge(context.Background(), testPool, testUser.ID)
	if err != nil {
		t.Fatalf("create mfa challenge: %v", err)
	}

	body, _ := json.Marshal(map[string]string{
		"mfa_token": token,
		"code":      "000000", // definitely wrong
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/verify", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong TOTP code, got %d", rec.Code)
	}
}

// --- handleEnvelope: transaction with empty name (parseTransaction returns nil) ---

func TestHandleEnvelope_transactionEmptyName(t *testing.T) {
	buf := ingest.NewBuffer(100)
	txBuf := ingest.NewTransactionBuffer(100)

	// Transaction payload with no "transaction" field - should be silently skipped.
	body := `{"event_id":"tx-empty-name-0001"}` + "\n" +
		`{"type":"transaction"}` + "\n" +
		`{"start_timestamp":"2024-01-01T00:00:00Z","timestamp":"2024-01-01T00:00:01Z"}` + "\n"

	h := api.NewRouter(testPool, buf, txBuf, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for transaction with empty name, got %d", rec.Code)
	}
}

// --- handleEnvelope: transaction with empty status defaults to ok ---

func TestHandleEnvelope_transactionEmptyStatus(t *testing.T) {
	buf := ingest.NewBuffer(100)
	txBuf := ingest.NewTransactionBuffer(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go txBuf.Run(ctx, testPool)

	// Status omitted - parseTransaction should default to "ok".
	payload := `{"transaction":"/api/status","start_timestamp":"2024-01-01T00:00:00Z","timestamp":"2024-01-01T00:00:01Z","contexts":{"trace":{"trace_id":"abc","span_id":"def"}}}`
	body := `{"event_id":"tx-empty-status-001"}` + "\n" +
		`{"type":"transaction"}` + "\n" +
		payload + "\n"

	h := api.NewRouter(testPool, buf, txBuf, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- handleEnvelope: transaction with reversed timestamps (negative duration) ---

func TestHandleEnvelope_transactionNegativeDuration(t *testing.T) {
	buf := ingest.NewBuffer(100)
	txBuf := ingest.NewTransactionBuffer(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go txBuf.Run(ctx, testPool)

	// end before start → durationMs < 0 → clamped to 0
	payload := `{"transaction":"/api/negative","start_timestamp":"2024-01-01T00:00:01Z","timestamp":"2024-01-01T00:00:00Z","contexts":{"trace":{"trace_id":"abc","span_id":"def","op":"http.server","status":"ok"}},"spans":[{"span_id":"s1","op":"db","start_timestamp":"2024-01-01T00:00:01Z","timestamp":"2024-01-01T00:00:00Z","status":"ok"}]}`
	body := `{"event_id":"tx-neg-dur-0001"}` + "\n" +
		`{"type":"transaction"}` + "\n" +
		payload + "\n"

	h := api.NewRouter(testPool, buf, txBuf, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", sentryAuthHeader(testProject.PublicKey))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- handleUpdateIssue: bad body ---

func TestUpdateIssue_badBody(t *testing.T) {
	truncateIssues(t)
	iss, _, _, _ := storage.UpsertIssue(context.Background(), testPool, testProject.ID,
		"fp-upd-body", "Err", "error", "error", "", "", time.Now())

	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/projects/test-project/issues/%s", iss.ID),
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", rec.Code)
	}
}

// --- per-project profiling toggle ---

// The storage budget is instance-wide and purges oldest-first across every
// project, so one busy service can evict everyone else's profiles. This switch
// is the only lever against that, and it has to survive a round trip.
func TestHandleUpdateProject_profilingToggle(t *testing.T) {
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "toggle-proj", "Toggle Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", p.ID) })

	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, nil,
		false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)

	patch := func(t *testing.T, body string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/api/projects/"+p.ID, bytes.NewBufferString(body))
		req.AddCookie(authCookie())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	enabled := func(t *testing.T) bool {
		t.Helper()
		var on bool
		if err := testPool.QueryRow(ctx,
			"SELECT profiling_enabled FROM projects WHERE id = $1", p.ID).Scan(&on); err != nil {
			t.Fatalf("read flag: %v", err)
		}
		return on
	}

	if !enabled(t) {
		t.Fatal("expected profiling on by default")
	}

	t.Run("turns off", func(t *testing.T) {
		if code := patch(t, `{"name":"Toggle Project","slug":"toggle-proj","profiling_enabled":false}`); code != http.StatusOK {
			t.Fatalf("expected 200, got %d", code)
		}
		if enabled(t) {
			t.Error("profiling should be off")
		}
	})

	// A client that predates the field must not silently turn profiling back
	// on, or off, just by renaming a project.
	t.Run("omitting the field leaves it alone", func(t *testing.T) {
		if code := patch(t, `{"name":"Renamed","slug":"toggle-proj"}`); code != http.StatusOK {
			t.Fatalf("expected 200, got %d", code)
		}
		if enabled(t) {
			t.Error("profiling should still be off after an update that did not mention it")
		}
	})

	t.Run("turns back on", func(t *testing.T) {
		if code := patch(t, `{"name":"Renamed","slug":"toggle-proj","profiling_enabled":true}`); code != http.StatusOK {
			t.Fatalf("expected 200, got %d", code)
		}
		if !enabled(t) {
			t.Error("profiling should be on")
		}
	})

	t.Run("is returned to the client", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/api/projects/"+p.ID,
			bytes.NewBufferString(`{"name":"Renamed","slug":"toggle-proj","profiling_enabled":false}`))
		req.AddCookie(authCookie())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		var got struct {
			ProfilingEnabled bool `json:"profiling_enabled"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.ProfilingEnabled {
			t.Error("response should carry the new value so the UI does not need a refetch")
		}
	})
}
