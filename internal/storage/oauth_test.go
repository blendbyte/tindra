package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

func truncateOAuth(t *testing.T) {
	t.Helper()
	testPool.Exec(context.Background(), "TRUNCATE oauth_identities, oauth_states CASCADE")
}

func TestFindOrCreateOAuthUser_newUser(t *testing.T) {
	truncateUsers(t)
	truncateOAuth(t)

	u, err := storage.FindOrCreateOAuthUser(context.Background(), testPool, "github", "gh-sub-001", "oauth@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected user, got nil")
	}
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

	first, _ := storage.FindOrCreateOAuthUser(context.Background(), testPool, "github", "gh-sub-002", "returning@example.com")
	second, err := storage.FindOrCreateOAuthUser(context.Background(), testPool, "github", "gh-sub-002", "returning@example.com")
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
	oauthUser, err := storage.FindOrCreateOAuthUser(context.Background(), testPool, "google", "google-sub-001", "linked@example.com")
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

	u, _ := storage.FindOrCreateOAuthUser(context.Background(), testPool, "github", "gh-sub-003", "UPPER@EXAMPLE.COM")
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
	if state == nil {
		t.Fatal("expected state, got nil")
	}
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
		INSERT INTO oauth_states (token, provider, verifier, expires_at)
		VALUES ($1, 'github', 'v', $2)
	`, token, time.Now().Add(-time.Hour))
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
