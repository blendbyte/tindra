package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Fixtures for tests in other packages (api, cmd, retention), which cannot see
// this package's _test.go files. They skip the flows that guard real accounts,
// such as MFA enrollment and confirmation, so production code must not call
// them; TestFixturesAreTestOnly fails if it does.

func CreateUser(ctx context.Context, pool *pgxpool.Pool, email, password string) (*User, error) {
	return createUser(ctx, pool, email, password)
}

func CreateSession(ctx context.Context, pool *pgxpool.Pool, userID string) (*Session, error) {
	return createSession(ctx, pool, userID)
}

// CreateOAuthUser creates a user with no local password (SSO-only). The empty
// password_hash intentionally prevents local login.
func CreateOAuthUser(ctx context.Context, pool *pgxpool.Pool, email string) (*User, error) {
	var u User
	// Same first-user-gets-all-permissions logic as CreateUser.
	err := pool.QueryRow(ctx, `
		WITH is_first AS (SELECT NOT EXISTS (SELECT 1 FROM users) AS v)
		INSERT INTO users (email, password_hash,
			perm_manage_projects, perm_manage_users, perm_manage_alerts, perm_manage_issues)
		SELECT $1, '', v, v, v, v FROM is_first
		RETURNING id, email, name, password_hash, mfa_enabled, weekly_digest, timezone,
			perm_manage_projects, perm_manage_users, perm_manage_alerts, perm_manage_issues, created_at
	`, strings.ToLower(email)).Scan(
		&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.MFAEnabled, &u.WeeklyDigest, &u.Timezone,
		&u.Permissions.ManageProjects, &u.Permissions.ManageUsers,
		&u.Permissions.ManageAlerts, &u.Permissions.ManageIssues, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}
	u.HasPassword = u.PasswordHash != ""
	return &u, nil
}

// CreateMFAChallenge issues a short-lived token after password passes but before TOTP is verified.
func CreateMFAChallenge(ctx context.Context, pool *pgxpool.Pool, userID string) (string, error) {
	return createMFAChallenge(ctx, pool, userID)
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

func DeleteSession(ctx context.Context, pool *pgxpool.Pool, token string) error {
	_, err := pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash(token))
	if err != nil {
		return fmt.Errorf("delete: %w", err)
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

// StoreMFASecret stores an active secret. Enrollment uses SetPendingMFASecret.
func StoreMFASecret(ctx context.Context, pool *pgxpool.Pool, userID, secret string) error {
	_, err := pool.Exec(ctx, `UPDATE users SET mfa_secret = $1 WHERE id = $2`, secret, userID)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	return nil
}
