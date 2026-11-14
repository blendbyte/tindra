package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PushSubscription is one browser or installed PWA that receives Web Push.
// The encryption keys never leave the server once stored.
type PushSubscription struct {
	ID            string     `json:"id"`
	UserID        string     `json:"-"`
	Endpoint      string     `json:"endpoint"`
	P256dh        string     `json:"-"`
	Auth          string     `json:"-"`
	Label         string     `json:"label"`
	FailureCount  int        `json:"failure_count"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

const pushSubscriptionColumns = `id, user_id, endpoint, p256dh, auth, label, failure_count, last_success_at, created_at`

func scanPushSubscription(row pgx.CollectableRow) (*PushSubscription, error) {
	var s PushSubscription
	err := row.Scan(&s.ID, &s.UserID, &s.Endpoint, &s.P256dh, &s.Auth, &s.Label, &s.FailureCount, &s.LastSuccessAt, &s.CreatedAt)
	return &s, err
}

// UpsertPushSubscription stores a subscription for userID. A browser keeps
// its endpoint across logins, so an endpoint that is already stored moves to
// the user who registered it last and gets the new keys.
func UpsertPushSubscription(ctx context.Context, pool *pgxpool.Pool, s *PushSubscription) (*PushSubscription, error) {
	rows, err := pool.Query(ctx, `
		INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, label)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (endpoint) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			p256dh = EXCLUDED.p256dh,
			auth = EXCLUDED.auth,
			label = EXCLUDED.label,
			failure_count = 0
		RETURNING `+pushSubscriptionColumns,
		s.UserID, s.Endpoint, s.P256dh, s.Auth, s.Label)
	if err != nil {
		return nil, fmt.Errorf("upsert push subscription: %w", err)
	}
	out, err := pgx.CollectExactlyOneRow(rows, scanPushSubscription)
	if err != nil {
		return nil, fmt.Errorf("upsert push subscription: %w", err)
	}
	return out, nil
}

// ListPushSubscriptions returns the user's devices, newest first.
func ListPushSubscriptions(ctx context.Context, pool *pgxpool.Pool, userID string) ([]*PushSubscription, error) {
	rows, err := pool.Query(ctx, `SELECT `+pushSubscriptionColumns+`
		FROM push_subscriptions WHERE user_id = $1 ORDER BY created_at DESC, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list push subscriptions: %w", err)
	}
	subs, err := pgx.CollectRows(rows, scanPushSubscription)
	if err != nil {
		return nil, fmt.Errorf("list push subscriptions: %w", err)
	}
	return subs, nil
}

// GetPushSubscription returns the subscription only when userID owns it.
func GetPushSubscription(ctx context.Context, pool *pgxpool.Pool, id, userID string) (*PushSubscription, error) {
	rows, err := pool.Query(ctx, `SELECT `+pushSubscriptionColumns+`
		FROM push_subscriptions WHERE id::text = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return nil, fmt.Errorf("get push subscription: %w", err)
	}
	s, err := pgx.CollectExactlyOneRow(rows, scanPushSubscription)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get push subscription: %w", err)
	}
	return s, nil
}

// DeletePushSubscription removes one of userID's devices.
func DeletePushSubscription(ctx context.Context, pool *pgxpool.Pool, id, userID string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE id::text = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return false, fmt.Errorf("delete push subscription: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// DeletePushSubscriptionByEndpoint removes userID's subscription for a
// browser endpoint, used when the browser unsubscribes or logs out.
func DeletePushSubscriptionByEndpoint(ctx context.Context, pool *pgxpool.Pool, endpoint, userID string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE endpoint = $1 AND user_id = $2`, endpoint, userID)
	if err != nil {
		return false, fmt.Errorf("delete push subscription: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ListPushSubscriptionsForRule returns every device of every recipient of a rule.
func ListPushSubscriptionsForRule(ctx context.Context, pool *pgxpool.Pool, ruleID string) ([]*PushSubscription, error) {
	rows, err := pool.Query(ctx, `SELECT ps.id, ps.user_id, ps.endpoint, ps.p256dh, ps.auth, ps.label, ps.failure_count, ps.last_success_at, ps.created_at
		FROM push_subscriptions ps
		JOIN alert_rule_recipients rr ON rr.user_id = ps.user_id
		WHERE rr.rule_id = $1
		ORDER BY ps.created_at, ps.id`, ruleID)
	if err != nil {
		return nil, fmt.Errorf("list rule push subscriptions: %w", err)
	}
	subs, err := pgx.CollectRows(rows, scanPushSubscription)
	if err != nil {
		return nil, fmt.Errorf("list rule push subscriptions: %w", err)
	}
	return subs, nil
}

// RecordPushSuccess resets the failure count after a delivery.
func RecordPushSuccess(ctx context.Context, pool *pgxpool.Pool, id string) error {
	if _, err := pool.Exec(ctx,
		`UPDATE push_subscriptions SET failure_count = 0, last_success_at = NOW() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("record push success: %w", err)
	}
	return nil
}

// RecordPushFailure counts a failed delivery and deletes the subscription
// once it has failed maxFailures times in a row. It reports whether the
// subscription was deleted.
func RecordPushFailure(ctx context.Context, pool *pgxpool.Pool, id string, maxFailures int) (bool, error) {
	var count int
	err := pool.QueryRow(ctx,
		`UPDATE push_subscriptions SET failure_count = failure_count + 1 WHERE id = $1 RETURNING failure_count`, id).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("record push failure: %w", err)
	}
	if count < maxFailures {
		return false, nil
	}
	return ExpirePushSubscription(ctx, pool, id)
}

// ExpirePushSubscription deletes a subscription the push service no longer knows.
func ExpirePushSubscription(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("expire push subscription: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// GetOrCreateInstanceSecret returns the stored value for key, generating and
// storing one on first use. Concurrent first starts agree on one value.
func GetOrCreateInstanceSecret(ctx context.Context, pool *pgxpool.Pool, key string, generate func() (string, error)) (string, error) {
	var value string
	err := pool.QueryRow(ctx, `SELECT value FROM instance_secrets WHERE key = $1`, key).Scan(&value)
	if err == nil {
		return value, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("read instance secret: %w", err)
	}
	fresh, err := generate()
	if err != nil {
		return "", err
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO instance_secrets (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`, key, fresh); err != nil {
		return "", fmt.Errorf("store instance secret: %w", err)
	}
	if err := pool.QueryRow(ctx, `SELECT value FROM instance_secrets WHERE key = $1`, key).Scan(&value); err != nil {
		return "", fmt.Errorf("read instance secret: %w", err)
	}
	return value, nil
}

// ExistingUserIDs returns which of ids belong to existing users. Malformed
// IDs are ignored rather than failing the query.
func ExistingUserIDs(ctx context.Context, pool *pgxpool.Pool, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return []string{}, nil
	}
	rows, err := pool.Query(ctx, `SELECT id::text FROM users WHERE id::text = ANY($1::text[]) ORDER BY id`, ids)
	if err != nil {
		return nil, fmt.Errorf("existing users: %w", err)
	}
	found, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("existing users: %w", err)
	}
	return found, nil
}
