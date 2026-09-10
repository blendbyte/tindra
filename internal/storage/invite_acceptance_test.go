package storage_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func acceptanceInvite(t *testing.T) (string, string) {
	t.Helper()
	email := uuid.NewString() + "@accept.test"
	token, err := storage.CreateInvite(t.Context(), testPool, "", email, "Invited name")
	require.NoError(t, err)
	return token, email
}

func TestInviteAcceptanceAtomicSuccess(t *testing.T) {
	for _, name := range []string{"", "Chosen name"} {
		t.Run(name, func(t *testing.T) {
			token, email := acceptanceInvite(t)
			u, s, err := storage.AcceptInviteWithSession(t.Context(), testPool, token, "new-password-1234", name, 0)
			require.NoError(t, err)
			require.NotNil(t, u)
			require.NotNil(t, s)
			require.Equal(t, email, u.Email)
			expected := name
			if expected == "" {
				expected = "Invited name"
			}
			require.Equal(t, expected, u.Name)
			stored, err := storage.GetUserByID(t.Context(), testPool, u.ID)
			require.NoError(t, err)
			require.Equal(t, expected, stored.Name)
			identity, err := storage.GetSessionIdentity(t.Context(), testPool, s.Token)
			require.NoError(t, err)
			require.Equal(t, u.ID, identity.UserID)
			inv, err := storage.GetInvite(t.Context(), testPool, token)
			require.NoError(t, err)
			require.Nil(t, inv)
			again, session, err := storage.AcceptInviteWithSession(t.Context(), testPool, token, "new-password-1234", "", 0)
			require.NoError(t, err)
			require.Nil(t, again)
			require.Nil(t, session)
		})
	}
}

func TestInviteAcceptanceFailuresRollBack(t *testing.T) {
	for _, stage := range []string{"FROM user_invites", "begin", "LOCK TABLE users", "UPDATE user_invites SET accepted_at", "SELECT count(*)", "INSERT INTO users", "UPDATE users SET name", "INSERT INTO sessions", "commit"} {
		t.Run(stage, func(t *testing.T) {
			token, email := acceptanceInvite(t)
			trace := &cancelGroupingQuery{match: stage}
			cfg := testPool.Config()
			cfg.ConnConfig.Tracer = trace
			pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
			require.NoError(t, err)
			defer pool.Close()
			u, s, err := storage.AcceptInviteWithSession(t.Context(), pool, token, "new-password-1234", "Chosen", 100000)
			require.Error(t, err)
			require.Nil(t, u)
			require.Nil(t, s)
			require.True(t, trace.hit.Load())
			inv, err := storage.GetInvite(t.Context(), testPool, token)
			require.NoError(t, err)
			require.NotNil(t, inv)
			stored, err := storage.GetUserByEmail(t.Context(), testPool, email)
			require.NoError(t, err)
			require.Nil(t, stored)
		})
	}
}

func TestInviteAcceptanceValidation(t *testing.T) {
	for _, kind := range []string{"short", "long", "expired", "revoked", "quota", "duplicate", "hash failure"} {
		t.Run(kind, func(t *testing.T) {
			token, email := acceptanceInvite(t)
			password := "new-password-1234"
			limit := 0
			switch kind {
			case "short":
				password = "short"
			case "long":
				password = strings.Repeat("x", 73)
			case "expired":
				_, err := testPool.Exec(t.Context(), `UPDATE user_invites SET expires_at=NOW()-interval '1 second' WHERE email=$1`, email)
				require.NoError(t, err)
			case "revoked":
				inv, err := storage.GetInvite(t.Context(), testPool, token)
				require.NoError(t, err)
				_, err = storage.DeleteInvite(t.Context(), testPool, inv.ID)
				require.NoError(t, err)
			case "quota":
				_, err := storage.CreateUser(t.Context(), testPool, uuid.NewString()+"@existing.test", "new-password-1234")
				require.NoError(t, err)
				count, err := storage.CountUsers(t.Context(), testPool)
				require.NoError(t, err)
				limit = int(count)
			case "duplicate":
				_, err := storage.CreateUser(t.Context(), testPool, email, password)
				require.NoError(t, err)
			case "hash failure":
				cost := storage.BcryptCost
				storage.BcryptCost = 32
				defer func() { storage.BcryptCost = cost }()
			}
			u, s, err := storage.AcceptInviteWithSession(t.Context(), testPool, token, password, "", limit)
			require.Nil(t, u)
			require.Nil(t, s)
			if kind == "expired" || kind == "revoked" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				inv, err := storage.GetInvite(t.Context(), testPool, token)
				require.NoError(t, err)
				require.NotNil(t, inv)
			}
		})
	}
}

func TestInviteAcceptanceConcurrentAdmission(t *testing.T) {
	for _, kind := range []string{"same invite", "last slot", "last slot with SSO"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			first, _ := acceptanceInvite(t)
			second, email := acceptanceInvite(t)
			count, err := storage.CountUsers(t.Context(), testPool)
			require.NoError(t, err)
			limit := int(count) + 1
			if kind == "same invite" {
				second = first
				limit = 0
			}
			type result struct {
				user *storage.User
				err  error
			}
			done := make(chan result, 2)
			start := make(chan struct{})
			for i, token := range []string{first, second} {
				go func() {
					<-start
					if kind == "last slot with SSO" && i == 1 {
						u, err := storage.FindOrCreateOAuthUser(ctx, testPool, "test", uuid.NewString(), email, true, limit)
						done <- result{u, err}
						return
					}
					u, _, err := storage.AcceptInviteWithSession(ctx, testPool, token, "new-password-1234", "", limit)
					done <- result{u, err}
				}()
			}
			close(start)
			successes := 0
			for range 2 {
				r := <-done
				if r.user != nil {
					require.NoError(t, r.err)
					successes++
				} else if kind == "same invite" {
					require.NoError(t, r.err)
				} else {
					require.Error(t, r.err)
				}
			}
			require.Equal(t, 1, successes)
			after, err := storage.CountUsers(t.Context(), testPool)
			require.NoError(t, err)
			require.Equal(t, count+1, after)
		})
	}
}

func TestInviteAcceptanceWinningClaimSerializesRevocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	token, email := acceptanceInvite(t)
	inv, err := storage.GetInvite(ctx, testPool, token)
	require.NoError(t, err)
	pause := &pauseLoginInsert{reached: make(chan struct{}), release: make(chan struct{})}
	cfg := testPool.Config()
	cfg.ConnConfig.Tracer = pause
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()
	type result struct {
		user    *storage.User
		session *storage.Session
		err     error
	}
	accepted := make(chan result, 1)
	go func() {
		user, session, err := storage.AcceptInviteWithSession(ctx, pool, token, "new-password-1234", "", 0)
		accepted <- result{user, session, err}
	}()
	select {
	case <-pause.reached:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	user, err := storage.GetUserByEmail(ctx, testPool, email)
	require.NoError(t, err)
	require.Nil(t, user)
	revoked := make(chan error, 1)
	go func() { _, err := storage.DeleteInvite(ctx, testPool, inv.ID); revoked <- err }()
	require.Eventually(t, func() bool {
		var waiting bool
		err := testPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%DELETE FROM user_invites%')`).Scan(&waiting)
		return err == nil && waiting
	}, 5*time.Second, 10*time.Millisecond)
	close(pause.release)
	admission := <-accepted
	require.NoError(t, admission.err)
	require.NotNil(t, admission.user)
	require.NotNil(t, admission.session)
	require.NoError(t, <-revoked)
	// Revoking an invitation after acceptance does not delete its admitted user.
	identity, err := storage.GetSessionIdentity(ctx, testPool, admission.session.Token)
	require.NoError(t, err)
	require.Equal(t, admission.user.ID, identity.UserID)
}

func TestInvalidInviteDoesNotLockAdmission(t *testing.T) {
	trace := &cancelGroupingQuery{match: "LOCK TABLE users"}
	cfg := testPool.Config()
	cfg.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	require.NoError(t, err)
	defer pool.Close()
	user, session, err := storage.AcceptInviteWithSession(t.Context(), pool, "invalid-token", "new-password-1234", "", 0)
	require.NoError(t, err)
	require.Nil(t, user)
	require.Nil(t, session)
	require.False(t, trace.hit.Load(), "invalid tokens must not acquire the user-table lock")
}
