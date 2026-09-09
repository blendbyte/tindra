package api

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestTokenTouchBoundsCoalescesAndRetries(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://unused:unused@localhost/unused")
	require.NoError(t, err)
	cfg.MaxConns = 4
	entered := make(chan struct{}, 10)
	release := make(chan struct{})
	var calls atomic.Int32
	cfg.BeforeConnect = func(ctx context.Context, _ *pgx.ConnConfig) error {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return errors.New("injected connection failure")
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	defer pool.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	ro := &router{pool: pool}
	for i := 0; i < 4; i++ {
		ro.touchAPIToken(fmt.Sprint(i))
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("touch did not start")
		}
	}
	for i := 0; i < 1000; i++ {
		ro.touchAPIToken("0")
		ro.touchAPIToken("overflow")
	}
	require.EqualValues(t, 4, calls.Load())
	close(release)
	require.Eventually(t, func() bool { ro.tokenTouchMu.Lock(); defer ro.tokenTouchMu.Unlock(); return len(ro.tokenTouches) == 0 }, time.Second, time.Millisecond)
	ro.touchAPIToken("0")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("failed touch was not retryable")
	}
	require.Eventually(t, func() bool { ro.tokenTouchMu.Lock(); defer ro.tokenTouchMu.Unlock(); return len(ro.tokenTouches) == 0 }, time.Second, time.Millisecond)
}
