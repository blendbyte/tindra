package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestLinkOAuthIdentityGuards(t *testing.T) {
	truncateUsers(t)
	user, err := storage.CreateUser(t.Context(), testPool, "link@example.com", "password1234")
	require.NoError(t, err)
	for _, tc := range []struct{ provider, sub string }{{"", "sub"}, {"provider", ""}, {" ", "sub"}, {"provider", "\t"}} {
		require.ErrorContains(t, storage.LinkOAuthIdentity(t.Context(), testPool, user.ID, tc.provider, tc.sub), "must not be blank")
	}
	require.ErrorContains(t, storage.LinkOAuthIdentity(t.Context(), testPool, uuid.NewString(), "provider", "subject"), "target user no longer exists")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorContains(t, storage.LinkOAuthIdentity(ctx, testPool, user.ID, "provider", "subject"), "link OAuth identity")
	require.NoError(t, storage.LinkOAuthIdentity(t.Context(), testPool, user.ID, "provider", "subject"))
	require.NoError(t, storage.LinkOAuthIdentity(t.Context(), testPool, user.ID, "provider", "subject"))
	_, err = storage.FindOrCreateOAuthUser(t.Context(), testPool, "other-provider", "subject", user.Email, false, 0)
	require.ErrorIs(t, err, storage.ErrOAuthEmailUnverified)
}

func TestLinkOAuthIdentityConcurrentOwners(t *testing.T) {
	truncateUsers(t)
	first, err := storage.CreateUser(t.Context(), testPool, "first@example.com", "password1234")
	require.NoError(t, err)
	second, err := storage.CreateUser(t.Context(), testPool, "second@example.com", "password1234")
	require.NoError(t, err)
	type result struct {
		id  string
		err error
	}
	start := make(chan struct{})
	done := make(chan result, 2)
	for _, id := range []string{first.ID, second.ID} {
		go func() {
			<-start
			done <- result{id, storage.LinkOAuthIdentity(t.Context(), testPool, id, "microsoft", "subject")}
		}()
	}
	close(start)
	a, b := <-done, <-done
	require.NotEqual(t, a.err == nil, b.err == nil, "exactly one owner must win")
	winner := a.id
	if a.err != nil {
		winner = b.id
	}
	linked, err := storage.FindOrCreateOAuthUser(t.Context(), testPool, "microsoft", "subject", "", false, 0)
	require.NoError(t, err)
	require.Equal(t, winner, linked.ID)
	var count int
	require.NoError(t, testPool.QueryRow(t.Context(), "SELECT count(*) FROM audit_log WHERE event_type='user.sso.link' AND target_id=ANY($1::text[])", []string{first.ID, second.ID}).Scan(&count))
	require.Equal(t, 1, count)
}

func TestLinkOAuthIdentityAuditFailureRollsBack(t *testing.T) {
	truncateUsers(t)
	user, err := storage.CreateUser(t.Context(), testPool, "rollback-link@example.com", "password1234")
	require.NoError(t, err)
	_, err = testPool.Exec(t.Context(), "ALTER TABLE audit_log ADD CONSTRAINT review_block_sso_link CHECK (event_type != 'user.sso.link') NOT VALID")
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := testPool.Exec(ctx, "ALTER TABLE audit_log DROP CONSTRAINT review_block_sso_link")
		require.NoError(t, err)
	})
	require.Error(t, storage.LinkOAuthIdentity(t.Context(), testPool, user.ID, "microsoft", "subject"))
	var count int
	require.NoError(t, testPool.QueryRow(t.Context(), "SELECT count(*) FROM oauth_identities WHERE user_id=$1", user.ID).Scan(&count))
	require.Zero(t, count)
}
