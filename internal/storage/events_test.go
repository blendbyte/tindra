package storage_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestGetLatestEventForIssue_found(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	issue, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-ev", "Error", "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	// Insert two events with different received_at times
	payload := json.RawMessage(`{"level":"error","message":"latest"}`)
	var ev1ID, ev2ID string
	testPool.QueryRow(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, NOW(), NOW() - interval '1 minute', $2, 'fp-ev', $3)
		RETURNING id
	`, project.ID, payload, issue.ID).Scan(&ev1ID)

	testPool.QueryRow(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, NOW(), NOW(), $2, 'fp-ev', $3)
		RETURNING id
	`, project.ID, payload, issue.ID).Scan(&ev2ID)

	ev, err := storage.GetLatestEventForIssue(ctx, testPool, issue.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.NotNil(t, ev)
	// Should return the most recently received event
	if ev.ID != ev2ID {
		t.Errorf("expected newest event %q, got %q", ev2ID, ev.ID)
	}
}

func TestGetLatestEventForIssue_noEvents(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	issue, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-noev", "Error", "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	ev, err := storage.GetLatestEventForIssue(ctx, testPool, issue.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev != nil {
		t.Errorf("expected nil for issue with no events, got %+v", ev)
	}
}
