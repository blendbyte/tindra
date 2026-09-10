package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestLinkSSOCmd(t *testing.T) {
	truncateUsersAndInvites(t)
	user, err := storage.CreateAdminUser(t.Context(), testUserPool, "sso@example.com", "SSO", "password1234")
	require.NoError(t, err)
	require.NoError(t, storage.StoreMFASecret(t.Context(), testUserPool, user.ID, "test-secret"))
	require.NoError(t, storage.EnableMFA(t.Context(), testUserPool, user.ID))
	for range 2 {
		cmd := usersCmd(inviteCfg())
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"link-sso", " SSO@example.com ", "--provider", "microsoft", "--subject", "exact-Subject_123"})
		require.NoError(t, cmd.Execute())
		require.Contains(t, out.String(), "Linked sso@example.com")
	}
	linked, err := storage.FindOrCreateOAuthUser(t.Context(), testUserPool, "microsoft", "exact-Subject_123", "", false, 0)
	require.NoError(t, err)
	require.Equal(t, user.ID, linked.ID)
	require.True(t, linked.MFAEnabled)
	require.Equal(t, user.Permissions, linked.Permissions)
	var count int
	require.NoError(t, testUserPool.QueryRow(t.Context(), "SELECT count(*) FROM oauth_identities WHERE user_id=$1", user.ID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, testUserPool.QueryRow(t.Context(), "SELECT count(*) FROM audit_log WHERE event_type='user.sso.link' AND target_id=$1 AND details->>'provider'='microsoft' AND details->>'source'='cli'", user.ID).Scan(&count))
	require.Equal(t, 2, count)
}

func TestLinkSSOCmdRejectsInvalidInput(t *testing.T) {
	for _, args := range [][]string{
		{}, {"a@example.com"}, {"a@example.com", "--provider", "microsoft"}, {"a@example.com", "--subject", "subject"},
		{" ", "--provider", "microsoft", "--subject", "subject"},
		{"a@example.com", "--provider", " ", "--subject", "subject"},
		{"a@example.com", "--provider", "microsoft", "--subject", " "},
		{"a@example.com", "extra", "--provider", "microsoft", "--subject", "subject"},
	} {
		cmd := usersLinkSSOCmd(config{})
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		require.Error(t, cmd.Execute())
	}
}

func TestLinkSSOCmdFailures(t *testing.T) {
	truncateUsersAndInvites(t)
	owner, err := storage.CreateUser(t.Context(), testUserPool, "owner@example.com", "password1234")
	require.NoError(t, err)
	_, err = storage.CreateUser(t.Context(), testUserPool, "other@example.com", "password1234")
	require.NoError(t, err)
	require.NoError(t, storage.LinkOAuthIdentity(t.Context(), testUserPool, owner.ID, "microsoft", "subject"))
	for _, tc := range []struct {
		name, email string
		cfg         config
		want        string
	}{
		{"missing user", "missing@example.com", inviteCfg(), "no user found"},
		{"conflict", "other@example.com", inviteCfg(), "subject belongs to another user"},
		{"connection", "owner@example.com", config{}, "DATABASE_URL is not set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := usersLinkSSOCmd(tc.cfg)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs([]string{tc.email, "--provider", "microsoft", "--subject", "subject"})
			require.ErrorContains(t, cmd.Execute(), tc.want)
		})
	}
	t.Run("lookup failure", func(t *testing.T) {
		_, err := testUserPool.Exec(t.Context(), "ALTER TABLE users RENAME TO sso_hidden_users")
		require.NoError(t, err)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := testUserPool.Exec(ctx, "ALTER TABLE sso_hidden_users RENAME TO users")
			require.NoError(t, err)
		})
		cmd := usersLinkSSOCmd(inviteCfg())
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"owner@example.com", "--provider", "microsoft", "--subject", "subject"})
		require.ErrorContains(t, cmd.Execute(), "look up user")
	})
}
