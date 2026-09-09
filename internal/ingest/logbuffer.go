package ingest

import (
	"context"
	"encoding/json"
	"log/slog"
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
	counters bufferCounters
	ch       chan BufferedLog
}

func NewLogBuffer(size int) *LogBuffer {
	return &LogBuffer{ch: make(chan BufferedLog, size)}
}

func (b *LogBuffer) Push(l BufferedLog) bool {
	select {
	case b.ch <- l:
		b.counters.accepted.Add(1)
		return true
	default:
		b.counters.rejected.Add(1)
		return false
	}
}

// Run is the batch writer loop for logs. Call in a dedicated goroutine.
func (b *LogBuffer) Run(ctx context.Context, pool *pgxpool.Pool) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	batch := make([]BufferedLog, 0, 1000)

	flush := func(ctx context.Context) {
		if len(batch) == 0 {
			return
		}
		start := time.Now()
		writeLogBatch(ctx, pool, batch)
		stats := b.Stats()
		slog.Debug("logbuffer flush", "attempted", len(batch), "duration_ms", time.Since(start).Milliseconds(), "queued", stats.Queued, "rejected", stats.Rejected)
		batch = batch[:0]
	}

	for {
		select {
		case l := <-b.ch:
			batch = append(batch, l)
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
				case l := <-b.ch:
					batch = append(batch, l)
				default:
					flush(drainCtx)
					return
				}
			}
		}
	}
}

func writeLogBatch(ctx context.Context, pool *pgxpool.Pool, batch []BufferedLog) {
	b := &pgx.Batch{}
	rows := make([][]any, 0, len(batch))
	for _, l := range batch {
		rows = append(rows, []any{l.ProjectID, l.Timestamp, l.Level, l.Body, nilStr(l.TraceID), nilStr(l.SpanID), nilStr(l.Environment), nilStr(l.Release), nilJSONDefault(l.Attributes)})
	}
	statements := queueInserts(b, "INSERT INTO logs (project_id, timestamp, level, body, trace_id, span_id, environment, release, attributes) VALUES ", "", rows, 0)
	results := pool.SendBatch(ctx, b)
	for range statements {
		if _, err := results.Exec(); err != nil {
			slog.Error("log insert", "err", err)
		}
	}
	if err := results.Close(); err != nil {
		slog.Error("log batch flush", "err", err)
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
	UpsertAppUsers(ctx, pool, appUsers)
}

func nilJSONDefault(b json.RawMessage) any {
	if len(b) == 0 || string(b) == "null" {
		return []byte("{}")
	}
	return b
}
