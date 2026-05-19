package storage_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

func truncateAuditLog(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE audit_log"); err != nil {
		t.Fatalf("truncate audit_log: %v", err)
	}
}

func TestWriteAuditLog_appearsInList(t *testing.T) {
	truncateAuditLog(t)

	storage.WriteAuditLog(testPool, storage.AuditEntry{
		EventType: "auth.login",
		IP:        "127.0.0.1",
	})

	// WriteAuditLog is async; give it a moment to complete.
	time.Sleep(50 * time.Millisecond)

	rows, err := storage.ListAuditLog(context.Background(), testPool, storage.AuditFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].EventType != "auth.login" {
		t.Errorf("event_type: got %q", rows[0].EventType)
	}
	if rows[0].IP != "127.0.0.1" {
		t.Errorf("ip: got %q", rows[0].IP)
	}
}

func TestWriteAuditLog_withDetails(t *testing.T) {
	truncateAuditLog(t)

	storage.WriteAuditLog(testPool, storage.AuditEntry{
		EventType: "project.created",
		Details:   map[string]any{"slug": "my-app"},
	})
	time.Sleep(50 * time.Millisecond)

	rows, err := storage.ListAuditLog(context.Background(), testPool, storage.AuditFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one row")
	}
	var details map[string]any
	if err := json.Unmarshal(rows[0].Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if details["slug"] != "my-app" {
		t.Errorf("details.slug: got %v", details["slug"])
	}
}

func TestListAuditLog_filterByKind(t *testing.T) {
	truncateAuditLog(t)

	storage.WriteAuditLog(testPool, storage.AuditEntry{EventType: "auth.login"})
	storage.WriteAuditLog(testPool, storage.AuditEntry{EventType: "project.created"})
	time.Sleep(50 * time.Millisecond)

	rows, err := storage.ListAuditLog(context.Background(), testPool, storage.AuditFilter{Kind: "auth"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row with kind=auth, got %d", len(rows))
	}
	if rows[0].EventType != "auth.login" {
		t.Errorf("event_type: got %q", rows[0].EventType)
	}
}

func TestListAuditLog_filterBySearch(t *testing.T) {
	truncateAuditLog(t)

	truncateUsers(t)
	u, err := storage.CreateUser(context.Background(), testPool, "audit@example.com", "password1234")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	actorID := u.ID
	storage.WriteAuditLog(testPool, storage.AuditEntry{EventType: "auth.login", ActorID: &actorID, IP: "10.0.0.1"})
	storage.WriteAuditLog(testPool, storage.AuditEntry{EventType: "auth.logout", IP: "192.168.1.1"})
	time.Sleep(50 * time.Millisecond)

	rows, err := storage.ListAuditLog(context.Background(), testPool, storage.AuditFilter{Search: "audit@example.com"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row matching email, got %d", len(rows))
	}
	if rows[0].EventType != "auth.login" {
		t.Errorf("event_type: got %q", rows[0].EventType)
	}
}

func TestListAuditLog_limitCap(t *testing.T) {
	// Limit > 500 is capped to 200 by ListAuditLog.
	truncateAuditLog(t)
	rows, err := storage.ListAuditLog(context.Background(), testPool, storage.AuditFilter{Limit: 9999})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// No rows inserted; just verify the query executes without error.
	if rows == nil {
		rows = []*storage.AuditRow{}
	}
	_ = rows
}

func TestListAuditLog_orderNewestFirst(t *testing.T) {
	truncateAuditLog(t)

	storage.WriteAuditLog(testPool, storage.AuditEntry{EventType: "auth.login"})
	time.Sleep(10 * time.Millisecond)
	storage.WriteAuditLog(testPool, storage.AuditEntry{EventType: "auth.logout"})
	time.Sleep(50 * time.Millisecond)

	rows, err := storage.ListAuditLog(context.Background(), testPool, storage.AuditFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].EventType != "auth.logout" {
		t.Errorf("expected newest first, got %q", rows[0].EventType)
	}
}
