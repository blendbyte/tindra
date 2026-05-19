package digest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/alerts"
)

type mockEmailSender struct {
	mu       sync.Mutex
	messages []alerts.EmailMessage
	err      error
}

func (m *mockEmailSender) Send(_ context.Context, msg alerts.EmailMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
	return m.err
}

func TestNewWorker_returnsNonNil(t *testing.T) {
	w := NewWorker(nil, nil, "https://example.com")
	if w == nil {
		t.Error("expected non-nil Worker")
	}
}

func TestNewWorker_storesFields(t *testing.T) {
	sender := &mockEmailSender{}
	w := NewWorker(nil, sender, "https://example.com")
	if w.email != sender {
		t.Error("expected worker to store the email sender")
	}
	if w.publicURL != "https://example.com" {
		t.Errorf("publicURL: got %q, want %q", w.publicURL, "https://example.com")
	}
}

func TestWorker_Run_nilEmailReturnsEarly(t *testing.T) {
	w := NewWorker(nil, nil, "https://example.com")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("Run with nil email should return quickly, but it blocked")
	}
}

func TestWorker_Run_cancelStopsLoop(t *testing.T) {
	sender := &mockEmailSender{}
	// Use a nil pool so sendDue will fail fast if it tries to query; but since
	// sendDue only runs in the 07-09 UTC window, it may not fire at all.
	// What we test is that cancelling the context stops the Run loop.
	w := NewWorker(nil, sender, "https://example.com")
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("Run loop should stop after context cancellation")
	}
}

func TestWorker_sendDue_outsideWindow(t *testing.T) {
	// sendDue only fires between 07:00 and 09:00 UTC. We test that it silently
	// does nothing at midnight (no panic, no call to pool) by passing a nil pool.
	// The function checks the hour first, so with an arbitrary time outside
	// the window it returns before touching the pool.
	//
	// We can't control time.Now() directly, so we test the observable behavior:
	// if the current UTC hour is outside [7,9), sendDue must not panic with a nil pool.
	// If the test happens to run inside the window, this test is a no-op and still passes.
	w := NewWorker(nil, &mockEmailSender{}, "https://example.com")
	hour := time.Now().UTC().Hour()
	if hour < 7 || hour >= 9 {
		// Safe to call: outside window, returns before using pool
		w.sendDue(context.Background())
	}
}

func TestWorker_buildReport_nilPool_returnsError(t *testing.T) {
	w := NewWorker(nil, &mockEmailSender{}, "https://example.com")
	from := time.Now().UTC().AddDate(0, 0, -7)
	to := time.Now().UTC()
	_, err := w.buildReport(context.Background(), []string{"proj-1"}, from, to)
	if err == nil {
		t.Error("expected error when pool is nil")
	}
}
