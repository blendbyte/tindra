package storage

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MonitorPool records per-minute contention without issuing database queries.
// Wait time is summed across successful acquisitions that found an empty pool;
// canceled acquisitions are reported separately. No connection strings are logged.
func MonitorPool(ctx context.Context, pool *pgxpool.Pool) {
	previous := pool.Stat()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := pool.Stat()
			slog.Debug("database pool", "max", current.MaxConns(),
				"acquired", current.AcquiredConns(), "idle", current.IdleConns(),
				"acquires", current.AcquireCount()-previous.AcquireCount(),
				"empty_acquires", current.EmptyAcquireCount()-previous.EmptyAcquireCount(),
				"wait_ms", float64(current.EmptyAcquireWaitTime()-previous.EmptyAcquireWaitTime())/float64(time.Millisecond),
				"canceled_acquires", current.CanceledAcquireCount()-previous.CanceledAcquireCount())
			previous = current
		}
	}
}
