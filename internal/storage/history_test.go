package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

func setupIssueForHistory(t *testing.T) (project *storage.Project, issueID string) {
	t.Helper()
	truncateProjects(t)

	p, err := storage.CreateProject(context.Background(), testPool, "hist-proj", "History Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	ts := time.Now().UTC()
	issue, _, _, err := storage.UpsertIssue(context.Background(), testPool, p.ID, "fp-history", "history error", "error", "error", "", "", ts)
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}
	return p, issue.ID
}

func TestInsertIssueHistory(t *testing.T) {
	_, issueID := setupIssueForHistory(t)

	err := storage.InsertIssueHistory(context.Background(), testPool, storage.IssueHistoryEntry{
		IssueID:   issueID,
		EventType: "status_changed",
		Details:   map[string]any{"from": "open", "to": "resolved"},
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func TestGetIssueHistory_empty(t *testing.T) {
	_, issueID := setupIssueForHistory(t)

	entries, err := storage.GetIssueHistory(context.Background(), testPool, issueID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestGetIssueHistory_returnsEntries(t *testing.T) {
	_, issueID := setupIssueForHistory(t)

	now := time.Now()
	storage.InsertIssueHistory(context.Background(), testPool, storage.IssueHistoryEntry{
		IssueID:   issueID,
		EventType: "status_changed",
		Details:   map[string]any{"from": "open", "to": "resolved"},
		CreatedAt: now,
	})
	storage.InsertIssueHistory(context.Background(), testPool, storage.IssueHistoryEntry{
		IssueID:   issueID,
		EventType: "status_changed",
		Details:   map[string]any{"from": "resolved", "to": "open"},
		CreatedAt: now.Add(time.Second),
	})

	entries, err := storage.GetIssueHistory(context.Background(), testPool, issueID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].EventType != "status_changed" {
		t.Errorf("event_type: got %q", entries[0].EventType)
	}
	if entries[0].IssueID != issueID {
		t.Errorf("issue_id: got %q, want %q", entries[0].IssueID, issueID)
	}
}

func TestGetIssueHistory_orderedAsc(t *testing.T) {
	_, issueID := setupIssueForHistory(t)

	base := time.Now()
	storage.InsertIssueHistory(context.Background(), testPool, storage.IssueHistoryEntry{
		IssueID: issueID, EventType: "first", CreatedAt: base,
	})
	storage.InsertIssueHistory(context.Background(), testPool, storage.IssueHistoryEntry{
		IssueID: issueID, EventType: "second", CreatedAt: base.Add(time.Second),
	})

	entries, err := storage.GetIssueHistory(context.Background(), testPool, issueID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries, got %d", len(entries))
	}
	if entries[0].EventType != "first" {
		t.Errorf("expected oldest first, got %q", entries[0].EventType)
	}
}

func TestInsertIssueHistory_nilDetailsDefaultsToEmpty(t *testing.T) {
	_, issueID := setupIssueForHistory(t)

	err := storage.InsertIssueHistory(context.Background(), testPool, storage.IssueHistoryEntry{
		IssueID:   issueID,
		EventType: "created",
		Details:   nil,
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("insert with nil details: %v", err)
	}

	entries, err := storage.GetIssueHistory(context.Background(), testPool, issueID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}
	if entries[0].Details == nil {
		t.Error("expected non-nil details map even when inserted nil")
	}
}
