package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SetupReceipt struct {
	Kind            string    `json:"kind"`
	FirstReceivedAt time.Time `json:"first_received_at"`
	LastReceivedAt  time.Time `json:"last_received_at"`
	LatestID        string    `json:"latest_id"`
}

type SetupObservation struct {
	Kind       string    `json:"kind"`
	Outcome    string    `json:"outcome"`
	Reason     string    `json:"reason"`
	ObservedAt time.Time `json:"observed_at"`
}

type SetupCheck struct {
	ID         string     `json:"id"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	ReceivedAt *time.Time `json:"received_at"`
	EventID    *string    `json:"event_id"`
}

func StartSetupCheck(ctx context.Context, pool *pgxpool.Pool, projectID string) (*SetupCheck, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// Serialize creation per project so concurrent tabs cannot exceed the bound.
	if _, err = tx.Exec(ctx, `SELECT id FROM projects WHERE id=$1 FOR UPDATE`, projectID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM project_setup_checks WHERE project_id=$1 AND
 (expires_at < now() OR id IN (SELECT id FROM project_setup_checks WHERE project_id=$1 ORDER BY created_at DESC, id DESC OFFSET 9))`, projectID); err != nil {
		return nil, err
	}
	var c SetupCheck
	err = tx.QueryRow(ctx, `INSERT INTO project_setup_checks(project_id) VALUES($1) RETURNING id, created_at, expires_at`, projectID).Scan(&c.ID, &c.CreatedAt, &c.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &c, tx.Commit(ctx)
}

func GetSetupCheck(ctx context.Context, pool *pgxpool.Pool, projectID, id string) (*SetupCheck, error) {
	var c SetupCheck
	err := pool.QueryRow(ctx, `SELECT id, created_at, expires_at, received_at, event_id FROM project_setup_checks WHERE project_id=$1 AND id=$2`, projectID, id).Scan(&c.ID, &c.CreatedAt, &c.ExpiresAt, &c.ReceivedAt, &c.EventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &c, err
}

func ListSetupReceipts(ctx context.Context, pool *pgxpool.Pool, projectID string) ([]SetupReceipt, error) {
	rows, err := pool.Query(ctx, `SELECT kind, first_received_at, last_received_at, latest_id FROM project_setup_receipts WHERE project_id=$1 ORDER BY kind`, projectID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[SetupReceipt])
}

func ListSetupObservations(ctx context.Context, pool *pgxpool.Pool, projectID string) ([]SetupObservation, error) {
	rows, err := pool.Query(ctx, `SELECT kind, outcome, reason, observed_at FROM project_setup_observations WHERE project_id=$1 AND observed_at > now() - interval '1 hour' ORDER BY observed_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[SetupObservation])
}

// RecordSetupObservations stores only fixed reason codes, never SDK payloads.
// Repeated observations are coalesced to at most one update every five seconds.
func RecordSetupObservations(ctx context.Context, pool *pgxpool.Pool, projectID string, observations []SetupObservation) error {
	if len(observations) == 0 {
		return nil
	}
	data, err := json.Marshal(observations)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `INSERT INTO project_setup_observations(project_id, kind, outcome, reason, observed_at)
 SELECT $1, kind, outcome, reason, observed_at FROM jsonb_to_recordset($2::jsonb)
 AS x(kind text, outcome text, reason text, observed_at timestamptz) ORDER BY kind, outcome
 ON CONFLICT(project_id,kind,outcome) DO UPDATE SET reason=EXCLUDED.reason, observed_at=EXCLUDED.observed_at
 WHERE EXCLUDED.observed_at > project_setup_observations.observed_at AND
 (EXCLUDED.reason <> project_setup_observations.reason OR EXCLUDED.observed_at > project_setup_observations.observed_at + interval '5 seconds')`, projectID, data)
	return err
}
