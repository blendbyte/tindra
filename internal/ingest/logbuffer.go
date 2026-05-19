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
	ch chan BufferedLog
}

func NewLogBuffer(size int) *LogBuffer {
	return &LogBuffer{ch: make(chan BufferedLog, size)}
}

func (b *LogBuffer) Push(l BufferedLog) bool {
	select {
	case b.ch <- l:
		return true
	default:
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
		writeLogBatch(ctx, pool, batch)
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
	for _, l := range batch {
		b.Queue(`
			INSERT INTO logs
				(project_id, timestamp, level, body, trace_id, span_id, environment, release, attributes)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		`,
			l.ProjectID,
			l.Timestamp,
			l.Level,
			l.Body,
			nilStr(l.TraceID),
			nilStr(l.SpanID),
			nilStr(l.Environment),
			nilStr(l.Release),
			nilJSONDefault(l.Attributes),
		)
	}
	results := pool.SendBatch(ctx, b)
	defer results.Close()
	for range batch {
		if _, err := results.Exec(); err != nil {
			slog.Error("log insert", "err", err)
		}
	}
	if err := results.Close(); err != nil {
		slog.Error("log batch flush", "err", err)
	}
}

func nilJSONDefault(b json.RawMessage) any {
	if len(b) == 0 || string(b) == "null" {
		return []byte("{}")
	}
	return b
}
