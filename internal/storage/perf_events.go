package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PerfEvent struct {
	ID            string    `json:"id"`
	IssueID       string    `json:"issue_id"`
	TransactionID string    `json:"transaction_id"`
	Transaction   string    `json:"transaction"`
	SpanCount     int       `json:"span_count"`
	TotalMs       int       `json:"total_ms"`
	CreatedAt     time.Time `json:"created_at"`
}

func InsertPerfEvent(ctx context.Context, pool *pgxpool.Pool, issueID, transactionID string, spanCount, totalMs int) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO perf_events (issue_id, transaction_id, span_count, total_ms)
		VALUES ($1, $2, $3, $4)
	`, issueID, transactionID, spanCount, totalMs)
	if err != nil {
		return fmt.Errorf("insert perf event: %w", err)
	}
	return nil
}

func ListPerfEvents(ctx context.Context, pool *pgxpool.Pool, issueID string, limit int) ([]*PerfEvent, error) {
	if limit <= 0 || limit > 50 {
		limit = 25
	}
	rows, err := pool.Query(ctx, `
		SELECT pe.id, pe.issue_id, pe.transaction_id, t.transaction,
		       pe.span_count, pe.total_ms, pe.created_at
		FROM perf_events pe
		JOIN transactions t ON t.id = pe.transaction_id
		WHERE pe.issue_id = $1
		ORDER BY pe.created_at DESC
		LIMIT $2
	`, issueID, limit)
	if err != nil {
		return nil, fmt.Errorf("list perf events: %w", err)
	}
	defer rows.Close()

	var events []*PerfEvent
	for rows.Next() {
		var e PerfEvent
		if err := rows.Scan(&e.ID, &e.IssueID, &e.TransactionID, &e.Transaction,
			&e.SpanCount, &e.TotalMs, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan perf event: %w", err)
		}
		events = append(events, &e)
	}
	return events, rows.Err()
}
