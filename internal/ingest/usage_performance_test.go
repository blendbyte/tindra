package ingest

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
)

func TestUsageBatchPerformance(t *testing.T) {
	if os.Getenv("TINDRA_PERF_USAGE") != "1" {
		t.Skip("set TINDRA_PERF_USAGE=1")
	}
	ctx := context.Background()
	pool, cleanup := testutil.SetupDB(ctx)
	defer cleanup()
	project, err := storage.CreateProject(ctx, pool, "batch-perf", "Batch")
	if err != nil {
		t.Fatal(err)
	}
	events := make([]BufferedEvent, 1000)
	for i := range events {
		events[i] = BufferedEvent{ProjectID: project.ID, Timestamp: time.Now(), Payload: []byte(`{}`)}
	}
	for _, grouped := range []bool{false, true} {
		state := "DISABLE"
		if grouped {
			state = "ENABLE"
		}
		if _, err = pool.Exec(ctx, "ALTER TABLE events "+state+" TRIGGER usage_insert"); err != nil {
			t.Fatal(err)
		}
		var samples []time.Duration
		for i := range 5 {
			if _, err = pool.Exec(ctx, "TRUNCATE events CASCADE"); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			if grouped {
				writeBatch(ctx, pool, events)
			} else {
				batch := &pgx.Batch{}
				for _, e := range events {
					batch.Queue(`INSERT INTO events(project_id,event_id,timestamp,payload,trace_id,span_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(project_id,event_id) WHERE event_id IS NOT NULL DO NOTHING`, e.ProjectID, e.EventID, e.Timestamp, e.Payload, nil, nil)
				}
				results := pool.SendBatch(ctx, batch)
				for range events {
					if _, err = results.Exec(); err != nil {
						t.Fatal(err)
					}
				}
				if err = results.Close(); err != nil {
					t.Fatal(err)
				}
			}
			elapsed := time.Since(start)
			var count int
			if err = pool.QueryRow(ctx, "SELECT count(*) FROM events").Scan(&count); err != nil || count != 1000 {
				t.Fatalf("count=%d err=%v", count, err)
			}
			if grouped {
				var usage int
				if err = pool.QueryRow(ctx, "SELECT sum(n) FROM telemetry_usage WHERE kind='events'").Scan(&usage); err != nil || usage != 1000 {
					t.Fatalf("usage=%d err=%v", usage, err)
				}
			}
			if i > 0 {
				samples = append(samples, elapsed)
			}
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		t.Logf("1000 event flush grouped_with_usage=%v median=%s", grouped, samples[len(samples)/2])
	}
}
