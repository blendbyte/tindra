package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func truncatePasswordResets(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE password_reset_tokens"); err != nil {
		t.Fatalf("truncate password_reset_tokens: %v", err)
	}
}

func TestCreatePasswordResetToken(t *testing.T) {
	truncateUsers(t)
	truncatePasswordResets(t)

	u, err := storage.CreateUser(context.Background(), testPool, "reset@example.com", "oldpassword1")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	token, err := storage.CreatePasswordResetToken(context.Background(), testPool, u.ID)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if len(token) != 64 { // 32 random bytes → 64 hex chars
		t.Errorf("expected 64-char token, got %d: %q", len(token), token)
	}
}

func TestGetPasswordResetUser_valid(t *testing.T) {
	truncateUsers(t)
	truncatePasswordResets(t)

	u, _ := storage.CreateUser(context.Background(), testPool, "getreset@example.com", "oldpassword1")
	token, _ := storage.CreatePasswordResetToken(context.Background(), testPool, u.ID)

	got, err := storage.GetPasswordResetUser(context.Background(), testPool, token)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	require.NotNil(t, got)
	if got.Email != "getreset@example.com" {
		t.Errorf("email: got %q", got.Email)
	}
}

func TestGetPasswordResetUser_notFound(t *testing.T) {
	got, err := storage.GetPasswordResetUser(context.Background(), testPool, "nonexistenttoken00000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for bad token, got %+v", got)
	}
}

func TestUsePasswordResetToken_valid(t *testing.T) {
	truncateUsers(t)
	truncatePasswordResets(t)

	u, _ := storage.CreateUser(context.Background(), testPool, "use@example.com", "oldpassword1")
	token, _ := storage.CreatePasswordResetToken(context.Background(), testPool, u.ID)

	got, err := storage.UsePasswordResetToken(context.Background(), testPool, token, "newpassword99")
	if err != nil {
		t.Fatalf("use: %v", err)
	}
	require.NotNil(t, got)
	if !got.HasPassword {
		t.Error("expected HasPassword=true after reset")
	}

	// Old token should be consumed and no longer usable.
	got2, err := storage.UsePasswordResetToken(context.Background(), testPool, token, "anotherpassword1")
	if err != nil {
		t.Fatalf("second use: %v", err)
	}
	if got2 != nil {
		t.Error("expected nil on second use of same token")
	}
}

func TestUsePasswordResetToken_notFound(t *testing.T) {
	got, err := storage.UsePasswordResetToken(context.Background(), testPool, "badtoken00000000000000000000000000000000000000000000000000000000", "newpassword99")
	if err != nil {
		t.Fatalf("use bad token: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for bad token, got %+v", got)
	}
}

func TestUsePasswordResetToken_passwordTooShort(t *testing.T) {
	truncateUsers(t)
	truncatePasswordResets(t)

	u, _ := storage.CreateUser(context.Background(), testPool, "short@example.com", "longpassword1")
	token, _ := storage.CreatePasswordResetToken(context.Background(), testPool, u.ID)

	_, err := storage.UsePasswordResetToken(context.Background(), testPool, token, "short")
	if err == nil {
		t.Error("expected error for password too short")
	}
}

func TestAdminSetPassword(t *testing.T) {
	truncateUsers(t)

	u, _ := storage.CreateUser(context.Background(), testPool, "admin@example.com", "oldpassword1")

	if err := storage.AdminSetPassword(context.Background(), testPool, u.ID, "newlongpassword1"); err != nil {
		t.Fatalf("admin set password: %v", err)
	}

	// Verify new password works.
	authed, err := storage.AuthenticateUser(context.Background(), testPool, "admin@example.com", "newlongpassword1")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if authed == nil {
		t.Error("expected successful auth with new password")
	}

	// Old password should no longer work.
	old, _ := storage.AuthenticateUser(context.Background(), testPool, "admin@example.com", "oldpassword1")
	if old != nil {
		t.Error("expected old password to be rejected")
	}
}

func TestAdminSetPassword_notFound(t *testing.T) {
	err := storage.AdminSetPassword(context.Background(), testPool, "00000000-0000-0000-0000-000000000000", "newlongpassword1")
	if err == nil {
		t.Error("expected error for unknown user")
	}
}

func TestAdminSetPassword_tooShort(t *testing.T) {
	truncateUsers(t)
	u, _ := storage.CreateUser(context.Background(), testPool, "admshort@example.com", "oldpassword1")
	err := storage.AdminSetPassword(context.Background(), testPool, u.ID, "short")
	if err == nil {
		t.Error("expected error for short password")
	}
}
