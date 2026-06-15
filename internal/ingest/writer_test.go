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

	// Poll until the ticker flushes the event or the deadline passes.
	// A fixed sleep is fragile under CI load; polling is resilient.
	deadline := time.Now().Add(2 * time.Second)
	var count int
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		testPool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM events WHERE project_id = $1 AND event_id = $2`,
			testProject.ID, eventID,
		).Scan(&count)
		if count == 1 {
			return
		}
	}
	t.Errorf("expected 1 event in DB after ticker flush, got %d", count)
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
