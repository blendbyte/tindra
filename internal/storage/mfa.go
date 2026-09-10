package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GetMFASecret returns the user's current TOTP secret (nil if none set).
func GetMFASecret(ctx context.Context, pool *pgxpool.Pool, userID string) (*string, error) {
	var secret *string
	err := pool.QueryRow(ctx, `SELECT mfa_secret FROM users WHERE id = $1`, userID).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return secret, nil
}

// StoreMFASecret stores an active secret. Enrollment uses SetPendingMFASecret.
func StoreMFASecret(ctx context.Context, pool *pgxpool.Pool, userID, secret string) error {
	_, err := pool.Exec(ctx, `UPDATE users SET mfa_secret = $1 WHERE id = $2`, secret, userID)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	return nil
}

// EnableMFA marks MFA as active. The secret must already be stored.
func EnableMFA(ctx context.Context, pool *pgxpool.Pool, userID string) error {
	_, err := pool.Exec(ctx, `UPDATE users SET mfa_enabled = true WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	return nil
}

// DisableMFA clears the secret and disables MFA.
func DisableMFA(ctx context.Context, pool *pgxpool.Pool, userID string) error {
	_, err := pool.Exec(ctx, `
		UPDATE users SET mfa_enabled = false, mfa_secret = NULL,
		mfa_pending_secret = NULL, mfa_pending_expires_at = NULL WHERE id = $1
	`, userID)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	return nil
}

// CreateMFAChallenge issues a short-lived token after password passes but before TOTP is verified.
func CreateMFAChallenge(ctx context.Context, pool *pgxpool.Pool, userID string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate: %w", err)
	}
	token := hex.EncodeToString(b)
	expiresAt := time.Now().Add(10 * time.Minute)
	_, err := pool.Exec(ctx, `
		INSERT INTO mfa_challenges (token_hash, user_id, expires_at) VALUES ($1, $2, $3)
	`, tokenHash(token), userID, expiresAt)
	if err != nil {
		return "", fmt.Errorf("insert: %w", err)
	}
	return token, nil
}

// GetMFAChallenge returns the user ID for a valid unexpired challenge without consuming it.
// Returns ("", nil) if the token is not found or expired.
func GetMFAChallenge(ctx context.Context, pool *pgxpool.Pool, token string) (string, error) {
	var userID string
	err := pool.QueryRow(ctx, `
		SELECT user_id FROM mfa_challenges
		WHERE token_hash = $1 AND expires_at > NOW()
	`, tokenHash(token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query: %w", err)
	}
	return userID, nil
}

// ConsumeMFAChallenge deletes a valid challenge and returns the user ID.
// Only call this after the TOTP code has been verified successfully.
// Returns ("", nil) if the token is not found or expired.
func ConsumeMFAChallenge(ctx context.Context, pool *pgxpool.Pool, token string) (string, error) {
	var userID string
	err := pool.QueryRow(ctx, `
		DELETE FROM mfa_challenges
		WHERE token_hash = $1 AND expires_at > NOW()
		RETURNING user_id
	`, tokenHash(token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query: %w", err)
	}
	return userID, nil
}

// SetPendingMFASecret stages enrollment only while the authenticated factor is
// still current. A nil activeSecret means this is initial enrollment.
func SetPendingMFASecret(ctx context.Context, pool *pgxpool.Pool, userID, secret string, activeSecret *string) (bool, error) {
	result, err := pool.Exec(ctx, `
		UPDATE users SET mfa_pending_secret = $2, mfa_pending_expires_at = NOW()+interval '10 minutes'
		WHERE id = $1 AND ((NOT mfa_enabled AND $3::text IS NULL)
			OR (mfa_enabled AND mfa_secret = $3))
	`, userID, secret, activeSecret)
	if err != nil {
		return false, fmt.Errorf("stage mfa: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

func GetPendingMFASecret(ctx context.Context, pool *pgxpool.Pool, userID string) (*string, error) {
	var secret *string
	err := pool.QueryRow(ctx, `SELECT mfa_pending_secret FROM users WHERE id=$1 AND mfa_pending_expires_at>NOW()`, userID).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query pending mfa: %w", err)
	}
	return secret, nil
}

// ConfirmMFA promotes only the pending secret whose code the caller verified.
// A concurrent restart, disable, confirmation, or expiry makes this fail closed.
func ConfirmMFA(ctx context.Context, pool *pgxpool.Pool, userID, secret string) (bool, error) {
	result, err := pool.Exec(ctx, `
		UPDATE users SET mfa_secret = mfa_pending_secret, mfa_enabled = true,
			mfa_pending_secret = NULL, mfa_pending_expires_at = NULL
		WHERE id = $1 AND mfa_pending_secret = $2 AND mfa_pending_expires_at > NOW()
	`, userID, secret)
	if err != nil {
		return false, fmt.Errorf("confirm mfa: %w", err)
	}
	return result.RowsAffected() == 1, nil
}
