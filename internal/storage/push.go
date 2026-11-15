package storage

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"
	"strings"
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

// ErrPushEndpointTaken is returned when an endpoint is registered to
// another user and the caller cannot show it is the same browser.
var ErrPushEndpointTaken = errors.New("endpoint belongs to another user")

// ErrTooManyPushDevices is returned when a user already has the maximum
// number of devices.
var ErrTooManyPushDevices = errors.New("too many devices registered; remove one first")

// PushUpsert is the outcome of UpsertPushSubscription.
type PushUpsert struct {
	Subscription *PushSubscription
	// Created is true when the device is new for this user.
	Created bool
	// PreviousOwner is the user the endpoint moved away from, if any.
	PreviousOwner string
}

// lockPushEndpoint serialises changes to one endpoint until the transaction
// ends, so the ownership checks below cannot race a concurrent insert.
func lockPushEndpoint(ctx context.Context, tx pgx.Tx, endpoint string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('push-endpoint:' || $1, 0))`, endpoint)
	return err
}

// ErrPushSessionEnded is returned when the session registering a device
// ended (signed out, or revoked by a password change) before it committed.
var ErrPushSessionEnded = errors.New("session ended")

// PushUpsertOptions limit UpsertPushSubscription.
type PushUpsertOptions struct {
	// MaxDevices is how many devices the user may have; 0 means no limit.
	MaxDevices int
	// SessionToken, when set, must still be a valid session of the user once
	// the user's devices are locked. A password change takes the same lock
	// before it deletes sessions and devices, so a registration racing it
	// cannot add a device after the revocation.
	SessionToken string
}

// lockUserRow takes the users row FOR KEY SHARE: the second step of the
// lock order every device change shares (endpoint lock, users row, the
// user's devices; see revokeCredentialArtifacts). It reports whether the
// user still exists.
func lockUserRow(ctx context.Context, tx pgx.Tx, userID string) (bool, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id = $1 FOR KEY SHARE`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// lockPushUser serialises changes to one user's devices until the
// transaction ends. Take it after any endpoint lock.
func lockPushUser(ctx context.Context, tx pgx.Tx, userID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('push-user:' || $1, 0))`, userID)
	return err
}

// UpsertPushSubscription stores a subscription for s.UserID.
//
// A browser keeps its endpoint and keys across logins, so a stored endpoint
// moves to whoever registers it next, but only with the same keys: those
// come from the browser itself, while an endpoint URL alone can leak (logs,
// screenshots). With other keys it stays with its owner and
// ErrPushEndpointTaken is returned. The owner may change the keys.
// Registering again resets the failure history.
func UpsertPushSubscription(ctx context.Context, pool *pgxpool.Pool, s *PushSubscription, opts PushUpsertOptions) (*PushUpsert, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("upsert push subscription: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// Lock order, the same everywhere: endpoint, then the users row, then
	// the user's devices. A password change holds the users row (FOR UPDATE)
	// when it takes the device lock, and the insert below needs the row for
	// its foreign key, so taking the row first here means waiting for the
	// password change instead of deadlocking with it. A deleted user has no
	// row left, which ends the registration too.
	if err := lockPushEndpoint(ctx, tx, s.Endpoint); err != nil {
		return nil, fmt.Errorf("upsert push subscription: %w", err)
	}
	userExists, err := lockUserRow(ctx, tx, s.UserID)
	if err != nil {
		return nil, fmt.Errorf("upsert push subscription: %w", err)
	}
	if !userExists {
		return nil, ErrPushSessionEnded
	}
	if err := lockPushUser(ctx, tx, s.UserID); err != nil {
		return nil, fmt.Errorf("upsert push subscription: %w", err)
	}
	if opts.SessionToken != "" {
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sessions WHERE token_hash = $1 AND user_id = $2 AND expires_at > NOW())`,
			tokenHash(opts.SessionToken), s.UserID).Scan(&valid); err != nil {
			return nil, fmt.Errorf("upsert push subscription: %w", err)
		}
		if !valid {
			return nil, ErrPushSessionEnded
		}
	}

	var owner, p256dh, auth string
	err = tx.QueryRow(ctx, `SELECT user_id, p256dh, auth FROM push_subscriptions WHERE endpoint = $1`,
		s.Endpoint).Scan(&owner, &p256dh, &auth)
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("upsert push subscription: %w", err)
	}
	if exists && owner != s.UserID && (!sameSecret(p256dh, s.P256dh) || !sameSecret(auth, s.Auth)) {
		return nil, ErrPushEndpointTaken
	}
	result := &PushUpsert{Created: !exists || owner != s.UserID}
	if exists && owner != s.UserID {
		result.PreviousOwner = owner
	}
	if result.Created && opts.MaxDevices > 0 {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM push_subscriptions WHERE user_id = $1`, s.UserID).Scan(&n); err != nil {
			return nil, fmt.Errorf("upsert push subscription: %w", err)
		}
		if n >= opts.MaxDevices {
			return nil, ErrTooManyPushDevices
		}
	}

	rows, err := tx.Query(ctx, `
		INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, label)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (endpoint) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			p256dh = EXCLUDED.p256dh,
			auth = EXCLUDED.auth,
			label = EXCLUDED.label,
			failure_count = 0,
			failing_since = NULL,
			-- A device that changes hands starts fresh: the new owner must
			-- not see when the previous one last got an alert.
			last_success_at = CASE WHEN push_subscriptions.user_id = EXCLUDED.user_id
				THEN push_subscriptions.last_success_at END,
			created_at = CASE WHEN push_subscriptions.user_id = EXCLUDED.user_id
				THEN push_subscriptions.created_at ELSE NOW() END
		RETURNING `+pushSubscriptionColumns,
		s.UserID, s.Endpoint, s.P256dh, s.Auth, s.Label)
	if err != nil {
		return nil, fmt.Errorf("upsert push subscription: %w", err)
	}
	if result.Subscription, err = pgx.CollectExactlyOneRow(rows, scanPushSubscription); err != nil {
		return nil, fmt.Errorf("upsert push subscription: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("upsert push subscription: %w", err)
	}
	return result, nil
}

// ReplacePushSubscription moves a device from oldEndpoint to a new endpoint
// and keys, keeping its ID, owner and label. The service worker does this
// when the push service rotates the subscription, possibly long after the
// session ended, so instead of a session it proves it is the same browser
// with the old subscription's auth secret. It returns nil when no device
// matches, so a device removed from the list stays removed.
func ReplacePushSubscription(ctx context.Context, pool *pgxpool.Pool, oldEndpoint, oldAuth string, s *PushSubscription) (*PushSubscription, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("replace push subscription: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// Two endpoints: always lock them in the same order.
	endpoints := []string{oldEndpoint, s.Endpoint}
	slices.Sort(endpoints)
	for _, e := range slices.Compact(endpoints) {
		if err := lockPushEndpoint(ctx, tx, e); err != nil {
			return nil, fmt.Errorf("replace push subscription: %w", err)
		}
	}

	var id, userID, auth string
	err = tx.QueryRow(ctx, `SELECT id, user_id, auth FROM push_subscriptions WHERE endpoint = $1`, oldEndpoint).Scan(&id, &userID, &auth)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("replace push subscription: %w", err)
	}
	if !sameSecret(auth, oldAuth) {
		return nil, nil
	}
	// Same order as UpsertPushSubscription: the users row, then the user's
	// devices. Deleting the user holds the row before its cascade reaches
	// the devices, so this waits for it instead of deadlocking on them. A
	// password change keeps one device by its endpoint under the device
	// lock; waiting for it means the device is moved after that, not lost.
	exists, err := lockUserRow(ctx, tx, userID)
	if err != nil {
		return nil, fmt.Errorf("replace push subscription: %w", err)
	}
	if !exists {
		return nil, nil
	}
	if err := lockPushUser(ctx, tx, userID); err != nil {
		return nil, fmt.Errorf("replace push subscription: %w", err)
	}
	var still bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM push_subscriptions WHERE id = $1 AND endpoint = $2)`, id, oldEndpoint).Scan(&still); err != nil {
		return nil, fmt.Errorf("replace push subscription: %w", err)
	}
	if !still {
		return nil, nil
	}
	if s.Endpoint != oldEndpoint {
		var owner string
		err = tx.QueryRow(ctx, `SELECT user_id FROM push_subscriptions WHERE endpoint = $1`, s.Endpoint).Scan(&owner)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return nil, fmt.Errorf("replace push subscription: %w", err)
		case owner != userID:
			return nil, ErrPushEndpointTaken
		default:
			// Registered again already (by the app, before the worker got here).
			if _, err := tx.Exec(ctx, `DELETE FROM push_subscriptions WHERE endpoint = $1`, s.Endpoint); err != nil {
				return nil, fmt.Errorf("replace push subscription: %w", err)
			}
		}
	}
	rows, err := tx.Query(ctx, `UPDATE push_subscriptions
		SET endpoint = $2, p256dh = $3, auth = $4, failure_count = 0, failing_since = NULL
		WHERE id = $1
		RETURNING `+pushSubscriptionColumns, id, s.Endpoint, s.P256dh, s.Auth)
	if err != nil {
		return nil, fmt.Errorf("replace push subscription: %w", err)
	}
	out, err := pgx.CollectExactlyOneRow(rows, scanPushSubscription)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("replace push subscription: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("replace push subscription: %w", err)
	}
	return out, nil
}

// sameSecret compares two base64url key values in constant time. Browsers
// may or may not pad them, so padding is ignored.
func sameSecret(a, b string) bool {
	a, b = strings.TrimRight(a, "="), strings.TrimRight(b, "=")
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// EndedSession is what EndSession did.
type EndedSession struct {
	// UserID is the session's user, "" when there was no such session.
	UserID string
	// Ended is false when the session was already gone (ended meanwhile).
	Ended bool
	// DeviceID is the removed push device, "" when none.
	DeviceID string
}

// EndSession deletes a session and, when pushEndpoint is set, the signed-out
// user's device at that endpoint. The device goes under the locks device
// registration takes (endpoint, users row, the user's devices, in that
// order, as everywhere; see revokeCredentialArtifacts), so a registration
// that already passed its session check commits first and is removed here
// instead of slipping in after the logout. The session itself is deleted
// last and always: should removing the device fail, the session is still
// ended and the error returned.
func EndSession(ctx context.Context, pool *pgxpool.Pool, token, pushEndpoint string) (EndedSession, error) {
	var err error
	if pushEndpoint != "" {
		var out EndedSession
		if out, err = endSessionWithDevice(ctx, pool, token, pushEndpoint); err == nil {
			return out, nil
		}
	}
	userID, derr := DeleteSessionReturningUserID(ctx, pool, token)
	if derr != nil {
		return EndedSession{}, errors.Join(err, derr)
	}
	return EndedSession{UserID: userID, Ended: userID != ""}, err
}

func endSessionWithDevice(ctx context.Context, pool *pgxpool.Pool, token, pushEndpoint string) (EndedSession, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return EndedSession{}, fmt.Errorf("end session: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// Read the session without locking it: password changes and user
	// deletion lock the users row and the user's devices before the
	// sessions, so the session row is only touched after both here too.
	var userID string
	err = tx.QueryRow(ctx, `SELECT user_id FROM sessions WHERE token_hash = $1`, tokenHash(token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return EndedSession{}, nil
	}
	if err != nil {
		return EndedSession{}, fmt.Errorf("end session: %w", err)
	}
	if err := lockPushEndpoint(ctx, tx, pushEndpoint); err != nil {
		return EndedSession{}, fmt.Errorf("end session: %w", err)
	}
	// A user deleted meanwhile took the device with it; carry on regardless.
	if _, err := lockUserRow(ctx, tx, userID); err != nil {
		return EndedSession{}, fmt.Errorf("end session: %w", err)
	}
	if err := lockPushUser(ctx, tx, userID); err != nil {
		return EndedSession{}, fmt.Errorf("end session: %w", err)
	}
	var deviceID string
	err = tx.QueryRow(ctx, `DELETE FROM push_subscriptions WHERE endpoint = $1 AND user_id = $2 RETURNING id`, pushEndpoint, userID).Scan(&deviceID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return EndedSession{}, fmt.Errorf("end session: %w", err)
	}
	tag, err := tx.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash(token))
	if err != nil {
		return EndedSession{}, fmt.Errorf("end session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return EndedSession{}, fmt.Errorf("end session: %w", err)
	}
	// Ended is false when a password change ended it meanwhile.
	return EndedSession{UserID: userID, Ended: tag.RowsAffected() == 1, DeviceID: deviceID}, nil
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
// id must be a UUID (the router checks).
func GetPushSubscription(ctx context.Context, pool *pgxpool.Pool, id, userID string) (*PushSubscription, error) {
	rows, err := pool.Query(ctx, `SELECT `+pushSubscriptionColumns+`
		FROM push_subscriptions WHERE id = $1 AND user_id = $2`, id, userID)
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
	tag, err := pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return false, fmt.Errorf("delete push subscription: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// DeletePushSubscriptionByEndpoint removes userID's subscription for a
// browser endpoint, used when the browser turns push off. (Logout removes
// its device through EndSession.) It
// returns the removed device's ID, or "" when there was none.
func DeletePushSubscriptionByEndpoint(ctx context.Context, pool *pgxpool.Pool, endpoint, userID string) (string, error) {
	var id string
	err := pool.QueryRow(ctx, `DELETE FROM push_subscriptions WHERE endpoint = $1 AND user_id = $2 RETURNING id`, endpoint, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("delete push subscription: %w", err)
	}
	return id, nil
}

// ListPushSubscriptionsForRule returns every device of every recipient of a
// rule, or, when only is not empty, just those of them (a retry's devices).
func ListPushSubscriptionsForRule(ctx context.Context, pool *pgxpool.Pool, ruleID string, only []string) ([]*PushSubscription, error) {
	var ids []string
	if len(only) > 0 {
		if ids = validUUIDs(only); len(ids) == 0 {
			return nil, nil
		}
	}
	rows, err := pool.Query(ctx, `SELECT `+pushSubscriptionColumns+`
		FROM push_subscriptions
		WHERE user_id IN (SELECT user_id FROM alert_rule_recipients WHERE rule_id = $1)
		  AND ($2::uuid[] IS NULL OR id = ANY($2::uuid[]))
		ORDER BY created_at, id`, ruleID, ids)
	if err != nil {
		return nil, fmt.Errorf("list rule push subscriptions: %w", err)
	}
	subs, err := pgx.CollectRows(rows, scanPushSubscription)
	if err != nil {
		return nil, fmt.Errorf("list rule push subscriptions: %w", err)
	}
	return subs, nil
}

// The bookkeeping after a send matches the device as it was sent to: its
// ID, endpoint and owner. If it was rotated to a new endpoint, or moved to
// another user, while the send was in flight, the answer is about something
// that no longer exists and must not touch the device.
const pushSentTo = `id = $1 AND endpoint = $2 AND user_id = $3`

// RecordPushSuccess resets the failure count after a delivery to sub.
func RecordPushSuccess(ctx context.Context, pool *pgxpool.Pool, sub *PushSubscription) error {
	if _, err := pool.Exec(ctx,
		`UPDATE push_subscriptions SET failure_count = 0, failing_since = NULL, last_success_at = NOW() WHERE `+pushSentTo,
		sub.ID, sub.Endpoint, sub.UserID); err != nil {
		return fmt.Errorf("record push success: %w", err)
	}
	return nil
}

// RecordPushFailure counts a failed delivery to sub. When mayDelete is
// set it also deletes the device once it has failed maxFailures times in a
// row over at least grace, and reports whether it did. The delete re-checks
// both conditions, so a device that was re-registered or delivered to in
// the meantime stays, and the grace period is measured on the database clock.
func RecordPushFailure(ctx context.Context, pool *pgxpool.Pool, sub *PushSubscription, maxFailures int, grace time.Duration, mayDelete bool) (bool, error) {
	if _, err := pool.Exec(ctx, `UPDATE push_subscriptions
		SET failure_count = failure_count + 1, failing_since = COALESCE(failing_since, NOW())
		WHERE `+pushSentTo, sub.ID, sub.Endpoint, sub.UserID); err != nil {
		return false, fmt.Errorf("record push failure: %w", err)
	}
	if !mayDelete {
		return false, nil
	}
	tag, err := pool.Exec(ctx, `DELETE FROM push_subscriptions
		WHERE `+pushSentTo+` AND failure_count >= $4 AND failing_since <= NOW() - make_interval(secs => $5)`,
		sub.ID, sub.Endpoint, sub.UserID, maxFailures, grace.Seconds())
	if err != nil {
		return false, fmt.Errorf("record push failure: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// PushDeliveredToService reports whether any device on a push service got
// a push within the last window, by the database clock (which wrote
// last_success_at). service is a host ("fcm.googleapis.com", with a port
// if any) or a "*." pattern ("*.notify.windows.com") for services whose
// devices sit on many hosts. When a push service fails for every device and
// none got anything lately, the instance is the likely cause (its VAPID key
// or contact), not the devices.
func PushDeliveredToService(ctx context.Context, pool *pgxpool.Pool, service string, window time.Duration) (bool, error) {
	// The host is cut out of the endpoint once, so a pattern can never
	// match inside a path, and both sides are compared in lower case.
	// Hosts from the allowlist carry no port, so the endpoint's port is
	// dropped for them; a host with a port (no allowlist) is compared whole.
	service = strings.ToLower(service)
	cond := `split_part(host, ':', 1) = $2`
	arg := service
	switch suffix, wildcard := strings.CutPrefix(service, "*."); {
	case wildcard:
		cond = `right(split_part(host, ':', 1), length($2)) = $2`
		arg = "." + suffix
	case strings.Contains(service, ":"):
		cond = `host = $2`
	}
	var ok bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM (
			SELECT lower(substring(endpoint from '^https://([^/?#]+)')) AS host
			FROM push_subscriptions WHERE last_success_at > NOW() - make_interval(secs => $1)
		) recent WHERE `+cond+`)`, window.Seconds(), arg).Scan(&ok); err != nil {
		return false, fmt.Errorf("push delivered to service: %w", err)
	}
	return ok, nil
}

// ExpirePushSubscription deletes a device whose endpoint the push service
// no longer knows.
func ExpirePushSubscription(ctx context.Context, pool *pgxpool.Pool, sub *PushSubscription) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE `+pushSentTo, sub.ID, sub.Endpoint, sub.UserID)
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
