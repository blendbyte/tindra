package storage_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func oauthTestHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func truncateOAuth(t *testing.T) {
	t.Helper()
	testPool.Exec(context.Background(), "TRUNCATE oauth_identities, oauth_states CASCADE")
}

func TestFindOrCreateOAuthUser_newUser(t *testing.T) {
	truncateUsers(t)
	truncateOAuth(t)

	_, err := storage.CreateInvite(t.Context(), testPool, "", "oauth@example.com", "Invited user")
	require.NoError(t, err)
	u, err := storage.FindOrCreateOAuthUser(context.Background(), testPool, "github", "gh-sub-001", "oauth@example.com", true, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.NotNil(t, u)
	if u.Email != "oauth@example.com" {
		t.Errorf("email: got %q", u.Email)
	}
	if u.PasswordHash != "" {
		t.Error("OAuth user should have empty password hash")
	}
}

func TestFindOrCreateOAuthUser_existingIdentity(t *testing.T) {
	truncateUsers(t)
	truncateOAuth(t)

	_, err := storage.CreateInvite(t.Context(), testPool, "", "returning@example.com", "")
	require.NoError(t, err)
	first, err := storage.FindOrCreateOAuthUser(context.Background(), testPool, "github", "gh-sub-002", "returning@example.com", true, 0)
	require.NoError(t, err)
	second, err := storage.FindOrCreateOAuthUser(context.Background(), testPool, "github", "gh-sub-002", "returning@example.com", true, 0)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if second.ID != first.ID {
		t.Error("expected same user on second call with same identity")
	}
}

func TestFindOrCreateOAuthUser_linksByEmail(t *testing.T) {
	truncateUsers(t)
	truncateOAuth(t)

	// Create a local user first
	local, _ := storage.CreateUser(context.Background(), testPool, "linked@example.com", "password1234")

	// OAuth login with matching email → should find and link to existing user
	oauthUser, err := storage.FindOrCreateOAuthUser(context.Background(), testPool, "google", "google-sub-001", "linked@example.com", true, 0)
	if err != nil {
		t.Fatalf("oauth link: %v", err)
	}
	if oauthUser.ID != local.ID {
		t.Error("expected existing user to be returned and linked")
	}
}

func TestFindOrCreateOAuthUser_caseInsensitiveEmail(t *testing.T) {
	truncateUsers(t)
	truncateOAuth(t)

	_, err := storage.CreateInvite(t.Context(), testPool, "", "upper@example.com", "")
	require.NoError(t, err)
	u, err := storage.FindOrCreateOAuthUser(context.Background(), testPool, "github", "gh-sub-003", "UPPER@EXAMPLE.COM", true, 0)
	require.NoError(t, err)
	if u.Email != "upper@example.com" {
		t.Errorf("email should be normalized: got %q", u.Email)
	}
}

func TestCreateOAuthState_and_consume(t *testing.T) {
	truncateOAuth(t)

	token, err := storage.CreateOAuthState(context.Background(), testPool, "github", "pkce-verifier-xyz")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	if len(token) != 32 {
		t.Errorf("token length: got %d, want 32", len(token))
	}

	state, err := storage.ConsumeOAuthState(context.Background(), testPool, token)
	if err != nil {
		t.Fatalf("consume state: %v", err)
	}
	require.NotNil(t, state)
	if state.Provider != "github" {
		t.Errorf("provider: got %q, want %q", state.Provider, "github")
	}
	if state.Verifier != "pkce-verifier-xyz" {
		t.Errorf("verifier: got %q, want %q", state.Verifier, "pkce-verifier-xyz")
	}

	// Second consume: token deleted
	second, err := storage.ConsumeOAuthState(context.Background(), testPool, token)
	if err != nil {
		t.Fatalf("second consume: %v", err)
	}
	if second != nil {
		t.Error("expected nil on second consume")
	}
}

func TestConsumeOAuthState_expired(t *testing.T) {
	truncateOAuth(t)

	token := "expired-oauth-state-000000000000001"
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO oauth_states (token_hash, provider, verifier, expires_at)
		VALUES ($1, 'github', 'v', $2)
	`, oauthTestHash(token), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("insert expired state: %v", err)
	}

	state, err := storage.ConsumeOAuthState(context.Background(), testPool, token)
	if err != nil {
		t.Fatalf("consume expired: %v", err)
	}
	if state != nil {
		t.Error("expected nil for expired state")
	}
}

func TestConsumeOAuthState_notFound(t *testing.T) {
	state, err := storage.ConsumeOAuthState(context.Background(), testPool, "unknown-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != nil {
		t.Errorf("expected nil, got %+v", state)
	}
}

func TestOAuthAdmissionRejectsMissingOrInvalidInvites(t *testing.T) {
	for _, state := range []string{"missing", "expired", "accepted", "revoked", "different-email"} {
		t.Run(state, func(t *testing.T) {
			truncateUsers(t)
			email := "admission@example.com"
			var token string
			if state != "missing" {
				invitedEmail := email
				if state == "different-email" {
					invitedEmail = "other@example.com"
				}
				var err error
				token, err = storage.CreateInvite(t.Context(), testPool, "", invitedEmail, "Invite")
				require.NoError(t, err)
				switch state {
				case "expired":
					_, err = testPool.Exec(t.Context(), `UPDATE user_invites SET expires_at=NOW()-interval '1 minute' WHERE token_hash=$1`, oauthTestHash(token))
				case "accepted":
					err = storage.MarkInviteAccepted(t.Context(), testPool, token)
				case "revoked":
					_, err = testPool.Exec(t.Context(), `DELETE FROM user_invites WHERE token_hash=$1`, oauthTestHash(token))
				}
				require.NoError(t, err)
			}
			user, err := storage.FindOrCreateOAuthUser(t.Context(), testPool, "google", "denied", email, true, 0)
			require.ErrorIs(t, err, storage.ErrOAuthInviteRequired)
			require.Nil(t, user)
			var users, identities int
			require.NoError(t, testPool.QueryRow(t.Context(), `SELECT count(*) FROM users`).Scan(&users))
			require.NoError(t, testPool.QueryRow(t.Context(), `SELECT count(*) FROM oauth_identities`).Scan(&identities))
			require.Zero(t, users)
			require.Zero(t, identities)
			_, err = testPool.Exec(t.Context(), `DELETE FROM user_invites`)
			require.NoError(t, err)
		})
	}
}

func TestOAuthAdmissionConsumesInviteWithoutGrantingAdmin(t *testing.T) {
	truncateUsers(t)
	token, err := storage.CreateInvite(t.Context(), testPool, "", "invited@example.com", "Invited Name")
	require.NoError(t, err)
	user, err := storage.FindOrCreateOAuthUser(t.Context(), testPool, "google", "invited", "INVITED@example.com", true, 1)
	require.NoError(t, err)
	require.Equal(t, "Invited Name", user.Name)
	require.Equal(t, storage.UserPermissions{}, user.Permissions)
	require.False(t, user.HasPassword)
	invite, err := storage.GetInvite(t.Context(), testPool, token)
	require.NoError(t, err)
	require.Nil(t, invite)
	// A linked identity remains authoritative even if the provider email changes.
	returning, err := storage.FindOrCreateOAuthUser(t.Context(), testPool, "google", "invited", "changed@example.com", true, 1)
	require.NoError(t, err)
	require.Equal(t, user.ID, returning.ID)
	// Existing users can also link another provider when the instance is full.
	linked, err := storage.FindOrCreateOAuthUser(t.Context(), testPool, "github", "invited", user.Email, true, 1)
	require.NoError(t, err)
	require.Equal(t, user.ID, linked.ID)
}

func TestOAuthAdmissionUserLimitPreservesInvite(t *testing.T) {
	truncateUsers(t)
	_, err := storage.CreateUser(t.Context(), testPool, "existing@example.com", "password1234")
	require.NoError(t, err)
	token, err := storage.CreateInvite(t.Context(), testPool, "", "waiting@example.com", "Waiting")
	require.NoError(t, err)
	user, err := storage.FindOrCreateOAuthUser(t.Context(), testPool, "google", "waiting", "waiting@example.com", true, 1)
	require.ErrorIs(t, err, storage.ErrOAuthUserLimit)
	require.Nil(t, user)
	invite, err := storage.GetInvite(t.Context(), testPool, token)
	require.NoError(t, err)
	require.NotNil(t, invite)
	user, err = storage.GetUserByEmail(t.Context(), testPool, "waiting@example.com")
	require.NoError(t, err)
	require.Nil(t, user)
	var n int
	require.NoError(t, testPool.QueryRow(t.Context(), `SELECT count(*) FROM oauth_identities`).Scan(&n))
	require.Zero(t, n)
	// Retrying after capacity becomes available consumes the same invitation.
	_, err = storage.FindOrCreateOAuthUser(t.Context(), testPool, "google", "waiting", "waiting@example.com", true, 2)
	require.NoError(t, err)
}

func TestOAuthAdmissionConcurrentLastSlot(t *testing.T) {
	truncateUsers(t)
	for _, email := range []string{"one@example.com", "two@example.com"} {
		_, err := storage.CreateInvite(t.Context(), testPool, "", email, "")
		require.NoError(t, err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, email := range []string{"one@example.com", "two@example.com"} {
		go func() {
			<-start
			_, err := storage.FindOrCreateOAuthUser(t.Context(), testPool, "google", email, email, true, 1)
			results <- err
		}()
	}
	close(start)
	var admitted, denied int
	for range 2 {
		err := <-results
		if err == nil {
			admitted++
		} else {
			require.ErrorIs(t, err, storage.ErrOAuthUserLimit)
			denied++
		}
	}
	require.Equal(t, 1, admitted)
	require.Equal(t, 1, denied)
}

func TestOAuthAdmissionConcurrentSameIdentity(t *testing.T) {
	truncateUsers(t)
	token, err := storage.CreateInvite(t.Context(), testPool, "", "same@example.com", "")
	require.NoError(t, err)
	type result struct {
		user *storage.User
		err  error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			user, err := storage.FindOrCreateOAuthUser(t.Context(), testPool, "google", "same", "same@example.com", true, 1)
			results <- result{user, err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	require.Equal(t, first.user.ID, second.user.ID)
	invite, err := storage.GetInvite(t.Context(), testPool, token)
	require.NoError(t, err)
	require.Nil(t, invite)
	var users, identities int
	require.NoError(t, testPool.QueryRow(t.Context(), `SELECT count(*) FROM users`).Scan(&users))
	require.NoError(t, testPool.QueryRow(t.Context(), `SELECT count(*) FROM oauth_identities`).Scan(&identities))
	require.Equal(t, 1, users)
	require.Equal(t, 1, identities)
}

func TestOAuthAdmissionDatabaseFailuresRollBack(t *testing.T) {
	for _, stage := range []string{
		"SELECT user_id FROM oauth_identities", "begin", "LOCK TABLE users",
		"SELECT id FROM users", "SELECT id, COALESCE(name", "SELECT count(*) FROM users",
		"INSERT INTO users", "UPDATE user_invites", "INSERT INTO oauth_identities", "commit",
	} {
		t.Run(stage, func(t *testing.T) {
			truncateUsers(t)
			token, err := storage.CreateInvite(t.Context(), testPool, "", "rollback@example.com", "Invited")
			require.NoError(t, err)
			trace := &cancelGroupingQuery{match: stage}
			cfg := testPool.Config()
			cfg.ConnConfig.Tracer = trace
			failing, err := pgxpool.NewWithConfig(t.Context(), cfg)
			require.NoError(t, err)
			user, err := storage.FindOrCreateOAuthUser(t.Context(), failing, "google", "rollback", "rollback@example.com", true, 1)
			require.Error(t, err)
			require.Nil(t, user)
			require.True(t, trace.hit.Load())
			failing.Close()
			var users, identities int
			require.NoError(t, testPool.QueryRow(t.Context(), `SELECT count(*) FROM users`).Scan(&users))
			require.NoError(t, testPool.QueryRow(t.Context(), `SELECT count(*) FROM oauth_identities`).Scan(&identities))
			require.Zero(t, users)
			require.Zero(t, identities)
			invite, err := storage.GetInvite(t.Context(), testPool, token)
			require.NoError(t, err)
			require.NotNil(t, invite)
			// A failed attempt must leave the same invitation usable on retry.
			user, err = storage.FindOrCreateOAuthUser(t.Context(), testPool, "google", "rollback", "rollback@example.com", true, 1)
			require.NoError(t, err)
			require.NotNil(t, user)
		})
	}
}

func TestOAuthAdmissionRequiresVerifiedEmail(t *testing.T) {
	truncateUsers(t)
	user, err := storage.CreateUser(t.Context(), testPool, "verified@example.com", "password1234")
	require.NoError(t, err)
	for _, tc := range []struct {
		name, email string
		verified    bool
	}{
		{"unverified existing account", user.Email, false},
		{"unverified new account", "unknown@example.com", false},
		{"missing email", "", true},
		{"blank email", " \t ", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := storage.FindOrCreateOAuthUser(t.Context(), testPool, "verification", "new", tc.email, tc.verified, 0)
			require.ErrorIs(t, err, storage.ErrOAuthEmailUnverified)
			require.Nil(t, got)
		})
	}
	linked, err := storage.FindOrCreateOAuthUser(t.Context(), testPool, "verification", "linked", user.Email, true, 0)
	require.NoError(t, err)
	require.Equal(t, user.ID, linked.ID)
	for _, email := range []string{"", "different@example.com"} {
		returning, err := storage.FindOrCreateOAuthUser(t.Context(), testPool, "verification", "linked", email, false, 0)
		require.NoError(t, err)
		require.Equal(t, user.ID, returning.ID)
		// The subject is scoped to its provider, not globally trusted.
		denied, err := storage.FindOrCreateOAuthUser(t.Context(), testPool, "other-provider", "linked", email, false, 0)
		require.ErrorIs(t, err, storage.ErrOAuthEmailUnverified)
		require.Nil(t, denied)
	}
}
