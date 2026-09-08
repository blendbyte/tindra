package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func TestListAppUsers_empty(t *testing.T) {
	p := setupProjectForTxns(t)
	users, err := storage.ListAppUsers(context.Background(), testPool, []string{p.ID}, "", 20)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("expected 0, got %d", len(users))
	}
}

func TestListAppUsers_andGet(t *testing.T) {
	p := setupProjectForTxns(t)
	ctx := context.Background()
	ingest.UpsertAppUsers(ctx, testPool, []ingest.AppUserRow{
		{ProjectID: p.ID, User: ingest.SentryUser{Identity: "u-1", ID: "u-1", Username: "alice", Name: "Alice"}, LastSeen: time.Now()},
		{ProjectID: p.ID, User: ingest.SentryUser{Identity: "u-2", ID: "u-2", Email: "bob@example.com"}, LastSeen: time.Now().Add(-time.Hour)},
	})

	all, err := storage.ListAppUsers(ctx, testPool, []string{p.ID}, "", 20)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}
	if all[0].Identity != "u-1" {
		t.Errorf("expected most recent first, got %q", all[0].Identity)
	}

	found, err := storage.ListAppUsers(ctx, testPool, []string{p.ID}, "alice", 20)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(found) != 1 || found[0].Identity != "u-1" {
		t.Fatalf("search alice: got %+v", found)
	}

	got, err := storage.GetAppUser(ctx, testPool, []string{p.ID}, "u-2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil || got.Identity != "u-2" {
		t.Fatalf("get: %+v", got)
	}
	missing, err := storage.GetAppUser(ctx, testPool, []string{p.ID}, "nope")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if missing != nil {
		t.Fatalf("expected nil, got %+v", missing)
	}
}

func TestListAllIssues_userIdentity(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()
	issA, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-user-a", "A", "error", "error", "", "", time.Now())
	issB, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-user-b", "B", "error", "error", "", "", time.Now())
	insertEventWithPayload(t, project.ID, issA.ID, `{"level":"error","user":{"id":"u-1"}}`)
	insertEventWithPayload(t, project.ID, issB.ID, `{"level":"error","user":{"id":"u-2"}}`)

	got, err := storage.ListAllIssues(ctx, testPool, storage.IssueFilter{
		ProjectIDs:   []string{project.ID},
		UserIdentity: "u-1",
		Limit:        50,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].ID != issA.ID {
		t.Fatalf("expected issue A, got %+v", got)
	}

	n, err := storage.CountAllIssues(ctx, testPool, storage.IssueFilter{
		ProjectIDs:   []string{project.ID},
		UserIdentity: "u-1",
	})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("count: got %d, want 1", n)
	}
}

func TestListLogs_userIdentity(t *testing.T) {
	p := setupProjectForLogs(t)
	ts := time.Now().UTC()
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO logs (project_id, timestamp, received_at, level, body, attributes)
		VALUES ($1, $2, $2, 'info', 'hello alice', '{"user.id":"u-1"}'::jsonb),
		       ($1, $2, $2, 'info', 'hello bob', '{"user.id":"u-2"}'::jsonb)
	`, p.ID, ts)
	if err != nil {
		t.Fatalf("insert logs: %v", err)
	}

	logs, _, err := storage.ListLogs(context.Background(), testPool, storage.LogFilter{
		ProjectIDs:   []string{p.ID},
		UserIdentity: "u-1",
		Limit:        50,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(logs) != 1 || logs[0].Body != "hello alice" {
		t.Fatalf("got %+v", logs)
	}
}

func TestListAllTransactions_userIdentity(t *testing.T) {
	p := setupProjectForTxns(t)
	now := time.Now().UTC()
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO transactions
			(project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, user_identity, user_id)
		VALUES
			($1, '/api/alice', 'http.server', 'ok', 10, $2, $2, 'u-1', 'u-1'),
			($1, '/api/bob', 'http.server', 'ok', 20, $2, $2, 'u-2', 'u-2')
	`, p.ID, now)
	if err != nil {
		t.Fatalf("insert tx: %v", err)
	}

	txns, err := storage.ListAllTransactions(context.Background(), testPool, storage.TransactionFilter{
		ProjectIDs:   []string{p.ID},
		UserIdentity: "u-1",
		Limit:        50,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(txns) != 1 || txns[0].Transaction != "/api/alice" {
		t.Fatalf("got %+v", txns)
	}
}
