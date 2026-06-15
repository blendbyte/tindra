package ingest_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/ingest"
)

func TestBuffer_Run_drainOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	eventID := "drain-test-evt-1"
	buf := ingest.NewBuffer(10)
	buf.Push(ingest.BufferedEvent{
		ProjectID: testProject.ID,
		EventID:   &eventID,
		Timestamp: time.Now(),
		Payload:   json.RawMessage(`{"level":"error"}`),
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf.Run(ctx, testPool)
	}()

	cancel()
	<-done

	var count int
	testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM events WHERE project_id = $1 AND event_id = $2`,
		testProject.ID, eventID,
	).Scan(&count)

	if count != 1 {
		t.Errorf("expected 1 event in DB after drain, got %d", count)
	}
}

func TestBuffer_Run_tickerFlush(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventID := "ticker-evt-1"
	buf := ingest.NewBuffer(10)
	buf.Push(ingest.BufferedEvent{
		ProjectID: testProject.ID,
		EventID:   &eventID,
		Timestamp: time.Now(),
		Payload:   json.RawMessage(`{"level":"warn"}`),
	})

	go buf.Run(ctx, testPool)

	// Wait longer than the 200ms ticker interval
	time.Sleep(400 * time.Millisecond)

	var count int
	testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM events WHERE project_id = $1 AND event_id = $2`,
		testProject.ID, eventID,
	).Scan(&count)

	if count != 1 {
		t.Errorf("expected 1 event in DB after ticker flush, got %d", count)
	}
}

func TestBuffer_Run_deduplicates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	eventID := "dedup-evt-1"
	buf := ingest.NewBuffer(10)
	// Push the same event_id twice
	for i := 0; i < 2; i++ {
		buf.Push(ingest.BufferedEvent{
			ProjectID: testProject.ID,
			EventID:   &eventID,
			Timestamp: time.Now(),
			Payload:   json.RawMessage(`{"level":"error"}`),
		})
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf.Run(ctx, testPool)
	}()

	cancel()
	<-done

	var count int
	testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM events WHERE project_id = $1 AND event_id = $2`,
		testProject.ID, eventID,
	).Scan(&count)

	if count != 1 {
		t.Errorf("expected exactly 1 row (dedup), got %d", count)
	}
}

func TestTransactionBuffer_Run_drainOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	now := time.Now()
	buf := ingest.NewTransactionBuffer(10)
	buf.Push(ingest.BufferedTransaction{
		ProjectID:      testProject.ID,
		TraceID:        "trace-drain-1",
		SpanID:         "span-drain-1",
		Transaction:    "/drain/test",
		Op:             "http.server",
		Status:         "ok",
		DurationMs:     10,
		StartTimestamp: now,
		Timestamp:      now.Add(10 * time.Millisecond),
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf.Run(ctx, testPool)
	}()

	cancel()
	<-done

	var count int
	testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM transactions WHERE project_id = $1 AND trace_id = $2`,
		testProject.ID, "trace-drain-1",
	).Scan(&count)

	if count != 1 {
		t.Errorf("expected 1 transaction in DB after drain, got %d", count)
	}
}

// TestBuffer_Run_withRelease ensures the release-upsert path in writeBatch is
// exercised. Events carrying a "release" JSON field cause writeBatch to INSERT
// into the releases table (lines 124-143 of buffer.go).
// Also passes a non-empty TraceID to cover nullableString's `return &s` branch.
func TestBuffer_Run_withRelease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	eventID := "release-event-cov1"
	buf := ingest.NewBuffer(10)
	buf.Push(ingest.BufferedEvent{
		ProjectID: testProject.ID,
		EventID:   &eventID,
		Timestamp: time.Now(),
		Payload:   json.RawMessage(`{"level":"error","release":"v1.0.0-cov"}`),
		TraceID:   "trace-release-cov-1",
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf.Run(ctx, testPool)
	}()

	cancel()
	<-done

	var count int
	testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM releases WHERE project_id = $1 AND version = $2`,
		testProject.ID, "v1.0.0-cov",
	).Scan(&count)
	if count < 1 {
		t.Errorf("expected release row in DB, got %d", count)
	}
}

// TestTransactionBuffer_Run_withRelease ensures the release-upsert and nilJSON
// paths in writeTxBatch are exercised. A transaction with a non-empty Release,
// non-null Measurements, and at least one Span covers txbuffer.go lines 176-196
// (the release-upsert path, which is only reached after span insertion) and
// nilJSON's `return b` branch (line 210).
func TestTransactionBuffer_Run_withRelease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	now := time.Now()
	buf := ingest.NewTransactionBuffer(10)
	buf.Push(ingest.BufferedTransaction{
		ProjectID:      testProject.ID,
		TraceID:        "trace-release-tx-1",
		SpanID:         "span-release-tx-root",
		Transaction:    "/release/test",
		Op:             "http.server",
		Status:         "ok",
		DurationMs:     5,
		StartTimestamp: now,
		Timestamp:      now.Add(5 * time.Millisecond),
		Release:        "v2.0.0-cov",
		Measurements:   json.RawMessage(`{"lcp":{"value":200,"unit":"millisecond"}}`),
		Spans: []ingest.BufferedSpan{
			{
				SpanID:         "span-release-tx-child",
				ParentSpanID:   "span-release-tx-root",
				Op:             "db.query",
				Description:    "SELECT 1",
				StartTimestamp: now,
				Timestamp:      now.Add(2 * time.Millisecond),
				DurationMs:     2,
				Status:         "ok",
			},
		},
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf.Run(ctx, testPool)
	}()

	cancel()
	<-done

	var count int
	testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM releases WHERE project_id = $1 AND version = $2`,
		testProject.ID, "v2.0.0-cov",
	).Scan(&count)
	if count < 1 {
		t.Errorf("expected release row in DB for tx release, got %d", count)
	}
}

func TestTransactionBuffer_Run_withSpans(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	now := time.Now()
	buf := ingest.NewTransactionBuffer(10)
	buf.Push(ingest.BufferedTransaction{
		ProjectID:      testProject.ID,
		TraceID:        "trace-spans-1",
		SpanID:         "span-root-1",
		Transaction:    "/spans/test",
		Op:             "http.server",
		Status:         "ok",
		DurationMs:     20,
		StartTimestamp: now,
		Timestamp:      now.Add(20 * time.Millisecond),
		Spans: []ingest.BufferedSpan{
			{
				SpanID:         "span-child-1",
				ParentSpanID:   "span-root-1",
				Op:             "db.query",
				Description:    "SELECT 1",
				StartTimestamp: now,
				Timestamp:      now.Add(5 * time.Millisecond),
				DurationMs:     5,
				Status:         "ok",
			},
		},
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf.Run(ctx, testPool)
	}()

	cancel()
	<-done

	var spanCount int
	testPool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM spans s
		JOIN transactions t ON t.id = s.transaction_id
		WHERE t.project_id = $1 AND t.trace_id = $2
	`, testProject.ID, "trace-spans-1").Scan(&spanCount)

	if spanCount != 1 {
		t.Errorf("expected 1 span in DB, got %d", spanCount)
	}
}
