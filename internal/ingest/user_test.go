package ingest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
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

func TestParseSentryUser_invalidAndFallbacks(t *testing.T) {
	for _, tc := range []struct{ name, raw, identity string }{
		{"invalid JSON", `{"id":`, ""},
		{"wrong shape", `[]`, ""},
		{"invalid username", `{"id":"valid","username":42}`, ""},
		{"null ID", `{"id":null,"email":" alice@example.com "}`, "alice@example.com"},
		{"empty ID", `{"id":"","username":" alice "}`, "alice"},
		{"numeric zero", `{"id":0}`, "0"},
		{"scrubbed ID", `{"id":"[Filtered]","username":"alice"}`, "alice"},
		{"all scrubbed", `{"id":"[Filtered]","username":"[Filtered]","email":"[Filtered]"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.identity, ingest.ParseSentryUser(json.RawMessage(tc.raw)).Identity)
		})
	}
	require.Equal(t, ingest.SentryUser{}, ingest.ParseSentryUserFromPayload(json.RawMessage(`{"user":`)))
	require.Equal(t, ingest.SentryUser{}, ingest.ParseSentryUserFromPayload(json.RawMessage(`{"message":"anonymous"}`)))
}

func TestParseSentryUserFromAttrs_scalarValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"integer", float64(7), "7"}, {"decimal", float64(7.25), "7.25"},
		{"json number", json.Number("9007199254740993"), "9007199254740993"},
		{"true", true, "true"}, {"false", false, "false"},
		{"null", nil, "fallback"}, {"unsupported object", map[string]any{"id": "nested"}, "fallback"},
		{"unsupported array", []any{"nested"}, "fallback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := ingest.ParseSentryUserFromAttrs(map[string]any{"user.id": tc.value, "user.username": "fallback"})
			require.Equal(t, tc.want, u.Identity)
		})
	}
	require.Equal(t, ingest.SentryUser{}, ingest.ParseSentryUserFromAttrs(nil))
}

func TestUpsertAppUsers_mergesBatchInEitherOrder(t *testing.T) {
	ctx := context.Background()
	for _, newestFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("newestFirst=%t", newestFirst), func(t *testing.T) {
			identity := fmt.Sprintf("merge-%t", newestFirst)
			now := time.Now().UTC().Truncate(time.Microsecond)
			older := ingest.AppUserRow{ProjectID: testProject.ID, User: ingest.SentryUser{Identity: identity, ID: identity, Username: "alice", Email: "old@example.com"}, LastSeen: now.Add(-time.Hour)}
			newer := ingest.AppUserRow{ProjectID: testProject.ID, User: ingest.SentryUser{Identity: identity, Email: "new@example.com", Name: "Alice"}, LastSeen: now}
			rows := []ingest.AppUserRow{older, newer}
			if newestFirst {
				rows = []ingest.AppUserRow{newer, older}
			}
			ingest.UpsertAppUsers(ctx, testPool, rows)
			u, err := storage.GetAppUser(ctx, testPool, []string{testProject.ID}, identity)
			require.NoError(t, err)
			require.NotNil(t, u)
			require.Equal(t, identity, *u.UserID)
			require.Equal(t, "alice", *u.Username)
			require.Equal(t, "new@example.com", *u.Email)
			require.Equal(t, "Alice", *u.Name)
			require.WithinDuration(t, now, u.LastSeen, time.Microsecond)
		})
	}
}

func TestUpsertAppUsers_ignoresEmptyRowsAndDefaultsTimestamp(t *testing.T) {
	ctx := context.Background()
	// Invalid rows must be skipped without touching even a missing pool.
	ingest.UpsertAppUsers(ctx, nil, nil)
	ingest.UpsertAppUsers(ctx, nil, []ingest.AppUserRow{
		{ProjectID: testProject.ID},
		{User: ingest.SentryUser{Identity: "missing-project"}},
		{ProjectID: testProject.ID, User: ingest.SentryUser{Identity: "[Filtered]"}},
	})
	before := time.Now().UTC()
	ingest.UpsertAppUsers(ctx, testPool, []ingest.AppUserRow{{ProjectID: testProject.ID, User: ingest.SentryUser{Identity: "zero-time", ID: "zero-time"}}})
	u, err := storage.GetAppUser(ctx, testPool, []string{testProject.ID}, "zero-time")
	require.NoError(t, err)
	require.NotNil(t, u)
	require.False(t, u.LastSeen.Before(before))
	require.False(t, u.LastSeen.After(time.Now().UTC()))
}

func TestUpsertAppUsers_cancelledBatchDoesNotPersist(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ingest.UpsertAppUsers(ctx, testPool, []ingest.AppUserRow{{ProjectID: testProject.ID, User: ingest.SentryUser{Identity: "cancelled-user", ID: "cancelled-user"}}})
	u, err := storage.GetAppUser(context.Background(), testPool, []string{testProject.ID}, "cancelled-user")
	require.NoError(t, err)
	require.Nil(t, u)
}
