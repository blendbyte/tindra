package retention_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/retention"
)

// stepClock only moves when a test advances it, so table budgets run out
// exactly where the test says.
type stepClock struct{ t time.Time }

func newStepClock() *stepClock               { return &stepClock{t: time.Unix(0, 0)} }
func (c *stepClock) Now() time.Time          { return c.t }
func (c *stepClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// wantBatches lists the batch sizes a drain of total rows reports: full
// batches, then the short batch that tells it the table caught up.
func wantBatches(total, size int64) []int64 {
	var out []int64
	for ; total >= size; total -= size {
		out = append(out, size)
	}
	return append(out, total)
}

// Events go first in a pass. Their first batch alone overruns the budget, yet
// every later table still gets its own full budget in the same pass.
func TestWorkerSlowTableDoesNotStarveOthers(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	issue := seedRetainedIssue(t, "open", 12000, 7)
	seedAgeRows(t, "logs", 12000, 7)
	seedUptimeBacklog(t, 12000, 7)
	clock := newStepClock()
	trace := &capTrace{matchSQL: "DELETE FROM events", afterBatch: func() { clock.Advance(2 * time.Minute) }}
	worker := retention.NewWorker(capPool(t, trace), 90).WithClock(clock.Now, time.Minute)
	require.True(t, worker.RunOnce(ctx), "events are still behind")
	require.Equal(t, []int64{5000}, trace.batches)
	require.Equal(t, 7007, capCount(t, "events"))
	require.Equal(t, 7007, issueEventCount(t, issue))
	require.Equal(t, 7, capCount(t, "logs"))
	require.Equal(t, 7, capCount(t, "uptime_checks"))
}

// A slow statement shrinks the next batch in proportion to the overrun, and
// fast ones grow it back toward the table's maximum.
func TestWorkerBatchSizeAdaptsToStatementDuration(t *testing.T) {
	seedAgeRows(t, "logs", 12000, 7)
	clock := newStepClock()
	trace := &capTrace{matchSQL: "DELETE FROM logs"}
	trace.afterBatch = func() {
		if len(trace.batches) == 1 {
			clock.Advance(4 * time.Second)
		}
	}
	require.False(t, retention.NewWorker(capPool(t, trace), 90).WithClock(clock.Now, time.Hour).RunOnce(context.Background()))
	require.Equal(t, []int64{5000, 1250, 2500, 3250}, trace.batches)
	require.Equal(t, 7, capCount(t, "logs"))
}

// The adapted size carries over to the next pass, so a table with heavy
// cascades does not open every pass with a slow statement.
func TestWorkerBatchSizeCarriesAcrossPasses(t *testing.T) {
	seedAgeRows(t, "logs", 12000, 7)
	clock := newStepClock()
	trace := &capTrace{matchSQL: "DELETE FROM logs"}
	trace.afterBatch = func() { clock.Advance(2 * time.Second) }
	worker := retention.NewWorker(capPool(t, trace), 90).WithClock(clock.Now, time.Second)
	require.True(t, worker.RunOnce(context.Background()))
	require.True(t, worker.RunOnce(context.Background()))
	require.Equal(t, []int64{5000, 2500}, trace.batches)
}
