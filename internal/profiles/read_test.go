package profiles_test

import (
	"context"
	"errors"
	"log"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/profiles"
	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
)

var (
	testPool    *pgxpool.Pool
	testProject *storage.Project
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	pool, cleanup := testutil.SetupDB(ctx)

	project, err := storage.CreateProject(ctx, pool, "profiles-test", "Profiles Test")
	if err != nil {
		log.Fatalf("create project: %v", err)
	}

	testPool = pool
	testProject = project

	code := m.Run()
	cleanup()
	os.Exit(code)
}

func reset(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		"TRUNCATE transactions, profile_chunks CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// insertTransaction writes a transaction row and returns its id.
func insertTransaction(t *testing.T, eventID, profilerID, threadID string, start, end time.Time) string {
	t.Helper()
	var id string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO transactions
			(project_id, transaction, op, status, duration_ms, start_timestamp, timestamp,
			 event_id, profiler_id, thread_id)
		VALUES ($1, 'GET /api/orders', 'http.server', 'ok', $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		testProject.ID, int(end.Sub(start).Milliseconds()), start, end,
		nilIfEmpty(eventID), nilIfEmpty(profilerID), nilIfEmpty(threadID),
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	return id
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func insertProfile(t *testing.T, p *ingest.Profile) {
	t.Helper()
	b, err := ingest.NewBufferedProfile(testProject.ID, p)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	_, err = testPool.Exec(context.Background(), `
		INSERT INTO profile_chunks
			(project_id, format, transaction_event_id, trace_id, profiler_id, chunk_id,
			 start_ts, end_ts, sample_count, size_bytes, encoding, data)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		b.ProjectID, int16(b.Format), nilIfEmpty(b.TransactionEventID), nilIfEmpty(b.TraceID),
		nilIfEmpty(b.ProfilerID), nilIfEmpty(b.ChunkID),
		b.StartTs, b.EndTs, b.SampleCount, b.SizeBytes(), b.Encoding, b.Data)
	if err != nil {
		t.Fatalf("insert profile: %v", err)
	}
}

// syntheticChunk builds a v2 chunk sampling one thread every 10ms from base.
func syntheticChunk(profilerID, threadID string, base time.Time, samples int) *ingest.Profile {
	p := &ingest.Profile{
		Format:     ingest.ProfileFormatV2,
		ChunkID:    "chunk-" + profilerID,
		ProfilerID: profilerID,
		Platform:   "python",
		Frames: []ingest.ProfileFrame{
			{Function: "main", Module: "app"},
			{Function: "handler", Module: "app.views"},
		},
		Stacks:      [][]int32{{1, 0}},
		ThreadNames: map[string]string{threadID: "MainThread"},
	}
	for i := range samples {
		p.Samples = append(p.Samples, ingest.ProfileSample{
			ThreadID:    threadID,
			StackID:     0,
			TimestampNs: base.Add(time.Duration(i) * 10 * time.Millisecond).UnixNano(),
		})
	}
	p.StartNs = p.Samples[0].TimestampNs
	p.EndNs = p.Samples[len(p.Samples)-1].TimestampNs
	p.BaseNs = p.StartNs
	return p
}

func TestFlameGraphForTransaction_v1(t *testing.T) {
	reset(t)
	ctx := context.Background()

	p := fixture(t, "v1_php_laravel.json", "profile")
	insertProfile(t, p)
	txID := insertTransaction(t, p.TransactionID, "", "", p.Start(), p.End())

	g, err := profiles.FlameGraphForTransaction(ctx, testPool, txID)
	if err != nil {
		t.Fatalf("flame graph: %v", err)
	}

	if g.SampleCount != len(p.Samples) {
		t.Errorf("sample count = %d, want %d", g.SampleCount, len(p.Samples))
	}
	if len(g.Root.Children) != 1 {
		t.Fatalf("root has %d children, want 1: %v", len(g.Root.Children), names(g.Root))
	}
	if g.Root.Children[0].Function != "/var/www/app/public/index.php" {
		t.Errorf("entry point = %q", g.Root.Children[0].Function)
	}
}

// The v2 chunk spans far more than the transaction, so the window is what makes
// the graph mean anything for one request.
func TestFlameGraphForTransaction_v2NarrowsToTheTransactionWindow(t *testing.T) {
	reset(t)
	ctx := context.Background()

	const profilerID = "4d229f1d3807421ba62a5f8bc295d836"
	const threadID = "8412331008"
	base := time.Now().Add(-time.Minute).UTC().Truncate(time.Millisecond)

	// A 60s chunk at 10ms intervals.
	insertProfile(t, syntheticChunk(profilerID, threadID, base, 600))

	// A transaction covering only 200ms of it, so 21 samples inclusive.
	txStart := base.Add(1 * time.Second)
	txEnd := txStart.Add(200 * time.Millisecond)
	txID := insertTransaction(t, "", profilerID, threadID, txStart, txEnd)

	g, err := profiles.FlameGraphForTransaction(ctx, testPool, txID)
	if err != nil {
		t.Fatalf("flame graph: %v", err)
	}

	if g.SampleCount != 21 {
		t.Errorf("sample count = %d, want 21 within the transaction window", g.SampleCount)
	}
	if g.ThreadName != "MainThread" {
		t.Errorf("thread name = %q, want MainThread", g.ThreadName)
	}
	if g.SampleIntervalNs != 10*int64(time.Millisecond) {
		t.Errorf("interval = %d ns, want 10ms", g.SampleIntervalNs)
	}
	main := child(t, g.Root, "main")
	handler := child(t, main, "handler")
	if handler.SelfSamples != 21 {
		t.Errorf("handler self = %d, want 21", handler.SelfSamples)
	}
}

// Only the sampled thread belongs to the transaction. Folding every thread
// would multiply the apparent cost of a request by the size of the pool.
func TestFlameGraphForTransaction_v2SelectsOnlyTheTransactionThread(t *testing.T) {
	reset(t)
	ctx := context.Background()

	const profilerID = "profiler-multithread"
	base := time.Now().Add(-time.Minute).UTC().Truncate(time.Millisecond)

	p := syntheticChunk(profilerID, "thread-a", base, 100)
	// A second thread sampling in the same window.
	other := syntheticChunk(profilerID, "thread-b", base, 100)
	p.Samples = append(p.Samples, other.Samples...)
	p.ThreadNames["thread-b"] = "Worker-1"
	insertProfile(t, p)

	txID := insertTransaction(t, "", profilerID, "thread-a",
		base, base.Add(990*time.Millisecond))

	g, err := profiles.FlameGraphForTransaction(ctx, testPool, txID)
	if err != nil {
		t.Fatalf("flame graph: %v", err)
	}
	if g.SampleCount != 100 {
		t.Errorf("sample count = %d, want 100 from thread-a alone", g.SampleCount)
	}
}

// A transaction can straddle a chunk boundary, which is the whole reason the
// fold accepts several profiles.
func TestFlameGraphForTransaction_v2SpansChunkBoundary(t *testing.T) {
	reset(t)
	ctx := context.Background()

	const profilerID = "profiler-spanning"
	const threadID = "1"
	base := time.Now().Add(-2 * time.Minute).UTC().Truncate(time.Millisecond)

	first := syntheticChunk(profilerID, threadID, base, 100) // base .. base+990ms
	secondBase := base.Add(1 * time.Second)
	second := syntheticChunk(profilerID, threadID, secondBase, 100) // +1s .. +1990ms
	second.ChunkID = "chunk-two"
	insertProfile(t, first)
	insertProfile(t, second)

	// Straddle the join: last 200ms of the first, first 200ms of the second.
	txStart := base.Add(800 * time.Millisecond)
	txEnd := secondBase.Add(200 * time.Millisecond)
	txID := insertTransaction(t, "", profilerID, threadID, txStart, txEnd)

	g, err := profiles.FlameGraphForTransaction(ctx, testPool, txID)
	if err != nil {
		t.Fatalf("flame graph: %v", err)
	}

	// 20 from the first chunk (800ms..990ms) and 21 from the second (0..200ms).
	if g.SampleCount != 41 {
		t.Errorf("sample count = %d, want 41 across the chunk boundary", g.SampleCount)
	}
}

func TestFlameGraphForTransaction_notFound(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name  string
		setup func(t *testing.T) string
	}{
		{
			name: "transaction does not exist",
			setup: func(t *testing.T) string {
				reset(t)
				return "00000000-0000-0000-0000-000000000000"
			},
		},
		{
			name: "transaction carries no profile link",
			setup: func(t *testing.T) string {
				reset(t)
				now := time.Now().UTC()
				return insertTransaction(t, "", "", "", now.Add(-time.Second), now)
			},
		},
		{
			name: "profile belongs to another profiler",
			setup: func(t *testing.T) string {
				reset(t)
				base := time.Now().Add(-time.Minute).UTC()
				insertProfile(t, syntheticChunk("someone-else", "1", base, 50))
				return insertTransaction(t, "", "our-profiler", "1", base, base.Add(time.Second))
			},
		},
		{
			name: "chunks exist but none overlap the window",
			setup: func(t *testing.T) string {
				reset(t)
				base := time.Now().Add(-time.Hour).UTC()
				insertProfile(t, syntheticChunk("p1", "1", base, 50))
				later := time.Now().UTC()
				return insertTransaction(t, "", "p1", "1", later, later.Add(time.Second))
			},
		},
		{
			name: "v1 link points at a profile that is gone",
			setup: func(t *testing.T) string {
				reset(t)
				now := time.Now().UTC()
				return insertTransaction(t, "missing-event-id", "", "", now.Add(-time.Second), now)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txID := tt.setup(t)
			_, err := profiles.FlameGraphForTransaction(ctx, testPool, txID)
			if !errors.Is(err, profiles.ErrNoProfile) {
				t.Errorf("err = %v, want ErrNoProfile", err)
			}
		})
	}
}

// A transaction from a mixed deployment can carry both links. The direct v1
// lookup is preferred, but a stale event id must not hide a usable chunk.
func TestFlameGraphForTransaction_fallsBackToChunksWhenV1Missing(t *testing.T) {
	reset(t)
	ctx := context.Background()

	const profilerID = "profiler-fallback"
	const threadID = "1"
	base := time.Now().Add(-time.Minute).UTC().Truncate(time.Millisecond)
	insertProfile(t, syntheticChunk(profilerID, threadID, base, 100))

	// event_id is set but no v1 profile exists for it.
	txID := insertTransaction(t, "no-such-profile", profilerID, threadID,
		base, base.Add(990*time.Millisecond))

	g, err := profiles.FlameGraphForTransaction(ctx, testPool, txID)
	if err != nil {
		t.Fatalf("flame graph: %v", err)
	}
	if g.SampleCount != 100 {
		t.Errorf("sample count = %d, want the chunk used as a fallback", g.SampleCount)
	}
}
