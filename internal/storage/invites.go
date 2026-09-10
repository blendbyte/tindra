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

type Invite struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	Name       string     `json:"name,omitempty"`
	InviterID  *string    `json:"inviter_id,omitempty"`
	ExpiresAt  time.Time  `json:"expires_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func CreateInvite(ctx context.Context, pool *pgxpool.Pool, inviterID, email, name string) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	token := hex.EncodeToString(b)
	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	_, err := pool.Exec(ctx, `
		INSERT INTO user_invites (token_hash, email, name, inviter_id, expires_at)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, '')::uuid, $5)
	`, tokenHash(token), email, name, inviterID, expiresAt)
	if err != nil {
		return "", fmt.Errorf("insert: %w", err)
	}
	return token, nil
}

func GetInvite(ctx context.Context, pool *pgxpool.Pool, token string) (*Invite, error) {
	var inv Invite
	var name *string
	err := pool.QueryRow(ctx, `
		SELECT id, email, name, inviter_id, expires_at, accepted_at, created_at
		FROM user_invites
		WHERE token_hash = $1 AND expires_at > NOW() AND accepted_at IS NULL
	`, tokenHash(token)).Scan(&inv.ID, &inv.Email, &name, &inv.InviterID, &inv.ExpiresAt, &inv.AcceptedAt, &inv.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	if name != nil {
		inv.Name = *name
	}
	return &inv, nil
}

func ListPendingInvites(ctx context.Context, pool *pgxpool.Pool) ([]*Invite, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, email, name, inviter_id, expires_at, accepted_at, created_at
		FROM user_invites
		WHERE expires_at > NOW() AND accepted_at IS NULL
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var invites []*Invite
	for rows.Next() {
		var inv Invite
		var name *string
		if err := rows.Scan(&inv.ID, &inv.Email, &name, &inv.InviterID, &inv.ExpiresAt, &inv.AcceptedAt, &inv.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if name != nil {
			inv.Name = *name
		}
		invites = append(invites, &inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return invites, nil
}

func MarkInviteAccepted(ctx context.Context, pool *pgxpool.Pool, token string) error {
	_, err := pool.Exec(ctx, `UPDATE user_invites SET accepted_at = NOW() WHERE token_hash = $1`, tokenHash(token))
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	return nil
}

// DeleteInvite removes an invite by its UUID (the stable admin identifier).
func DeleteInvite(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM user_invites WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

var (
	ErrInviteUserLimit        = errors.New("user limit reached")
	ErrInvitePasswordTooShort = fmt.Errorf("password must be at least %d characters", minPasswordLen)
	ErrInvitePasswordTooLong  = fmt.Errorf("password must be at most %d characters", maxPasswordLen)
)

// AcceptInviteWithSession claims a live invitation and creates the account and
// session atomically. A nil user means the invitation is no longer usable.
func AcceptInviteWithSession(ctx context.Context, pool *pgxpool.Pool, token, password, name string, userLimit int) (*User, *Session, error) {
	if len(password) < minPasswordLen {
		return nil, nil, ErrInvitePasswordTooShort
	}
	if len(password) > maxPasswordLen {
		return nil, nil, ErrInvitePasswordTooLong
	}
	// Reject arbitrary invalid tokens without taking the instance-wide admission
	// lock. The transactional claim below remains authoritative after this read.
	invite, err := GetInvite(ctx, pool, token)
	if err != nil {
		return nil, nil, fmt.Errorf("look up invitation: %w", err)
	}
	if invite == nil {
		return nil, nil, nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin invitation acceptance: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// Match OAuth admission's lock order: users table, then invitation row. This
	// serializes quota checks with all user inserts, including concurrent SSO.
	if _, err := tx.Exec(ctx, `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return nil, nil, fmt.Errorf("lock invitation admission: %w", err)
	}
	var email, invitedName string
	err = tx.QueryRow(ctx, `UPDATE user_invites SET accepted_at=clock_timestamp()
		WHERE token_hash=$1 AND accepted_at IS NULL AND expires_at>clock_timestamp()
		RETURNING email, COALESCE(name, '')`, tokenHash(token)).Scan(&email, &invitedName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("claim invitation: %w", err)
	}
	if userLimit > 0 {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
			return nil, nil, fmt.Errorf("count users: %w", err)
		}
		if count >= userLimit {
			return nil, nil, ErrInviteUserLimit
		}
	}
	user, err := createUser(ctx, tx, email, password)
	if err != nil {
		return nil, nil, err
	}
	if name == "" {
		name = invitedName
	}
	if name != "" {
		if _, err := tx.Exec(ctx, `UPDATE users SET name=$2 WHERE id=$1`, user.ID, name); err != nil {
			return nil, nil, fmt.Errorf("set invited user name: %w", err)
		}
		user.Name = name
	}
	// The new account is not visible outside this transaction yet. A later
	// password reset cannot race ahead of issuance and will revoke this session.
	session, err := createSession(ctx, tx, user.ID)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit invitation acceptance: %w", err)
	}
	return user, session, nil
}
