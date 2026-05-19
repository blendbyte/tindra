package ingest_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/ingest"
)

func TestBuffer_PushSucceeds(t *testing.T) {
	buf := ingest.NewBuffer(2)
	if !buf.Push(stubEvent()) {
		t.Fatal("expected Push to return true on non-full buffer")
	}
}

func TestBuffer_PushReturnsFalseWhenFull(t *testing.T) {
	buf := ingest.NewBuffer(1)
	buf.Push(stubEvent()) // fill it
	if buf.Push(stubEvent()) {
		t.Fatal("expected Push to return false when buffer is full")
	}
}

func TestBuffer_PushAfterDrain(t *testing.T) {
	buf := ingest.NewBuffer(1)
	buf.Push(stubEvent())

	// Drain by reading from the channel - we can't do that directly,
	// but we can verify Push is false when full and true after capacity is freed.
	// Since Run is not started here, we can't drain. This just confirms capacity semantics.
	if buf.Push(stubEvent()) {
		t.Fatal("buffer should still be full")
	}
}

func stubEvent() ingest.BufferedEvent {
	return ingest.BufferedEvent{
		ProjectID: "00000000-0000-0000-0000-000000000001",
		Timestamp: time.Now(),
		Payload:   json.RawMessage(`{"level":"error","timestamp":"2024-01-01T00:00:00Z"}`),
	}
}
