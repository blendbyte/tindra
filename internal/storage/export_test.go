package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Helpers that only this package's tests use. They live in a _test.go file so
// they are compiled for tests alone: production code reaches the same logic
// through the flows that wrap it (sessions, invitations, password resets).

func GetCheckin(ctx context.Context, pool *pgxpool.Pool, id string) (*CronCheckin, error) {
	var ci CronCheckin
	err := pool.QueryRow(ctx, `
		SELECT id, monitor_id, status, duration_ms, environment, started_at, finished_at, received_at
		FROM cron_checkins WHERE id=$1`, id,
	).Scan(
		&ci.ID, &ci.MonitorID, &ci.Status,
		&ci.DurationMs, &ci.Environment,
		&ci.StartedAt, &ci.FinishedAt, &ci.ReceivedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return &ci, nil
}

// LinkEventToIssue sets fingerprint and issue_id on an event row.
func LinkEventToIssue(ctx context.Context, pool *pgxpool.Pool, eventID, issueID, fingerprint string) error {
	_, err := pool.Exec(ctx, `
		UPDATE events SET fingerprint = $1, issue_id = $2 WHERE id = $3
	`, fingerprint, issueID, eventID)
	if err != nil {
		return fmt.Errorf("link event: %w", err)
	}
	return nil
}

func GetSession(ctx context.Context, pool *pgxpool.Pool, token string) (*Session, error) {
	s := Session{Token: token}
	err := pool.QueryRow(ctx, `
		SELECT user_id, expires_at, created_at
		FROM sessions WHERE token_hash = $1 AND expires_at > NOW()
	`, tokenHash(token)).Scan(&s.UserID, &s.ExpiresAt, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return &s, nil
}

// ChangeUserPassword changes credentials and revokes all existing authentication artifacts.
func ChangeUserPassword(ctx context.Context, pool *pgxpool.Pool, userID, currentPassword, newPassword string) error {
	_, err := changeUserPassword(ctx, pool, userID, currentPassword, newPassword, "")
	return err
}

// UsePasswordResetToken resets credentials and revokes all existing authentication artifacts.
func UsePasswordResetToken(ctx context.Context, pool *pgxpool.Pool, token, newPassword string) (*User, error) {
	user, _, err := usePasswordResetToken(ctx, pool, token, newPassword, false)
	return user, err
}

func MarkInviteAccepted(ctx context.Context, pool *pgxpool.Pool, token string) error {
	_, err := pool.Exec(ctx, `UPDATE user_invites SET accepted_at = NOW() WHERE token_hash = $1`, tokenHash(token))
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	return nil
}

// ConsumeOAuthState looks up the state token, deletes it, and returns the stored provider and verifier.
// Returns nil if not found or expired.
func ConsumeOAuthState(ctx context.Context, pool *pgxpool.Pool, token string) (*oauthState, error) {
	var s oauthState
	err := pool.QueryRow(ctx, `
		DELETE FROM oauth_states
		WHERE token_hash = $1 AND expires_at > NOW()
		RETURNING provider, verifier
	`, tokenHash(token)).Scan(&s.Provider, &s.Verifier)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return &s, nil
}
