package ingest

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProfileBuffer batches profile writes the same way TransactionBuffer batches
// transactions, with one deliberate difference: callers push already-compressed
// blobs. Compression happens on the request goroutine so that a full buffer
// holds tens of MB rather than the gigabytes raw sample data would occupy.
type ProfileBuffer struct {
	ch chan BufferedProfile
}

// NewProfileBuffer sizes the queue. Profiles are far larger than events, so
// this is configured well below INGEST_BUFFER_SIZE.
func NewProfileBuffer(size int) *ProfileBuffer {
	return &ProfileBuffer{ch: make(chan BufferedProfile, size)}
}

// Push queues a profile, reporting false when the buffer is full.
func (b *ProfileBuffer) Push(p BufferedProfile) bool {
	select {
	case b.ch <- p:
		return true
	default:
		return false
	}
}

// Run is the batch writer loop for profiles. Call in a dedicated goroutine.
func (b *ProfileBuffer) Run(ctx context.Context, pool *pgxpool.Pool) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	// Batches are smaller than the transaction writer's: each row carries a
	// blob, so a hundred of them at once would be a large single statement.
	const batchSize = 20
	batch := make([]BufferedProfile, 0, batchSize)

	flush := func(ctx context.Context) {
		if len(batch) == 0 {
			return
		}
		writeProfileBatch(ctx, pool, batch)
		batch = batch[:0]
	}

	for {
		select {
		case p := <-b.ch:
			batch = append(batch, p)
			if len(batch) >= batchSize {
				flush(ctx)
			}
		case <-ticker.C:
			flush(ctx)
		case <-ctx.Done():
			drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for {
				select {
				case p := <-b.ch:
					batch = append(batch, p)
				default:
					flush(drainCtx)
					return
				}
			}
		}
	}
}

func writeProfileBatch(ctx context.Context, pool *pgxpool.Pool, batch []BufferedProfile) {
	pb := &pgx.Batch{}
	for _, p := range batch {
		pb.Queue(`
			INSERT INTO profile_chunks
				(project_id, format, transaction_event_id, trace_id, profiler_id, chunk_id,
				 start_ts, end_ts, environment, release, platform,
				 sample_count, size_bytes, encoding, data)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		`,
			p.ProjectID, int16(p.Format), nilStr(p.TransactionEventID), nilStr(p.TraceID),
			nilStr(p.ProfilerID), nilStr(p.ChunkID),
			p.StartTs, p.EndTs, nilStr(p.Environment), nilStr(p.Release), nilStr(p.Platform),
			p.SampleCount, p.SizeBytes(), p.Encoding, p.Data,
		)
	}

	results := pool.SendBatch(ctx, pb)
	for range batch {
		if _, err := results.Exec(); err != nil {
			slog.Error("profile insert", "err", err)
		}
	}
	if err := results.Close(); err != nil {
		slog.Error("profile batch close", "err", err)
	}
}
