package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func setupUserForMFA(t *testing.T) *storage.User {
	t.Helper()
	truncateUsers(t)
	u, err := storage.CreateUser(context.Background(), testPool, "mfa@example.com", "password1234")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func TestGetMFASecret_none(t *testing.T) {
	u := setupUserForMFA(t)
	secret, err := storage.GetMFASecret(context.Background(), testPool, u.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secret != nil {
		t.Errorf("expected nil secret, got %q", *secret)
	}
}

func TestStoreMFASecret_and_GetMFASecret(t *testing.T) {
	u := setupUserForMFA(t)

	if err := storage.StoreMFASecret(context.Background(), testPool, u.ID, "TOTP_SECRET_ABC"); err != nil {
		t.Fatalf("store secret: %v", err)
	}

	secret, err := storage.GetMFASecret(context.Background(), testPool, u.ID)
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	require.NotNil(t, secret)
	if *secret != "TOTP_SECRET_ABC" {
		t.Errorf("secret: got %q, want %q", *secret, "TOTP_SECRET_ABC")
	}
}

func TestEnableMFA(t *testing.T) {
	u := setupUserForMFA(t)

	if err := storage.StoreMFASecret(context.Background(), testPool, u.ID, "SECRET"); err != nil {
		t.Fatalf("store secret: %v", err)
	}
	if err := storage.EnableMFA(context.Background(), testPool, u.ID); err != nil {
		t.Fatalf("enable mfa: %v", err)
	}

	// Authenticate to get fresh user with MFAEnabled set
	got, err := storage.AuthenticateUser(context.Background(), testPool, "mfa@example.com", "password1234")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if !got.MFAEnabled {
		t.Error("expected MFAEnabled=true after enabling")
	}
}

func TestDisableMFA(t *testing.T) {
	u := setupUserForMFA(t)
	storage.StoreMFASecret(context.Background(), testPool, u.ID, "SECRET")
	storage.EnableMFA(context.Background(), testPool, u.ID)

	if err := storage.DisableMFA(context.Background(), testPool, u.ID); err != nil {
		t.Fatalf("disable mfa: %v", err)
	}

	got, err := storage.AuthenticateUser(context.Background(), testPool, "mfa@example.com", "password1234")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if got.MFAEnabled {
		t.Error("expected MFAEnabled=false after disabling")
	}

	secret, _ := storage.GetMFASecret(context.Background(), testPool, u.ID)
	if secret != nil {
		t.Error("expected secret=nil after disabling")
	}
}

func TestCreateMFAChallenge_and_consume(t *testing.T) {
	u := setupUserForMFA(t)

	token, err := storage.CreateMFAChallenge(context.Background(), testPool, u.ID)
	if err != nil {
		t.Fatalf("create challenge: %v", err)
	}
	if len(token) != 32 { // 16 bytes hex = 32 chars
		t.Errorf("token length: got %d, want 32", len(token))
	}

	userID, err := storage.ConsumeMFAChallenge(context.Background(), testPool, token)
	if err != nil {
		t.Fatalf("consume challenge: %v", err)
	}
	if userID != u.ID {
		t.Errorf("userID: got %q, want %q", userID, u.ID)
	}

	// Second consume: token is gone
	second, err := storage.ConsumeMFAChallenge(context.Background(), testPool, token)
	if err != nil {
		t.Fatalf("second consume: %v", err)
	}
	if second != "" {
		t.Error("expected empty string on second consume")
	}
}

func TestConsumeMFAChallenge_expired(t *testing.T) {
	u := setupUserForMFA(t)

	// Insert an already-expired challenge directly
	token := "expired-mfa-token-0000000000000001"
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO mfa_challenges (token, user_id, expires_at)
		VALUES ($1, $2, $3)
	`, token, u.ID, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("insert expired challenge: %v", err)
	}

	userID, err := storage.ConsumeMFAChallenge(context.Background(), testPool, token)
	if err != nil {
		t.Fatalf("consume expired: %v", err)
	}
	if userID != "" {
		t.Error("expected empty string for expired challenge")
	}
}

func TestConsumeMFAChallenge_notFound(t *testing.T) {
	userID, err := storage.ConsumeMFAChallenge(context.Background(), testPool, "nonexistent-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userID != "" {
		t.Error("expected empty string for unknown token")
	}
}

func TestGetMFAChallenge_found(t *testing.T) {
	truncateUsers(t)

	u, _ := storage.CreateUser(context.Background(), testPool, "mfach@example.com", "password1234")

	token, err := storage.CreateMFAChallenge(context.Background(), testPool, u.ID)
	if err != nil {
		t.Fatalf("create challenge: %v", err)
	}

	userID, err := storage.GetMFAChallenge(context.Background(), testPool, token)
	if err != nil {
		t.Fatalf("get challenge: %v", err)
	}
	if userID != u.ID {
		t.Errorf("userID: got %q, want %q", userID, u.ID)
	}
}

func TestGetMFAChallenge_notFound(t *testing.T) {
	userID, err := storage.GetMFAChallenge(context.Background(), testPool, "nonexistent-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userID != "" {
		t.Errorf("expected empty userID, got %q", userID)
	}
}
