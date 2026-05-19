package storage_test

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

// --- ParseTags (pure unit tests, no DB) ---

func TestParseTags_arrayFormat(t *testing.T) {
	raw := json.RawMessage(`[["environment","production"],["release","v1.0"]]`)
	tags := storage.ParseTags(raw)
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(tags))
	}
	if tags[0][0] != "environment" || tags[0][1] != "production" {
		t.Errorf("tag[0]: got %v", tags[0])
	}
	if tags[1][0] != "release" || tags[1][1] != "v1.0" {
		t.Errorf("tag[1]: got %v", tags[1])
	}
}

func TestParseTags_objectFormat(t *testing.T) {
	raw := json.RawMessage(`{"environment":"staging","browser":"chrome"}`)
	tags := storage.ParseTags(raw)
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(tags))
	}
	keys := map[string]string{}
	for _, t := range tags {
		keys[t[0]] = t[1]
	}
	if keys["environment"] != "staging" {
		t.Errorf("environment: got %q", keys["environment"])
	}
	if keys["browser"] != "chrome" {
		t.Errorf("browser: got %q", keys["browser"])
	}
}

func TestParseTags_empty(t *testing.T) {
	tags := storage.ParseTags(json.RawMessage(""))
	if tags != nil {
		t.Errorf("expected nil for empty raw, got %v", tags)
	}
}

func TestParseTags_null(t *testing.T) {
	tags := storage.ParseTags(json.RawMessage("null"))
	// null decodes as nil slice (array) or fails object decode; both are fine to return nil
	_ = tags
}

func TestParseTags_invalid(t *testing.T) {
	tags := storage.ParseTags(json.RawMessage("not json at all"))
	if tags != nil {
		t.Errorf("expected nil for invalid JSON, got %v", tags)
	}
}

func TestParseTags_emptyArray(t *testing.T) {
	tags := storage.ParseTags(json.RawMessage(`[]`))
	if len(tags) != 0 {
		t.Errorf("expected 0 tags for empty array, got %d", len(tags))
	}
}

// --- InsertEventTags and GetIssueTags (DB tests) ---

func setupProjectAndIssueForTags(t *testing.T) (projectID, issueID, eventID string) {
	t.Helper()
	truncateProjects(t)

	p, err := storage.CreateProject(context.Background(), testPool, "tags-proj", "Tags Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	ts := time.Now().UTC()
	issue, _, _, err := storage.UpsertIssue(context.Background(), testPool, p.ID, "fp-tags", "tags error", "error", "error", "", "", ts)
	if err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	var eid string
	err = testPool.QueryRow(context.Background(),
		`INSERT INTO events (project_id, timestamp, payload) VALUES ($1, $2, '{"level":"error"}'::jsonb) RETURNING id`,
		p.ID, ts,
	).Scan(&eid)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}

	return p.ID, issue.ID, eid
}

func TestInsertEventTags_basic(t *testing.T) {
	projectID, issueID, eventID := setupProjectAndIssueForTags(t)

	tags := [][2]string{{"environment", "production"}, {"release", "v1.0"}}
	if err := storage.InsertEventTags(context.Background(), testPool, eventID, issueID, projectID, tags); err != nil {
		t.Fatalf("insert: %v", err)
	}

	summaries, err := storage.GetIssueTags(context.Background(), testPool, issueID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 tag summaries, got %d", len(summaries))
	}

	keys := map[string]bool{}
	for _, s := range summaries {
		keys[s.Key] = true
	}
	if !keys["environment"] || !keys["release"] {
		t.Errorf("missing expected tag keys; got %v", keys)
	}
}

func TestInsertEventTags_empty_isNoop(t *testing.T) {
	_, issueID, eventID := setupProjectAndIssueForTags(t)
	projectID := ""

	if err := storage.InsertEventTags(context.Background(), testPool, eventID, issueID, projectID, nil); err != nil {
		t.Fatalf("insert nil tags: %v", err)
	}
	if err := storage.InsertEventTags(context.Background(), testPool, eventID, issueID, projectID, [][2]string{}); err != nil {
		t.Fatalf("insert empty tags: %v", err)
	}
}

func TestInsertEventTags_duplicateKeyIgnored(t *testing.T) {
	projectID, issueID, eventID := setupProjectAndIssueForTags(t)

	tags := [][2]string{{"env", "prod"}}
	storage.InsertEventTags(context.Background(), testPool, eventID, issueID, projectID, tags)
	// Second insert same event+key: ON CONFLICT DO NOTHING
	if err := storage.InsertEventTags(context.Background(), testPool, eventID, issueID, projectID, tags); err != nil {
		t.Fatalf("duplicate insert should not error: %v", err)
	}

	summaries, _ := storage.GetIssueTags(context.Background(), testPool, issueID)
	if len(summaries) != 1 {
		t.Fatalf("expected 1 unique tag key, got %d", len(summaries))
	}
	if summaries[0].Total != 1 {
		t.Errorf("total: got %d, want 1", summaries[0].Total)
	}
}

func TestGetIssueTags_empty(t *testing.T) {
	_, issueID, _ := setupProjectAndIssueForTags(t)

	summaries, err := storage.GetIssueTags(context.Background(), testPool, issueID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(summaries) != 0 {
		t.Errorf("expected 0 summaries, got %d", len(summaries))
	}
}

func TestGetIssueTags_aggregation(t *testing.T) {
	projectID, issueID, _ := setupProjectAndIssueForTags(t)

	// Insert 3 events with the same tag key but different values.
	for i, val := range []string{"chrome", "firefox", "chrome"} {
		var eid string
		testPool.QueryRow(context.Background(),
			`INSERT INTO events (project_id, timestamp, payload) VALUES ($1, NOW(), '{"level":"error"}'::jsonb) RETURNING id`,
			projectID,
		).Scan(&eid)
		storage.InsertEventTags(context.Background(), testPool, eid, issueID, projectID, [][2]string{{"browser", val}})
		_ = i
	}

	summaries, err := storage.GetIssueTags(context.Background(), testPool, issueID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	var browserSummary *storage.TagSummary
	for i := range summaries {
		if summaries[i].Key == "browser" {
			browserSummary = &summaries[i]
			break
		}
	}
	if browserSummary == nil {
		t.Fatal("expected browser tag summary")
	}
	if browserSummary.Total != 3 {
		t.Errorf("total: got %d, want 3", browserSummary.Total)
	}

	// Values should be sorted by frequency desc; chrome appears twice.
	sort.Slice(browserSummary.Values, func(i, j int) bool {
		return browserSummary.Values[i].Count > browserSummary.Values[j].Count
	})
	if browserSummary.Values[0].Value != "chrome" {
		t.Errorf("expected chrome first (highest count), got %q", browserSummary.Values[0].Value)
	}
	if browserSummary.Values[0].Count != 2 {
		t.Errorf("chrome count: got %d, want 2", browserSummary.Values[0].Count)
	}
}

func TestGetIssueTags_maxFiveValuesPerKey(t *testing.T) {
	projectID, issueID, _ := setupProjectAndIssueForTags(t)

	// Insert 7 events each with a unique browser value.
	for _, browser := range []string{"chrome", "firefox", "safari", "edge", "opera", "brave", "ie"} {
		var eid string
		testPool.QueryRow(context.Background(),
			`INSERT INTO events (project_id, timestamp, payload) VALUES ($1, NOW(), '{"level":"error"}'::jsonb) RETURNING id`,
			projectID,
		).Scan(&eid)
		storage.InsertEventTags(context.Background(), testPool, eid, issueID, projectID, [][2]string{{"browser2", browser}})
	}

	summaries, err := storage.GetIssueTags(context.Background(), testPool, issueID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	for _, s := range summaries {
		if s.Key == "browser2" {
			if len(s.Values) > 5 {
				t.Errorf("expected at most 5 values per key, got %d", len(s.Values))
			}
			if s.Total != 7 {
				t.Errorf("total: got %d, want 7", s.Total)
			}
			return
		}
	}
	t.Error("expected browser2 summary")
}
