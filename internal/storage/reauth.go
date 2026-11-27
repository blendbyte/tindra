package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReauthMaxFailures is how many wrong answers a session may give when it
// re-confirms the password or two-factor code. The session ends at the limit.
const ReauthMaxFailures = 5

// ReauthSession describes the session a re-confirmation runs in.
type ReauthSession struct {
	// MFAEnrolledInSession is true when the current authenticator was
	// confirmed after the session began.
	MFAEnrolledInSession bool
}

// BeginReauth counts an attempt against the session before the answer is
// checked, so parallel requests cannot get more than ReauthMaxFailures
// guesses. FinishReauth settles it. It returns nil when the session is gone,
// expired, belongs to someone else, or has no attempts left.
func BeginReauth(ctx context.Context, pool *pgxpool.Pool, token, userID string) (*ReauthSession, error) {
	var s ReauthSession
	err := pool.QueryRow(ctx, `
		UPDATE sessions s SET reauth_attempts = s.reauth_attempts + 1
		FROM users u
		WHERE s.token_hash = $1 AND s.user_id = $2 AND u.id = s.user_id
			AND s.expires_at > NOW() AND s.reauth_attempts < $3
		RETURNING COALESCE(u.mfa_confirmed_at > s.created_at, FALSE)
	`, tokenHash(token), userID, ReauthMaxFailures).Scan(&s.MFAEnrolledInSession)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("begin reauth: %w", err)
	}
	return &s, nil
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

// SessionSignedInSince reports whether the session began within window,
// meaning its holder signed in that recently.
func SessionSignedInSince(ctx context.Context, pool *pgxpool.Pool, token string, window time.Duration) (bool, error) {
	var recent bool
	err := pool.QueryRow(ctx, `
		SELECT created_at > NOW() - $2::interval FROM sessions WHERE token_hash = $1 AND expires_at > NOW()
	`, tokenHash(token), window).Scan(&recent)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("session sign-in time: %w", err)
	}
	return recent, nil
}
