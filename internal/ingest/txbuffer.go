package ingest

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
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
	UserIdentity   string
	UserID         string
	UserUsername   string
	UserEmail      string
	UserName       string
}

type TransactionBuffer struct {
	*queue[BufferedTransaction]
	Hook func(context.Context, *pgxpool.Pool, []BufferedTransaction, []string)
}

func NewTransactionBuffer(size int) *TransactionBuffer {
	q := newQueue[BufferedTransaction]("transactions", size, false)
	return &TransactionBuffer{queue: q}
}

// Run flushes bounded batches and drains on cancellation. Stop producers first.
func (b *TransactionBuffer) Run(ctx context.Context, pool *pgxpool.Pool) {
	b.run(ctx, 100, func(ctx context.Context, batch []BufferedTransaction) error {
		start := time.Now()
		var ids []string
		err := atomicWrite(ctx, pool, func(db batchSender) error {
			var err error
			ids, err = writeTxBatch(ctx, db, batch)
			return err
		})
		writtenAt := time.Now()
		if err == nil && b.Hook != nil {
			b.Hook(ctx, pool, batch, ids)
		}
		stats := b.Stats()
		b.logger.Debug("transaction flush", "attempted", len(batch), "write_ms", writtenAt.Sub(start).Milliseconds(), "hook_ms", time.Since(writtenAt).Milliseconds(), "queued", stats.Queued, "rejected", stats.Rejected)
		return err
	})
}

func writeTxBatch(ctx context.Context, pool batchSender, batch []BufferedTransaction) ([]string, error) {
	// Phase 1: explicit IDs preserve input-to-span linkage across grouped inserts.
	txBatch := &pgx.Batch{}
	txIDs := make([]string, len(batch))
	rows := make([][]any, 0, len(batch))
	for i, tx := range batch {
		var id [16]byte
		_, _ = rand.Read(id[:])
		id[6] = (id[6] & 0x0f) | 0x40
		id[8] = (id[8] & 0x3f) | 0x80
		txIDs[i] = fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
		rows = append(rows, []any{txIDs[i], tx.ProjectID, nilStr(tx.TraceID), nilStr(tx.SpanID), tx.Transaction,
			tx.Op, tx.Status, tx.DurationMs, tx.StartTimestamp, tx.Timestamp,
			nilStr(tx.Environment), nilStr(tx.Release), nilStr(tx.Platform), nilJSON(tx.Measurements),
			nilStr(tx.EventID), nilStr(tx.ProfilerID), nilStr(tx.ThreadID),
			nilStr(tx.UserIdentity), nilStr(tx.UserID), nilStr(tx.UserUsername), nilStr(tx.UserEmail), nilStr(tx.UserName)})
	}
	queueInserts(txBatch, `INSERT INTO transactions
		(id,project_id,trace_id,span_id,transaction,op,status,duration_ms,start_timestamp,timestamp,
		 environment,release,platform,measurements,event_id,profiler_id,thread_id,
		 user_identity,user_id,user_username,user_email,user_name) VALUES `, "", rows, 1)
	if err := execBatch(ctx, pool, txBatch); err != nil {
		return nil, err
	}

	var appUsers []AppUserRow
	for _, tx := range batch {
		if tx.UserIdentity == "" {
			continue
		}
		appUsers = append(appUsers, AppUserRow{
			ProjectID: tx.ProjectID,
			User: SentryUser{
				Identity: tx.UserIdentity,
				ID:       tx.UserID,
				Username: tx.UserUsername,
				Email:    tx.UserEmail,
				Name:     tx.UserName,
			},
			LastSeen: tx.Timestamp,
		})
	}
	if err := UpsertAppUsers(ctx, pool, appUsers); err != nil {
		return nil, err
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
	if err := execBatch(ctx, pool, spanBatch); err != nil {
		return nil, err
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
		return txIDs, nil
	}
	rb := &pgx.Batch{}
	for k := range seen {
		rb.Queue(`INSERT INTO releases (project_id, version) VALUES ($1, $2) ON CONFLICT (project_id, version) DO NOTHING`, k.projectID, k.version)
	}
	if err := execBatch(ctx, pool, rb); err != nil {
		return nil, err
	}
	return txIDs, nil
}

func nilStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nilJSON(b json.RawMessage) any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return b
}
