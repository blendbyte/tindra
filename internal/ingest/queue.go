package ingest

import (
	"container/list"
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Stats counts items, except FailedWrites and Retries, which count batch attempts.
// Persisted includes duplicates already present in the database. Unknown counts
// items whose commit could not be confirmed; it does not claim they were lost.
type Stats struct {
	Accepted             uint64            `json:"accepted"`
	Persisted            uint64            `json:"persisted"`
	Rejected             uint64            `json:"rejected"`
	Dropped              map[string]uint64 `json:"dropped"`
	Unknown              uint64            `json:"unknown"`
	FailedWrites         uint64            `json:"failed_writes"`
	Retries              uint64            `json:"retries"`
	Pending              int               `json:"pending"`
	Queued               int               `json:"queued"`
	Capacity             int               `json:"capacity"`
	CapacityBytes        int64             `json:"capacity_bytes"`
	PendingBytes         int64             `json:"pending_bytes"`
	OldestPendingSeconds float64           `json:"oldest_pending_seconds"`
	LastSuccess          *time.Time        `json:"last_success"`
	Degraded             bool              `json:"degraded"`
}

type queued[T any] struct {
	value   T
	at      time.Time
	bytes   int64
	element *list.Element
}
type queue[T any] struct {
	mu              sync.Mutex
	ch              chan *queued[T]
	pending         list.List
	stats           Stats
	name            string
	closed          bool
	bestEffort      bool
	size            func(T) int64
	maxBytes        int64
	lastWarning     time.Time
	lastAbandonment time.Time
	byteLimited     bool
	logger          *slog.Logger
}

func newQueue[T any](name string, size int, bestEffort bool) *queue[T] {
	return &queue[T]{ch: make(chan *queued[T], size), name: name, bestEffort: bestEffort, logger: slog.Default(), stats: Stats{Dropped: map[string]uint64{}}}
}

func (q *queue[T]) Push(value T) bool {
	q.mu.Lock()
	var size int64
	if q.size != nil {
		size = q.size(value)
	}
	reason := ""
	if q.closed {
		reason = "shutdown"
	} else if q.maxBytes > 0 && q.stats.PendingBytes+size > q.maxBytes {
		reason = "buffer_bytes"
		q.byteLimited = true
	} else if len(q.ch) == cap(q.ch) {
		reason = "buffer_full"
	}
	if reason != "" {
		if q.bestEffort {
			q.stats.Dropped[reason]++
		} else {
			q.stats.Rejected++
		}
		warn := q.allowWarningLocked()
		rejected, dropped := q.stats.Rejected, q.stats.Dropped[reason]
		q.mu.Unlock()
		if warn {
			q.logger.Warn("ingestion buffer refused data", "type", q.name, "reason", reason, "rejected", rejected, "dropped", dropped)
		}
		return false
	}
	item := &queued[T]{value: value, at: time.Now(), bytes: size}
	item.element = q.pending.PushBack(item)
	q.stats.Accepted++
	q.stats.PendingBytes += size
	q.ch <- item
	q.mu.Unlock()
	return true
}

func (q *queue[T]) Stats() Stats {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := q.stats
	s.Dropped = make(map[string]uint64, len(q.stats.Dropped))
	for k, v := range q.stats.Dropped {
		s.Dropped[k] = v
	}
	s.CapacityBytes = q.maxBytes
	s.Pending, s.Queued, s.Capacity = q.pending.Len(), len(q.ch), cap(q.ch)
	if e := q.pending.Front(); e != nil {
		s.OldestPendingSeconds = time.Since(e.Value.(*queued[T]).at).Seconds()
	}
	s.Degraded = s.Degraded || q.byteLimited || s.OldestPendingSeconds > 5 ||
		(s.Capacity > 0 && s.Queued >= s.Capacity) ||
		(s.CapacityBytes > 0 && s.PendingBytes >= s.CapacityBytes)
	return s
}

func (q *queue[T]) finish(batch []*queued[T], reason string) {
	q.mu.Lock()
	n := uint64(len(batch))
	switch reason {
	case "":
		q.stats.Persisted += n
		now := time.Now()
		q.stats.LastSuccess = &now
		q.stats.Degraded = false
	case "unknown_commit":
		q.stats.Unknown += n
	default:
		q.stats.Dropped[reason] += n
	}
	for _, e := range batch {
		q.pending.Remove(e.element)
		q.stats.PendingBytes -= e.bytes
	}
	// A completed batch frees space. Until then a refusal caused by a large
	// incoming item remains visible even when usage is below the byte ceiling.
	if len(batch) > 0 {
		q.byteLimited = false
	}
	report := reason != "" && time.Since(q.lastAbandonment) >= 10*time.Second
	if report {
		q.lastAbandonment = time.Now()
	}
	dropped, unknown := q.stats.Dropped[reason], q.stats.Unknown
	q.mu.Unlock()
	if report {
		q.logger.Error("ingestion batch abandoned", "type", q.name, "reason", reason, "items", n, "dropped", dropped, "unknown", unknown)
	}
}

// unknownCommit is deliberately never retried: replaying a possibly committed
// batch would duplicate logs and transactions that have no deduplication key.
type unknownCommit struct{ error }

func permanent(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return len(pgErr.Code) >= 2 && (pgErr.Code[:2] == "22" || pgErr.Code[:2] == "23")
}

func retryable(err error) bool {
	var unknown *unknownCommit
	if errors.As(err, &unknown) {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "40001" || pgErr.Code == "40P01" || pgErr.Code == "53300" || pgErr.Code == "57P01" || pgErr.Code == "57P02" || pgErr.Code == "57P03" || pgErr.Code == "57014" || (len(pgErr.Code) >= 2 && pgErr.Code[:2] == "08")
	}
	return true // transport, pool acquisition and attempt timeout failures before commit
}

func (q *queue[T]) flush(ctx context.Context, batch []*queued[T], write func(context.Context, []T) error) {
	values := make([]T, len(batch))
	for i, e := range batch {
		values[i] = e.value
	}
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if ctx.Err() != nil {
			err = ctx.Err()
			break
		}
		if attempt > 0 {
			delay := (100 * time.Millisecond << (attempt - 1)) + time.Duration(rand.Int64N(int64(100*time.Millisecond)))
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				err = ctx.Err()
			case <-timer.C:
			}
			if ctx.Err() != nil {
				break
			}
			q.mu.Lock()
			q.stats.Retries++
			q.mu.Unlock()
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = write(attemptCtx, values)
		cancel()
		if err == nil {
			q.finish(batch, "")
			return
		}
		q.mu.Lock()
		q.stats.FailedWrites++
		q.stats.Degraded = true
		warn := q.allowWarningLocked()
		failures := q.stats.FailedWrites
		q.mu.Unlock()
		if warn {
			q.logger.Warn("ingestion write failed", "type", q.name, "items", len(batch), "attempt", attempt+1, "failed_writes", failures, "err", err)
		}
		if !retryable(err) {
			break
		}
	}
	// Data/constraint errors roll back the entire transaction. Split to preserve
	// healthy records, sharing the parent's deadline so isolation stays bounded.
	if permanent(err) && len(batch) > 1 && ctx.Err() == nil {
		mid := len(batch) / 2
		q.flush(ctx, batch[:mid], write)
		q.flush(ctx, batch[mid:], write)
		return
	}
	reason := "write_failed"
	var unknown *unknownCommit
	if errors.As(err, &unknown) {
		reason = "unknown_commit"
	} else if ctx.Err() != nil {
		reason = "deadline"
	} else if permanent(err) {
		reason = "invalid_record"
	}
	q.finish(batch, reason)
}

func (q *queue[T]) run(ctx context.Context, batchSize int, write func(context.Context, []T) error) {
	defer func() { q.logger.Info("ingestion writer stopped", "type", q.name, "stats", q.Stats()) }()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	batch := make([]*queued[T], 0, batchSize)
	flush := func(parent context.Context) {
		if len(batch) == 0 {
			return
		}
		flushCtx, cancel := context.WithTimeout(parent, 30*time.Second)
		q.flush(flushCtx, batch, write)
		cancel()
		clear(batch)
		batch = batch[:0]
	}
	for {
		// Prefer shutdown over a ready ticker/channel so no new 30-second flush
		// starts after cancellation. An already-running flush keeps its own budget.
		if ctx.Err() != nil {
			q.StopAccepting()
			drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for {
				select {
				case e := <-q.ch:
					batch = append(batch, e)
					if len(batch) >= batchSize {
						flush(drainCtx)
					}
				default:
					flush(drainCtx)
					return
				}
			}
		}
		select {
		case e := <-q.ch:
			batch = append(batch, e)
			if len(batch) >= batchSize && ctx.Err() == nil {
				flush(context.WithoutCancel(ctx))
			}
		case <-ticker.C:
			if ctx.Err() == nil {
				flush(context.WithoutCancel(ctx))
			}
		case <-ctx.Done():
		}
	}

}

// batchSender allows primary records and their dependent writes to share a transaction.
type batchSender interface {
	SendBatch(context.Context, *pgx.Batch) pgx.BatchResults
}

type transactionStarter interface {
	Begin(context.Context) (pgx.Tx, error)
}

func atomicWrite(ctx context.Context, pool transactionStarter, write func(batchSender) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	if err := write(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		var pgErr *pgconn.PgError
		if errors.Is(err, pgx.ErrTxCommitRollback) || (errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01" || permanent(err))) {
			return err
		}
		return &unknownCommit{err}
	}
	return nil
}

func execBatch(ctx context.Context, db batchSender, batch *pgx.Batch) error {
	if batch.Len() == 0 {
		return nil
	}
	results := db.SendBatch(ctx, batch)
	var first error
	for range batch.Len() {
		if _, err := results.Exec(); first == nil && err != nil {
			first = err
		}
	}
	closeErr := results.Close()
	if first != nil {
		return first
	}
	return closeErr
}

// StopAccepting closes admission without closing the channel or racing producers.
func (q *queue[T]) StopAccepting() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
}

// RecordDrop counts a parsed item discarded before it could enter the queue.
func (q *queue[T]) RecordDrop(reason string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.stats.Dropped[reason]++
}

// Caller holds mu. Logging uses snapshots after releasing the lock, so slow
// log output cannot block admission, accounting, or metrics on the mutex.
func (q *queue[T]) allowWarningLocked() bool {
	if time.Since(q.lastWarning) < 10*time.Second {
		return false
	}
	q.lastWarning = time.Now()
	return true
}
