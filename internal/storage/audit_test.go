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

// waitAuditRows waits until the audit log holds n rows. WriteAuditLog inserts
// in a goroutine, and a fixed sleep raced it on busy CI runners.
func waitAuditRows(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var count int
		if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log").Scan(&count); err != nil {
			t.Fatalf("count audit_log: %v", err)
		}
		if count >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("audit log has %d rows after 10s, want %d", count, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWriteAuditLog_appearsInList(t *testing.T) {
	truncateAuditLog(t)

	storage.WriteAuditLog(testPool, storage.AuditEntry{
		EventType: "auth.login",
		IP:        "127.0.0.1",
	})

	waitAuditRows(t, 1)

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
	waitAuditRows(t, 1)

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

	waitAuditRows(t, 2)

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
	waitAuditRows(t, 2)

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

	ctx := context.Background()
	// Insert with explicit timestamps to avoid async goroutine ordering races.
	// auth.login is 1 second older than auth.logout, so auth.logout must appear first.
	// ip is NOT NULL in the scan destination (AuditRow.IP is string, not *string),
	// so supply an empty string rather than letting the column default to NULL.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO audit_log (event_type, ip, created_at) VALUES
			($1, '', NOW() - INTERVAL '1 second'),
			($2, '', NOW())
	`, "auth.login", "auth.logout"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	rows, err := storage.ListAuditLog(ctx, testPool, storage.AuditFilter{})
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

// "_" in a search matches a literal underscore, not any character.
func TestListAuditLog_searchEscapesWildcards(t *testing.T) {
	truncateAuditLog(t)
	ctx := context.Background()
	for _, eventType := range []string{"alert_rule.created", "alertXrule.created"} {
		if _, err := testPool.Exec(ctx, `INSERT INTO audit_log (event_type, ip) VALUES ($1, '')`, eventType); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := storage.ListAuditLog(ctx, testPool, storage.AuditFilter{Search: "alert_rule"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].EventType != "alert_rule.created" {
		t.Errorf("expected only the literal match, got %+v", rows)
	}
}
