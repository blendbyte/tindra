package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type AlertFiring struct {
	ID          string     `json:"id"`
	RuleID      string     `json:"rule_id"`
	FiredAt     time.Time  `json:"fired_at"`
	Trigger     string     `json:"trigger"`
	Channel     string     `json:"channel"`
	Status      string     `json:"status"` // pending, success, failed
	StatusCode  *int       `json:"status_code,omitempty"`
	Error       *string    `json:"error,omitempty"`
	ItemCount   *int       `json:"item_count,omitempty"`
	Attempt     int        `json:"attempt"`
	NextRetryAt *time.Time `json:"next_retry_at,omitempty"`
	Payload     []byte     `json:"-"` // stored for retry, not exposed via API
}

func CreateAlertFiring(ctx context.Context, pool *pgxpool.Pool, f *AlertFiring) (string, error) {
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO alert_firings
			(rule_id, trigger, channel, status, status_code, error, item_count, attempt, next_retry_at, payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id`,
		f.RuleID, f.Trigger, f.Channel, f.Status,
		f.StatusCode, f.Error, f.ItemCount,
		f.Attempt, f.NextRetryAt, nullableJSON(f.Payload),
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert: %w", err)
	}
	return id, nil
}

// ResolveAlertFiring marks a firing as success or permanently failed (clears retry state).
func ResolveAlertFiring(ctx context.Context, pool *pgxpool.Pool, id string, status string, attempt int, statusCode *int, errMsg *string) {
	_, _ = pool.Exec(ctx, `
		UPDATE alert_firings
		SET status=$2, attempt=$3, status_code=$4, error=$5, next_retry_at=NULL, payload=NULL
		WHERE id=$1`,
		id, status, attempt, statusCode, errMsg,
	)
}

// ScheduleAlertFiringRetry updates a firing to indicate another attempt is pending.
func ScheduleAlertFiringRetry(ctx context.Context, pool *pgxpool.Pool, id string, attempt int, nextRetryAt time.Time, statusCode *int, errMsg *string, payload []byte) {
	_, _ = pool.Exec(ctx, `
		UPDATE alert_firings
		SET status='pending', attempt=$2, next_retry_at=$3, status_code=$4, error=$5, payload=$6
		WHERE id=$1`,
		id, attempt, nextRetryAt, statusCode, errMsg, nullableJSON(payload),
	)
}

// ListAlertFirings returns the most recent firings for a rule (newest first).
func ListAlertFirings(ctx context.Context, pool *pgxpool.Pool, ruleID string, limit int) ([]*AlertFiring, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, rule_id, fired_at, trigger, channel, status, status_code, error, item_count, attempt, next_retry_at
		FROM alert_firings
		WHERE rule_id = $1
		ORDER BY fired_at DESC
		LIMIT $2`,
		ruleID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var firings []*AlertFiring
	for rows.Next() {
		var f AlertFiring
		if err := rows.Scan(
			&f.ID, &f.RuleID, &f.FiredAt, &f.Trigger, &f.Channel,
			&f.Status, &f.StatusCode, &f.Error, &f.ItemCount,
			&f.Attempt, &f.NextRetryAt,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		firings = append(firings, &f)
	}
	return firings, rows.Err()
}

// ListPendingRetries returns firings due for retry.
func ListPendingRetries(ctx context.Context, pool *pgxpool.Pool) ([]*AlertFiring, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, rule_id, fired_at, trigger, channel, status, status_code, error, item_count, attempt, next_retry_at, payload
		FROM alert_firings
		WHERE status = 'pending' AND next_retry_at IS NOT NULL AND next_retry_at <= NOW()
		ORDER BY next_retry_at
		LIMIT 50`,
	)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var firings []*AlertFiring
	for rows.Next() {
		var f AlertFiring
		var rawPayload []byte
		if err := rows.Scan(
			&f.ID, &f.RuleID, &f.FiredAt, &f.Trigger, &f.Channel,
			&f.Status, &f.StatusCode, &f.Error, &f.ItemCount,
			&f.Attempt, &f.NextRetryAt, &rawPayload,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		f.Payload = rawPayload
		firings = append(firings, &f)
	}
	return firings, rows.Err()
}

// nullableJSON returns nil for empty/nil slices so they are stored as SQL NULL
// rather than an empty JSONB value.
func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
