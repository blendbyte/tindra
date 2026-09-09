package storage_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

type lockedLogBuffer struct {
	sync.Mutex
	bytes.Buffer
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(p)
}
func (b *lockedLogBuffer) snapshot() []byte {
	b.Lock()
	defer b.Unlock()
	return bytes.Clone(b.Bytes())
}

func TestMonitorPoolReportsIntervalDeltasWithoutQueries(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://unused:secret@localhost:1/unused?sslmode=disable")
	require.NoError(t, err)
	defer pool.Close()
	var output lockedLogBuffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previous)
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go storage.MonitorPool(ctx, pool)
		synctest.Wait()
		require.Empty(t, output.snapshot())
		aborted, abort := context.WithCancel(context.Background())
		abort()
		_, err := pool.Acquire(aborted)
		require.ErrorIs(t, err, context.Canceled)
		time.Sleep(time.Minute)
		synctest.Wait()
		time.Sleep(time.Minute)
		synctest.Wait()
		cancel()
		synctest.Wait()
		records := bytes.Split(bytes.TrimSpace(output.snapshot()), []byte("\n"))
		require.Len(t, records, 2)
		for i, line := range records {
			var record map[string]any
			require.NoError(t, json.Unmarshal(line, &record))
			require.Equal(t, "database pool", record["msg"])
			require.Equal(t, float64(1-i), record["canceled_acquires"])
			require.Equal(t, float64(0), record["acquires"])
			require.Equal(t, float64(0), record["wait_ms"])
		}
		require.Zero(t, pool.Stat().TotalConns())
		require.NotContains(t, string(output.snapshot()), "secret")
	})
}
