package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestQueueRetryRetainsPending(t *testing.T) {
	q := newQueue[int]("events", 2, false)
	q.Push(1)
	item := <-q.ch
	attempts := 0
	q.flush(t.Context(), []*queued[int]{item}, func(context.Context, []int) error {
		attempts++
		s := q.Stats()
		if s.Pending != 1 || s.Queued != 0 || s.OldestPendingSeconds <= 0 {
			t.Fatalf("missing in-flight backlog: %+v", s)
		}
		if attempts == 1 {
			return errors.New("connection lost before commit")
		}
		return nil
	})
	s := q.Stats()
	if s.Accepted != 1 || s.Persisted != 1 || s.Pending != 0 || s.Retries != 1 || s.FailedWrites != 1 || s.LastSuccess == nil || s.Degraded {
		t.Fatalf("unexpected recovery: %+v", s)
	}
}

func TestQueueIsolatesInvalidRecord(t *testing.T) {
	q := newQueue[int]("events", 3, false)
	var batch []*queued[int]
	for _, v := range []int{1, 2, 3} {
		q.Push(v)
		batch = append(batch, <-q.ch)
	}
	var persisted []int
	q.flush(t.Context(), batch, func(_ context.Context, values []int) error {
		for _, v := range values {
			if v == 2 {
				return &pgconn.PgError{Code: "22P02"}
			}
		}
		persisted = append(persisted, values...)
		return nil
	})
	s := q.Stats()
	if len(persisted) != 2 || persisted[0] != 1 || persisted[1] != 3 || s.Persisted != 2 || s.Dropped["invalid_record"] != 1 || s.Pending != 0 || s.Retries != 0 {
		t.Fatalf("bad isolation: persisted=%v stats=%+v", persisted, s)
	}
}

func TestQueueUnknownCommitNotRetried(t *testing.T) {
	q := newQueue[int]("logs", 1, true)
	q.Push(1)
	calls := 0
	q.flush(t.Context(), []*queued[int]{<-q.ch}, func(context.Context, []int) error {
		calls++
		return &unknownCommit{errors.New("connection lost during commit")}
	})
	s := q.Stats()
	if calls != 1 || s.Unknown != 1 || s.Persisted != 0 || s.Pending != 0 || len(s.Dropped) != 0 || !s.Degraded {
		t.Fatalf("ambiguous outcome mishandled: %+v", s)
	}
}

func TestQueueExhaustsRetries(t *testing.T) {
	q := newQueue[int]("events", 1, false)
	q.Push(1)
	q.flush(t.Context(), []*queued[int]{<-q.ch}, func(context.Context, []int) error { return errors.New("offline") })
	s := q.Stats()
	if s.FailedWrites != 5 || s.Retries != 4 || s.Dropped["write_failed"] != 1 || s.Pending != 0 {
		t.Fatalf("unexpected retry limit: %+v", s)
	}
}

func TestQueueCancelledDrainCountsRemaining(t *testing.T) {
	q := newQueue[int]("logs", 1, true)
	q.Push(1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	q.flush(ctx, []*queued[int]{<-q.ch}, func(context.Context, []int) error { t.Fatal("write after deadline"); return nil })
	if s := q.Stats(); s.Dropped["deadline"] != 1 || s.Pending != 0 {
		t.Fatalf("lost accounting: %+v", s)
	}
}

func TestQueueOverflowAndConcurrentByteLimit(t *testing.T) {
	q := newQueue[int]("profiles", 100, true)
	q.size = func(int) int64 { return 10 }
	q.maxBytes = 100
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() { q.Push(1) })
	}
	wg.Wait()
	s := q.Stats()
	if s.Accepted != 10 || s.PendingBytes != 100 || s.Dropped["buffer_bytes"] != 90 {
		t.Fatalf("byte cap raced: %+v", s)
	}
	s.Dropped["buffer_bytes"] = 0
	if q.Stats().Dropped["buffer_bytes"] != 90 {
		t.Fatal("snapshot exposes mutable counters")
	}
	events := newQueue[int]("events", 1, false)
	events.Push(1)
	if events.Push(2) || events.Stats().Rejected != 1 {
		t.Fatal("overflow not rejected")
	}
	logs := newQueue[int]("logs", 1, true)
	logs.Push(1)
	if logs.Push(2) || logs.Stats().Dropped["buffer_full"] != 1 {
		t.Fatal("overflow not counted")
	}
}

func TestQueueShutdownFinishesInFlightAndDrains(t *testing.T) {
	q := newQueue[int]("events", 4, false)
	ctx, cancel := context.WithCancel(t.Context())
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	q.Push(1)
	calls := 0
	go func() {
		defer close(done)
		q.run(ctx, 1, func(writeCtx context.Context, values []int) error {
			calls++
			if calls == 1 {
				close(started)
				<-release
			}
			return writeCtx.Err()
		})
	}()
	<-started
	q.Push(2)
	q.Push(3)
	q.StopAccepting()
	cancel()
	if q.Push(4) {
		t.Fatal("accepted after shutdown")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("drain stuck")
	}
	if s := q.Stats(); s.Persisted != 3 || s.Pending != 0 || s.Rejected != 1 {
		t.Fatalf("shutdown lost data: %+v", s)
	}
}

type fakeTransaction struct {
	pgx.Tx
	commitErr             error
	committed, rolledBack bool
}

func (tx *fakeTransaction) Commit(context.Context) error   { tx.committed = true; return tx.commitErr }
func (tx *fakeTransaction) Rollback(context.Context) error { tx.rolledBack = true; return nil }

type fakeStarter struct{ tx *fakeTransaction }

func (s fakeStarter) Begin(context.Context) (pgx.Tx, error) { return s.tx, nil }

func TestAtomicWriteOutcomes(t *testing.T) {
	for _, tt := range []struct {
		name                string
		writeErr, commitErr error
		commit, unknown     bool
	}{
		{name: "success", commit: true},
		{name: "write failed", writeErr: errors.New("transport failure")},
		{name: "commit unknown", commitErr: errors.New("transport failure"), commit: true, unknown: true},
		{name: "commit serialization failure", commitErr: &pgconn.PgError{Code: "40001"}, commit: true},
		{name: "commit fatal", commitErr: &pgconn.PgError{Code: "57P01"}, commit: true, unknown: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tx := &fakeTransaction{commitErr: tt.commitErr}
			err := atomicWrite(t.Context(), fakeStarter{tx}, func(batchSender) error { return tt.writeErr })
			var unknown *unknownCommit
			if tx.committed != tt.commit || !tx.rolledBack || errors.As(err, &unknown) != tt.unknown {
				t.Fatalf("incorrect transaction outcome: tx=%+v err=%v", tx, err)
			}
			if tt.writeErr == nil && tt.commitErr == nil && err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Each logging path must release the queue mutex before invoking a handler.
// A stalled sink may delay that caller, but must not lock out metrics or shutdown.
type blockingQueueLogHandler struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *blockingQueueLogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *blockingQueueLogHandler) Handle(context.Context, slog.Record) error {
	h.once.Do(func() { close(h.entered) })
	<-h.release
	return nil
}
func (h *blockingQueueLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *blockingQueueLogHandler) WithGroup(string) slog.Handler      { return h }

func TestQueueLoggingDoesNotHoldMutex(t *testing.T) {
	for _, action := range []string{"refusal", "write_failure", "abandonment"} {
		t.Run(action, func(t *testing.T) {
			q := newQueue[int]("events", 1, false)
			h := &blockingQueueLogHandler{entered: make(chan struct{}), release: make(chan struct{})}
			q.logger = slog.New(h)
			q.Push(1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				switch action {
				case "refusal":
					q.Push(2)
				case "write_failure":
					q.flush(t.Context(), []*queued[int]{<-q.ch}, func(context.Context, []int) error { return &pgconn.PgError{Code: "22P02"} })
				case "abandonment":
					q.finish([]*queued[int]{<-q.ch}, "deadline")
				}
			}()
			t.Cleanup(func() { close(h.release); <-done })
			select {
			case <-h.entered:
			case <-time.After(time.Second):
				t.Fatal("log handler not reached")
			}
			available := make(chan struct{})
			go func() { q.Stats(); q.StopAccepting(); q.RecordDrop("deadline"); close(available) }()
			select {
			case <-available:
			case <-time.After(time.Second):
				t.Fatal("logging blocked metrics, admission shutdown, or accounting")
			}
		})
	}
}

func TestQueueByteLimitHealthAndRecovery(t *testing.T) {
	for _, usage := range []int{90, 100} {
		t.Run(fmt.Sprint(usage), func(t *testing.T) {
			q := newQueue[int]("profiles", 10, true)
			q.maxBytes = 100
			q.size = func(n int) int64 { return int64(n) }
			q.Push(usage)
			if q.Push(20) {
				t.Fatal("accepted item beyond byte limit")
			}
			s := q.Stats()
			if !s.Degraded || s.CapacityBytes != 100 || s.PendingBytes != int64(usage) || s.Dropped["buffer_bytes"] != 1 {
				t.Fatalf("missing saturation: %+v", s)
			}
			item := <-q.ch
			if !q.Stats().Degraded {
				t.Fatal("dequeue hid bytes still awaiting persistence")
			}
			q.finish([]*queued[int]{item}, "")
			s = q.Stats()
			if s.Degraded || s.PendingBytes != 0 || s.Dropped["buffer_bytes"] != 1 {
				t.Fatalf("bad recovery accounting: %+v", s)
			}
		})
	}
}
