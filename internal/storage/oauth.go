package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OAuthIdentity struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Provider  string    `json:"provider"`
	Sub       string    `json:"sub"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

var (
	ErrOAuthEmailUnverified = errors.New("a verified email is required to link an account")
	ErrOAuthInviteRequired  = errors.New("an invitation is required to sign in")
	ErrOAuthUserLimit       = errors.New("user limit reached")
)

// LinkOAuthIdentity is an operator-only admission path for an existing user.
// It must never be called based on an unverified provider email. A subject
// already bound to another user cannot be reassigned, including concurrently.
func LinkOAuthIdentity(ctx context.Context, pool *pgxpool.Pool, userID, provider, sub string) error {
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(sub) == "" {
		return fmt.Errorf("provider and subject must not be blank")
	}
	var linkedID string
	err := pool.QueryRow(ctx, `WITH linked AS (INSERT INTO oauth_identities (user_id, provider, sub, email)
		SELECT id, $2, $3, email FROM users WHERE id = $1
		ON CONFLICT (provider, sub) DO UPDATE SET sub = EXCLUDED.sub
		WHERE oauth_identities.user_id = EXCLUDED.user_id
		RETURNING user_id)
		INSERT INTO audit_log (event_type, target_id, details)
		SELECT 'user.sso.link', user_id,
			jsonb_build_object('provider', $2::text, 'subject', $3::text, 'source', 'cli') FROM linked
		RETURNING target_id`, userID, provider, sub).Scan(&linkedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("cannot link identity: subject belongs to another user or target user no longer exists")
	}
	if err != nil {
		return fmt.Errorf("link OAuth identity: %w", err)
	}
	return nil
}

// FindOrCreateOAuthUser links an existing account or redeems a valid invitation
// for a verified provider email. New users receive no management permissions.
func FindOrCreateOAuthUser(ctx context.Context, pool *pgxpool.Pool, provider, sub, email string, emailVerified bool, userLimit int) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	// Returning identities do not need an invitation or an available user slot.
	var userID string
	err := pool.QueryRow(ctx, `SELECT user_id FROM oauth_identities WHERE provider = $1 AND sub = $2`, provider, sub).Scan(&userID)
	if err == nil {
		return GetUserByID(ctx, pool, userID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("lookup identity: %w", err)
	}

	if !emailVerified || email == "" {
		return nil, ErrOAuthEmailUnverified
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin OAuth admission: %w", err)
	}
	defer tx.Rollback(ctx)
	// Serialize first-time admissions and block concurrent user inserts while
	// checking the quota. Ordinary reads and returning SSO logins remain unlocked.
	if _, err := tx.Exec(ctx, `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return nil, fmt.Errorf("lock OAuth admission: %w", err)
	}
	// Another callback may have linked this identity while we waited.
	err = tx.QueryRow(ctx, `SELECT user_id FROM oauth_identities WHERE provider = $1 AND sub = $2`, provider, sub).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(&userID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var inviteID, name string
		err = tx.QueryRow(ctx, `SELECT id, COALESCE(name, '') FROM user_invites
			WHERE lower(email) = $1 AND accepted_at IS NULL AND expires_at > NOW()
			ORDER BY created_at, id LIMIT 1 FOR UPDATE`, email).Scan(&inviteID, &name)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOAuthInviteRequired
		}
		if err != nil {
			return nil, fmt.Errorf("find OAuth invitation: %w", err)
		}
		if userLimit > 0 {
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
				return nil, fmt.Errorf("count users: %w", err)
			}
			if count >= userLimit {
				return nil, ErrOAuthUserLimit
			}
		}
		err = tx.QueryRow(ctx, `INSERT INTO users (email, name, password_hash) VALUES ($1, $2, '') RETURNING id`, email, name).Scan(&userID)
		if err != nil {
			return nil, fmt.Errorf("create invited OAuth user: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE user_invites SET accepted_at = NOW() WHERE id = $1`, inviteID); err != nil {
			return nil, fmt.Errorf("accept OAuth invitation: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("lookup OAuth account: %w", err)
	}

	// Re-read the winning identity so a callback never returns a different user
	// from the one actually linked to this provider subject.
	err = tx.QueryRow(ctx, `INSERT INTO oauth_identities (user_id, provider, sub, email)
		VALUES ($1, $2, $3, $4) ON CONFLICT (provider, sub) DO UPDATE SET sub = EXCLUDED.sub
		RETURNING user_id`, userID, provider, sub, email).Scan(&userID)
	if err != nil {
		return nil, fmt.Errorf("link OAuth identity: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit OAuth admission: %w", err)
	}
	return GetUserByID(ctx, pool, userID)
}

// CreateOAuthState stores a short-lived PKCE state token.
func CreateOAuthState(ctx context.Context, pool *pgxpool.Pool, provider, verifier string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate: %w", err)
	}
	token := hex.EncodeToString(b)
	expiresAt := time.Now().Add(10 * time.Minute)
	_, err := pool.Exec(ctx, `
		INSERT INTO oauth_states (token_hash, provider, verifier, expires_at) VALUES ($1, $2, $3, $4)
	`, tokenHash(token), provider, verifier, expiresAt)
	if err != nil {
		return "", fmt.Errorf("insert: %w", err)
	}
	return token, nil
}

type oauthState struct {
	Provider string
	Verifier string
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

// ConsumeBoundOAuthState consumes a state only when both its provider and
// browser-held PKCE verifier match. Failed binding checks leave it untouched.
func ConsumeBoundOAuthState(ctx context.Context, pool *pgxpool.Pool, token, provider, verifier string) (*oauthState, error) {
	var s oauthState
	err := pool.QueryRow(ctx, `
		DELETE FROM oauth_states
		WHERE token_hash = $1 AND provider = $2 AND verifier = $3 AND expires_at > NOW()
		RETURNING provider, verifier
	`, tokenHash(token), provider, verifier).Scan(&s.Provider, &s.Verifier)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("consume bound state: %w", err)
	}
	return &s, nil
}
