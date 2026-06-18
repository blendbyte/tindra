package ingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/ingest"
)

func stubTransaction() ingest.BufferedTransaction {
	now := time.Now()
	return ingest.BufferedTransaction{
		ProjectID:      "00000000-0000-0000-0000-000000000001",
		Transaction:    "/api/test",
		Op:             "http.server",
		Status:         "ok",
		DurationMs:     42,
		StartTimestamp: now,
		Timestamp:      now.Add(42 * time.Millisecond),
	}
}

func TestTransactionBuffer_PushSucceeds(t *testing.T) {
	buf := ingest.NewTransactionBuffer(2)
	if !buf.Push(stubTransaction()) {
		t.Fatal("expected Push to return true on non-full buffer")
	}
}

func TestTransactionBuffer_PushReturnsFalseWhenFull(t *testing.T) {
	buf := ingest.NewTransactionBuffer(1)
	buf.Push(stubTransaction())
	if buf.Push(stubTransaction()) {
		t.Fatal("expected Push to return false when buffer is full")
	}
}

func TestTransactionBuffer_PushWithSpans(t *testing.T) {
	buf := ingest.NewTransactionBuffer(10)
	tx := stubTransaction()
	tx.Spans = []ingest.BufferedSpan{
		{
			SpanID:         "abcd1234",
			Op:             "db.query",
			StartTimestamp: tx.StartTimestamp,
			Timestamp:      tx.StartTimestamp.Add(10 * time.Millisecond),
			DurationMs:     10,
			Status:         "ok",
		},
	}
	if !buf.Push(tx) {
		t.Fatal("Push with spans should succeed on non-full buffer")
	}
}

// TestWriteTxBatch_spansDenormalized verifies that project_id, environment, and
// release are written onto span rows from the parent transaction.
func TestWriteTxBatch_spansDenormalized(t *testing.T) {
	buf := ingest.NewTransactionBuffer(10)

	now := time.Now().UTC()
	tx := ingest.BufferedTransaction{
		ProjectID:      testProject.ID,
		Transaction:    "/api/denorm-test",
		Op:             "http.server",
		Status:         "ok",
		DurationMs:     50,
		StartTimestamp: now,
		Timestamp:      now.Add(50 * time.Millisecond),
		Environment:    "staging",
		Release:        "v9.0.0",
		Spans: []ingest.BufferedSpan{
			{
				SpanID:         "denorm-span-1",
				Op:             "db.query",
				Description:    "SELECT denorm",
				StartTimestamp: now,
				Timestamp:      now.Add(10 * time.Millisecond),
				DurationMs:     10,
				Status:         "ok",
			},
		},
	}
	buf.Push(tx)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go buf.Run(ctx, testPool)

	// Wait long enough for the 200ms ticker to flush.
	time.Sleep(400 * time.Millisecond)
	cancel()

	var projectID, environment, release string
	err := testPool.QueryRow(context.Background(), `
		SELECT s.project_id::text, COALESCE(s.environment, ''), COALESCE(s.release, '')
		FROM spans s
		JOIN transactions t ON t.id = s.transaction_id
		WHERE t.transaction = '/api/denorm-test' AND t.project_id = $1
		LIMIT 1
	`, testProject.ID).Scan(&projectID, &environment, &release)
	if err != nil {
		t.Fatalf("query span: %v", err)
	}

	if projectID != testProject.ID {
		t.Errorf("project_id: got %q, want %q", projectID, testProject.ID)
	}
	if environment != "staging" {
		t.Errorf("environment: got %q, want staging", environment)
	}
	if release != "v9.0.0" {
		t.Errorf("release: got %q, want v9.0.0", release)
	}
}

func TestTransactionBuffer_batchFlushOn100(t *testing.T) {
	buf := ingest.NewTransactionBuffer(200)

	// Pre-fill 100 transactions - when Run starts processing them the batch
	// reaches 100 and triggers the early flush before the 200ms ticker fires.
	tx := stubTransaction()
	tx.ProjectID = testProject.ID
	for range 100 {
		buf.Push(tx)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go buf.Run(ctx, testPool)

	// Give the goroutine time to drain the pre-filled buffer.
	time.Sleep(300 * time.Millisecond)
	cancel()
}
