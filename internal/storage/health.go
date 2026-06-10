package storage

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type InstanceHealth struct {
	DBSizeBytes     int64      `json:"db_size_bytes"`
	EventsTotal     int64      `json:"events_total"`
	TxTotal         int64      `json:"tx_total"`
	LogsTotal       int64      `json:"logs_total"`
	Events24h       int64      `json:"events_24h"`
	Tx24h           int64      `json:"tx_24h"`
	Logs24h         int64      `json:"logs_24h"`
	OldestEventAt   *time.Time `json:"oldest_event_at"`
	OldestTxAt      *time.Time `json:"oldest_tx_at"`
	OldestLogAt     *time.Time `json:"oldest_log_at"`
	EventsSizeBytes int64      `json:"events_size_bytes"`
	TxSizeBytes     int64      `json:"tx_size_bytes"`
	LogsSizeBytes   int64      `json:"logs_size_bytes"`
}

func GetInstanceHealth(ctx context.Context, pool *pgxpool.Pool) (*InstanceHealth, error) {
	var h InstanceHealth
	err := pool.QueryRow(ctx, `
		SELECT
			pg_database_size(current_database()),
			(SELECT COUNT(*) FROM events),
			(SELECT COUNT(*) FROM transactions),
			(SELECT COUNT(*) FROM logs),
			(SELECT COUNT(*) FROM events      WHERE received_at > NOW() - INTERVAL '24 hours'),
			(SELECT COUNT(*) FROM transactions WHERE received_at > NOW() - INTERVAL '24 hours'),
			(SELECT COUNT(*) FROM logs         WHERE received_at > NOW() - INTERVAL '24 hours'),
			(SELECT MIN(received_at) FROM events),
			(SELECT MIN(received_at) FROM transactions),
			(SELECT MIN(received_at) FROM logs),
			pg_total_relation_size('events'),
			pg_total_relation_size('transactions'),
			pg_total_relation_size('logs')
	`).Scan(
		&h.DBSizeBytes,
		&h.EventsTotal,
		&h.TxTotal,
		&h.LogsTotal,
		&h.Events24h,
		&h.Tx24h,
		&h.Logs24h,
		&h.OldestEventAt,
		&h.OldestTxAt,
		&h.OldestLogAt,
		&h.EventsSizeBytes,
		&h.TxSizeBytes,
		&h.LogsSizeBytes,
	)
	if err != nil {
		return nil, err
	}
	return &h, nil
}
