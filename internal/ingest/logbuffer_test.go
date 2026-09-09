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

func TestLogBuffer_persistsAppUser(t *testing.T) {
	log := stubLog()
	log.Attributes = []byte(`{"user.id":42,"user.username":"alice","user.email":"alice@example.com"}`)
	log.Timestamp = time.Now().UTC().Truncate(time.Microsecond)
	buf := ingest.NewLogBuffer(10)
	if !buf.Push(log) {
		t.Fatal("push failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	buf.Run(ctx, testPool)
	var identity, username, email string
	var lastSeen time.Time
	err := testPool.QueryRow(context.Background(), `SELECT identity,username,email,last_seen FROM app_users WHERE project_id=$1 AND identity='42'`, testProject.ID).Scan(&identity, &username, &email, &lastSeen)
	if err != nil {
		t.Fatal(err)
	}
	if identity != "42" || username != "alice" || email != "alice@example.com" || !lastSeen.Equal(log.Timestamp) {
		t.Fatalf("unexpected user: %q %q %q %v", identity, username, email, lastSeen)
	}
}
