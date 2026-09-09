package ingest

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const MaxLogBody = 2048

type BufferedLog struct {
	ProjectID   string
	Timestamp   time.Time
	Level       string
	Body        string
	TraceID     string
	SpanID      string
	Environment string
	Release     string
	Attributes  json.RawMessage
}

type LogBuffer struct {
	*queue[BufferedLog]
}

func NewLogBuffer(size int) *LogBuffer {
	q := newQueue[BufferedLog]("logs", size, true)
	return &LogBuffer{queue: q}
}

// Run flushes bounded batches and drains on cancellation. Stop producers first.
func (b *LogBuffer) Run(ctx context.Context, pool *pgxpool.Pool) {
	b.run(ctx, 1000, func(ctx context.Context, batch []BufferedLog) error {
		start := time.Now()
		defer func() {
			stats := b.Stats()
			b.logger.Debug("log flush", "attempted", len(batch), "write_ms", time.Since(start).Milliseconds(), "queued", stats.Queued, "rejected", stats.Rejected)
		}()
		return atomicWrite(ctx, pool, func(db batchSender) error { return writeLogBatch(ctx, db, batch) })
	})
}

func writeLogBatch(ctx context.Context, pool batchSender, batch []BufferedLog) error {
	b := &pgx.Batch{}
	rows := make([][]any, 0, len(batch))
	for _, l := range batch {
		rows = append(rows, []any{l.ProjectID, l.Timestamp, l.Level, l.Body, nilStr(l.TraceID), nilStr(l.SpanID), nilStr(l.Environment), nilStr(l.Release), nilJSONDefault(l.Attributes)})
	}
	queueInserts(b, "INSERT INTO logs (project_id, timestamp, level, body, trace_id, span_id, environment, release, attributes) VALUES ", "", rows, 0)
	if err := execBatch(ctx, pool, b); err != nil {
		return err
	}

	var appUsers []AppUserRow
	for _, l := range batch {
		var attrs map[string]any
		if len(l.Attributes) > 0 {
			_ = json.Unmarshal(l.Attributes, &attrs)
		}
		u := ParseSentryUserFromAttrs(attrs)
		if u.Identity == "" {
			continue
		}
		appUsers = append(appUsers, AppUserRow{ProjectID: l.ProjectID, User: u, LastSeen: l.Timestamp})
	}
	if err := UpsertAppUsers(ctx, pool, appUsers); err != nil {
		return err
	}
	return nil
}

func nilJSONDefault(b json.RawMessage) any {
	if len(b) == 0 || string(b) == "null" {
		return []byte("{}")
	}
	return b
}
