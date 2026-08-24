package ingest

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BufferedSpan struct {
	SpanID         string
	ParentSpanID   string
	Op             string
	Description    string
	StartTimestamp time.Time
	Timestamp      time.Time
	DurationMs     int
	Status         string
	Data           json.RawMessage
}

type BufferedTransaction struct {
	ProjectID string
	// EventID is the transaction event's own id. A v1 profile names it to say
	// which transaction it belongs to.
	EventID string
	// ProfilerID and ThreadID come from contexts.profile and
	// contexts.trace.data on continuous-profiling events, and are how a
	// transaction finds the profile chunks covering its window.
	ProfilerID     string
	ThreadID       string
	TraceID        string
	SpanID         string
	Transaction    string
	Op             string
	Status         string
	DurationMs     int
	StartTimestamp time.Time
	Timestamp      time.Time
	Environment    string
	Release        string
	Platform       string
	Measurements   json.RawMessage
	Spans          []BufferedSpan
}

type TransactionBuffer struct {
	ch   chan BufferedTransaction
	Hook func(ctx context.Context, pool *pgxpool.Pool, txs []BufferedTransaction, txIDs []string)
}

func NewTransactionBuffer(size int) *TransactionBuffer {
	return &TransactionBuffer{ch: make(chan BufferedTransaction, size)}
}

func (b *TransactionBuffer) Push(tx BufferedTransaction) bool {
	select {
	case b.ch <- tx:
		return true
	default:
		return false
	}
}

// Run is the batch writer loop for transactions. Call in a dedicated goroutine.
func (b *TransactionBuffer) Run(ctx context.Context, pool *pgxpool.Pool) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	batch := make([]BufferedTransaction, 0, 100)

	flush := func(ctx context.Context) {
		if len(batch) == 0 {
			return
		}
		txIDs := writeTxBatch(ctx, pool, batch)
		if b.Hook != nil {
			b.Hook(ctx, pool, batch, txIDs)
		}
		batch = batch[:0]
	}

	for {
		select {
		case tx := <-b.ch:
			batch = append(batch, tx)
			if len(batch) >= 100 {
				flush(ctx)
			}
		case <-ticker.C:
			flush(ctx)
		case <-ctx.Done():
			drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for {
				select {
				case tx := <-b.ch:
					batch = append(batch, tx)
				default:
					flush(drainCtx)
					return
				}
			}
		}
	}
}

func writeTxBatch(ctx context.Context, pool *pgxpool.Pool, batch []BufferedTransaction) []string {
	// Phase 1: insert transactions, collect generated IDs
	txBatch := &pgx.Batch{}
	for _, tx := range batch {
		txBatch.Queue(`
			INSERT INTO transactions
				(project_id, trace_id, span_id, transaction, op, status, duration_ms,
				 start_timestamp, timestamp, environment, release, platform, measurements,
				 event_id, profiler_id, thread_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
			RETURNING id
		`,
			tx.ProjectID, nilStr(tx.TraceID), nilStr(tx.SpanID), tx.Transaction,
			tx.Op, tx.Status, tx.DurationMs, tx.StartTimestamp, tx.Timestamp,
			nilStr(tx.Environment), nilStr(tx.Release), nilStr(tx.Platform),
			nilJSON(tx.Measurements),
			nilStr(tx.EventID), nilStr(tx.ProfilerID), nilStr(tx.ThreadID),
		)
	}

	txResults := pool.SendBatch(ctx, txBatch)
	txIDs := make([]string, len(batch))
	for i := range batch {
		if err := txResults.QueryRow().Scan(&txIDs[i]); err != nil {
			slog.Error("transaction insert", "err", err)
		}
	}
	if err := txResults.Close(); err != nil {
		slog.Error("transaction batch close", "err", err)
	}

	// Phase 2: insert spans referencing the transaction IDs
	type indexedSpan struct {
		txID        string
		projectID   string
		environment string
		release     string
		sp          BufferedSpan
	}
	var toInsert []indexedSpan
	for i, tx := range batch {
		if txIDs[i] == "" {
			continue
		}
		for _, sp := range tx.Spans {
			toInsert = append(toInsert, indexedSpan{txIDs[i], tx.ProjectID, tx.Environment, tx.Release, sp})
		}
	}
	if len(toInsert) == 0 {
		return txIDs
	}

	spanBatch := &pgx.Batch{}
	for _, s := range toInsert {
		spanBatch.Queue(`
			INSERT INTO spans
				(transaction_id, span_id, parent_span_id, op, description,
				 start_timestamp, timestamp, duration_ms, status, data,
				 project_id, environment, release)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		`,
			s.txID, s.sp.SpanID, nilStr(s.sp.ParentSpanID), s.sp.Op, nilStr(s.sp.Description),
			s.sp.StartTimestamp, s.sp.Timestamp, s.sp.DurationMs, s.sp.Status, nilJSON(s.sp.Data),
			s.projectID, nilStr(s.environment), nilStr(s.release),
		)
	}
	spanResults := pool.SendBatch(ctx, spanBatch)
	for range toInsert {
		if _, err := spanResults.Exec(); err != nil {
			slog.Error("span insert", "err", err)
		}
	}
	if err := spanResults.Close(); err != nil {
		slog.Error("span batch close", "err", err)
	}

	// Upsert releases for any transaction that carries a release.
	type releaseKey struct{ projectID, version string }
	seen := map[releaseKey]struct{}{}
	for _, tx := range batch {
		if tx.Release != "" {
			seen[releaseKey{tx.ProjectID, tx.Release}] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return txIDs
	}
	rb := &pgx.Batch{}
	for k := range seen {
		rb.Queue(`INSERT INTO releases (project_id, version) VALUES ($1, $2) ON CONFLICT (project_id, version) DO NOTHING`, k.projectID, k.version)
	}
	rr := pool.SendBatch(ctx, rb)
	for range seen {
		if _, err := rr.Exec(); err != nil {
			slog.Error("release upsert (tx)", "err", err)
		}
	}
	if err := rr.Close(); err != nil {
		slog.Error("release batch close (tx)", "err", err)
	}
	return txIDs
}

func nilStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nilJSON(b json.RawMessage) interface{} {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return b
}
