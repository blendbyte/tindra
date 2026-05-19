package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type APIToken struct {
	ID         string     `json:"id"`
	ProjectID  string     `json:"project_id"`
	Name       string     `json:"name"`
	Writable   bool       `json:"writable"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  time.Time  `json:"expires_at"`
}

// CreateAPIToken generates a new token for the given project. The plaintext is
// returned exactly once - only the SHA-256 hash is stored.
func CreateAPIToken(ctx context.Context, pool *pgxpool.Pool, projectID, name string, writable bool) (*APIToken, string, error) {
	plaintext, hash, err := generateAPIToken()
	if err != nil {
		return nil, "", fmt.Errorf("generate token: %w", err)
	}
	var t APIToken
	err = pool.QueryRow(ctx, `
		INSERT INTO api_tokens (project_id, name, token_hash, writable, expires_at)
		VALUES ($1, $2, $3, $4, NOW() + INTERVAL '90 days')
		RETURNING id, project_id, name, writable, created_at, last_used_at, expires_at
	`, projectID, name, hash, writable).Scan(&t.ID, &t.ProjectID, &t.Name, &t.Writable, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt)
	if err != nil {
		return nil, "", fmt.Errorf("insert: %w", err)
	}
	return &t, plaintext, nil
}

func ListAPITokens(ctx context.Context, pool *pgxpool.Pool, projectID string) ([]*APIToken, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, project_id, name, writable, created_at, last_used_at, expires_at
		FROM api_tokens WHERE project_id = $1 ORDER BY created_at DESC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var tokens []*APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.Name, &t.Writable, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		tokens = append(tokens, &t)
	}
	return tokens, rows.Err()
}

// DeleteAPIToken removes a token by ID, scoped to the project for safety.
// Returns true if a row was deleted.
func DeleteAPIToken(ctx context.Context, pool *pgxpool.Pool, id, projectID string) (bool, error) {
	tag, err := pool.Exec(ctx,
		`DELETE FROM api_tokens WHERE id = $1 AND project_id = $2`, id, projectID)
	if err != nil {
		return false, fmt.Errorf("delete: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func ListAllAPITokens(ctx context.Context, pool *pgxpool.Pool) ([]*APIToken, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, project_id, name, writable, created_at, last_used_at, expires_at
		FROM api_tokens ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var tokens []*APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.Name, &t.Writable, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		tokens = append(tokens, &t)
	}
	return tokens, rows.Err()
}

func DeleteAPITokenByID(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM api_tokens WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("delete: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// GetAPITokenByHash looks up a token by the SHA-256 hash of its plaintext value.
// Returns nil, nil if not found.
func GetAPITokenByHash(ctx context.Context, pool *pgxpool.Pool, hash string) (*APIToken, error) {
	var t APIToken
	err := pool.QueryRow(ctx, `
		SELECT id, project_id, name, writable, created_at, last_used_at, expires_at
		FROM api_tokens WHERE token_hash = $1 AND expires_at > NOW()
	`, hash).Scan(&t.ID, &t.ProjectID, &t.Name, &t.Writable, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return &t, nil
}

// TouchAPIToken updates last_used_at to now. Call in a goroutine - no need to
// block the request on a non-critical write.
func TouchAPIToken(ctx context.Context, pool *pgxpool.Pool, id string) {
	_, _ = pool.Exec(ctx, `UPDATE api_tokens SET last_used_at = NOW() WHERE id = $1`, id)
}

// HashAPIToken returns the SHA-256 hash of a plaintext token value. Used by the
// auth middleware to look up an incoming Bearer token without a round-trip.
func HashAPIToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

func generateAPIToken() (plaintext, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	plaintext = "tindra_" + hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(plaintext))
	hash = hex.EncodeToString(sum[:])
	return
}
