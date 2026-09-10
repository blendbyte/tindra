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

func TestGetTopFrames_withFrames(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	issue, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-frames", "Error", "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	payload := json.RawMessage(`{
		"exception": {"values": [{"stacktrace": {"frames": [
			{"function": "main", "filename": "main.go", "lineno": 10, "in_app": false},
			{"function": "handler", "filename": "handler.go", "lineno": 42, "in_app": true},
			{"function": "process", "filename": "process.go", "lineno": 7, "in_app": true}
		]}}]}
	}`)
	testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, NOW(), NOW(), $2, 'fp-frames', $3)
	`, project.ID, payload, issue.ID)

	frames := storage.GetTopFrames(ctx, testPool, issue.ID, 5)
	if len(frames) == 0 {
		t.Error("expected non-empty frames")
	}
	// In-app frames preferred, top of call stack first
	if frames[0] != "process  process.go:7" {
		t.Errorf("expected top in-app frame first, got %q", frames[0])
	}
}

func TestGetTopFrames_noEvent(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	issue, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-noframes", "Error", "error", "error", "", "", time.Now())
	frames := storage.GetTopFrames(ctx, testPool, issue.ID, 5)
	if frames != nil {
		t.Errorf("expected nil for issue with no events, got %v", frames)
	}
}

func TestGetAlertEventData_withEvent(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	issue, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-alertdata", "Error", "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	payload := json.RawMessage(`{
		"message": "something went wrong",
		"request": {"url": "https://example.com/api", "method": "POST"},
		"exception": {"values": [{"stacktrace": {"frames": [
			{"function": "handler", "filename": "handler.go", "lineno": 10, "in_app": true}
		]}}]}
	}`)
	testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, NOW(), NOW(), $2, 'fp-alertdata', $3)
	`, project.ID, payload, issue.ID)

	data := storage.GetAlertEventData(ctx, testPool, issue.ID, 3)
	if data.Message != "something went wrong" {
		t.Errorf("message: got %q", data.Message)
	}
	if data.RequestURL != "https://example.com/api" {
		t.Errorf("request_url: got %q", data.RequestURL)
	}
	if data.RequestMethod != "POST" {
		t.Errorf("request_method: got %q", data.RequestMethod)
	}
	if data.OccurredAt == nil {
		t.Error("expected non-nil OccurredAt")
	}
}

func TestGetAlertEventData_noEvent(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	issue, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-noalertdata", "Error", "error", "error", "", "", time.Now())
	data := storage.GetAlertEventData(ctx, testPool, issue.ID, 3)
	if data.OccurredAt != nil {
		t.Error("expected nil OccurredAt for issue with no event")
	}
}

func TestListEventsForIssue(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	issue, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-list-ev", "Error", "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	payload := json.RawMessage(`{"level":"error"}`)
	for i := range 3 {
		testPool.Exec(ctx, `
			INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
			VALUES ($1, NOW(), NOW() - ($2 * interval '1 second'), $3, 'fp-list-ev', $4)
		`, project.ID, i, payload, issue.ID)
	}

	events, hasMore, err := storage.ListEventsForIssue(ctx, testPool, issue.ID, nil, nil, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("expected 3 events, got %d", len(events))
	}
	if hasMore {
		t.Error("expected hasMore=false")
	}
}

func TestListEventsForIssue_pagination(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	issue, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-evpage", "Error", "error", "error", "", "", time.Now())
	payload := json.RawMessage(`{"level":"error"}`)
	for i := range 5 {
		testPool.Exec(ctx, `
			INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
			VALUES ($1, NOW(), NOW() - ($2 * interval '1 second'), $3, 'fp-evpage', $4)
		`, project.ID, i, payload, issue.ID)
	}

	events, hasMore, err := storage.ListEventsForIssue(ctx, testPool, issue.ID, nil, nil, 3)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("expected 3 events (limit), got %d", len(events))
	}
	if !hasMore {
		t.Error("expected hasMore=true")
	}

	// Use cursor from last event
	lastTime := events[len(events)-1].ReceivedAt
	lastID := events[len(events)-1].ID
	page2, _, err := storage.ListEventsForIssue(ctx, testPool, issue.ID, &lastTime, &lastID, 3)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) == 0 {
		t.Error("expected non-empty page 2")
	}
}

func TestEventNavigationUsesReceiptTimeAndUUIDTies(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()
	issue, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "receipt-order", "Receipt order", "error", "error", "", "", time.Now())
	require.NoError(t, err)
	ids := []string{
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000002",
		"00000000-0000-0000-0000-000000000003",
	}
	for i, id := range ids {
		_, err := testPool.Exec(ctx, `INSERT INTO events(id, project_id, issue_id, timestamp, received_at, payload)
		 VALUES ($1, $2, $3, NOW() - ($4 * INTERVAL '1 day'), '2026-01-01', '{"level":"error"}')`, id, project.ID, issue.ID, i)
		require.NoError(t, err)
		require.NoError(t, storage.InsertEventTags(ctx, testPool, id, issue.ID, project.ID, [][2]string{{"event", id}}))
	}
	page, more, err := storage.ListEventsForIssue(ctx, testPool, issue.ID, nil, nil, 2)
	require.NoError(t, err)
	require.True(t, more)
	require.Len(t, page, 2)
	require.Equal(t, ids[2], page[0].ID)
	require.Equal(t, ids[1], page[1].ID)
	for i, e := range page {
		require.Equal(t, map[string]string{"event": e.ID}, e.Tags)
		detail, err := storage.GetEventForIssueAtOffset(ctx, testPool, issue.ID, i)
		require.NoError(t, err)
		require.Equal(t, e.ID, detail.ID)
	}
	latest, err := storage.GetLatestEventForIssue(ctx, testPool, issue.ID)
	require.NoError(t, err)
	require.Equal(t, page[0].ID, latest.ID)
	last := page[1]
	page, more, err = storage.ListEventsForIssue(ctx, testPool, issue.ID, &last.ReceivedAt, &last.ID, 2)
	require.NoError(t, err)
	require.False(t, more)
	require.Len(t, page, 1)
	require.Equal(t, ids[0], page[0].ID)
	require.Equal(t, ids[0], page[0].Tags["event"])
	last = page[0]
	page, more, err = storage.ListEventsForIssue(ctx, testPool, issue.ID, &last.ReceivedAt, &last.ID, 2)
	require.NoError(t, err)
	require.Empty(t, page)
	require.False(t, more)
}

func TestGetIssueHistogram(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	firstSeen := time.Now().Add(-2 * time.Hour)
	issue, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-hist", "Error", "error", "error", "", "", firstSeen)
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	payload := json.RawMessage(`{"level":"error"}`)
	testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, NOW(), NOW(), $2, 'fp-hist', $3)
	`, project.ID, payload, issue.ID)

	result, err := storage.GetIssueHistogram(ctx, testPool, issue.ID, firstSeen)
	if err != nil {
		t.Fatalf("histogram: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.BucketSize != "hour" {
		t.Errorf("bucket_size: got %q, want %q", result.BucketSize, "hour")
	}
	if len(result.Buckets) == 0 {
		t.Error("expected non-empty buckets")
	}

	total := int64(0)
	for _, b := range result.Buckets {
		total += b.Count
	}
	if total < 1 {
		t.Error("expected at least one event in histogram")
	}
}

func TestGetIssueHistogram_dayBuckets(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	firstSeen := time.Now().Add(-10 * 24 * time.Hour)
	issue, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-histday", "Error", "error", "error", "", "", firstSeen)

	result, err := storage.GetIssueHistogram(ctx, testPool, issue.ID, firstSeen)
	if err != nil {
		t.Fatalf("histogram: %v", err)
	}
	if result.BucketSize != "day" {
		t.Errorf("bucket_size: got %q, want day", result.BucketSize)
	}
}

func TestGetIssueHistogram_weekBuckets(t *testing.T) {
	project, _ := setupProjectAndEvent(t)
	ctx := context.Background()

	// >60 days old triggers week-bucket granularity — exercises truncateBucket("week") and advanceBucket("week")
	firstSeen := time.Now().Add(-90 * 24 * time.Hour)
	issue, _, _, _ := storage.UpsertIssue(ctx, testPool, project.ID, "fp-histweek", "Error", "error", "error", "", "", firstSeen)

	result, err := storage.GetIssueHistogram(ctx, testPool, issue.ID, firstSeen)
	if err != nil {
		t.Fatalf("histogram: %v", err)
	}
	if result.BucketSize != "week" {
		t.Errorf("bucket_size: got %q, want week", result.BucketSize)
	}
	if len(result.Buckets) == 0 {
		t.Error("expected at least one week bucket")
	}
}

func TestGetEventForIssueByIDPreservesExactScopedEvent(t *testing.T) {
	ctx := t.Context()
	project, _ := setupProjectAndEvent(t)
	issue, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "exact-event", "Error", "error", "error", "", "", time.Now())
	require.NoError(t, err)
	other, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "other-exact-event", "Other", "error", "error", "", "", time.Now())
	require.NoError(t, err)
	var original, newer string
	require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO events(project_id,issue_id,timestamp,received_at,trace_id,payload) VALUES($1,$2,now(),now()-interval '1 minute','trace-original','{"message":"original","release":"v1"}') RETURNING id`, project.ID, issue.ID).Scan(&original))
	require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO events(project_id,issue_id,timestamp,payload) VALUES($1,$2,now(),'{}') RETURNING id`, project.ID, issue.ID).Scan(&newer))
	event, err := storage.GetEventForIssueByID(ctx, testPool, issue.ID, original)
	require.NoError(t, err)
	require.NotNil(t, event)
	require.Equal(t, original, event.ID)
	require.NotEqual(t, newer, event.ID)
	require.Equal(t, "v1", *event.Release)
	require.Equal(t, "trace-original", *event.TraceID)
	require.JSONEq(t, `{"message":"original","release":"v1"}`, string(event.Payload))
	event, err = storage.GetEventForIssueByID(ctx, testPool, other.ID, original)
	require.NoError(t, err)
	require.Nil(t, event, "an event from another issue must not be returned")
	_, err = testPool.Exec(ctx, `DELETE FROM events WHERE id=$1`, original)
	require.NoError(t, err)
	event, err = storage.GetEventForIssueByID(ctx, testPool, issue.ID, original)
	require.NoError(t, err)
	require.Nil(t, event, "a removed event must not fall back to the latest event")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = storage.GetEventForIssueByID(cancelled, testPool, issue.ID, newer)
	require.ErrorIs(t, err, context.Canceled)
}
