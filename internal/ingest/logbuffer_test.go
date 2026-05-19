package ingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/ingest"
)

func stubLog() ingest.BufferedLog {
	return ingest.BufferedLog{
		ProjectID: testProject.ID,
		Timestamp: time.Now(),
		Level:     "info",
		Body:      "test log message",
	}
}

func TestNewLogBuffer(t *testing.T) {
	buf := ingest.NewLogBuffer(42)
	if buf == nil {
		t.Fatal("NewLogBuffer returned nil")
	}
}

func TestLogBuffer_PushSucceeds(t *testing.T) {
	buf := ingest.NewLogBuffer(2)
	if !buf.Push(stubLog()) {
		t.Fatal("expected Push to return true on non-full buffer")
	}
}

func TestLogBuffer_PushReturnsFalseWhenFull(t *testing.T) {
	buf := ingest.NewLogBuffer(1)
	buf.Push(stubLog()) // fill it
	if buf.Push(stubLog()) {
		t.Fatal("expected Push to return false when buffer is full")
	}
}

func TestLogBuffer_Run_flushesOnShutdown(t *testing.T) {
	ctx := context.Background()

	// Clean up any logs written by this test.
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM logs WHERE project_id = $1", testProject.ID)
	})

	buf := ingest.NewLogBuffer(100)

	// Pre-fill three log entries before starting Run so they are waiting in the channel.
	for range 3 {
		buf.Push(stubLog())
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		buf.Run(runCtx, testPool)
		close(done)
	}()

	// Cancel immediately - Run should drain the buffered items before returning.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancel within 5s")
	}

	var count int
	testPool.QueryRow(ctx,
		"SELECT COUNT(*) FROM logs WHERE project_id = $1", testProject.ID,
	).Scan(&count)
	if count != 3 {
		t.Errorf("expected 3 logs flushed on shutdown, got %d", count)
	}
}
