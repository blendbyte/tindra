package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func pushUser(t *testing.T, email string) *storage.User {
	t.Helper()
	u, err := storage.CreateUser(context.Background(), testPool, email, "password123456")
	require.NoError(t, err)
	return u
}

func pushSub(userID, endpoint string) *storage.PushSubscription {
	return &storage.PushSubscription{UserID: userID, Endpoint: endpoint, P256dh: "pub", Auth: "auth", Label: "Firefox on Linux"}
}

func TestPushSubscriptions_upsertListDelete(t *testing.T) {
	ctx := context.Background()
	truncateUsers(t)
	alice := pushUser(t, "alice@example.com")
	bob := pushUser(t, "bob@example.com")

	a1, err := storage.UpsertPushSubscription(ctx, testPool, pushSub(alice.ID, "https://push.example.com/a1"))
	require.NoError(t, err)
	require.Equal(t, "Firefox on Linux", a1.Label)
	_, err = storage.UpsertPushSubscription(ctx, testPool, pushSub(alice.ID, "https://push.example.com/a2"))
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
	got, err = storage.GetPushSubscription(ctx, testPool, "not-a-uuid", alice.ID)
	require.NoError(t, err)
	require.Nil(t, got)

	// The same browser registering for another user moves the endpoint and resets failures.
	_, err = storage.RecordPushFailure(ctx, testPool, a1.ID, 5)
	require.NoError(t, err)
	moved := pushSub(bob.ID, "https://push.example.com/a1")
	moved.P256dh = "newpub"
	m, err := storage.UpsertPushSubscription(ctx, testPool, moved)
	require.NoError(t, err)
	require.Equal(t, a1.ID, m.ID)
	require.Equal(t, bob.ID, m.UserID)
	require.Equal(t, "newpub", m.P256dh)
	require.Zero(t, m.FailureCount)

	subs, err = storage.ListPushSubscriptions(ctx, testPool, alice.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)

	deleted, err = storage.DeletePushSubscriptionByEndpoint(ctx, testPool, "https://push.example.com/a1", alice.ID)
	require.NoError(t, err)
	require.False(t, deleted, "alice no longer owns a1")
	deleted, err = storage.DeletePushSubscriptionByEndpoint(ctx, testPool, "https://push.example.com/a1", bob.ID)
	require.NoError(t, err)
	require.True(t, deleted)

	deleted, err = storage.DeletePushSubscription(ctx, testPool, subs[0].ID, alice.ID)
	require.NoError(t, err)
	require.True(t, deleted)
}

func TestPushSubscriptions_failureAndSuccessBookkeeping(t *testing.T) {
	ctx := context.Background()
	truncateUsers(t)
	u := pushUser(t, "carol@example.com")
	s, err := storage.UpsertPushSubscription(ctx, testPool, pushSub(u.ID, "https://push.example.com/c"))
	require.NoError(t, err)

	for range 2 {
		gone, err := storage.RecordPushFailure(ctx, testPool, s.ID, 3)
		require.NoError(t, err)
		require.False(t, gone)
	}
	require.NoError(t, storage.RecordPushSuccess(ctx, testPool, s.ID))
	got, err := storage.GetPushSubscription(ctx, testPool, s.ID, u.ID)
	require.NoError(t, err)
	require.Zero(t, got.FailureCount)
	require.NotNil(t, got.LastSuccessAt)

	for i := range 3 {
		gone, err := storage.RecordPushFailure(ctx, testPool, s.ID, 3)
		require.NoError(t, err)
		require.Equal(t, i == 2, gone)
	}
	got, err = storage.GetPushSubscription(ctx, testPool, s.ID, u.ID)
	require.NoError(t, err)
	require.Nil(t, got)

	gone, err := storage.RecordPushFailure(ctx, testPool, s.ID, 3)
	require.NoError(t, err)
	require.False(t, gone, "missing subscription is a no-op")
}

func TestPushSubscriptions_deletedWithUser(t *testing.T) {
	ctx := context.Background()
	truncateUsers(t)
	u := pushUser(t, "dave@example.com")
	_, err := storage.UpsertPushSubscription(ctx, testPool, pushSub(u.ID, "https://push.example.com/d"))
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
		RecipientIDs: []string{alice.ID, "not-a-uuid", "00000000-0000-0000-0000-000000000000", alice.ID},
		CooldownMins: 60,
	}
	created, err := storage.CreateAlertRule(ctx, testPool, rule)
	require.NoError(t, err)
	require.Equal(t, []string{alice.ID}, created.RecipientIDs, "unknown and duplicate IDs are dropped")

	got, err := storage.GetAlertRule(ctx, testPool, created.ID)
	require.NoError(t, err)
	require.Equal(t, []string{alice.ID}, got.RecipientIDs)

	_, err = storage.UpsertPushSubscription(ctx, testPool, pushSub(alice.ID, "https://push.example.com/alice"))
	require.NoError(t, err)
	_, err = storage.UpsertPushSubscription(ctx, testPool, pushSub(bob.ID, "https://push.example.com/bob"))
	require.NoError(t, err)
	subs, err := storage.ListPushSubscriptionsForRule(ctx, testPool, created.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	require.Equal(t, "https://push.example.com/alice", subs[0].Endpoint)
	require.Equal(t, "pub", subs[0].P256dh)

	got.RecipientIDs = []string{bob.ID, alice.ID}
	updated, err := storage.UpdateAlertRule(ctx, testPool, got)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{alice.ID, bob.ID}, updated.RecipientIDs)
	subs, err = storage.ListPushSubscriptionsForRule(ctx, testPool, created.ID)
	require.NoError(t, err)
	require.Len(t, subs, 2)

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
