package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

func setupIssueForComments(t *testing.T) (project *storage.Project, issueID string) {
	t.Helper()
	truncateProjects(t)

	p, err := storage.CreateProject(context.Background(), testPool, "comment-proj", "Comment Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	ts := time.Now().UTC()
	issue, _, _, err := storage.UpsertIssue(context.Background(), testPool, p.ID, "fp-comment", "comment error", "error", "error", "", "", ts)
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}
	return p, issue.ID
}

func TestCreateComment(t *testing.T) {
	truncateUsers(t)
	_, issueID := setupIssueForComments(t)

	u, err := storage.CreateUser(context.Background(), testPool, "commenter@example.com", "password1234")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	c, err := storage.CreateComment(context.Background(), testPool, issueID, u.ID, "looks like a nil pointer")
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}
	if c.ID == "" {
		t.Error("expected non-empty ID")
	}
	if c.IssueID != issueID {
		t.Errorf("issue_id: got %q, want %q", c.IssueID, issueID)
	}
	if c.UserID != u.ID {
		t.Errorf("user_id: got %q, want %q", c.UserID, u.ID)
	}
	if c.UserEmail != "commenter@example.com" {
		t.Errorf("user_email: got %q", c.UserEmail)
	}
	if c.Body != "looks like a nil pointer" {
		t.Errorf("body: got %q", c.Body)
	}
	if c.CreatedAt.IsZero() {
		t.Error("expected non-zero created_at")
	}
}

func TestListComments_empty(t *testing.T) {
	truncateUsers(t)
	_, issueID := setupIssueForComments(t)

	comments, err := storage.ListComments(context.Background(), testPool, issueID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("expected 0 comments, got %d", len(comments))
	}
}

func TestListComments_orderedAsc(t *testing.T) {
	truncateUsers(t)
	_, issueID := setupIssueForComments(t)

	u, err := storage.CreateUser(context.Background(), testPool, "ordered@example.com", "password1234")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	storage.CreateComment(context.Background(), testPool, issueID, u.ID, "first")
	time.Sleep(5 * time.Millisecond)
	storage.CreateComment(context.Background(), testPool, issueID, u.ID, "second")

	comments, err := storage.ListComments(context.Background(), testPool, issueID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}
	if comments[0].Body != "first" {
		t.Errorf("expected first comment first, got %q", comments[0].Body)
	}
	if comments[1].Body != "second" {
		t.Errorf("expected second comment second, got %q", comments[1].Body)
	}
}
