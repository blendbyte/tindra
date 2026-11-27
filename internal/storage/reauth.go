package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ReauthMaxFailures is how many wrong answers a session may give when it
// re-confirms the password or two-factor code. The session ends at the limit.
const ReauthMaxFailures = 5

// BeginReauth counts an attempt against the session before the answer is
// checked, so parallel requests cannot get more than ReauthMaxFailures
// guesses. FinishReauth settles it. It returns false when the session is
// gone, expired, belongs to someone else, or has no attempts left.
func BeginReauth(ctx context.Context, pool *pgxpool.Pool, token, userID string) (bool, error) {
	result, err := pool.Exec(ctx, `
		UPDATE sessions SET reauth_attempts = reauth_attempts + 1
		WHERE token_hash = $1 AND user_id = $2 AND expires_at > NOW() AND reauth_attempts < $3
	`, tokenHash(token), userID, ReauthMaxFailures)
	if err != nil {
		return false, fmt.Errorf("begin reauth: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

// FinishReauth settles an attempt from BeginReauth. A right answer clears the
// count, so occasional typos never add up. A wrong one stays counted, and once
// the session has used every attempt it is deleted; ended reports that.
func FinishReauth(ctx context.Context, pool *pgxpool.Pool, token string, ok bool) (ended bool, err error) {
	if ok {
		if _, err := pool.Exec(ctx, `UPDATE sessions SET reauth_attempts = 0 WHERE token_hash = $1`, tokenHash(token)); err != nil {
			return false, fmt.Errorf("finish reauth: %w", err)
		}
		return false, nil
	}
	result, err := pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1 AND reauth_attempts >= $2`, tokenHash(token), ReauthMaxFailures)
	if err != nil {
		return false, fmt.Errorf("finish reauth: %w", err)
	}
	return result.RowsAffected() == 1, nil
}
