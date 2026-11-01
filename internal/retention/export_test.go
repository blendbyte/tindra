package retention

import "time"

// WithClock replaces the time source behind table budgets and statement
// durations, and the per-table budget itself, so tests can run a table out of
// budget without waiting.
func (w *Worker) WithClock(now func() time.Time, budget time.Duration) *Worker {
	w.now = now
	w.tableBudget = budget
	return w
}

// Batch limits, exported for assertions on batch sizes.
const (
	AgePurgeBatch = agePurgeBatch
	TxPurgeBatch  = txPurgeBatch
)
