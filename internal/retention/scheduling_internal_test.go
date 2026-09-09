package retention

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestRetentionRetriesExhaustedBudgetThenReturnsToHourly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int32
		go runPurgeLoop(ctx, func(context.Context) bool { return calls.Add(1) == 1 })
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("startup calls=%d", calls.Load())
		}
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("backlog retry calls=%d", calls.Load())
		}
		time.Sleep(59 * time.Minute)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("quiet cycle ran early: %d", calls.Load())
		}
		time.Sleep(time.Minute)
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
		time.Sleep(14 * time.Minute)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("passes overlapped: %d", calls.Load())
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("backlog did not resume: %d", calls.Load())
		}
		cancel()
		synctest.Wait()
	})
}
