package background

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/testutil"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	pool, cleanup := testutil.SetupDB(context.Background())
	testPool = pool
	leaderRetry = 50 * time.Millisecond
	restartDelay = 20 * time.Millisecond
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// Two instances compete for leadership: only one runs the singleton job at a
// time, and the standby takes over when the leader stops.
func TestSingletonRunsOnOneInstanceAndFailsOver(t *testing.T) {
	var running, maxRunning, starts atomic.Int32
	job := Job{Name: "singleton", Singleton: true, Run: func(ctx context.Context) {
		starts.Add(1)
		n := running.Add(1)
		for {
			m := maxRunning.Load()
			if n <= m || maxRunning.CompareAndSwap(m, n) {
				break
			}
		}
		<-ctx.Done()
		running.Add(-1)
	}}

	ctxA, cancelA := context.WithCancel(context.Background())
	waitA := Start(ctxA, testPool, []Job{job})
	require.Eventually(t, func() bool { return running.Load() == 1 }, 5*time.Second, 10*time.Millisecond)

	ctxB, cancelB := context.WithCancel(context.Background())
	waitB := Start(ctxB, testPool, []Job{job})
	time.Sleep(300 * time.Millisecond) // several standby retries
	require.EqualValues(t, 1, maxRunning.Load(), "only the leader may run the job")

	cancelA()
	waitA()
	require.Eventually(t, func() bool { return running.Load() == 1 && starts.Load() == 2 }, 5*time.Second, 10*time.Millisecond,
		"the standby must take over after the leader stops")
	require.EqualValues(t, 1, maxRunning.Load())

	cancelB()
	waitB()
	require.Zero(t, running.Load(), "wait returns only after the job stopped")
}

// Non-singleton jobs run on every instance.
func TestNonSingletonRunsEverywhere(t *testing.T) {
	var running atomic.Int32
	job := Job{Name: "everywhere", Run: func(ctx context.Context) {
		running.Add(1)
		<-ctx.Done()
		running.Add(-1)
	}}
	ctx, cancel := context.WithCancel(context.Background())
	waitA := Start(ctx, testPool, []Job{job})
	waitB := Start(ctx, testPool, []Job{job})
	require.Eventually(t, func() bool { return running.Load() == 2 }, 5*time.Second, 10*time.Millisecond)
	cancel()
	waitA()
	waitB()
	require.Zero(t, running.Load())
}

// A panicking job is recovered and restarted instead of crashing the process.
func TestPanickingJobRestarts(t *testing.T) {
	var runs atomic.Int32
	job := Job{Name: "flaky", Run: func(ctx context.Context) {
		if runs.Add(1) == 1 {
			panic("boom")
		}
		<-ctx.Done()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	wait := Start(ctx, testPool, []Job{job})
	require.Eventually(t, func() bool { return runs.Load() == 2 }, 5*time.Second, 10*time.Millisecond)
	cancel()
	wait()
}

// A job that returns by itself (e.g. disabled by configuration) is not restarted.
func TestReturningJobIsNotRestarted(t *testing.T) {
	var runs atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wait := Start(ctx, testPool, []Job{{Name: "disabled", Run: func(context.Context) { runs.Add(1) }}})
	wait()
	require.EqualValues(t, 1, runs.Load())
}

func TestEveryRunsImmediatelyThenOnInterval(t *testing.T) {
	var runs atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Every(ctx, 20*time.Millisecond, func(context.Context) { runs.Add(1) })
	}()
	require.Eventually(t, func() bool { return runs.Load() >= 1 }, time.Second, time.Millisecond, "first run is immediate")
	require.Eventually(t, func() bool { return runs.Load() >= 3 }, 5*time.Second, 5*time.Millisecond)
	cancel()
	<-done
}
