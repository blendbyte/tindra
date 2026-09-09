package ingest

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProfileBuffer batches profile writes the same way TransactionBuffer batches
// transactions, with one deliberate difference: callers push already-compressed
// blobs. Compression happens on the request goroutine so that a full buffer
// holds tens of MB rather than the gigabytes raw sample data would occupy.
type ProfileBuffer struct {
	*queue[BufferedProfile]
}

const maxQueuedProfileBytes = 128 << 20

func NewProfileBuffer(size int) *ProfileBuffer {
	q := newQueue[BufferedProfile]("profiles", size, true)
	q.size = func(p BufferedProfile) int64 { return int64(p.SizeBytes()) }
	q.maxBytes = maxQueuedProfileBytes
	return &ProfileBuffer{queue: q}
}

func (b *ProfileBuffer) QueuedBytes() int64 { return b.Stats().PendingBytes }

// Run flushes bounded batches and drains on cancellation. Stop producers first.
func (b *ProfileBuffer) Run(ctx context.Context, pool *pgxpool.Pool) {
	b.run(ctx, 20, func(ctx context.Context, batch []BufferedProfile) error {
		return atomicWrite(ctx, pool, func(db batchSender) error { return writeProfileBatch(ctx, db, batch) })
	})
}

func writeProfileBatch(ctx context.Context, pool batchSender, batch []BufferedProfile) error {
	// ON CONFLICT DO NOTHING below leans on the partial unique indexes: a
	// retried envelope must not fold into the graph twice.
	pb := &pgx.Batch{}
	for _, p := range batch {
		pb.Queue(`
			INSERT INTO profile_chunks
				(project_id, format, transaction_event_id, trace_id, profiler_id, chunk_id,
				 start_ts, end_ts, environment, release, platform,
				 sample_count, size_bytes, encoding, data)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			ON CONFLICT DO NOTHING
		`,
			p.ProjectID, int16(p.Format), nilStr(p.TransactionEventID), nilStr(p.TraceID),
			nilStr(p.ProfilerID), nilStr(p.ChunkID),
			p.StartTs, p.EndTs, nilStr(p.Environment), nilStr(p.Release), nilStr(p.Platform),
			p.SampleCount, p.SizeBytes(), p.Encoding, p.Data,
		)
	}

	if err := execBatch(ctx, pool, pb); err != nil {
		return err
	}
	return nil
}
