package storage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

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
	require.NotNil(t, u)
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
	require.NotNil(t, got)
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
	require.NotNil(t, got)
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
	require.NotNil(t, got)
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
	require.NotNil(t, got)
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
	require.NotNil(t, updated)
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

func TestCountUsers(t *testing.T) {
	truncateUsers(t)

	n, err := storage.CountUsers(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 users, got %d", n)
	}

	storage.CreateUser(context.Background(), testPool, "count1@example.com", "password1234")
	storage.CreateUser(context.Background(), testPool, "count2@example.com", "password1234")

	n, err = storage.CountUsers(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 users, got %d", n)
	}
}

func TestDeleteUser(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "delete@example.com", "password1234")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	deleted, err := storage.DeleteUser(context.Background(), testPool, u.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !deleted {
		t.Error("expected deleted=true")
	}

	got, err := storage.GetUserByID(context.Background(), testPool, u.ID)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestDeleteUser_notFound(t *testing.T) {
	deleted, err := storage.DeleteUser(context.Background(), testPool, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted {
		t.Error("expected deleted=false for unknown ID")
	}
}

func TestUpdateUserProfile(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "profile@example.com", "password1234")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := storage.UpdateUserProfile(context.Background(), testPool, u.ID, "Alice", "new@example.com", "America/New_York")
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if updated == nil {
		t.Fatal("expected non-nil result")
	}
	if updated.Name != "Alice" {
		t.Errorf("name: got %q, want %q", updated.Name, "Alice")
	}
	if updated.Email != "new@example.com" {
		t.Errorf("email: got %q, want %q", updated.Email, "new@example.com")
	}
	if updated.Timezone != "America/New_York" {
		t.Errorf("timezone: got %q, want %q", updated.Timezone, "America/New_York")
	}
}

func TestUpdateUserProfile_normalizesEmail(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "prof2@example.com", "password1234")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := storage.UpdateUserProfile(context.Background(), testPool, u.ID, "Bob", "UPPER@EXAMPLE.COM", "UTC")
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if updated.Email != "upper@example.com" {
		t.Errorf("expected lowercase email, got %q", updated.Email)
	}
}

func TestChangeUserPassword(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "changepw@example.com", "oldpassword1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := storage.ChangeUserPassword(context.Background(), testPool, u.ID, "oldpassword1", "newpassword1"); err != nil {
		t.Fatalf("change password: %v", err)
	}

	// Verify new password works
	authenticated, err := storage.AuthenticateUser(context.Background(), testPool, "changepw@example.com", "newpassword1")
	if err != nil {
		t.Fatalf("authenticate after change: %v", err)
	}
	if authenticated == nil {
		t.Error("expected successful auth with new password")
	}

	// Verify old password no longer works
	authenticated, err = storage.AuthenticateUser(context.Background(), testPool, "changepw@example.com", "oldpassword1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if authenticated != nil {
		t.Error("expected old password to be rejected")
	}
}

func TestChangeUserPassword_wrongCurrent(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "changepw2@example.com", "correctpass1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	err = storage.ChangeUserPassword(context.Background(), testPool, u.ID, "wrongpass", "newpassword1")
	if err == nil {
		t.Error("expected error for wrong current password")
	}
}

func TestChangeUserPassword_tooShort(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "changepw3@example.com", "oldpassword1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	err = storage.ChangeUserPassword(context.Background(), testPool, u.ID, "oldpassword1", "short")
	if err == nil {
		t.Error("expected error for too-short password")
	}
}

func TestDeleteSessionReturningUserID(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "delsess@example.com", "password1234")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	sess, err := storage.CreateSession(context.Background(), testPool, u.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	userID, err := storage.DeleteSessionReturningUserID(context.Background(), testPool, sess.Token)
	if err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if userID != u.ID {
		t.Errorf("userID: got %q, want %q", userID, u.ID)
	}

	// Deleting again should return empty string
	userID2, err := storage.DeleteSessionReturningUserID(context.Background(), testPool, sess.Token)
	if err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if userID2 != "" {
		t.Errorf("expected empty userID on second delete, got %q", userID2)
	}
}

func TestUpdateUserWeeklyDigest(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "digest@example.com", "password1234")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := storage.UpdateUserWeeklyDigest(context.Background(), testPool, u.ID, true); err != nil {
		t.Fatalf("enable digest: %v", err)
	}

	got, err := storage.GetUserByID(context.Background(), testPool, u.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if !got.WeeklyDigest {
		t.Error("expected weekly_digest=true")
	}

	if err := storage.UpdateUserWeeklyDigest(context.Background(), testPool, u.ID, false); err != nil {
		t.Fatalf("disable digest: %v", err)
	}
	got, _ = storage.GetUserByID(context.Background(), testPool, u.ID)
	if got.WeeklyDigest {
		t.Error("expected weekly_digest=false after disable")
	}
}

func TestListDigestDueUsers(t *testing.T) {
	truncateUsers(t)

	u1, _ := storage.CreateUser(context.Background(), testPool, "du1@example.com", "password1234")
	u2, _ := storage.CreateUser(context.Background(), testPool, "du2@example.com", "password1234")
	u3, _ := storage.CreateUser(context.Background(), testPool, "du3@example.com", "password1234")

	// weekly_digest defaults to true; explicitly disable u3
	storage.UpdateUserWeeklyDigest(context.Background(), testPool, u1.ID, true)
	storage.UpdateUserWeeklyDigest(context.Background(), testPool, u2.ID, true)
	storage.UpdateUserWeeklyDigest(context.Background(), testPool, u3.ID, false)

	users, err := storage.ListDigestDueUsers(context.Background(), testPool, true)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("expected 2 due users, got %d", len(users))
	}
}

func TestListDigestDueUsers_respectsTimeFilter(t *testing.T) {
	truncateUsers(t)

	u, _ := storage.CreateUser(context.Background(), testPool, "dtime@example.com", "password1234")
	storage.UpdateUserWeeklyDigest(context.Background(), testPool, u.ID, true)

	// Mark digest sent recently
	storage.MarkDigestSent(context.Background(), testPool, u.ID)

	// Without force, user sent recently should not appear
	users, err := storage.ListDigestDueUsers(context.Background(), testPool, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, du := range users {
		if du.ID == u.ID {
			t.Error("user with recent digest should not appear without force=true")
		}
	}
}

func TestMarkDigestSent(t *testing.T) {
	truncateUsers(t)

	u, _ := storage.CreateUser(context.Background(), testPool, "mds@example.com", "password1234")
	storage.UpdateUserWeeklyDigest(context.Background(), testPool, u.ID, true)

	if err := storage.MarkDigestSent(context.Background(), testPool, u.ID); err != nil {
		t.Fatalf("mark digest sent: %v", err)
	}

	// User should not appear in due list (sent just now)
	users, _ := storage.ListDigestDueUsers(context.Background(), testPool, false)
	for _, du := range users {
		if du.ID == u.ID {
			t.Error("user with digest just sent should not be due")
		}
	}
}

func TestCreateAdminUser(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateAdminUser(context.Background(), testPool, "admin@example.com", "Admin User", "adminpassword1")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if u.ID == "" {
		t.Error("expected non-empty ID")
	}
	if !u.Permissions.ManageProjects || !u.Permissions.ManageUsers ||
		!u.Permissions.ManageAlerts || !u.Permissions.ManageIssues {
		t.Errorf("admin user should have all permissions, got %+v", u.Permissions)
	}
}

// ---------------------------------------------------------------------------
// CreateUser password validation (lines 65-70)
// ---------------------------------------------------------------------------

func TestCreateUser_shortPassword(t *testing.T) {
	_, err := storage.CreateUser(context.Background(), testPool, "short-pw@example.com", "short")
	if err == nil {
		t.Error("expected error for password shorter than 12 chars")
	}
}

func TestCreateUser_longPassword(t *testing.T) {
	_, err := storage.CreateUser(context.Background(), testPool, "long-pw@example.com", strings.Repeat("x", 73))
	if err == nil {
		t.Error("expected error for password longer than 72 chars")
	}
}

// ---------------------------------------------------------------------------
// CreateAdminUser password validation and duplicate email (lines 98-120)
// ---------------------------------------------------------------------------

func TestCreateAdminUser_shortPassword(t *testing.T) {
	_, err := storage.CreateAdminUser(context.Background(), testPool, "admin-short@example.com", "Admin", "short")
	if err == nil {
		t.Error("expected error for password shorter than 12 chars")
	}
}

func TestCreateAdminUser_longPassword(t *testing.T) {
	_, err := storage.CreateAdminUser(context.Background(), testPool, "admin-long@example.com", "Admin", strings.Repeat("x", 73))
	if err == nil {
		t.Error("expected error for password longer than 72 chars")
	}
}

func TestCreateAdminUser_duplicateEmail(t *testing.T) {
	truncateUsers(t)

	if _, err := storage.CreateAdminUser(context.Background(), testPool, "admin-dup@example.com", "Admin One", "adminpassword1"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := storage.CreateAdminUser(context.Background(), testPool, "admin-dup@example.com", "Admin Two", "adminpassword2")
	if err == nil {
		t.Error("expected error for duplicate email")
	}
}

// ---------------------------------------------------------------------------
// CreateOAuthUser duplicate email (line 142)
// ---------------------------------------------------------------------------

func TestCreateOAuthUser_duplicateEmail(t *testing.T) {
	truncateUsers(t)

	if _, err := storage.CreateOAuthUser(context.Background(), testPool, "oauth-dup@example.com"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := storage.CreateOAuthUser(context.Background(), testPool, "oauth-dup@example.com")
	if err == nil {
		t.Error("expected error for duplicate OAuth email")
	}
}

// ---------------------------------------------------------------------------
// AuthenticateUser locked user (lines 211-213)
// ---------------------------------------------------------------------------

func TestAuthenticateUser_lockedUser(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "locked@example.com", "correctpass12")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Lock the user by setting locked_until to a future time.
	if _, err := testPool.Exec(context.Background(),
		`UPDATE users SET locked_until = NOW() + INTERVAL '15 minutes' WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("lock user: %v", err)
	}

	got, err := storage.AuthenticateUser(context.Background(), testPool, "locked@example.com", "correctpass12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Error("expected nil for locked user")
	}
}

// ---------------------------------------------------------------------------
// ChangeUserPassword additional paths (lines 330,336,342)
// ---------------------------------------------------------------------------

func TestChangeUserPassword_longPassword(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "changepw-long@example.com", "oldpassword1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	err = storage.ChangeUserPassword(context.Background(), testPool, u.ID, "oldpassword1", strings.Repeat("x", 73))
	if err == nil {
		t.Error("expected error for too-long new password")
	}
}

func TestChangeUserPassword_userNotFound(t *testing.T) {
	err := storage.ChangeUserPassword(context.Background(), testPool,
		"00000000-0000-0000-0000-000000000000", "anypassword1", "newpassword1")
	if err == nil {
		t.Error("expected error for nonexistent user")
	}
}

func TestChangeUserPassword_oauthUser(t *testing.T) {
	truncateUsers(t)

	u, err := storage.CreateOAuthUser(context.Background(), testPool, "oauth-changepw@example.com")
	if err != nil {
		t.Fatalf("create oauth user: %v", err)
	}

	err = storage.ChangeUserPassword(context.Background(), testPool, u.ID, "", "newpassword1")
	if err == nil {
		t.Error("expected error for OAuth user with no password hash")
	}
}
