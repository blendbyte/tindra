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

// FindOrCreateOAuthUser looks up an existing OAuth identity and returns its user.
// If the identity doesn't exist, it finds or creates a local user by email and links the identity.
func FindOrCreateOAuthUser(ctx context.Context, pool *pgxpool.Pool, provider, sub, email string) (*User, error) {
	email = strings.ToLower(email)

	// Fast path: identity already linked.
	var userID string
	err := pool.QueryRow(ctx, `
		SELECT user_id FROM oauth_identities WHERE provider = $1 AND sub = $2
	`, provider, sub).Scan(&userID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("lookup identity: %w", err)
	}
	if err == nil {
		return GetUserByID(ctx, pool, userID)
	}

	// Identity not found - find or create user by email, then link.
	user, err := GetUserByEmail(ctx, pool, email)
	if err != nil {
		return nil, err
	}
	if user == nil {
		user, err = CreateOAuthUser(ctx, pool, email)
		if err != nil {
			return nil, err
		}
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO oauth_identities (user_id, provider, sub, email)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (provider, sub) DO NOTHING
	`, user.ID, provider, sub, email)
	if err != nil {
		return nil, fmt.Errorf("insert identity: %w", err)
	}
	return user, nil
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
		INSERT INTO oauth_states (token, provider, verifier, expires_at) VALUES ($1, $2, $3, $4)
	`, token, provider, verifier, expiresAt)
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
		WHERE token = $1 AND expires_at > NOW()
		RETURNING provider, verifier
	`, token).Scan(&s.Provider, &s.Verifier)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return &s, nil
}
