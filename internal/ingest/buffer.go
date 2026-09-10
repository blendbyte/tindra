package ingest

import (
	"bytes"
	"context"
	"encoding/json"
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
	*queue[BufferedEvent]
}

func NewBuffer(size int) *Buffer {
	q := newQueue[BufferedEvent]("events", size, false)
	return &Buffer{queue: q}
}

// Push normalizes the payload once so primary and enrichment writes use the
// same data, including on retries. The caller's payload is not modified.
func (b *Buffer) Push(e BufferedEvent) bool {
	e.Payload = sanitizeJSONPayload(e.Payload)
	return b.queue.Push(e)
}

// Run flushes bounded batches and drains on cancellation. Stop producers first.
func (b *Buffer) Run(ctx context.Context, pool *pgxpool.Pool) {
	b.run(ctx, 1000, func(ctx context.Context, batch []BufferedEvent) error {
		start := time.Now()
		defer func() {
			stats := b.Stats()
			b.logger.Debug("event flush", "attempted", len(batch), "write_ms", time.Since(start).Milliseconds(), "queued", stats.Queued, "rejected", stats.Rejected)
		}()
		err := atomicWrite(ctx, pool, func(db batchSender) error { return writeBatch(ctx, db, batch) })
		if err != nil {
			ids := make([]string, 0, len(batch))
			for _, event := range batch {
				ids = append(ids, event.ProjectID)
			}
			recordSetupWriteFailure(ctx, pool, "events", ids, err)
		}
		return err
	})
}

// sanitizeJSONPayload removes Unicode null escapes rejected by PostgreSQL JSONB.
func sanitizeJSONPayload(p json.RawMessage) json.RawMessage {
	const nullEscape = `\u0000`
	if !bytes.Contains(p, []byte(nullEscape)) {
		return p
	}
	var cleaned json.RawMessage
	start := 0
	for i := 0; i < len(p); i++ {
		if p[i] != '\\' {
			continue
		}
		if bytes.HasPrefix(p[i:], []byte(nullEscape)) {
			if cleaned == nil {
				cleaned = make(json.RawMessage, 0, len(p))
			}
			cleaned = append(cleaned, p[start:i]...)
			i += len(nullEscape) - 1
			start = i + 1
		} else {
			// Skip the escaped byte. In particular, \\u0000 is literal text,
			// whereas \\\u0000 is an escaped backslash followed by a null escape.
			i++
		}
	}
	if cleaned == nil {
		return p
	}
	return append(cleaned, p[start:]...)
}

func writeBatch(ctx context.Context, pool batchSender, batch []BufferedEvent) error {
	b := &pgx.Batch{}
	rows := make([][]any, 0, len(batch))
	for _, e := range batch {
		rows = append(rows, []any{e.ProjectID, e.EventID, e.Timestamp, e.Payload, nilStr(e.TraceID), nilStr(e.SpanID)})
	}
	queueInserts(b, "INSERT INTO events (project_id, event_id, timestamp, payload, trace_id, span_id) VALUES ", " ON CONFLICT (project_id, event_id) WHERE event_id IS NOT NULL DO NOTHING", rows, 0)
	if err := execBatch(ctx, pool, b); err != nil {
		return err
	}

	var appUsers []AppUserRow
	for _, e := range batch {
		u := ParseSentryUserFromPayload(e.Payload)
		if u.Identity == "" {
			continue
		}
		appUsers = append(appUsers, AppUserRow{ProjectID: e.ProjectID, User: u, LastSeen: e.Timestamp})
	}
	if err := UpsertAppUsers(ctx, pool, appUsers); err != nil {
		return err
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
		return nil
	}
	rb := &pgx.Batch{}
	for k := range seen {
		rb.Queue(`INSERT INTO releases (project_id, version) VALUES ($1, $2) ON CONFLICT (project_id, version) DO NOTHING`, k.projectID, k.version)
	}
	if err := execBatch(ctx, pool, rb); err != nil {
		return err
	}
	return nil
}
