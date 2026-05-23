package issues_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/issues"
	"github.com/blendbyte/tindra/internal/storage"
)

func truncateIssuesAndEvents(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE events CASCADE"); err != nil {
		t.Fatalf("truncate events: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), "TRUNCATE issues CASCADE"); err != nil {
		t.Fatalf("truncate issues: %v", err)
	}
}

func insertRawEvent(t *testing.T, payload json.RawMessage) string {
	t.Helper()
	var id string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO events (project_id, timestamp, payload)
		VALUES ($1, $2, $3)
		RETURNING id
	`, testProject.ID, time.Now(), payload).Scan(&id)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	return id
}

func TestGrouper_createsIssueForUngroupedEvent(t *testing.T) {
	truncateIssuesAndEvents(t)

	payload := json.RawMessage(`{"level":"error","message":"database connection failed","timestamp":"2024-01-01T00:00:00Z"}`)
	insertRawEvent(t, payload)

	g := issues.NewGrouper(testPool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	// Wait for grouper tick (500ms) + a bit of headroom
	time.Sleep(700 * time.Millisecond)

	issueList, err := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if len(issueList) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issueList))
	}
	if issueList[0].Title != "database connection failed" {
		t.Errorf("title: got %q", issueList[0].Title)
	}
	if issueList[0].EventCount != 1 {
		t.Errorf("event_count: got %d, want 1", issueList[0].EventCount)
	}
}

func TestGrouper_groupsSameFingerprint(t *testing.T) {
	truncateIssuesAndEvents(t)

	payload := json.RawMessage(`{"level":"error","message":"same error","timestamp":"2024-01-01T00:00:00Z"}`)
	insertRawEvent(t, payload)
	insertRawEvent(t, payload)

	g := issues.NewGrouper(testPool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	// Poll until both events are grouped or we time out. The grouper ticks at
	// 500ms, then needs multiple DB round-trips per event; a fixed 700ms sleep
	// is too tight on loaded CI machines running many packages in parallel.
	var issueList []*storage.Issue
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		var err error
		issueList, err = storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 50})
		if err != nil {
			t.Fatalf("list issues: %v", err)
		}
		if len(issueList) == 1 && issueList[0].EventCount == 2 {
			break
		}
	}

	if len(issueList) != 1 {
		t.Errorf("expected 1 issue for same fingerprint, got %d", len(issueList))
	}
	if issueList[0].EventCount != 2 {
		t.Errorf("event_count: got %d, want 2", issueList[0].EventCount)
	}
}

func TestGrouper_separateFingerprints(t *testing.T) {
	truncateIssuesAndEvents(t)

	insertRawEvent(t, json.RawMessage(`{"level":"error","message":"error A","timestamp":"2024-01-01T00:00:00Z"}`))
	insertRawEvent(t, json.RawMessage(`{"level":"error","message":"error B","timestamp":"2024-01-01T00:00:00Z"}`))

	g := issues.NewGrouper(testPool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	time.Sleep(700 * time.Millisecond)

	issueList, err := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if len(issueList) != 2 {
		t.Errorf("expected 2 issues for different messages, got %d", len(issueList))
	}
}

func TestGrouper_defaultsLevelToError(t *testing.T) {
	truncateIssuesAndEvents(t)

	// Payload with no "level" field - grouper should default to "error"
	payload := json.RawMessage(`{"message":"no level field","timestamp":"2024-01-01T00:00:00Z"}`)
	insertRawEvent(t, payload)

	g := issues.NewGrouper(testPool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	time.Sleep(700 * time.Millisecond)

	issueList, _ := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 50})
	if len(issueList) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issueList))
	}
	if issueList[0].Level != "error" {
		t.Errorf("expected level=error when payload has no level, got %q", issueList[0].Level)
	}
}

func TestGrouper_setsEventIssueID(t *testing.T) {
	truncateIssuesAndEvents(t)

	payload := json.RawMessage(`{"level":"error","message":"link test","timestamp":"2024-01-01T00:00:00Z"}`)
	eventID := insertRawEvent(t, payload)

	g := issues.NewGrouper(testPool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	time.Sleep(700 * time.Millisecond)

	var issueID interface{}
	err := testPool.QueryRow(context.Background(),
		"SELECT issue_id FROM events WHERE id = $1", eventID,
	).Scan(&issueID)
	if err != nil {
		t.Fatalf("query event: %v", err)
	}
	if issueID == nil {
		t.Error("expected issue_id to be set on event after grouping")
	}
}

func TestGrouper_storesImplicitTags(t *testing.T) {
	truncateIssuesAndEvents(t)

	payload := json.RawMessage(`{
		"level": "warning",
		"environment": "staging",
		"message": "implicit tag test",
		"timestamp": "2024-01-01T00:00:00Z",
		"server_name": "web-01",
		"contexts": {
			"browser": {"name": "Chrome", "version": "120.0"},
			"runtime": {"name": "php", "version": "8.2.0"}
		},
		"exception": {
			"values": [{"mechanism": {"type": "generic", "handled": false}}]
		}
	}`)
	insertRawEvent(t, payload)

	g := issues.NewGrouper(testPool)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)
	time.Sleep(700 * time.Millisecond)

	issueList, err := storage.ListIssues(context.Background(), testPool, testProject.ID, storage.IssueFilter{Limit: 50})
	if err != nil || len(issueList) == 0 {
		t.Fatalf("expected 1 issue after grouping, got %d (err: %v)", len(issueList), err)
	}

	tags, err := storage.GetIssueTags(context.Background(), testPool, issueList[0].ID)
	if err != nil {
		t.Fatalf("get issue tags: %v", err)
	}

	tagMap := make(map[string]string)
	for _, ts := range tags {
		if len(ts.Values) > 0 {
			tagMap[ts.Key] = ts.Values[0].Value
		}
	}

	want := map[string]string{
		"level":           "warning",
		"environment":     "staging",
		"server_name":     "web-01",
		"browser":         "Chrome 120.0",
		"browser.name":    "Chrome",
		"browser.version": "120.0",
		"runtime.name":    "php",
		"runtime.version": "8.2.0",
		"mechanism":       "generic",
		"handled":         "no",
	}
	for k, v := range want {
		if got := tagMap[k]; got != v {
			t.Errorf("tag %q: got %q, want %q", k, got, v)
		}
	}
}
