package storage_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

// waitUntilBlocked waits until another connection is waiting on a lock in
// a query containing fragment, so a test continues only once the
// interleaving it is about has really happened.
func waitUntilBlocked(t *testing.T, fragment string) {
	t.Helper()
	require.Eventually(t, func() bool {
		var waiting bool
		err := testPool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND position($1 in query) > 0)`, fragment).Scan(&waiting)
		return err == nil && waiting
	}, 10*time.Second, 10*time.Millisecond, "nothing blocked on %q", fragment)
}

func pushUser(t *testing.T, email string) *storage.User {
	t.Helper()
	u, err := storage.CreateUser(context.Background(), testPool, email, "password123456")
	require.NoError(t, err)
	return u
}

func pushSub(userID, endpoint string) *storage.PushSubscription {
	return &storage.PushSubscription{UserID: userID, Endpoint: endpoint, P256dh: "pub", Auth: "auth", Label: "Firefox on Linux"}
}

// upsertPush registers s without a device limit and returns the row.
func upsertPush(ctx context.Context, s *storage.PushSubscription) (*storage.PushSubscription, error) {
	res, err := storage.UpsertPushSubscription(ctx, testPool, s, storage.PushUpsertOptions{})
	if err != nil {
		return nil, err
	}
	return res.Subscription, nil
}

func TestPushSubscriptions_upsertListDelete(t *testing.T) {
	ctx := context.Background()
	truncateUsers(t)
	alice := pushUser(t, "alice@example.com")
	bob := pushUser(t, "bob@example.com")

	a1, err := upsertPush(ctx, pushSub(alice.ID, "https://push.example.com/a1"))
	require.NoError(t, err)
	require.Equal(t, "Firefox on Linux", a1.Label)
	_, err = upsertPush(ctx, pushSub(alice.ID, "https://push.example.com/a2"))
	require.NoError(t, err)

	subs, err := storage.ListPushSubscriptions(ctx, testPool, alice.ID)
	require.NoError(t, err)
	require.Len(t, subs, 2)

	// Owner scoping.
	got, err := storage.GetPushSubscription(ctx, testPool, a1.ID, bob.ID)
	require.NoError(t, err)
	require.Nil(t, got)
	deleted, err := storage.DeletePushSubscription(ctx, testPool, a1.ID, bob.ID)
	require.NoError(t, err)
	require.False(t, deleted)

	// Another user with other keys (someone who only knows the URL) cannot
	// take the endpoint.
	_, err = storage.RecordPushFailure(ctx, testPool, a1, 5, 0, true)
	require.NoError(t, err)
	stolen := pushSub(bob.ID, "https://push.example.com/a1")
	stolen.P256dh = "attackerpub"
	_, err = storage.UpsertPushSubscription(ctx, testPool, stolen, storage.PushUpsertOptions{})
	require.ErrorIs(t, err, storage.ErrPushEndpointTaken)

	// The same browser (same keys) registering for another user moves the
	// endpoint, names the previous owner and resets its history, so bob
	// cannot see when alice last got an alert.
	require.NoError(t, storage.RecordPushSuccess(ctx, testPool, a1))
	_, err = testPool.Exec(ctx, `UPDATE push_subscriptions SET created_at = NOW() - interval '30 days' WHERE id = $1`, a1.ID)
	require.NoError(t, err)
	res, err := storage.UpsertPushSubscription(ctx, testPool, pushSub(bob.ID, "https://push.example.com/a1"), storage.PushUpsertOptions{})
	require.NoError(t, err)
	require.Equal(t, a1.ID, res.Subscription.ID)
	require.Equal(t, bob.ID, res.Subscription.UserID)
	require.Equal(t, alice.ID, res.PreviousOwner)
	require.True(t, res.Created, "new for bob")
	require.Zero(t, res.Subscription.FailureCount)
	require.Nil(t, res.Subscription.LastSuccessAt)
	require.WithinDuration(t, time.Now(), res.Subscription.CreatedAt, time.Minute)

	// Its owner may change the keys; nobody else is named.
	rekeyed := pushSub(bob.ID, "https://push.example.com/a1")
	rekeyed.P256dh = "newpub"
	res, err = storage.UpsertPushSubscription(ctx, testPool, rekeyed, storage.PushUpsertOptions{})
	require.NoError(t, err)
	require.Equal(t, "newpub", res.Subscription.P256dh)
	require.Empty(t, res.PreviousOwner)
	require.False(t, res.Created)

	subs, err = storage.ListPushSubscriptions(ctx, testPool, alice.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)

	removed, err := storage.DeletePushSubscriptionByEndpoint(ctx, testPool, "https://push.example.com/a1", alice.ID)
	require.NoError(t, err)
	require.Empty(t, removed, "alice no longer owns a1")
	removed, err = storage.DeletePushSubscriptionByEndpoint(ctx, testPool, "https://push.example.com/a1", bob.ID)
	require.NoError(t, err)
	require.Equal(t, a1.ID, removed)

	deleted, err = storage.DeletePushSubscription(ctx, testPool, subs[0].ID, alice.ID)
	require.NoError(t, err)
	require.True(t, deleted)
}

func TestPushSubscriptions_failureAndSuccessBookkeeping(t *testing.T) {
	ctx := context.Background()
	truncateUsers(t)
	u := pushUser(t, "carol@example.com")
	s, err := upsertPush(ctx, pushSub(u.ID, "https://push.example.com/c"))
	require.NoError(t, err)

	for range 2 {
		gone, err := storage.RecordPushFailure(ctx, testPool, s, 3, 0, true)
		require.NoError(t, err)
		require.False(t, gone)
	}
	require.NoError(t, storage.RecordPushSuccess(ctx, testPool, s))
	got, err := storage.GetPushSubscription(ctx, testPool, s.ID, u.ID)
	require.NoError(t, err)
	require.Zero(t, got.FailureCount)
	require.NotNil(t, got.LastSuccessAt)

	// Failures are counted, but the device stays until it has been failing
	// for the whole grace period.
	for range 4 {
		gone, err := storage.RecordPushFailure(ctx, testPool, s, 3, time.Hour, true)
		require.NoError(t, err)
		require.False(t, gone)
	}
	got, err = storage.GetPushSubscription(ctx, testPool, s.ID, u.ID)
	require.NoError(t, err)
	require.Equal(t, 4, got.FailureCount)

	// Registering again starts over, however old the device is.
	_, err = testPool.Exec(ctx, `UPDATE push_subscriptions SET created_at = NOW() - interval '30 days', failing_since = NOW() - interval '2 hours' WHERE id = $1`, s.ID)
	require.NoError(t, err)
	_, err = upsertPush(ctx, pushSub(u.ID, "https://push.example.com/c"))
	require.NoError(t, err)
	for range 3 {
		gone, err := storage.RecordPushFailure(ctx, testPool, s, 3, time.Hour, true)
		require.NoError(t, err)
		require.False(t, gone, "re-registered just now")
	}

	_, err = testPool.Exec(ctx, `UPDATE push_subscriptions SET failing_since = NOW() - interval '2 hours' WHERE id = $1`, s.ID)
	require.NoError(t, err)
	// Without leave to delete (its push service delivers nothing lately) it
	// is only counted.
	gone, err := storage.RecordPushFailure(ctx, testPool, s, 3, time.Hour, false)
	require.NoError(t, err)
	require.False(t, gone)
	// An answer about another endpoint (the device was rotated meanwhile)
	// does not touch it.
	rotated := *s
	rotated.Endpoint = "https://push.example.com/rotated-away"
	gone, err = storage.RecordPushFailure(ctx, testPool, &rotated, 3, time.Hour, true)
	require.NoError(t, err)
	require.False(t, gone)
	expired, err := storage.ExpirePushSubscription(ctx, testPool, &rotated)
	require.NoError(t, err)
	require.False(t, expired)
	// Nor does an answer from a send made while it belonged to someone else.
	other := *s
	other.UserID = "3f1c2a52-8a7e-4c7b-9a51-2b1f9d0e7c11"
	gone, err = storage.RecordPushFailure(ctx, testPool, &other, 3, time.Hour, true)
	require.NoError(t, err)
	require.False(t, gone)
	before, err := storage.GetPushSubscription(ctx, testPool, s.ID, u.ID)
	require.NoError(t, err)
	require.NoError(t, storage.RecordPushSuccess(ctx, testPool, &other))
	got, err = storage.GetPushSubscription(ctx, testPool, s.ID, u.ID)
	require.NoError(t, err)
	require.Equal(t, before.LastSuccessAt, got.LastSuccessAt, "the success was not this owner's")
	require.Equal(t, before.FailureCount, got.FailureCount)

	gone, err = storage.RecordPushFailure(ctx, testPool, s, 3, time.Hour, true)
	require.NoError(t, err)
	require.True(t, gone)
	got, err = storage.GetPushSubscription(ctx, testPool, s.ID, u.ID)
	require.NoError(t, err)
	require.Nil(t, got)

	gone, err = storage.RecordPushFailure(ctx, testPool, s, 3, 0, true)
	require.NoError(t, err)
	require.False(t, gone, "missing subscription is a no-op")
}

func TestReplacePushSubscription(t *testing.T) {
	ctx := context.Background()
	truncateUsers(t)
	alice := pushUser(t, "alice-rotate@example.com")
	bob := pushUser(t, "bob-rotate@example.com")
	old, err := upsertPush(ctx, pushSub(alice.ID, "https://push.example.com/old"))
	require.NoError(t, err)
	_, err = storage.RecordPushFailure(ctx, testPool, old, 5, 0, true)
	require.NoError(t, err)

	// The old auth secret is the proof; without it nothing moves.
	rotated := &storage.PushSubscription{Endpoint: "https://push.example.com/new", P256dh: "newpub", Auth: "newauth"}
	got, err := storage.ReplacePushSubscription(ctx, testPool, "https://push.example.com/old", "wrong", rotated)
	require.NoError(t, err)
	require.Nil(t, got)

	got, err = storage.ReplacePushSubscription(ctx, testPool, "https://push.example.com/old", "auth==", rotated)
	require.NoError(t, err)
	require.NotNil(t, got, "padding is ignored")
	require.Equal(t, old.ID, got.ID, "same device, new endpoint")
	require.Equal(t, alice.ID, got.UserID)
	require.Equal(t, "https://push.example.com/new", got.Endpoint)
	require.Equal(t, "newpub", got.P256dh)
	require.Equal(t, old.Label, got.Label)
	require.Zero(t, got.FailureCount)

	// A device that is no longer listed is not brought back.
	got, err = storage.ReplacePushSubscription(ctx, testPool, "https://push.example.com/removed", "auth", rotated)
	require.NoError(t, err)
	require.Nil(t, got)

	// The new endpoint already belongs to someone else.
	_, err = upsertPush(ctx, pushSub(bob.ID, "https://push.example.com/bobs"))
	require.NoError(t, err)
	_, err = storage.ReplacePushSubscription(ctx, testPool, "https://push.example.com/new", "newauth",
		&storage.PushSubscription{Endpoint: "https://push.example.com/bobs", P256dh: "p", Auth: "a"})
	require.ErrorIs(t, err, storage.ErrPushEndpointTaken)

	// The app registered the new endpoint before the worker moved the old one.
	_, err = upsertPush(ctx, pushSub(alice.ID, "https://push.example.com/newer"))
	require.NoError(t, err)
	got, err = storage.ReplacePushSubscription(ctx, testPool, "https://push.example.com/new", "newauth",
		&storage.PushSubscription{Endpoint: "https://push.example.com/newer", P256dh: "p", Auth: "a"})
	require.NoError(t, err)
	require.Equal(t, old.ID, got.ID)
	subs, err := storage.ListPushSubscriptions(ctx, testPool, alice.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)
}

func TestUpsertPushSubscription_concurrent(t *testing.T) {
	ctx := context.Background()
	truncateUsers(t)
	alice := pushUser(t, "alice-race@example.com")
	bob := pushUser(t, "bob-race@example.com")

	// Two users register the same new endpoint with different keys at once:
	// exactly one gets it, the other is refused, never silently moved.
	for round := range 10 {
		endpoint := fmt.Sprintf("https://push.example.com/race-%d", round)
		errs := make(chan error, 2)
		for _, u := range []*storage.User{alice, bob} {
			go func() {
				s := pushSub(u.ID, endpoint)
				s.P256dh, s.Auth = "pub-"+u.ID, "auth-"+u.ID
				_, err := storage.UpsertPushSubscription(ctx, testPool, s, storage.PushUpsertOptions{})
				errs <- err
			}()
		}
		var taken, ok int
		for range 2 {
			switch err := <-errs; {
			case err == nil:
				ok++
			case errors.Is(err, storage.ErrPushEndpointTaken):
				taken++
			default:
				t.Fatal(err)
			}
		}
		require.Equal(t, 1, ok, "round %d", round)
		require.Equal(t, 1, taken, "round %d", round)
	}

	// Parallel registrations cannot push a user past the device limit.
	carol := pushUser(t, "carol-race@example.com")
	errs := make(chan error, 8)
	for i := range 8 {
		go func() {
			_, err := storage.UpsertPushSubscription(ctx, testPool, pushSub(carol.ID, fmt.Sprintf("https://push.example.com/cap-%d", i)), storage.PushUpsertOptions{MaxDevices: 5})
			errs <- err
		}()
	}
	limited := 0
	for range 8 {
		if err := <-errs; errors.Is(err, storage.ErrTooManyPushDevices) {
			limited++
		} else {
			require.NoError(t, err)
		}
	}
	require.Equal(t, 3, limited)
	subs, err := storage.ListPushSubscriptions(ctx, testPool, carol.ID)
	require.NoError(t, err)
	require.Len(t, subs, 5)

	// A device that is already registered can still refresh at the limit.
	_, err = storage.UpsertPushSubscription(ctx, testPool, pushSub(carol.ID, subs[0].Endpoint), storage.PushUpsertOptions{MaxDevices: 5})
	require.NoError(t, err)
}

func TestPushSubscriptions_revokedWithCredentials(t *testing.T) {
	ctx := context.Background()
	truncateUsers(t)
	u := pushUser(t, "erin@example.com")
	other := pushUser(t, "frank@example.com")
	_, err := upsertPush(ctx, pushSub(u.ID, "https://push.example.com/e"))
	require.NoError(t, err)
	_, err = upsertPush(ctx, pushSub(other.ID, "https://push.example.com/f"))
	require.NoError(t, err)

	require.NoError(t, storage.AdminSetPassword(ctx, testPool, u.ID, "a-new-password-123"))
	subs, err := storage.ListPushSubscriptions(ctx, testPool, u.ID)
	require.NoError(t, err)
	require.Empty(t, subs, "a password reset signs out push devices too")
	subs, err = storage.ListPushSubscriptions(ctx, testPool, other.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)
}

func TestPushDeliveredToService(t *testing.T) {
	ctx := context.Background()
	truncateUsers(t)
	u := pushUser(t, "gina@example.com")
	a, err := upsertPush(ctx, pushSub(u.ID, "https://fcm.googleapis.com/fcm/send/a"))
	require.NoError(t, err)
	_, err = upsertPush(ctx, pushSub(u.ID, "https://web.push.apple.com/b"))
	require.NoError(t, err)
	_, err = upsertPush(ctx, pushSub(u.ID, "https://fcmXgoogleapis.com/c"))
	require.NoError(t, err)
	wns, err := upsertPush(ctx, pushSub(u.ID, "https://wns2-par02p.notify.windows.com/w/?token=x"))
	require.NoError(t, err)
	hour := time.Hour
	delivered := func(service string, window time.Duration) bool {
		ok, err := storage.PushDeliveredToService(ctx, testPool, service, window)
		require.NoError(t, err)
		return ok
	}

	require.False(t, delivered("fcm.googleapis.com", hour), "nothing delivered yet")
	require.NoError(t, storage.RecordPushSuccess(ctx, testPool, a))
	require.True(t, delivered("fcm.googleapis.com", hour))
	require.False(t, delivered("web.push.apple.com", hour), "other push services are separate")
	require.False(t, delivered("fcm_googleapis.com", hour), "LIKE wildcards in the host are escaped")
	_, err = testPool.Exec(ctx, `UPDATE push_subscriptions SET last_success_at = NOW() - interval '2 hours' WHERE id = $1`, a.ID)
	require.NoError(t, err)
	require.False(t, delivered("fcm.googleapis.com", hour), "only deliveries within the window count")
	require.True(t, delivered("fcm.googleapis.com", 3*time.Hour))

	// A service on many hosts counts as one.
	require.False(t, delivered("*.notify.windows.com", hour))
	require.NoError(t, storage.RecordPushSuccess(ctx, testPool, wns))
	require.True(t, delivered("*.notify.windows.com", hour))
	require.False(t, delivered("*.windows.com.example", hour))

	// Only the host counts, never the path, and case does not matter.
	odd, err := upsertPush(ctx, pushSub(u.ID, "https://ntfy.example.com/x.notify.windows.com/abc"))
	require.NoError(t, err)
	upper, err := upsertPush(ctx, pushSub(u.ID, "https://UPDATES.push.services.mozilla.com/wpush/v2/z"))
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `UPDATE push_subscriptions SET last_success_at = NULL WHERE id = $1`, wns.ID)
	require.NoError(t, err)
	require.NoError(t, storage.RecordPushSuccess(ctx, testPool, odd))
	require.False(t, delivered("*.notify.windows.com", hour), "a path that looks like the service is not the service")
	require.NoError(t, storage.RecordPushSuccess(ctx, testPool, upper))
	require.True(t, delivered("*.push.services.mozilla.com", hour))
	require.True(t, delivered("ntfy.example.com", hour))

	// An allowlisted host carries no port; endpoints on an explicit port
	// still belong to it. A service named with a port (no allowlist) must
	// match the port too.
	ported, err := upsertPush(ctx, pushSub(u.ID, "https://autopush.example.org:8443/wpush/x"))
	require.NoError(t, err)
	require.NoError(t, storage.RecordPushSuccess(ctx, testPool, ported))
	require.True(t, delivered("autopush.example.org", hour))
	require.True(t, delivered("autopush.example.org:8443", hour))
	require.False(t, delivered("autopush.example.org:9443", hour))
}

func TestUpsertPushSubscription_deletedUser(t *testing.T) {
	ctx := context.Background()
	truncateUsers(t)
	_, err := storage.UpsertPushSubscription(ctx, testPool, pushSub("3f1c2a52-8a7e-4c7b-9a51-2b1f9d0e7c11", "https://push.example.com/ghost"), storage.PushUpsertOptions{})
	require.ErrorIs(t, err, storage.ErrPushSessionEnded)
}

func TestUpsertPushSubscription_waitsForPasswordChange(t *testing.T) {
	// A password change holds the users row while it revokes; a registration
	// in the middle of that waits for it (and is then refused), instead of
	// deadlocking on the device lock.
	ctx := context.Background()
	truncateUsers(t)
	u := pushUser(t, "jack@example.com")
	sess, err := storage.CreateSession(ctx, testPool, u.ID)
	require.NoError(t, err)

	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	_, err = tx.Exec(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, u.ID)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := storage.UpsertPushSubscription(ctx, testPool, pushSub(u.ID, "https://push.example.com/mid"), storage.PushUpsertOptions{SessionToken: sess.Token})
		done <- err
	}()
	waitUntilBlocked(t, `FROM users WHERE id = $1 FOR KEY SHARE`)
	// Now the revocation's own steps, as revokeCredentialArtifacts does them.
	_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, u.ID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('push-user:' || $1, 0))`, u.ID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `DELETE FROM push_subscriptions WHERE user_id = $1`, u.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	select {
	case err := <-done:
		require.ErrorIs(t, err, storage.ErrPushSessionEnded)
	case <-time.After(10 * time.Second):
		t.Fatal("registration did not finish")
	}
	subs, err := storage.ListPushSubscriptions(ctx, testPool, u.ID)
	require.NoError(t, err)
	require.Empty(t, subs)
}

func TestUpsertPushSubscription_needsLiveSession(t *testing.T) {
	ctx := context.Background()
	truncateUsers(t)
	u := pushUser(t, "ivy@example.com")
	sess, err := storage.CreateSession(ctx, testPool, u.ID)
	require.NoError(t, err)
	opts := storage.PushUpsertOptions{SessionToken: sess.Token}
	_, err = storage.UpsertPushSubscription(ctx, testPool, pushSub(u.ID, "https://push.example.com/live"), opts)
	require.NoError(t, err)

	// Revoked (a password change elsewhere): the registration is refused.
	require.NoError(t, storage.AdminSetPassword(ctx, testPool, u.ID, "a-new-password-123"))
	_, err = storage.UpsertPushSubscription(ctx, testPool, pushSub(u.ID, "https://push.example.com/late"), opts)
	require.ErrorIs(t, err, storage.ErrPushSessionEnded)
	subs, err := storage.ListPushSubscriptions(ctx, testPool, u.ID)
	require.NoError(t, err)
	require.Empty(t, subs)
}

func TestPushSubscriptions_keptForPasswordChangeOnThisDevice(t *testing.T) {
	ctx := context.Background()
	truncateUsers(t)
	u, err := storage.CreateUser(ctx, testPool, "hank@example.com", "old-password-1234")
	require.NoError(t, err)
	_, err = upsertPush(ctx, pushSub(u.ID, "https://push.example.com/here"))
	require.NoError(t, err)
	_, err = upsertPush(ctx, pushSub(u.ID, "https://push.example.com/laptop"))
	require.NoError(t, err)
	sess, err := storage.CreateSession(ctx, testPool, u.ID)
	require.NoError(t, err)

	_, err = storage.ChangeUserPasswordWithSession(ctx, testPool, u.ID, "old-password-1234", "new-password-1234", sess.Token, "https://push.example.com/here")
	require.NoError(t, err)
	subs, err := storage.ListPushSubscriptions(ctx, testPool, u.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	require.Equal(t, "https://push.example.com/here", subs[0].Endpoint)

	// Without the current session nothing is kept.
	require.NoError(t, storage.ChangeUserPassword(ctx, testPool, u.ID, "new-password-1234", "third-password-1234"))
	subs, err = storage.ListPushSubscriptions(ctx, testPool, u.ID)
	require.NoError(t, err)
	require.Empty(t, subs)
}

func TestDeleteUser_pausesPushRulesLeftWithoutRecipients(t *testing.T) {
	ctx := context.Background()
	p := setupProjectForAlerts(t)
	truncateAlerts(t)
	truncateUsers(t)
	keep := pushUser(t, "keep@example.com")
	gone := pushUser(t, "gone@example.com")
	pushRule := func(name string, recipients ...string) *storage.AlertRule {
		r, err := storage.CreateAlertRule(ctx, testPool, &storage.AlertRule{
			ProjectIDs: []string{p.ID}, Name: name, Enabled: true, Trigger: "new_issue",
			Channel: "push", RecipientIDs: recipients, CooldownMins: 60,
		})
		require.NoError(t, err)
		return r
	}
	sole := pushRule("sole", gone.ID)
	shared := pushRule("shared", gone.ID, keep.ID)
	hook, err := storage.CreateAlertRule(ctx, testPool, webhookRule(p.ID))
	require.NoError(t, err)

	deleted, err := storage.DeleteUser(ctx, testPool, gone.ID)
	require.NoError(t, err)
	require.True(t, deleted)
	for rule, enabled := range map[string]bool{sole.ID: false, shared.ID: true, hook.ID: hook.Enabled} {
		got, err := storage.GetAlertRule(ctx, testPool, rule)
		require.NoError(t, err)
		require.Equal(t, enabled, got.Enabled, got.Name)
	}
}

func TestUpdateAlertRule_waitsForUserDeletion(t *testing.T) {
	// Deleting a user locks the row, cascades into the recipients and then
	// pauses rules left without any. A rule save naming that user must wait
	// for it, not deadlock on the recipients and the rule row.
	ctx := context.Background()
	p := setupProjectForAlerts(t)
	truncateAlerts(t)
	truncateUsers(t)
	keep := pushUser(t, "keep-lock@example.com")
	gone := pushUser(t, "gone-lock@example.com")
	rule, err := storage.CreateAlertRule(ctx, testPool, &storage.AlertRule{
		ProjectIDs: []string{p.ID}, Name: "r", Enabled: true, Trigger: "new_issue",
		Channel: "push", RecipientIDs: []string{gone.ID}, CooldownMins: 60,
	})
	require.NoError(t, err)

	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	_, err = tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, gone.ID)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		r := *rule
		r.RecipientIDs = []string{gone.ID, keep.ID}
		_, err := storage.UpdateAlertRule(ctx, testPool, &r)
		done <- err
	}()
	waitUntilBlocked(t, `ORDER BY id FOR KEY SHARE`)
	_, err = tx.Exec(ctx, `UPDATE alert_rules ar SET enabled = FALSE WHERE ar.channel = 'push' AND ar.enabled
		AND NOT EXISTS (SELECT 1 FROM alert_rule_recipients rr WHERE rr.rule_id = ar.id)`)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("rule save did not finish")
	}
	got, err := storage.GetAlertRule(ctx, testPool, rule.ID)
	require.NoError(t, err)
	require.Equal(t, []string{keep.ID}, got.RecipientIDs)
}

func TestDeleteUser_whileARuleSaveDropsTheUser(t *testing.T) {
	// A rule save that drops user u holds the rule row and is about to
	// replace its recipients when u is deleted. Deleting u must wait for the
	// rule row before its cascade takes the recipient rows; otherwise the
	// save waits on those rows, the deletion on the rule row, and Postgres
	// aborts one with a deadlock.
	ctx := context.Background()
	p := setupProjectForAlerts(t)
	truncateAlerts(t)
	truncateUsers(t)
	pushUser(t, "admin-order@example.com") // the first user manages users; the two below do not
	keep := pushUser(t, "keep-order@example.com")
	gone := pushUser(t, "gone-order@example.com")
	rule, err := storage.CreateAlertRule(ctx, testPool, &storage.AlertRule{
		ProjectIDs: []string{p.ID}, Name: "r", Enabled: true, Trigger: "new_issue",
		Channel: "push", RecipientIDs: []string{gone.ID}, CooldownMins: 60,
	})
	require.NoError(t, err)

	// The save, step by step as UpdateAlertRule runs it.
	save, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer save.Rollback(ctx) //nolint:errcheck
	_, err = save.Exec(ctx, `SELECT id FROM users WHERE id = $1 FOR KEY SHARE`, keep.ID)
	require.NoError(t, err)
	_, err = save.Exec(ctx, `UPDATE alert_rules SET name = 'saved' WHERE id = $1`, rule.ID)
	require.NoError(t, err)

	deleted := make(chan error, 1)
	go func() { _, err := storage.DeleteUser(ctx, testPool, gone.ID); deleted <- err }()
	waitUntilBlocked(t, `FOR NO KEY UPDATE`)

	_, err = save.Exec(ctx, `DELETE FROM alert_rule_recipients WHERE rule_id = $1`, rule.ID)
	require.NoError(t, err, "the save must not deadlock with the deletion")
	_, err = save.Exec(ctx, `INSERT INTO alert_rule_recipients (rule_id, user_id) VALUES ($1, $2)`, rule.ID, keep.ID)
	require.NoError(t, err)
	require.NoError(t, save.Commit(ctx))

	select {
	case err := <-deleted:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("user deletion did not finish")
	}
	got, err := storage.GetAlertRule(ctx, testPool, rule.ID)
	require.NoError(t, err)
	require.True(t, got.Enabled, "it still has a recipient")
	require.Equal(t, []string{keep.ID}, got.RecipientIDs)
}

func TestDeleteUser_whileARuleSaveLocksAManagerRecipient(t *testing.T) {
	// A rule save names a user who is being deleted and a user manager. The
	// deletion's last-manager guard locks every manager; that must not
	// conflict with the save's lock on its recipients, or the two deadlock.
	ctx := context.Background()
	p := setupProjectForAlerts(t)
	truncateAlerts(t)
	truncateUsers(t)
	manager := pushUser(t, "manager-order@example.com") // the first user manages users
	pushUser(t, "manager-two@example.com")
	_, err := testPool.Exec(ctx, `UPDATE users SET perm_manage_users = TRUE WHERE email = 'manager-two@example.com'`)
	require.NoError(t, err)
	gone := pushUser(t, "gone-manager-order@example.com")
	rule, err := storage.CreateAlertRule(ctx, testPool, &storage.AlertRule{
		ProjectIDs: []string{p.ID}, Name: "r", Enabled: true, Trigger: "new_issue",
		Channel: "push", RecipientIDs: []string{manager.ID}, CooldownMins: 60,
	})
	require.NoError(t, err)

	save, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer save.Rollback(ctx) //nolint:errcheck
	_, err = save.Exec(ctx, `SELECT id FROM users WHERE id = $1 FOR KEY SHARE`, gone.ID)
	require.NoError(t, err)

	deleted := make(chan error, 1)
	go func() { _, err := storage.DeleteUser(ctx, testPool, gone.ID); deleted <- err }()
	waitUntilBlocked(t, `FROM users WHERE id = $1 FOR UPDATE`)

	_, err = save.Exec(ctx, `SELECT id FROM users WHERE id = $1 FOR KEY SHARE`, manager.ID)
	require.NoError(t, err, "the save must not deadlock with the deletion")
	_, err = save.Exec(ctx, `UPDATE alert_rules SET name = 'saved' WHERE id = $1`, rule.ID)
	require.NoError(t, err)
	require.NoError(t, save.Commit(ctx))

	select {
	case err := <-deleted:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("user deletion did not finish")
	}
}

func TestEndSession(t *testing.T) {
	ctx := context.Background()
	truncateUsers(t)
	u := pushUser(t, "kim@example.com")
	sess, err := storage.CreateSession(ctx, testPool, u.ID)
	require.NoError(t, err)
	here, err := upsertPush(ctx, pushSub(u.ID, "https://push.example.com/here"))
	require.NoError(t, err)
	_, err = upsertPush(ctx, pushSub(u.ID, "https://push.example.com/phone"))
	require.NoError(t, err)

	out, err := storage.EndSession(ctx, testPool, sess.Token, "https://push.example.com/here")
	require.NoError(t, err)
	require.Equal(t, storage.EndedSession{UserID: u.ID, Ended: true, DeviceID: here.ID}, out)
	identity, err := storage.GetSessionIdentity(ctx, testPool, sess.Token)
	require.NoError(t, err)
	require.Nil(t, identity)
	subs, err := storage.ListPushSubscriptions(ctx, testPool, u.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)

	// An unknown session removes nothing.
	out, err = storage.EndSession(ctx, testPool, "not-a-session", "https://push.example.com/phone")
	require.NoError(t, err)
	require.Equal(t, storage.EndedSession{}, out)
	subs, err = storage.ListPushSubscriptions(ctx, testPool, u.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)
}

func TestEndSession_whilePasswordChanges(t *testing.T) {
	// A password change holds the users row and then deletes the sessions;
	// a logout must wait for the row before it touches its session, or the
	// two deadlock.
	ctx := context.Background()
	truncateUsers(t)
	u := pushUser(t, "lee@example.com")
	sess, err := storage.CreateSession(ctx, testPool, u.ID)
	require.NoError(t, err)
	_, err = upsertPush(ctx, pushSub(u.ID, "https://push.example.com/here"))
	require.NoError(t, err)

	change, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer change.Rollback(ctx) //nolint:errcheck
	_, err = change.Exec(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, u.ID)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := storage.EndSession(ctx, testPool, sess.Token, "https://push.example.com/here")
		done <- err
	}()
	waitUntilBlocked(t, `FROM users WHERE id = $1 FOR KEY SHARE`)
	_, err = change.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, u.ID)
	require.NoError(t, err, "the password change must not deadlock with the logout")
	_, err = change.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('push-user:' || $1, 0))`, u.ID)
	require.NoError(t, err)
	_, err = change.Exec(ctx, `DELETE FROM push_subscriptions WHERE user_id = $1`, u.ID)
	require.NoError(t, err)
	require.NoError(t, change.Commit(ctx))

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("logout did not finish")
	}
	identity, err := storage.GetSessionIdentity(ctx, testPool, sess.Token)
	require.NoError(t, err)
	require.Nil(t, identity)
}

func TestAdminSetPassword_whileLoggingOut(t *testing.T) {
	// A logout holds the user's device lock and is about to delete its
	// session when an admin resets the password. The reset must wait for
	// the logout instead of taking the sessions first and deadlocking.
	ctx := context.Background()
	truncateUsers(t)
	u := pushUser(t, "max@example.com")
	_, err := storage.CreateSession(ctx, testPool, u.ID)
	require.NoError(t, err)

	// The logout, step by step as EndSession runs it.
	logout, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer logout.Rollback(ctx) //nolint:errcheck
	_, err = logout.Exec(ctx, `SELECT id FROM users WHERE id = $1 FOR KEY SHARE`, u.ID)
	require.NoError(t, err)
	_, err = logout.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('push-user:' || $1, 0))`, u.ID)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- storage.AdminSetPassword(ctx, testPool, u.ID, "an-admin-chosen-pass") }()
	waitUntilBlocked(t, `FROM users WHERE id = $1 FOR UPDATE`)

	_, err = logout.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, u.ID)
	require.NoError(t, err, "the logout must not deadlock with the reset")
	require.NoError(t, logout.Commit(ctx))

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("password reset did not finish")
	}
}

func TestPushSubscriptions_deletedWithUser(t *testing.T) {
	ctx := context.Background()
	truncateUsers(t)
	u := pushUser(t, "dave@example.com")
	_, err := upsertPush(ctx, pushSub(u.ID, "https://push.example.com/d"))
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID)
	require.NoError(t, err)
	var n int
	require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM push_subscriptions`).Scan(&n))
	require.Zero(t, n)
}

func TestAlertRuleRecipients(t *testing.T) {
	ctx := context.Background()
	p := setupProjectForAlerts(t)
	truncateAlerts(t)
	truncateUsers(t)
	alice := pushUser(t, "alice@example.com")
	bob := pushUser(t, "bob@example.com")

	rule := &storage.AlertRule{
		ProjectIDs:   []string{p.ID},
		Name:         "push rule",
		Enabled:      true,
		Trigger:      "new_issue",
		Channel:      "push",
		RecipientIDs: []string{alice.ID, "not-a-uuid", "urn:uuid:" + bob.ID, "{" + bob.ID + "}", "00000000-0000-0000-0000-000000000000", alice.ID},
		CooldownMins: 60,
	}
	created, err := storage.CreateAlertRule(ctx, testPool, rule)
	require.NoError(t, err)
	require.Equal(t, []string{alice.ID}, created.RecipientIDs, "unknown and duplicate IDs are dropped")

	got, err := storage.GetAlertRule(ctx, testPool, created.ID)
	require.NoError(t, err)
	require.Equal(t, []string{alice.ID}, got.RecipientIDs)

	_, err = upsertPush(ctx, pushSub(alice.ID, "https://push.example.com/alice"))
	require.NoError(t, err)
	_, err = upsertPush(ctx, pushSub(bob.ID, "https://push.example.com/bob"))
	require.NoError(t, err)
	subs, err := storage.ListPushSubscriptionsForRule(ctx, testPool, created.ID, nil)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	require.Equal(t, "https://push.example.com/alice", subs[0].Endpoint)
	require.Equal(t, "pub", subs[0].P256dh)

	got.RecipientIDs = []string{bob.ID, alice.ID}
	updated, err := storage.UpdateAlertRule(ctx, testPool, got)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{alice.ID, bob.ID}, updated.RecipientIDs)
	subs, err = storage.ListPushSubscriptionsForRule(ctx, testPool, created.ID, nil)
	require.NoError(t, err)
	require.Len(t, subs, 2)

	// A retry asks for its devices only; IDs that are not UUIDs match none.
	only, err := storage.ListPushSubscriptionsForRule(ctx, testPool, created.ID, []string{subs[1].ID, "nope"})
	require.NoError(t, err)
	require.Len(t, only, 1)
	require.Equal(t, subs[1].ID, only[0].ID)
	none, err := storage.ListPushSubscriptionsForRule(ctx, testPool, created.ID, []string{"nope"})
	require.NoError(t, err)
	require.Empty(t, none)

	// Rules without recipients list an empty slice, not null.
	plain, err := storage.CreateAlertRule(ctx, testPool, webhookRule(p.ID))
	require.NoError(t, err)
	require.NotNil(t, plain.RecipientIDs)
	rules, err := storage.ListAlertRules(ctx, testPool, "")
	require.NoError(t, err)
	for _, r := range rules {
		require.NotNil(t, r.RecipientIDs)
	}

	// Deleting a user removes them as a recipient.
	_, err = testPool.Exec(ctx, `DELETE FROM users WHERE id = $1`, bob.ID)
	require.NoError(t, err)
	got, err = storage.GetAlertRule(ctx, testPool, created.ID)
	require.NoError(t, err)
	require.Equal(t, []string{alice.ID}, got.RecipientIDs)

	// A push rule needs at least one existing recipient; the check runs in
	// the same transaction as the save, so nothing is written without one.
	ghost := *rule
	ghost.Name = "nobody"
	ghost.RecipientIDs = []string{"00000000-0000-0000-0000-000000000000"}
	_, err = storage.CreateAlertRule(ctx, testPool, &ghost)
	require.ErrorIs(t, err, storage.ErrNoPushRecipients)
	all, err := storage.ListAlertRules(ctx, testPool, "")
	require.NoError(t, err)
	for _, r := range all {
		require.NotEqual(t, "nobody", r.Name)
	}
	got.RecipientIDs = []string{bob.ID}
	_, err = storage.UpdateAlertRule(ctx, testPool, got)
	require.ErrorIs(t, err, storage.ErrNoPushRecipients, "bob was deleted")
	got, err = storage.GetAlertRule(ctx, testPool, created.ID)
	require.NoError(t, err)
	require.Equal(t, []string{alice.ID}, got.RecipientIDs, "the failed update changed nothing")
}

func TestGetOrCreateInstanceSecret(t *testing.T) {
	ctx := context.Background()
	_, err := testPool.Exec(ctx, `TRUNCATE instance_secrets`)
	require.NoError(t, err)

	calls := 0
	gen := func() (string, error) { calls++; return "first", nil }
	v, err := storage.GetOrCreateInstanceSecret(ctx, testPool, "k", gen)
	require.NoError(t, err)
	require.Equal(t, "first", v)

	v, err = storage.GetOrCreateInstanceSecret(ctx, testPool, "k", func() (string, error) { return "second", nil })
	require.NoError(t, err)
	require.Equal(t, "first", v, "stored value wins")
	require.Equal(t, 1, calls)

	_, err = storage.GetOrCreateInstanceSecret(ctx, testPool, "other", func() (string, error) { return "", errors.New("boom") })
	require.Error(t, err)
}
