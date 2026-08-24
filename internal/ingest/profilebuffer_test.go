package ingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/ingest"
)

func stubProfile(t *testing.T, file, itemType string) ingest.BufferedProfile {
	t.Helper()
	p, err := ingest.ParseProfileItem(itemType, fixture(t, file))
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	b, err := ingest.NewBufferedProfile(testProject.ID, p)
	if err != nil {
		t.Fatalf("buffer %s: %v", file, err)
	}
	return b
}

func truncateProfiles(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE profile_chunks CASCADE"); err != nil {
		t.Fatalf("truncate profile_chunks: %v", err)
	}
}

func TestProfileBuffer_PushReturnsFalseWhenFull(t *testing.T) {
	buf := ingest.NewProfileBuffer(1)
	if !buf.Push(stubProfile(t, "v1_php_laravel.json", "profile")) {
		t.Fatal("expected Push to return true on an empty buffer")
	}
	if buf.Push(stubProfile(t, "v1_php_laravel.json", "profile")) {
		t.Fatal("expected Push to return false when the buffer is full")
	}
}

// The blob has to survive the BYTEA round trip through Postgres, not just the
// codec, since STORAGE EXTERNAL and TOAST sit in between.
func TestProfileBuffer_writesAndReadsBackV1(t *testing.T) {
	truncateProfiles(t)

	buf := ingest.NewProfileBuffer(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Run(ctx, testPool)

	want, err := ingest.ParseProfile(fixture(t, "v1_php_laravel.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b, err := ingest.NewBufferedProfile(testProject.ID, want)
	if err != nil {
		t.Fatalf("buffer: %v", err)
	}
	if !buf.Push(b) {
		t.Fatal("push failed")
	}

	time.Sleep(400 * time.Millisecond)

	var (
		format      int16
		txEventID   string
		profilerID  *string
		sampleCount int
		sizeBytes   int
		encoding    int16
		data        []byte
		startTs     time.Time
		endTs       time.Time
	)
	err = testPool.QueryRow(context.Background(), `
		SELECT format, transaction_event_id, profiler_id, sample_count,
		       size_bytes, encoding, data, start_ts, end_ts
		FROM profile_chunks WHERE project_id = $1`, testProject.ID,
	).Scan(&format, &txEventID, &profilerID, &sampleCount, &sizeBytes,
		&encoding, &data, &startTs, &endTs)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	if format != int16(ingest.ProfileFormatV1) {
		t.Errorf("format = %d, want %d", format, ingest.ProfileFormatV1)
	}
	if txEventID != want.TransactionID {
		t.Errorf("transaction_event_id = %q, want %q", txEventID, want.TransactionID)
	}
	if profilerID != nil {
		t.Errorf("profiler_id = %q, want NULL for a v1 profile", *profilerID)
	}
	if sampleCount != len(want.Samples) {
		t.Errorf("sample_count = %d, want %d", sampleCount, len(want.Samples))
	}
	if sizeBytes != len(data) {
		t.Errorf("size_bytes = %d but the blob is %d bytes", sizeBytes, len(data))
	}
	// TIMESTAMPTZ resolves to microseconds while sample times are nanoseconds,
	// so these columns are a coarse index into the blob rather than an exact
	// copy. Four orders of magnitude finer than the 101 Hz sample rate is
	// ample for the range lookup, and the authoritative times live inside the
	// payload.
	if !startTs.Equal(want.Start().Truncate(time.Microsecond)) {
		t.Errorf("start_ts = %s, want %s", startTs.UTC(), want.Start().Truncate(time.Microsecond))
	}
	if !endTs.Equal(want.End().Truncate(time.Microsecond)) {
		t.Errorf("end_ts = %s, want %s", endTs.UTC(), want.End().Truncate(time.Microsecond))
	}

	got, err := ingest.DecodeProfile(encoding, data)
	if err != nil {
		t.Fatalf("decode stored blob: %v", err)
	}
	if len(got.Samples) != len(want.Samples) || len(got.Frames) != len(want.Frames) {
		t.Errorf("stored profile has %d samples / %d frames, want %d / %d",
			len(got.Samples), len(got.Frames), len(want.Samples), len(want.Frames))
	}
	if got.Frames[2].Function != want.Frames[2].Function {
		t.Errorf("frame function = %q, want %q", got.Frames[2].Function, want.Frames[2].Function)
	}
}

func TestProfileBuffer_writesV2Chunk(t *testing.T) {
	truncateProfiles(t)

	buf := ingest.NewProfileBuffer(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Run(ctx, testPool)

	if !buf.Push(stubProfile(t, "v2_python_chunk.json", "profile_chunk")) {
		t.Fatal("push failed")
	}
	time.Sleep(400 * time.Millisecond)

	var (
		format    int16
		profiler  string
		chunkID   string
		txEventID *string
	)
	err := testPool.QueryRow(context.Background(), `
		SELECT format, profiler_id, chunk_id, transaction_event_id
		FROM profile_chunks WHERE project_id = $1`, testProject.ID,
	).Scan(&format, &profiler, &chunkID, &txEventID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	if format != int16(ingest.ProfileFormatV2) {
		t.Errorf("format = %d, want %d", format, ingest.ProfileFormatV2)
	}
	if profiler != "4d229f1d3807421ba62a5f8bc295d836" {
		t.Errorf("profiler_id = %q", profiler)
	}
	if chunkID == "" {
		t.Error("chunk_id should be set")
	}
	if txEventID != nil {
		t.Errorf("transaction_event_id = %q, want NULL for a chunk", *txEventID)
	}
}

// The v2 read path finds chunks by profiler and time range, so the index and
// the bounded scan the design relies on need to actually return the right rows.
func TestProfileChunks_rangeLookupByProfiler(t *testing.T) {
	truncateProfiles(t)

	buf := ingest.NewProfileBuffer(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Run(ctx, testPool)

	chunk := stubProfile(t, "v2_python_chunk.json", "profile_chunk")

	// A neighbouring chunk from the same profiler, an hour earlier, which the
	// window must exclude.
	old := chunk
	old.ChunkID = "0000000000000000000000000000dead"
	old.StartTs = chunk.StartTs.Add(-time.Hour)
	old.EndTs = chunk.EndTs.Add(-time.Hour)

	// A chunk from a different profiler in the same window, also excluded.
	other := chunk
	other.ChunkID = "0000000000000000000000000000beef"
	other.ProfilerID = "ffffffffffffffffffffffffffffffff"

	for _, c := range []ingest.BufferedProfile{chunk, old, other} {
		if !buf.Push(c) {
			t.Fatal("push failed")
		}
	}
	time.Sleep(400 * time.Millisecond)

	// The lookup the read path will issue: bounded on start_ts because chunks
	// cannot exceed 66s, then filtered on end_ts for true overlap.
	rows, err := testPool.Query(context.Background(), `
		SELECT chunk_id FROM profile_chunks
		WHERE project_id = $1 AND profiler_id = $2
		  AND start_ts >= $3::timestamptz - INTERVAL '70 seconds'
		  AND start_ts <= $4::timestamptz
		  AND end_ts   >= $3::timestamptz
		ORDER BY start_ts`,
		testProject.ID, chunk.ProfilerID, chunk.StartTs, chunk.EndTs)
	if err != nil {
		t.Fatalf("range query: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("matched %d chunks, want exactly the overlapping one: %v", len(got), got)
	}
	if got[0] != chunk.ChunkID {
		t.Errorf("matched chunk %q, want %q", got[0], chunk.ChunkID)
	}
}

func TestProfileBuffer_flushesOnShutdown(t *testing.T) {
	truncateProfiles(t)

	buf := ingest.NewProfileBuffer(10)
	ctx, cancel := context.WithCancel(context.Background())
	go buf.Run(ctx, testPool)

	if !buf.Push(stubProfile(t, "v1_python.json", "profile")) {
		t.Fatal("push failed")
	}
	// Cancel well inside the 200ms tick so the drain path is what writes it.
	cancel()
	time.Sleep(300 * time.Millisecond)

	var count int
	if err := testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM profile_chunks WHERE project_id = $1", testProject.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("stored %d profiles, want 1 flushed on shutdown", count)
	}
}

// A row the database rejects must not take the writer goroutine down with it.
// The batch is best effort: one bad profile should not stop the ones behind it.
func TestProfileBuffer_survivesAFailedInsert(t *testing.T) {
	truncateProfiles(t)

	buf := ingest.NewProfileBuffer(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Run(ctx, testPool)

	// No such project, so the foreign key rejects it.
	bad := stubProfile(t, "v1_php_laravel.json", "profile")
	bad.ProjectID = "00000000-0000-0000-0000-000000000000"
	if !buf.Push(bad) {
		t.Fatal("push failed")
	}
	time.Sleep(400 * time.Millisecond)

	// The writer is still running and still accepting work.
	if !buf.Push(stubProfile(t, "v1_python.json", "profile")) {
		t.Fatal("push after a failed insert failed")
	}
	time.Sleep(400 * time.Millisecond)

	var count int
	if err := testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM profile_chunks WHERE project_id = $1", testProject.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("stored %d profiles, want the good one written after the bad one failed", count)
	}
}
