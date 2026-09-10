package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrAuthenticationChanged means the credentials validated by the caller are no
// longer current. The caller must restart authentication rather than issue a cookie.
var ErrAuthenticationChanged = errors.New("authentication changed")

// withAuthenticatedUser serializes credential issuance with password changes.
// Check the snapshot only after locking the user, and retain the lock through
// issuance so a later reset necessarily revokes the newly issued artifact.
func withAuthenticatedUser(ctx context.Context, pool *pgxpool.Pool, user *User, issue func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin login: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var hash string
	var mfa bool
	err = tx.QueryRow(ctx, `SELECT password_hash, mfa_enabled FROM users WHERE id=$1 FOR UPDATE`, user.ID).Scan(&hash, &mfa)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAuthenticationChanged
	}
	if err != nil {
		return fmt.Errorf("lock login user: %w", err)
	}
	if hash != user.PasswordHash || mfa != user.MFAEnabled {
		return ErrAuthenticationChanged
	}
	if err := issue(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit login: %w", err)
	}
	return nil
}

// CreateAuthenticatedSession issues a session only while the authenticated user
// snapshot remains current. MFA users must instead complete their challenge.
func CreateAuthenticatedSession(ctx context.Context, pool *pgxpool.Pool, user *User) (*Session, error) {
	if user.MFAEnabled {
		return nil, ErrAuthenticationChanged
	}
	var session *Session
	err := withAuthenticatedUser(ctx, pool, user, func(tx pgx.Tx) (err error) {
		session, err = createSession(ctx, tx, user.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

// CreateAuthenticatedMFAChallenge binds issuance to the validated first factor.
func CreateAuthenticatedMFAChallenge(ctx context.Context, pool *pgxpool.Pool, user *User) (string, error) {
	if !user.MFAEnabled {
		return "", ErrAuthenticationChanged
	}
	var token string
	err := withAuthenticatedUser(ctx, pool, user, func(tx pgx.Tx) (err error) {
		token, err = createMFAChallenge(ctx, tx, user.ID)
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// CompleteMFALogin checks the validated secret and consumes the challenge in
// the same user-locked transaction as session issuance. Reset paths lock the
// user before deleting challenges, so either login commits first and is revoked,
// or reset commits first and this operation cannot use the deleted challenge.
func CompleteMFALogin(ctx context.Context, pool *pgxpool.Pool, token, userID, verifiedSecret string) (*Session, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin mfa login: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var secret *string
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT mfa_secret, mfa_enabled FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&secret, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAuthenticationChanged
	}
	if err != nil {
		return nil, fmt.Errorf("lock mfa user: %w", err)
	}
	if !enabled || secret == nil || *secret != verifiedSecret {
		return nil, ErrAuthenticationChanged
	}
	tag, err := tx.Exec(ctx, `DELETE FROM mfa_challenges WHERE token_hash=$1 AND user_id=$2 AND expires_at>NOW()`, tokenHash(token), userID)
	if err != nil {
		return nil, fmt.Errorf("consume mfa challenge: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return nil, ErrAuthenticationChanged
	}
	session, err := createSession(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit mfa login: %w", err)
	}
	return session, nil
}
