package ingest

import "sync/atomic"

// BufferStats is a point-in-time queue observation, not a committed-row count.
// Accepted and Rejected are cumulative for this process lifetime.
type BufferStats struct {
	Queued, Capacity   int
	Accepted, Rejected uint64
}

type bufferCounters struct{ accepted, rejected atomic.Uint64 }

func (b *Buffer) Stats() BufferStats {
	return BufferStats{len(b.ch), cap(b.ch), b.counters.accepted.Load(), b.counters.rejected.Load()}
}
func (b *TransactionBuffer) Stats() BufferStats {
	return BufferStats{len(b.ch), cap(b.ch), b.counters.accepted.Load(), b.counters.rejected.Load()}
}
func (b *LogBuffer) Stats() BufferStats {
	return BufferStats{len(b.ch), cap(b.ch), b.counters.accepted.Load(), b.counters.rejected.Load()}
}
