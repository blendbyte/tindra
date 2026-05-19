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

// StoreMFASecret saves a pending TOTP secret without enabling MFA yet.
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
		UPDATE users SET mfa_enabled = false, mfa_secret = NULL WHERE id = $1
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
		INSERT INTO mfa_challenges (token, user_id, expires_at) VALUES ($1, $2, $3)
	`, token, userID, expiresAt)
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
		WHERE token = $1 AND expires_at > NOW()
	`, token).Scan(&userID)
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
		WHERE token = $1 AND expires_at > NOW()
		RETURNING user_id
	`, token).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query: %w", err)
	}
	return userID, nil
}
