package storage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/storage"
)

func truncateUsers(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE users CASCADE"); err != nil {
		t.Fatalf("truncate users: %v", err)
	}
}

func TestCreateUser(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "alice@example.com", "secret123abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.ID == "" {
		t.Error("expected non-empty ID")
	}
	if u.Email != "alice@example.com" {
		t.Errorf("email: got %q", u.Email)
	}
	if u.PasswordHash == "" {
		t.Error("expected non-empty password hash")
	}
	if strings.Contains(u.PasswordHash, "secret123abc") {
		t.Error("password hash must not contain plaintext password")
	}
}

func TestCreateUser_normalizesEmail(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "UPPER@Example.COM", "password1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Email != "upper@example.com" {
		t.Errorf("expected lowercase email, got %q", u.Email)
	}
}

func TestCreateUser_duplicateEmail(t *testing.T) {
	truncateUsers(t)

	if _, err := storage.CreateUser(context.Background(), testPool, "dup@example.com", "password1234"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := storage.CreateUser(context.Background(), testPool, "dup@example.com", "password5678")
	if err == nil {
		t.Error("expected error on duplicate email")
	}
}

func TestAuthenticateUser_success(t *testing.T) {
	truncateUsers(t)

	if _, err := storage.CreateUser(context.Background(), testPool, "bob@example.com", "correctpass12"); err != nil {
		t.Fatalf("create: %v", err)
	}

	u, err := storage.AuthenticateUser(context.Background(), testPool, "bob@example.com", "correctpass12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected user, got nil")
	}
	if u.Email != "bob@example.com" {
		t.Errorf("email: got %q", u.Email)
	}
}

func TestAuthenticateUser_wrongPassword(t *testing.T) {
	truncateUsers(t)

	if _, err := storage.CreateUser(context.Background(), testPool, "carol@example.com", "rightpassword1"); err != nil {
		t.Fatalf("create: %v", err)
	}

	u, err := storage.AuthenticateUser(context.Background(), testPool, "carol@example.com", "wrong")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u != nil {
		t.Error("expected nil for wrong password")
	}
}

func TestAuthenticateUser_notFound(t *testing.T) {
	u, err := storage.AuthenticateUser(context.Background(), testPool, "nobody@example.com", "pw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u != nil {
		t.Error("expected nil for unknown email")
	}
}

func TestCreateSession_and_GetSession(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "dan@example.com", "password1234")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	sess, err := storage.CreateSession(context.Background(), testPool, u.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if len(sess.Token) != 64 {
		t.Errorf("expected 64-char token, got %d", len(sess.Token))
	}

	got, err := storage.GetSession(context.Background(), testPool, sess.Token)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.UserID != u.ID {
		t.Errorf("user_id mismatch: got %q, want %q", got.UserID, u.ID)
	}
}

func TestGetSession_expired(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "eve@example.com", "password1234")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	sess, err := storage.CreateSession(context.Background(), testPool, u.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Backdate the session so it is already expired
	_, err = testPool.Exec(context.Background(), `
		UPDATE sessions SET expires_at = NOW() - interval '1 hour' WHERE user_id = $1
	`, u.ID)
	if err != nil {
		t.Fatalf("expire session: %v", err)
	}

	got, err := storage.GetSession(context.Background(), testPool, sess.Token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Error("expected nil for expired session")
	}
}

func TestListUsers(t *testing.T) {
	truncateUsers(t)

	storage.CreateUser(context.Background(), testPool, "u1@example.com", "password1234")
	storage.CreateUser(context.Background(), testPool, "u2@example.com", "password1234")

	users, err := storage.ListUsers(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("expected 2 users, got %d", len(users))
	}
}

func TestListUsers_empty(t *testing.T) {
	truncateUsers(t)

	users, err := storage.ListUsers(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("expected 0 users, got %d", len(users))
	}
}

func TestCreateOAuthUser(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateOAuthUser(context.Background(), testPool, "OAuthUser@EXAMPLE.COM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Email != "oauthuser@example.com" {
		t.Errorf("email: got %q", u.Email)
	}
	if u.PasswordHash != "" {
		t.Error("expected empty password hash for OAuth user")
	}
}

func TestGetUserByID_found(t *testing.T) {
	truncateUsers(t)

	created, _ := storage.CreateUser(context.Background(), testPool, "byid@example.com", "password1234")

	got, err := storage.GetUserByID(context.Background(), testPool, created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected user, got nil")
	}
	if got.ID != created.ID {
		t.Errorf("ID mismatch: got %q", got.ID)
	}
}

func TestGetUserByID_notFound(t *testing.T) {
	got, err := storage.GetUserByID(context.Background(), testPool, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestGetUserByEmail_found(t *testing.T) {
	truncateUsers(t)

	storage.CreateUser(context.Background(), testPool, "byemail@example.com", "password1234")

	got, err := storage.GetUserByEmail(context.Background(), testPool, "byemail@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected user, got nil")
	}
	if got.Email != "byemail@example.com" {
		t.Errorf("email: got %q", got.Email)
	}
}

func TestGetUserByEmail_notFound(t *testing.T) {
	got, err := storage.GetUserByEmail(context.Background(), testPool, "nobody@nowhere.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestGetUserByEmail_caseInsensitive(t *testing.T) {
	truncateUsers(t)

	storage.CreateUser(context.Background(), testPool, "case@example.com", "password1234")

	got, err := storage.GetUserByEmail(context.Background(), testPool, "CASE@EXAMPLE.COM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected user for uppercase email lookup, got nil")
	}
}

func TestCreateUser_firstUserGetsAllPerms(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "first@example.com", "longpassword1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !u.Permissions.ManageProjects || !u.Permissions.ManageUsers ||
		!u.Permissions.ManageAlerts || !u.Permissions.ManageIssues {
		t.Errorf("first user should get all permissions, got %+v", u.Permissions)
	}
}

func TestCreateUser_secondUserGetsNoPerms(t *testing.T) {
	truncateUsers(t)

	if _, err := storage.CreateUser(context.Background(), testPool, "first@example.com", "longpassword1"); err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := storage.CreateUser(context.Background(), testPool, "second@example.com", "longpassword2")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if second.Permissions.ManageProjects || second.Permissions.ManageUsers ||
		second.Permissions.ManageAlerts || second.Permissions.ManageIssues {
		t.Errorf("second user should have no permissions, got %+v", second.Permissions)
	}
}

func TestUpdateUserPermissions(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "perms@example.com", "longpassword1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := storage.UpdateUserPermissions(context.Background(), testPool, u.ID, storage.UserPermissions{
		ManageProjects: true,
		ManageUsers:    false,
		ManageAlerts:   true,
		ManageIssues:   false,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated == nil {
		t.Fatal("expected user, got nil")
	}
	if !updated.Permissions.ManageProjects {
		t.Error("ManageProjects should be true")
	}
	if updated.Permissions.ManageUsers {
		t.Error("ManageUsers should be false")
	}
	if !updated.Permissions.ManageAlerts {
		t.Error("ManageAlerts should be true")
	}
	if updated.Permissions.ManageIssues {
		t.Error("ManageIssues should be false")
	}

	// Verify persisted by re-reading.
	got, err := storage.GetUserByID(context.Background(), testPool, u.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.Permissions != updated.Permissions {
		t.Errorf("persisted perms mismatch: got %+v", got.Permissions)
	}
}

func TestUpdateUserPermissions_notFound(t *testing.T) {
	got, err := storage.UpdateUserPermissions(context.Background(), testPool,
		"00000000-0000-0000-0000-000000000000", storage.UserPermissions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for unknown user, got %+v", got)
	}
}

func TestDeleteSession(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "frank@example.com", "password1234")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	sess, err := storage.CreateSession(context.Background(), testPool, u.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := storage.DeleteSession(context.Background(), testPool, sess.Token); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	got, err := storage.GetSession(context.Background(), testPool, sess.Token)
	if err != nil {
		t.Fatalf("get session after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}
