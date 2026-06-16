package digest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/storage"
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
	// Use a nil pool — sendSlot guards against nil pool and returns early,
	// so this is safe regardless of the time of day. What we test is that
	// cancelling the context stops the Run loop.
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

func TestUserDigestSlot_inRange(t *testing.T) {
	ids := []string{"user-1", "user-2", "user-abc", "a", "", "x"}
	for _, id := range ids {
		slot := userDigestSlot(id)
		if slot < 0 || slot >= digestWindowHours {
			t.Errorf("userDigestSlot(%q) = %d, out of range [0, %d)", id, slot, digestWindowHours)
		}
	}
}

func TestUserDigestSlot_deterministic(t *testing.T) {
	id := "some-stable-user-id"
	s1 := userDigestSlot(id)
	s2 := userDigestSlot(id)
	if s1 != s2 {
		t.Errorf("expected same slot on repeated calls, got %d then %d", s1, s2)
	}
}

func TestUserDigestSlot_distributes(t *testing.T) {
	seen := make(map[int]bool)
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("user-%d", i)
		seen[userDigestSlot(id)] = true
	}
	if len(seen) < digestWindowHours {
		t.Errorf("expected all %d slots covered across 200 IDs, only saw %d", digestWindowHours, len(seen))
	}
}

func TestWorker_send_nilPool_returnsWithoutPanic(t *testing.T) {
	w := NewWorker(nil, &mockEmailSender{}, "https://example.com")
	// nil pool returns early before any DB call
	w.send(context.Background(), false)
	w.send(context.Background(), true)
}

func TestWorker_sendToUsers_emptyList(t *testing.T) {
	sender := &mockEmailSender{}
	w := NewWorker(nil, sender, "https://example.com")
	// empty/nil slice should return without sending anything
	w.sendToUsers(context.Background(), nil)
	w.sendToUsers(context.Background(), []storage.DigestUser{})
	if len(sender.messages) != 0 {
		t.Errorf("expected 0 emails for empty user list, got %d", len(sender.messages))
	}
}

func TestWorker_SendNow_nilPool_returnsWithoutPanic(t *testing.T) {
	w := NewWorker(nil, &mockEmailSender{}, "https://example.com")
	w.SendNow(context.Background(), false)
	w.SendNow(context.Background(), true)
}

func TestWorker_buildReport_realPool(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "wr-build-real", "Build Report Real")

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -7)
	seedEvent(t, p.ID, now.AddDate(0, 0, -1))

	w := NewWorker(testPool, &mockEmailSender{}, "https://example.com")
	report, err := w.buildReport(context.Background(), []string{p.ID}, from, now)
	if err != nil {
		t.Fatalf("buildReport: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.TotalErrors < 1 {
		t.Errorf("expected at least 1 total error, got %d", report.TotalErrors)
	}
}

func TestWorker_sendSlot_callsDB(t *testing.T) {
	truncateAll(t)
	// With no users in DB, sendSlot should complete without panic
	w := NewWorker(testPool, &mockEmailSender{}, "https://example.com")
	w.sendSlot(context.Background(), 0)
	w.sendSlot(context.Background(), 1)
}

func TestWorker_sendSlot_nilPool_returnsWithoutPanic(t *testing.T) {
	w := NewWorker(nil, &mockEmailSender{}, "https://example.com")
	// nil pool guard at top of sendSlot — must return without panicking.
	w.sendSlot(context.Background(), 0)
}

func TestWorker_sendToUsers_noProjects_returnsEarly(t *testing.T) {
	truncateAll(t) // removes all projects and cascade-clears everything

	ctx := context.Background()
	var userID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash)
		VALUES ('digest-noprojects@example.com', 'No Projects', 'x')
		ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", userID)
	})

	sender := &mockEmailSender{}
	w := NewWorker(testPool, sender, "https://example.com")
	w.send(ctx, true) // force=true; no projects exist so sendToUsers returns early

	if len(sender.messages) != 0 {
		t.Errorf("expected 0 emails with no projects, got %d", len(sender.messages))
	}
}

func TestWorker_sendToUsers_emailError_doesNotPanic(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "wr-email-err", "Email Error Project")
	seedEvent(t, p.ID, time.Now().UTC().AddDate(0, 0, -1))

	ctx := context.Background()
	var userID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash)
		VALUES ('digest-emailerr@example.com', 'Email Err', 'x')
		ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", userID)
	})

	sender := &mockEmailSender{err: fmt.Errorf("smtp refused")}
	w := NewWorker(testPool, sender, "https://example.com")
	w.send(ctx, true) // error from sender.Send should be logged, not panic

	if len(sender.messages) != 1 {
		t.Errorf("expected 1 send attempt, got %d", len(sender.messages))
	}
}

func TestWorker_send_realPool_withUser(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "wr-send-real", "Send Real Project")
	seedEvent(t, p.ID, time.Now().UTC().AddDate(0, 0, -1))

	ctx := context.Background()
	var userID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash)
		VALUES ('digest-send-worker-test@example.com', 'Send User', 'x')
		ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", userID)
	})

	sender := &mockEmailSender{}
	w := NewWorker(testPool, sender, "https://example.com")
	w.send(ctx, true) // force=true bypasses the 7-day cooldown

	if len(sender.messages) != 1 {
		t.Errorf("expected 1 email sent, got %d", len(sender.messages))
	}
	if len(sender.messages) > 0 && sender.messages[0].To != "digest-send-worker-test@example.com" {
		t.Errorf("unexpected To: %q", sender.messages[0].To)
	}
}
