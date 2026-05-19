package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func txHandler() http.Handler {
	return api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, nil)
}

func truncateTransactions(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE transactions CASCADE"); err != nil {
		t.Fatalf("truncate transactions: %v", err)
	}
}

func seedTransactionRow(t *testing.T, name string, durationMs int) *storage.Transaction {
	t.Helper()
	start := time.Now().UTC()
	end := start.Add(time.Duration(durationMs) * time.Millisecond)
	var tx storage.Transaction
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO transactions
			(project_id, transaction, op, status, duration_ms, start_timestamp, timestamp)
		VALUES ($1, $2, 'http.server', 'ok', $3, $4, $5)
		RETURNING id, project_id, COALESCE(trace_id,''), COALESCE(span_id,''), transaction,
		          op, status, duration_ms, start_timestamp, timestamp, received_at,
		          COALESCE(environment,''), COALESCE(release,''), COALESCE(platform,'')
	`, testProject.ID, name, durationMs, start, end).Scan(
		&tx.ID, &tx.ProjectID, &tx.TraceID, &tx.SpanID, &tx.Transaction,
		&tx.Op, &tx.Status, &tx.DurationMs, &tx.StartTimestamp, &tx.Timestamp, &tx.ReceivedAt,
		&tx.Environment, &tx.Release, &tx.Platform,
	)
	if err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
	return &tx
}

func TestListTransactions_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/transactions", nil)
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestListTransactions_empty(t *testing.T) {
	truncateTransactions(t)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/transactions", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []json.RawMessage `json:"transactions"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Transactions) != 0 {
		t.Errorf("expected 0 transactions, got %d", len(resp.Transactions))
	}
}

func TestListTransactions_withData(t *testing.T) {
	truncateTransactions(t)

	seedTransactionRow(t, "/api/a", 10)
	seedTransactionRow(t, "/api/b", 20)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/transactions", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Transactions []*storage.Transaction `json:"transactions"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Transactions) != 2 {
		t.Errorf("expected 2, got %d", len(resp.Transactions))
	}
}

func TestListTransactions_unknownProject(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/projects/no-such-project/transactions", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestGetTransaction_found(t *testing.T) {
	truncateTransactions(t)
	tx := seedTransactionRow(t, "/api/detail", 42)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/transactions/"+tx.ID, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transaction *storage.Transaction `json:"transaction"`
		Spans       []json.RawMessage    `json:"spans"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Transaction == nil {
		t.Fatal("expected transaction in response")
	}
	if resp.Transaction.ID != tx.ID {
		t.Errorf("ID mismatch: got %q", resp.Transaction.ID)
	}
}

func TestGetTransaction_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/transactions/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestTransactionStats_empty(t *testing.T) {
	truncateTransactions(t)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/transactions/stats", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var stats storage.TransactionPercentiles
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stats.SampleCount != 0 {
		t.Errorf("expected 0 samples, got %d", stats.SampleCount)
	}
}

func TestTransactionStats_withData(t *testing.T) {
	truncateTransactions(t)

	for _, d := range []int{10, 20, 50, 100, 200} {
		seedTransactionRow(t, "/api/perf", d)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/transactions/stats?hours=24", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var stats storage.TransactionPercentiles
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stats.SampleCount != 5 {
		t.Errorf("expected 5, got %d", stats.SampleCount)
	}
	if stats.P50 <= 0 {
		t.Errorf("expected positive P50, got %f", stats.P50)
	}
}

func TestTransactionStats_unknownProject(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/projects/no-such-project/transactions/stats", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestTransactionStats_invalidHoursParam(t *testing.T) {
	// Non-numeric hours → Atoi fails → falls back to default 24h
	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/transactions/stats?hours=abc", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for invalid hours=abc, got %d", rec.Code)
	}
}

func TestTransactionStats_outOfRangeHours(t *testing.T) {
	// hours=0 is out of range (n >= 1 check fails) → falls back to default 24h
	req := httptest.NewRequest(http.MethodGet, "/api/projects/test-project/transactions/stats?hours=0", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for out-of-range hours=0, got %d", rec.Code)
	}
}

func TestListTransactions_statusFilter(t *testing.T) {
	truncateTransactions(t)
	seedTransactionRow(t, "/api/users", 10)

	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/transactions?status=ok", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestListTransactions_cursorTimeWithoutCursorID(t *testing.T) {
	truncateTransactions(t)
	cursorTime := time.Now().UTC().Format(time.RFC3339Nano)

	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/test-project/transactions?cursor_time="+cursorTime, nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	txHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 when cursor_id absent, got %d", rec.Code)
	}
}

func TestHandleEnvelope_transactionItem(t *testing.T) {
	truncateTransactions(t)

	buf := ingest.NewBuffer(100)
	txBuf := ingest.NewTransactionBuffer(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Run(ctx, testPool)
	go txBuf.Run(ctx, testPool)

	payload := `{"transaction":"/api/users","start_timestamp":"2024-01-01T00:00:00.000Z","timestamp":"2024-01-01T00:00:00.150Z","contexts":{"trace":{"trace_id":"abc123","span_id":"def456","op":"http.server","status":"ok"}},"spans":[]}`
	body := `{"event_id":"tx-envelope-0001"}` + "\n" +
		`{"type":"transaction"}` + "\n" +
		payload + "\n"

	handler := api.NewRouter(testPool, buf, txBuf, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/"+testProject.ID+"/envelope/",
		bytes.NewBufferString(body))
	req.Header.Set("X-Sentry-Auth", "Sentry sentry_version=7, sentry_key="+testProject.PublicKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	time.Sleep(350 * time.Millisecond)

	var count int
	testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM transactions WHERE project_id = $1", testProject.ID,
	).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 transaction in DB, got %d", count)
	}
}
