package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BufferedEvent struct {
	ProjectID string
	EventID   *string // nil if absent from envelope header
	Timestamp time.Time
	Payload   json.RawMessage
	TraceID   string // from contexts.trace.trace_id; empty if absent
	SpanID    string // from contexts.trace.span_id; empty if absent
}

type Buffer struct {
	ch chan BufferedEvent
}

func NewBuffer(size int) *Buffer {
	return &Buffer{ch: make(chan BufferedEvent, size)}
}

// Push adds an event to the buffer. Returns false if the buffer is full.
func (b *Buffer) Push(e BufferedEvent) bool {
	select {
	case b.ch <- e:
		return true
	default:
		return false
	}
}

// Run is the batch writer loop. It flushes to Postgres every 200ms or when
// 1000 events accumulate, whichever comes first. Call in a dedicated goroutine.
// Drains remaining events on context cancellation before returning.
func (b *Buffer) Run(ctx context.Context, pool *pgxpool.Pool) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	batch := make([]BufferedEvent, 0, 1000)

	flush := func(ctx context.Context) {
		if len(batch) == 0 {
			return
		}
		writeBatch(ctx, pool, batch)
		batch = batch[:0]
	}

	for {
		select {
		case e := <-b.ch:
			batch = append(batch, e)
			if len(batch) >= 1000 {
				flush(ctx)
			}
		case <-ticker.C:
			flush(ctx)
		case <-ctx.Done():
			drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for {
				select {
				case e := <-b.ch:
					batch = append(batch, e)
				default:
					flush(drainCtx)
					return
				}
			}
		}
	}
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// sanitizeJSONPayload removes backslash-u0000 escape sequences from a JSON payload.
// PostgreSQL JSONB rejects the Unicode null escape (SQLSTATE 22P05), which
// some SDKs (e.g. PHP) embed in exception messages and stack frames.
func sanitizeJSONPayload(p json.RawMessage) json.RawMessage {
	return bytes.ReplaceAll(p, []byte("\\u0000"), []byte{})
}

func writeBatch(ctx context.Context, pool *pgxpool.Pool, batch []BufferedEvent) {
	b := &pgx.Batch{}
	for _, e := range batch {
		b.Queue(`
			INSERT INTO events (project_id, event_id, timestamp, payload, trace_id, span_id)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (project_id, event_id) WHERE event_id IS NOT NULL DO NOTHING
		`, e.ProjectID, e.EventID, e.Timestamp, sanitizeJSONPayload(e.Payload), nullableString(e.TraceID), nullableString(e.SpanID))
	}
	results := pool.SendBatch(ctx, b)
	for range batch {
		if _, err := results.Exec(); err != nil {
			slog.Error("event insert", "err", err)
		}
	}
	if err := results.Close(); err != nil {
		slog.Error("batch flush", "err", err)
	}

	// Upsert releases for any event that carries a release field.
	type releaseKey struct{ projectID, version string }
	seen := map[releaseKey]struct{}{}
	for _, e := range batch {
		var partial struct {
			Release string `json:"release"`
		}
		if json.Unmarshal(e.Payload, &partial) == nil && partial.Release != "" {
			seen[releaseKey{e.ProjectID, partial.Release}] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return
	}
	rb := &pgx.Batch{}
	for k := range seen {
		rb.Queue(`INSERT INTO releases (project_id, version) VALUES ($1, $2) ON CONFLICT (project_id, version) DO NOTHING`, k.projectID, k.version)
	}
	rr := pool.SendBatch(ctx, rb)
	for range seen {
		if _, err := rr.Exec(); err != nil {
			slog.Error("release upsert", "err", err)
		}
	}
	if err := rr.Close(); err != nil {
		slog.Error("release batch close", "err", err)
	}
}
