package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

func setupProjectForSpans(t *testing.T) *storage.Project {
	t.Helper()
	truncateProjects(t)
	p, err := storage.CreateProject(context.Background(), testPool, "spans-proj", "Spans Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p
}

// seedSpan inserts a span attached to the given transaction.
func seedSpan(t *testing.T, txnID, spanID, op, description string, durationMs int, start time.Time) {
	t.Helper()
	end := start.Add(time.Duration(durationMs) * time.Millisecond)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO spans (transaction_id, span_id, op, description, start_timestamp, timestamp, duration_ms, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'ok')
	`, txnID, spanID, op, nullableString(description), start, end, durationMs); err != nil {
		t.Fatalf("seed span: %v", err)
	}
}

// TestGetSpanSummaries_empty verifies an empty slice is returned when there are no spans.
func TestGetSpanSummaries_empty(t *testing.T) {
	setupProjectForSpans(t)

	summaries, err := storage.GetSpanSummaries(context.Background(), testPool, "db", nil, 24, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summaries) != 0 {
		t.Errorf("expected 0 summaries, got %d", len(summaries))
	}
}

// TestGetSpanSummaries_basicAggregation verifies that spans are grouped by op+description
// and that aggregated values are computed correctly.
func TestGetSpanSummaries_basicAggregation(t *testing.T) {
	p := setupProjectForSpans(t)
	now := time.Now().UTC()

	tx := seedTransaction(t, p.ID, "/api/summary", 200, now)

	// Three db.query spans for the same op+description.
	for i, dur := range []int{10, 20, 30} {
		seedSpan(t, tx.ID, "span-s"+string(rune('a'+i)), "db.query", "SELECT 1", dur, now.Add(time.Duration(i)*time.Millisecond))
	}

	summaries, err := storage.GetSpanSummaries(context.Background(), testPool, "db", []string{p.ID}, 24, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summaries) == 0 {
		t.Fatal("expected at least one summary")
	}

	var found *storage.SpanSummary
	for _, s := range summaries {
		if s.Op == "db.query" && s.Description == "SELECT 1" {
			found = s
			break
		}
	}
	if found == nil {
		t.Fatal("expected summary for db.query/SELECT 1")
	}
	if found.SampleCount != 3 {
		t.Errorf("sample_count: got %d, want 3", found.SampleCount)
	}
	if found.P50 <= 0 {
		t.Errorf("expected positive P50, got %f", found.P50)
	}
	if found.TotalMs != 60 {
		t.Errorf("total_ms: got %d, want 60", found.TotalMs)
	}
}

// TestGetSpanSummaries_categoryFilter verifies the op-prefix filter (db vs cache).
func TestGetSpanSummaries_categoryFilter(t *testing.T) {
	p := setupProjectForSpans(t)
	now := time.Now().UTC()

	tx := seedTransaction(t, p.ID, "/api/cat-filter", 100, now)
	seedSpan(t, tx.ID, "db-span", "db.query", "SELECT 1", 10, now)
	seedSpan(t, tx.ID, "cache-span", "cache.get", "user:1", 5, now.Add(time.Millisecond))

	dbSummaries, err := storage.GetSpanSummaries(context.Background(), testPool, "db", []string{p.ID}, 24, "", "")
	if err != nil {
		t.Fatalf("db filter: %v", err)
	}
	for _, s := range dbSummaries {
		if s.Op == "cache.get" {
			t.Errorf("cache span leaked into db category result")
		}
	}

	cacheSummaries, err := storage.GetSpanSummaries(context.Background(), testPool, "cache", []string{p.ID}, 24, "", "")
	if err != nil {
		t.Fatalf("cache filter: %v", err)
	}
	for _, s := range cacheSummaries {
		if s.Op == "db.query" {
			t.Errorf("db span leaked into cache category result")
		}
	}
}

// TestGetSpanSummaries_projectFilter verifies that passing a project ID limits results.
func TestGetSpanSummaries_projectFilter(t *testing.T) {
	truncateProjects(t)
	now := time.Now().UTC()

	p1, _ := storage.CreateProject(context.Background(), testPool, "span-p1", "P1")
	p2, _ := storage.CreateProject(context.Background(), testPool, "span-p2", "P2")

	tx1 := seedTransaction(t, p1.ID, "/from-p1", 50, now)
	tx2 := seedTransaction(t, p2.ID, "/from-p2", 50, now)

	seedSpan(t, tx1.ID, "sp1", "db.query", "FROM P1", 10, now)
	seedSpan(t, tx2.ID, "sp2", "db.query", "FROM P2", 10, now)

	// Only request summaries for p1.
	summaries, err := storage.GetSpanSummaries(context.Background(), testPool, "db", []string{p1.ID}, 24, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range summaries {
		if s.Description == "FROM P2" {
			t.Error("p2 span appeared in p1-scoped summary")
		}
	}
}

// TestGetSpanTimeseries_empty verifies an empty bucket list is returned when there are no spans.
func TestGetSpanTimeseries_empty(t *testing.T) {
	setupProjectForSpans(t)

	ts, err := storage.GetSpanTimeseries(context.Background(), testPool, "db", nil, 24, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts == nil {
		t.Fatal("expected non-nil SpanTimeseries")
	}
	if len(ts.Buckets) != 0 {
		t.Errorf("expected 0 buckets, got %d", len(ts.Buckets))
	}
	if ts.BucketSize == "" {
		t.Error("expected non-empty BucketSize")
	}
}

// TestGetSpanTimeseries_withData verifies buckets are produced for spans within the time window.
func TestGetSpanTimeseries_withData(t *testing.T) {
	p := setupProjectForSpans(t)
	now := time.Now().UTC()

	// Use two different hours so we get two distinct hour-buckets.
	tx1 := seedTransaction(t, p.ID, "/ts1", 50, now.Add(-2*time.Hour))
	tx2 := seedTransaction(t, p.ID, "/ts2", 50, now.Add(-1*time.Hour))

	seedSpan(t, tx1.ID, "ts-sp1", "db.query", "q1", 10, now.Add(-2*time.Hour))
	seedSpan(t, tx2.ID, "ts-sp2", "db.query", "q2", 20, now.Add(-1*time.Hour))

	ts, err := storage.GetSpanTimeseries(context.Background(), testPool, "db", []string{p.ID}, 24, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ts.Buckets) == 0 {
		t.Error("expected at least one bucket")
	}
	if ts.BucketSize != "hour" {
		t.Errorf("expected bucket_size=hour for 24h window, got %q", ts.BucketSize)
	}
	for _, b := range ts.Buckets {
		if b.Count <= 0 {
			t.Errorf("bucket count should be positive, got %d", b.Count)
		}
	}
}

// TestGetSpanTimeseries_bucketSizeDay verifies the bucket size is "day" for windows > 48h.
func TestGetSpanTimeseries_bucketSizeDay(t *testing.T) {
	setupProjectForSpans(t)

	ts, err := storage.GetSpanTimeseries(context.Background(), testPool, "db", nil, 168, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts.BucketSize != "day" {
		t.Errorf("expected bucket_size=day for 168h window, got %q", ts.BucketSize)
	}
}

// TestGetSpanSamples_empty verifies an empty slice is returned when there are no matching spans.
func TestGetSpanSamples_empty(t *testing.T) {
	setupProjectForSpans(t)

	samples, err := storage.GetSpanSamples(context.Background(), testPool, "db.query", "SELECT 1", nil, 24, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 0 {
		t.Errorf("expected 0 samples, got %d", len(samples))
	}
}

// TestGetSpanSamples_returnsMatchingSpans verifies that samples for the exact op+description are returned.
func TestGetSpanSamples_returnsMatchingSpans(t *testing.T) {
	p := setupProjectForSpans(t)
	now := time.Now().UTC()

	tx := seedTransaction(t, p.ID, "/api/samples", 100, now)
	seedSpan(t, tx.ID, "samp-1", "db.query", "SELECT users", 15, now)
	seedSpan(t, tx.ID, "samp-2", "db.query", "SELECT orders", 25, now.Add(time.Millisecond))

	// Only SELECT users should come back.
	samples, err := storage.GetSpanSamples(context.Background(), testPool, "db.query", "SELECT users", []string{p.ID}, 24, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(samples))
	}
	if samples[0].Description != "SELECT users" {
		t.Errorf("description: got %q, want 'SELECT users'", samples[0].Description)
	}
	if samples[0].Op != "db.query" {
		t.Errorf("op: got %q, want 'db.query'", samples[0].Op)
	}
	if samples[0].TransactionName != "/api/samples" {
		t.Errorf("transaction_name: got %q, want '/api/samples'", samples[0].TransactionName)
	}
}

// TestGetSpanSamples_projectFilter verifies project scoping works.
func TestGetSpanSamples_projectFilter(t *testing.T) {
	truncateProjects(t)
	now := time.Now().UTC()

	p1, _ := storage.CreateProject(context.Background(), testPool, "samp-p1", "P1")
	p2, _ := storage.CreateProject(context.Background(), testPool, "samp-p2", "P2")

	tx1 := seedTransaction(t, p1.ID, "/p1-txn", 50, now)
	tx2 := seedTransaction(t, p2.ID, "/p2-txn", 50, now)

	seedSpan(t, tx1.ID, "s-p1", "cache.get", "user:1", 5, now)
	seedSpan(t, tx2.ID, "s-p2", "cache.get", "user:1", 5, now)

	samples, err := storage.GetSpanSamples(context.Background(), testPool, "cache.get", "user:1", []string{p1.ID}, 24, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range samples {
		if s.TransactionName == "/p2-txn" {
			t.Error("p2 span appeared in p1-scoped samples")
		}
	}
}
