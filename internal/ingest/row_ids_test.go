package ingest_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/blendbyte/tindra/internal/ingest"
)

// Events, logs, transactions and spans get time-ordered (v7) primary keys
// from ingest, so inserts append to their primary-key indexes instead of
// landing on random pages.
func TestIngestWritesTimeOrderedRowIDs(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	eventID := "row-id-v7-event"

	events := ingest.NewBuffer(10)
	events.Push(ingest.BufferedEvent{ProjectID: testProject.ID, EventID: &eventID, Timestamp: now, Payload: json.RawMessage(`{"level":"error"}`)})
	logs := ingest.NewLogBuffer(10)
	logs.Push(ingest.BufferedLog{ProjectID: testProject.ID, Timestamp: now, Level: "info", Body: "row-id-v7-log"})
	txs := ingest.NewTransactionBuffer(10)
	txs.Push(ingest.BufferedTransaction{
		ProjectID: testProject.ID, TraceID: "row-id-v7-trace", SpanID: "row-id-root", Transaction: "/row-ids",
		Op: "http.server", Status: "ok", DurationMs: 5, StartTimestamp: now, Timestamp: now.Add(5 * time.Millisecond),
		Spans: []ingest.BufferedSpan{
			{SpanID: "row-id-a", ParentSpanID: "row-id-root", Op: "db.query", StartTimestamp: now, Timestamp: now, DurationMs: 1, Status: "ok"},
			{SpanID: "row-id-b", ParentSpanID: "row-id-root", Op: "view.render", StartTimestamp: now, Timestamp: now, DurationMs: 1, Status: "ok"},
		},
	})

	// A cancelled context drains each buffer once and returns.
	runCtx, cancel := context.WithCancel(ctx)
	cancel()
	events.Run(runCtx, testPool)
	logs.Run(runCtx, testPool)
	txs.Run(runCtx, testPool)

	queries := map[string]string{
		"events":       `SELECT id::text FROM events WHERE project_id = $1 AND event_id = 'row-id-v7-event'`,
		"logs":         `SELECT id::text FROM logs WHERE project_id = $1 AND body = 'row-id-v7-log'`,
		"transactions": `SELECT id::text FROM transactions WHERE project_id = $1 AND trace_id = 'row-id-v7-trace'`,
		"spans":        `SELECT id::text FROM spans WHERE project_id = $1 AND span_id IN ('row-id-a', 'row-id-b')`,
	}
	want := map[string]int{"events": 1, "logs": 1, "transactions": 1, "spans": 2}
	for table, q := range queries {
		rows, err := testPool.Query(ctx, q, testProject.ID)
		if err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		n := 0
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatalf("%s: %v", table, err)
			}
			parsed, err := uuid.Parse(id)
			if err != nil {
				t.Fatalf("%s: %v", table, err)
			}
			if parsed.Version() != 7 {
				t.Errorf("%s id %s is version %d, want 7", table, id, parsed.Version())
			}
			n++
		}
		rows.Close()
		if n != want[table] {
			t.Errorf("%s: got %d rows, want %d", table, n, want[table])
		}
	}
}
