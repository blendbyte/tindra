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
		INSERT INTO password_reset_tokens (token_hash, user_id, expires_at)
		VALUES ($1, $2, NOW() + INTERVAL '24 hours')
	`, tokenHash(token), userID)
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
		WHERE t.token_hash = $1 AND t.expires_at > NOW() AND t.used_at IS NULL
	`, tokenHash(token)).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.MFAEnabled,
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

// UsePasswordResetToken resets credentials and revokes all existing authentication artifacts.
func UsePasswordResetToken(ctx context.Context, pool *pgxpool.Pool, token, newPassword string) (*User, error) {
	user, _, err := usePasswordResetToken(ctx, pool, token, newPassword, false)
	return user, err
}

// UsePasswordResetTokenWithSession includes the recovery session in the transaction.
func UsePasswordResetTokenWithSession(ctx context.Context, pool *pgxpool.Pool, token, newPassword string) (*User, *Session, error) {
	return usePasswordResetToken(ctx, pool, token, newPassword, true)
}

func usePasswordResetToken(ctx context.Context, pool *pgxpool.Pool, token, newPassword string, issueSession bool) (*User, *Session, error) {
	if len(newPassword) < minPasswordLen {
		return nil, nil, fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}
	if len(newPassword) > maxPasswordLen {
		return nil, nil, fmt.Errorf("password must be at most %d characters", maxPasswordLen)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var userID string
	err = tx.QueryRow(ctx, `SELECT user_id FROM password_reset_tokens WHERE token_hash=$1 AND expires_at>NOW() AND used_at IS NULL`, tokenHash(token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("find reset: %w", err)
	}
	// Lock the user first across every credential-change path, then recheck the
	// token after acquiring the lock. Competing reset links cannot both succeed.
	var lockedID string
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&lockedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("lock user: %w", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE password_reset_tokens SET used_at=NOW() WHERE token_hash=$1 AND expires_at>NOW() AND used_at IS NULL`, tokenHash(token))
	if err != nil {
		return nil, nil, fmt.Errorf("consume reset: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, nil, nil
	}
	// Validate and claim the token before paying the bcrypt cost. The claim is
	// rolled back with the transaction if hashing or a later write fails.
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), BcryptCost)
	if err != nil {
		return nil, nil, fmt.Errorf("hash password: %w", err)
	}
	// Recovery links are administrator-issued and require MFA re-enrollment.
	var u User
	err = tx.QueryRow(ctx, `
		UPDATE users SET password_hash=$2, failed_attempts=0, locked_until=NULL,
			mfa_enabled=false, mfa_secret=NULL, mfa_pending_secret=NULL, mfa_pending_expires_at=NULL
		WHERE id=$1
		RETURNING id,email,name,password_hash,mfa_enabled,
			perm_manage_projects,perm_manage_users,perm_manage_alerts,perm_manage_issues,created_at
	`, userID, string(hash)).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.MFAEnabled,
		&u.Permissions.ManageProjects, &u.Permissions.ManageUsers, &u.Permissions.ManageAlerts, &u.Permissions.ManageIssues, &u.CreatedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("reset password: %w", err)
	}
	if err := revokeCredentialArtifacts(ctx, tx, userID); err != nil {
		return nil, nil, err
	}
	var session *Session
	if issueSession {
		session, err = createSession(ctx, tx, userID)
		if err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit reset: %w", err)
	}
	u.HasPassword = true
	return &u, session, nil
}

// revokeCredentialArtifacts runs after locking/updating the user in the caller's transaction.
func revokeCredentialArtifacts(ctx context.Context, tx pgx.Tx, userID string) error {
	for _, query := range []string{
		`DELETE FROM sessions WHERE user_id=$1`,
		`DELETE FROM mfa_challenges WHERE user_id=$1`,
		`DELETE FROM password_reset_tokens WHERE user_id=$1`,
	} {
		if _, err := tx.Exec(ctx, query, userID); err != nil {
			return fmt.Errorf("revoke credentials: %w", err)
		}
	}
	return nil
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
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx,
		`UPDATE users SET password_hash = $1, failed_attempts = 0, locked_until = NULL, mfa_pending_secret = NULL, mfa_pending_expires_at = NULL WHERE id = $2`,
		string(hash), userID,
	)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	if err := revokeCredentialArtifacts(ctx, tx, userID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit admin password: %w", err)
	}
	return nil
}
