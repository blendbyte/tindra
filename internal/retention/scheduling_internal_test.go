package retention

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestRetentionRetriesBacklogSoonThenReturnsToHourly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int32
		go runPurgeLoop(ctx, func(context.Context) bool { return calls.Add(1) == 1 })
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("startup calls=%d", calls.Load())
		}
		// A pass that left a table behind is followed half a minute later,
		// not after the five minutes that used to cap throughput.
		time.Sleep(29 * time.Second)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("backlog retry ran early: %d", calls.Load())
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("backlog retry calls=%d", calls.Load())
		}
		time.Sleep(time.Hour - time.Second)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("quiet cycle ran early: %d", calls.Load())
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if calls.Load() != 3 {
			t.Fatalf("hourly calls=%d", calls.Load())
		}
		cancel()
		synctest.Wait()
	})
}

func TestRetentionWaitStartsAfterPassFinishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int32
		go runPurgeLoop(ctx, func(ctx context.Context) bool {
			calls.Add(1)
			select {
			case <-time.After(10 * time.Minute):
			case <-ctx.Done():
			}
			return true
		})
		synctest.Wait()
		time.Sleep(10*time.Minute + backlogDelay - time.Second)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("passes overlapped: %d", calls.Load())
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("backlog did not resume: %d", calls.Load())
		}
		cancel()
		synctest.Wait()
	})
}
