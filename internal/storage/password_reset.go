package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func CreatePasswordResetToken(ctx context.Context, pool *pgxpool.Pool, userID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	token := hex.EncodeToString(b)
	_, err := pool.Exec(ctx, `
		INSERT INTO password_reset_tokens (token, user_id, expires_at)
		VALUES ($1, $2, NOW() + INTERVAL '24 hours')
	`, token, userID)
	if err != nil {
		return "", fmt.Errorf("insert: %w", err)
	}
	return token, nil
}

func GetPasswordResetUser(ctx context.Context, pool *pgxpool.Pool, token string) (*User, error) {
	var u User
	err := pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.name, u.password_hash, u.mfa_enabled,
			u.perm_manage_projects, u.perm_manage_users, u.perm_manage_alerts, u.perm_manage_issues,
			u.created_at
		FROM password_reset_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE t.token = $1 AND t.expires_at > NOW() AND t.used_at IS NULL
	`, token).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.MFAEnabled,
		&u.Permissions.ManageProjects, &u.Permissions.ManageUsers,
		&u.Permissions.ManageAlerts, &u.Permissions.ManageIssues, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	u.HasPassword = u.PasswordHash != ""
	return &u, nil
}

// UsePasswordResetToken marks the token used and sets the new password atomically.
// Returns nil user if the token is expired or already used.
func UsePasswordResetToken(ctx context.Context, pool *pgxpool.Pool, token, newPassword string) (*User, error) {
	if len(newPassword) < minPasswordLen {
		return nil, fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}
	if len(newPassword) > maxPasswordLen {
		return nil, fmt.Errorf("password must be at most %d characters", maxPasswordLen)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), BcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	var u User
	err = pool.QueryRow(ctx, `
		WITH reset AS (
			UPDATE password_reset_tokens
			SET used_at = NOW()
			WHERE token = $1 AND expires_at > NOW() AND used_at IS NULL
			RETURNING user_id
		)
		UPDATE users
		SET password_hash = $2, failed_attempts = 0, locked_until = NULL
		FROM reset
		WHERE users.id = reset.user_id
		RETURNING users.id, users.email, users.name, users.password_hash, users.mfa_enabled,
			users.perm_manage_projects, users.perm_manage_users, users.perm_manage_alerts, users.perm_manage_issues,
			users.created_at
	`, token, string(hash)).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.MFAEnabled,
		&u.Permissions.ManageProjects, &u.Permissions.ManageUsers,
		&u.Permissions.ManageAlerts, &u.Permissions.ManageIssues, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reset password: %w", err)
	}
	u.HasPassword = true
	return &u, nil
}

// AdminSetPassword sets a user's password without requiring their old password.
func AdminSetPassword(ctx context.Context, pool *pgxpool.Pool, userID, newPassword string) error {
	if len(newPassword) < minPasswordLen {
		return fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}
	if len(newPassword) > maxPasswordLen {
		return fmt.Errorf("password must be at most %d characters", maxPasswordLen)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), BcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	tag, err := pool.Exec(ctx,
		`UPDATE users SET password_hash = $1, failed_attempts = 0, locked_until = NULL WHERE id = $2`,
		string(hash), userID,
	)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}
