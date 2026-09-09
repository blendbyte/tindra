package retention

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestRetentionRetriesExhaustedBudgetThenReturnsToHourly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		go runPurgeLoop(ctx, func(context.Context) bool { calls++; return calls == 1 })
		synctest.Wait()
		if calls != 1 {
			t.Fatalf("startup calls=%d", calls)
		}
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if calls != 2 {
			t.Fatalf("backlog retry calls=%d", calls)
		}
		time.Sleep(59 * time.Minute)
		synctest.Wait()
		if calls != 2 {
			t.Fatalf("quiet cycle ran early: %d", calls)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if calls != 3 {
			t.Fatalf("hourly calls=%d", calls)
		}
		cancel()
		synctest.Wait()
	})
}

func TestRetentionWaitStartsAfterPassFinishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		go runPurgeLoop(ctx, func(ctx context.Context) bool {
			calls++
			select {
			case <-time.After(10 * time.Minute):
			case <-ctx.Done():
			}
			return true
		})
		synctest.Wait()
		time.Sleep(14 * time.Minute)
		synctest.Wait()
		if calls != 1 {
			t.Fatalf("passes overlapped: %d", calls)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if calls != 2 {
			t.Fatalf("backlog did not resume: %d", calls)
		}
		cancel()
		synctest.Wait()
	})
}
