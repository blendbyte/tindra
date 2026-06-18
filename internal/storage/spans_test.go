package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
// project_id, environment, and release are pulled from the parent transaction row.
func seedSpan(t *testing.T, txnID, spanID, op, description string, durationMs int, start time.Time) {
	t.Helper()
	end := start.Add(time.Duration(durationMs) * time.Millisecond)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO spans
			(transaction_id, span_id, op, description, start_timestamp, timestamp, duration_ms, status,
			 project_id, environment, release)
		SELECT $1, $2, $3, $4, $5, $6, $7, 'ok',
		       project_id, environment, release
		FROM transactions WHERE id = $1
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
	require.NotNil(t, found, "expected summary for db.query/SELECT 1")
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
	require.NotNil(t, ts, "expected non-nil SpanTimeseries")
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

// TestGetSpanSummaries_jobCategory verifies the "job" op-prefix filter covers task/queue/job spans.
func TestGetSpanSummaries_jobCategory(t *testing.T) {
	p := setupProjectForSpans(t)
	now := time.Now().UTC()

	tx := seedTransaction(t, p.ID, "/worker", 200, now)
	seedSpan(t, tx.ID, "job-sp-1", "queue.consume", "process_email", 50, now)
	seedSpan(t, tx.ID, "job-sp-2", "task.execute", "send_notification", 30, now.Add(time.Millisecond))

	summaries, err := storage.GetSpanSummaries(context.Background(), testPool, "job", []string{p.ID}, 24, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summaries) == 0 {
		t.Error("expected job-category spans in summaries")
	}
	for _, s := range summaries {
		if s.Op != "queue.consume" && s.Op != "task.execute" {
			t.Errorf("unexpected op in job category result: %q", s.Op)
		}
	}
}

// TestGetSpanSummaries_unknownCategory verifies that an unknown category uses the default filter (TRUE).
func TestGetSpanSummaries_unknownCategory(t *testing.T) {
	p := setupProjectForSpans(t)
	now := time.Now().UTC()

	tx := seedTransaction(t, p.ID, "/custom", 100, now)
	seedSpan(t, tx.ID, "any-sp", "custom.thing", "do something", 10, now)

	summaries, err := storage.GetSpanSummaries(context.Background(), testPool, "unknown_category", []string{p.ID}, 24, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var found bool
	for _, s := range summaries {
		if s.Op == "custom.thing" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected custom.thing span visible with unknown category (default filter = TRUE)")
	}
}

// TestGetSpanSummaries_withEnvFilter verifies the environment filter passes through to query.
func TestGetSpanSummaries_withEnvFilter(t *testing.T) {
	p := setupProjectForSpans(t)
	now := time.Now().UTC()

	// Transaction with environment=production
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, environment)
		VALUES ($1, '/env-txn', 'http.server', 'ok', 100, $2, $3, 'production')
	`, p.ID, now, now.Add(100*time.Millisecond))
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	var txID string
	testPool.QueryRow(context.Background(), `SELECT id FROM transactions WHERE transaction = '/env-txn' AND project_id = $1`, p.ID).Scan(&txID)
	seedSpan(t, txID, "env-sp", "db.query", "SELECT env", 10, now)

	// With env filter matching production
	got, err := storage.GetSpanSummaries(context.Background(), testPool, "db", []string{p.ID}, 24, "production", "")
	if err != nil {
		t.Fatalf("env filter: %v", err)
	}
	var found bool
	for _, s := range got {
		if s.Description == "SELECT env" {
			found = true
		}
	}
	if !found {
		t.Error("expected span visible when environment matches")
	}

	// With env filter not matching
	none, err := storage.GetSpanSummaries(context.Background(), testPool, "db", []string{p.ID}, 24, "staging", "")
	if err != nil {
		t.Fatalf("staging filter: %v", err)
	}
	for _, s := range none {
		if s.Description == "SELECT env" {
			t.Error("span from production should not appear when filtering by staging")
		}
	}
}

// TestGetSpanTimeseries_5minBuckets verifies the bucket size for hours <= 1.
func TestGetSpanTimeseries_5minBuckets(t *testing.T) {
	setupProjectForSpans(t)

	ts, err := storage.GetSpanTimeseries(context.Background(), testPool, "db", nil, 1, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts.BucketSize != "5min" {
		t.Errorf("expected bucket_size=5min for hours=1, got %q", ts.BucketSize)
	}
}

// TestGetSpanTimeseries_envFilter verifies that timeseries excludes spans from a non-matching environment.
func TestGetSpanTimeseries_envFilter(t *testing.T) {
	p := setupProjectForSpans(t)
	now := time.Now().UTC()

	_, err := testPool.Exec(context.Background(), `
		INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, environment)
		VALUES ($1, '/prod-txn', 'http.server', 'ok', 100, $2, $3, 'production')
	`, p.ID, now.Add(-time.Hour), now.Add(-time.Hour+100*time.Millisecond))
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	var txID string
	testPool.QueryRow(context.Background(), `SELECT id FROM transactions WHERE transaction = '/prod-txn' AND project_id = $1`, p.ID).Scan(&txID)
	seedSpan(t, txID, "ts-env-sp", "db.query", "prod-only", 15, now.Add(-time.Hour))

	// Should appear with matching env filter.
	ts, err := storage.GetSpanTimeseries(context.Background(), testPool, "db", []string{p.ID}, 24, "production", "")
	if err != nil {
		t.Fatalf("production filter: %v", err)
	}
	if len(ts.Buckets) == 0 {
		t.Error("expected at least one bucket for production env")
	}

	// Should be empty with non-matching env filter.
	ts2, err := storage.GetSpanTimeseries(context.Background(), testPool, "db", []string{p.ID}, 24, "staging", "")
	if err != nil {
		t.Fatalf("staging filter: %v", err)
	}
	if len(ts2.Buckets) != 0 {
		t.Errorf("expected no buckets for staging env, got %d", len(ts2.Buckets))
	}
}

// TestGetSpanTimeseries_projectFilter verifies timeseries is scoped to the requested project.
func TestGetSpanTimeseries_projectFilter(t *testing.T) {
	truncateProjects(t)
	now := time.Now().UTC()

	p1, _ := storage.CreateProject(context.Background(), testPool, "ts-p1", "P1")
	p2, _ := storage.CreateProject(context.Background(), testPool, "ts-p2", "P2")

	tx1 := seedTransaction(t, p1.ID, "/p1", 50, now.Add(-time.Hour))
	tx2 := seedTransaction(t, p2.ID, "/p2", 50, now.Add(-time.Hour))

	seedSpan(t, tx1.ID, "ts-proj-sp1", "db.query", "from-p1", 10, now.Add(-time.Hour))
	seedSpan(t, tx2.ID, "ts-proj-sp2", "db.query", "from-p2", 10, now.Add(-time.Hour))

	ts, err := storage.GetSpanTimeseries(context.Background(), testPool, "db", []string{p1.ID}, 24, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// p1 has one span so there should be exactly one non-empty bucket.
	total := int64(0)
	for _, b := range ts.Buckets {
		total += b.Count
	}
	if total != 1 {
		t.Errorf("expected total count=1 (only p1 spans), got %d", total)
	}
}

// TestGetSpanSummaries_releaseFilter verifies that the release filter is applied to summaries.
func TestGetSpanSummaries_releaseFilter(t *testing.T) {
	p := setupProjectForSpans(t)
	now := time.Now().UTC()

	_, err := testPool.Exec(context.Background(), `
		INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, release)
		VALUES ($1, '/rel-txn', 'http.server', 'ok', 100, $2, $3, 'v2.0.0')
	`, p.ID, now, now.Add(100*time.Millisecond))
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	var txID string
	testPool.QueryRow(context.Background(), `SELECT id FROM transactions WHERE transaction = '/rel-txn' AND project_id = $1`, p.ID).Scan(&txID)
	seedSpan(t, txID, "rel-sp", "db.query", "SELECT release", 10, now)

	// Matching release — span should appear.
	got, err := storage.GetSpanSummaries(context.Background(), testPool, "db", []string{p.ID}, 24, "", "v2.0.0")
	if err != nil {
		t.Fatalf("release filter: %v", err)
	}
	var found bool
	for _, s := range got {
		if s.Description == "SELECT release" {
			found = true
		}
	}
	if !found {
		t.Error("expected span visible when release matches")
	}

	// Non-matching release — span should not appear.
	none, err := storage.GetSpanSummaries(context.Background(), testPool, "db", []string{p.ID}, 24, "", "v1.0.0")
	if err != nil {
		t.Fatalf("v1 release filter: %v", err)
	}
	for _, s := range none {
		if s.Description == "SELECT release" {
			t.Error("span from v2.0.0 should not appear when filtering by v1.0.0")
		}
	}
}

// TestGetSpanSamples_envFilter verifies that samples are scoped to the requested environment.
func TestGetSpanSamples_envFilter(t *testing.T) {
	p := setupProjectForSpans(t)
	now := time.Now().UTC()

	_, err := testPool.Exec(context.Background(), `
		INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, environment)
		VALUES ($1, '/samp-env-txn', 'http.server', 'ok', 100, $2, $3, 'production')
	`, p.ID, now, now.Add(100*time.Millisecond))
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	var txID string
	testPool.QueryRow(context.Background(), `SELECT id FROM transactions WHERE transaction = '/samp-env-txn' AND project_id = $1`, p.ID).Scan(&txID)
	seedSpan(t, txID, "samp-env-sp", "cache.get", "user:env", 5, now)

	// Matching env.
	got, err := storage.GetSpanSamples(context.Background(), testPool, "cache.get", "user:env", []string{p.ID}, 24, "production", "")
	if err != nil {
		t.Fatalf("production filter: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 sample for production env, got %d", len(got))
	}

	// Non-matching env.
	none, err := storage.GetSpanSamples(context.Background(), testPool, "cache.get", "user:env", []string{p.ID}, 24, "staging", "")
	if err != nil {
		t.Fatalf("staging filter: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 samples for staging env, got %d", len(none))
	}
}

// TestGetSpanSamples_releaseFilter verifies that samples are scoped to the requested release.
func TestGetSpanSamples_releaseFilter(t *testing.T) {
	p := setupProjectForSpans(t)
	now := time.Now().UTC()

	_, err := testPool.Exec(context.Background(), `
		INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp, release)
		VALUES ($1, '/samp-rel-txn', 'http.server', 'ok', 100, $2, $3, 'v3.0.0')
	`, p.ID, now, now.Add(100*time.Millisecond))
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	var txID string
	testPool.QueryRow(context.Background(), `SELECT id FROM transactions WHERE transaction = '/samp-rel-txn' AND project_id = $1`, p.ID).Scan(&txID)
	seedSpan(t, txID, "samp-rel-sp", "cache.get", "user:rel", 5, now)

	got, err := storage.GetSpanSamples(context.Background(), testPool, "cache.get", "user:rel", []string{p.ID}, 24, "", "v3.0.0")
	if err != nil {
		t.Fatalf("release filter: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 sample for v3.0.0, got %d", len(got))
	}

	none, err := storage.GetSpanSamples(context.Background(), testPool, "cache.get", "user:rel", []string{p.ID}, 24, "", "v1.0.0")
	if err != nil {
		t.Fatalf("v1 release filter: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 samples for v1.0.0, got %d", len(none))
	}
}
