package storage_test

import (
	"context"
	"testing"

	"github.com/blendbyte/tindra/internal/storage"
)

func truncateInvites(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE user_invites"); err != nil {
		t.Fatalf("truncate user_invites: %v", err)
	}
}

func TestCreateInvite_returnsToken(t *testing.T) {
	truncateInvites(t)

	token, err := storage.CreateInvite(context.Background(), testPool, "", "invite@example.com", "")
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if token == "" {
		t.Error("expected non-empty token")
	}
	if len(token) != 48 { // 24 random bytes → 48 hex chars
		t.Errorf("expected 48-char token, got %d", len(token))
	}
}

func TestGetInvite_found(t *testing.T) {
	truncateInvites(t)

	token, err := storage.CreateInvite(context.Background(), testPool, "", "found@example.com", "Alice")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	inv, err := storage.GetInvite(context.Background(), testPool, token)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if inv == nil {
		t.Fatal("expected invite, got nil")
	}
	if inv.Token != token {
		t.Errorf("token: got %q, want %q", inv.Token, token)
	}
	if inv.Email != "found@example.com" {
		t.Errorf("email: got %q", inv.Email)
	}
	if inv.Name != "Alice" {
		t.Errorf("name: got %q", inv.Name)
	}
	if inv.AcceptedAt != nil {
		t.Error("expected AcceptedAt nil for pending invite")
	}
}

func TestGetInvite_notFound(t *testing.T) {
	inv, err := storage.GetInvite(context.Background(), testPool, "nonexistenttoken00000000000000000000000000000000")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if inv != nil {
		t.Errorf("expected nil, got %+v", inv)
	}
}

func TestGetInvite_afterAccepted_returnsNil(t *testing.T) {
	truncateInvites(t)

	token, err := storage.CreateInvite(context.Background(), testPool, "", "accept@example.com", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := storage.MarkInviteAccepted(context.Background(), testPool, token); err != nil {
		t.Fatalf("mark accepted: %v", err)
	}

	inv, err := storage.GetInvite(context.Background(), testPool, token)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if inv != nil {
		t.Error("expected nil for accepted invite")
	}
}

func TestListPendingInvites_empty(t *testing.T) {
	truncateInvites(t)

	invites, err := storage.ListPendingInvites(context.Background(), testPool)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(invites) != 0 {
		t.Errorf("expected 0 invites, got %d", len(invites))
	}
}

func TestListPendingInvites_showsOnlyPending(t *testing.T) {
	truncateInvites(t)

	tok1, _ := storage.CreateInvite(context.Background(), testPool, "", "pending1@example.com", "")
	tok2, _ := storage.CreateInvite(context.Background(), testPool, "", "pending2@example.com", "")
	tok3, _ := storage.CreateInvite(context.Background(), testPool, "", "accepted@example.com", "")

	storage.MarkInviteAccepted(context.Background(), testPool, tok3)

	invites, err := storage.ListPendingInvites(context.Background(), testPool)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(invites) != 2 {
		t.Fatalf("expected 2 pending invites, got %d", len(invites))
	}

	emails := map[string]bool{}
	for _, inv := range invites {
		emails[inv.Email] = true
	}
	if !emails["pending1@example.com"] || !emails["pending2@example.com"] {
		t.Errorf("pending invites: got emails %v", emails)
	}
	_ = tok1
	_ = tok2
}

func TestDeleteInvite_found(t *testing.T) {
	truncateInvites(t)

	token, _ := storage.CreateInvite(context.Background(), testPool, "", "del@example.com", "")

	deleted, err := storage.DeleteInvite(context.Background(), testPool, token)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !deleted {
		t.Error("expected deleted=true")
	}

	inv, _ := storage.GetInvite(context.Background(), testPool, token)
	if inv != nil {
		t.Error("expected nil after delete")
	}
}

func TestDeleteInvite_notFound(t *testing.T) {
	deleted, err := storage.DeleteInvite(context.Background(), testPool, "nosuchtoken000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted {
		t.Error("expected deleted=false for non-existent token")
	}
}

func TestCreateInvite_withInviterID(t *testing.T) {
	truncateInvites(t)
	truncateUsers(t)

	u, err := storage.CreateUser(context.Background(), testPool, "inviter@example.com", "password1234")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	token, err := storage.CreateInvite(context.Background(), testPool, u.ID, "invitee@example.com", "Bob")
	if err != nil {
		t.Fatalf("create invite with inviter: %v", err)
	}

	inv, err := storage.GetInvite(context.Background(), testPool, token)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if inv == nil {
		t.Fatal("expected invite, got nil")
	}
	if inv.InviterID == nil || *inv.InviterID != u.ID {
		t.Errorf("inviter_id: got %v, want %q", inv.InviterID, u.ID)
	}
}
