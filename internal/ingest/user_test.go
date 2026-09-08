package ingest_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/ingest"
)

func TestParseSentryUser_idString(t *testing.T) {
	u := ingest.ParseSentryUser(json.RawMessage(`{"id":"u-1","username":"alice","email":"a@x.com","name":"Alice"}`))
	if u.Identity != "u-1" || u.ID != "u-1" || u.Username != "alice" || u.Email != "a@x.com" || u.Name != "Alice" {
		t.Fatalf("got %+v", u)
	}
}

func TestParseSentryUser_numericID(t *testing.T) {
	u := ingest.ParseSentryUser(json.RawMessage(`{"id":42,"username":"bob"}`))
	if u.ID != "42" || u.Identity != "42" {
		t.Fatalf("numeric id: got %+v", u)
	}
}

func TestParseSentryUser_usernameFallback(t *testing.T) {
	u := ingest.ParseSentryUser(json.RawMessage(`{"username":"carol"}`))
	if u.Identity != "carol" {
		t.Fatalf("identity: got %q", u.Identity)
	}
}

func TestParseSentryUser_empty(t *testing.T) {
	if ingest.ParseSentryUser(nil).Identity != "" {
		t.Fatal("nil user should be empty")
	}
	if ingest.ParseSentryUser(json.RawMessage(`null`)).Identity != "" {
		t.Fatal("null user should be empty")
	}
	if ingest.ParseSentryUser(json.RawMessage(`{}`)).Identity != "" {
		t.Fatal("empty object should be empty")
	}
}

func TestParseSentryUserFromPayload(t *testing.T) {
	u := ingest.ParseSentryUserFromPayload(json.RawMessage(`{"message":"x","user":{"id":"u-9"}}`))
	if u.Identity != "u-9" {
		t.Fatalf("got %+v", u)
	}
}

func TestParseSentryUserFromAttrs(t *testing.T) {
	u := ingest.ParseSentryUserFromAttrs(map[string]any{
		"user.id":       float64(7),
		"user.username": "dana",
		"user.email":    "d@x.com",
	})
	if u.ID != "7" || u.Identity != "7" || u.Username != "dana" {
		t.Fatalf("got %+v", u)
	}
}

func TestUpsertAppUsers_skipsFilteredIdentity(t *testing.T) {
	ctx := context.Background()
	ingest.UpsertAppUsers(ctx, testPool, []ingest.AppUserRow{
		{ProjectID: testProject.ID, User: ingest.SentryUser{Identity: "[Filtered]", Email: "[Filtered]"}, LastSeen: time.Now()},
	})
	var n int
	if err := testPool.QueryRow(ctx, `SELECT COUNT(*) FROM app_users WHERE project_id = $1 AND identity = '[Filtered]'`, testProject.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("filtered identity should not be stored, got %d rows", n)
	}
}

func TestUserIdentity_prefersID(t *testing.T) {
	if got := ingest.UserIdentity("id", "user", "e@x.com"); got != "id" {
		t.Fatalf("got %q", got)
	}
	if got := ingest.UserIdentity("", "user", "e@x.com"); got != "user" {
		t.Fatalf("got %q", got)
	}
	if got := ingest.UserIdentity("", "", "e@x.com"); got != "e@x.com" {
		t.Fatalf("got %q", got)
	}
	if got := ingest.UserIdentity("  ", "", ""); got != "" {
		t.Fatalf("whitespace-only should be empty, got %q", got)
	}
}
