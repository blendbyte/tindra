package issues

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/ingest"
)

func TestWorkerSchedulingPerformance(t *testing.T) {
	if os.Getenv("TINDRA_PERF_WORKERS") != "1" {
		t.Skip("set TINDRA_PERF_WORKERS=1")
	}
	pool, project := schedulingDB(t)
	ctx := context.Background()
	const n = 1000
	for _, drain := range []bool{false, true} {
		_, err := pool.Exec(ctx, "TRUNCATE events,issues CASCADE")
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `INSERT INTO events(project_id,timestamp,payload) SELECT $1,now(),'{"message":"same"}' FROM generate_series(1,$2)`, project, n)
		require.NoError(t, err)
		g := NewGrouper(pool)
		start := time.Now()
		grouped := 0
		if drain {
			var cursor *groupingCursor
			for grouped < n {
				stats := g.runPass(ctx, cursor)
				require.Zero(t, stats.Failed)
				grouped += stats.Grouped
				cursor = stats.next
				if grouped < n {
					time.Sleep(50 * time.Millisecond)
				}
			}
		} else {
			ticker := time.NewTicker(500 * time.Millisecond)
			for grouped < n {
				<-ticker.C
				cursor := &groupingCursor{through: &rawEvent{ReceivedAt: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), ID: "ffffffff-ffff-ffff-ffff-ffffffffffff"}}
				events, err := g.readPage(ctx, cursor)
				require.NoError(t, err)
				for _, event := range events {
					ok, err := g.group(ctx, event)
					require.NoError(t, err)
					if ok {
						grouped++
					}
				}
			}
			ticker.Stop()
		}
		t.Logf("1000 events drain=%v elapsed=%s", drain, time.Since(start))
	}
	// Isolate CPU on a large transaction that stays below the duration threshold.
	tx := ingest.BufferedTransaction{Spans: make([]ingest.BufferedSpan, 5000)}
	for i := range tx.Spans {
		tx.Spans[i] = ingest.BufferedSpan{Op: "db.query", Description: fmt.Sprintf("SELECT * FROM users WHERE id = %d", i%10)}
	}
	for _, cached := range []bool{false, true} {
		var samples []time.Duration
		for i := range 6 {
			start := time.Now()
			if cached {
				NewN1Detector(nil).detectTx(ctx, tx, "")
			} else {
				require.Equal(t, 1, legacyNormalizationGroups(tx))
			}
			if i > 0 {
				samples = append(samples, time.Since(start))
			}
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		t.Logf("5000 repeated SQL spans normalization cache=%v median=%s", cached, samples[len(samples)/2])
	}
	// Measure the synchronous hook's database work when every transaction qualifies.
	_, err := pool.Exec(ctx, "TRUNCATE perf_events,issues,transactions CASCADE")
	require.NoError(t, err)
	var txs []ingest.BufferedTransaction
	var ids []string
	for range 100 {
		var id string
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO transactions(project_id,start_timestamp,timestamp,transaction,duration_ms) VALUES($1,now(),now(),'n1',100) RETURNING id`, project).Scan(&id))
		item := ingest.BufferedTransaction{ProjectID: project, Transaction: "n1", Timestamp: time.Now(), Spans: make([]ingest.BufferedSpan, 8)}
		for j := range item.Spans {
			item.Spans[j] = ingest.BufferedSpan{Op: "db.query", Description: "SELECT * FROM users", DurationMs: 10}
		}
		txs = append(txs, item)
		ids = append(ids, id)
	}
	start := time.Now()
	NewN1Detector(pool).ProcessBatch(ctx, pool, txs, ids)
	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM perf_events").Scan(&count))
	require.Equal(t, 100, count)
	t.Logf("100 qualifying transactions synchronous hook=%s", time.Since(start))
}

func legacyNormalizationGroups(tx ingest.BufferedTransaction) int {
	type group struct {
		count, totalMs int
		exampleDesc    string
	}
	groups := map[string]*group{}
	for _, sp := range tx.Spans {
		if !strings.HasPrefix(sp.Op, "db") {
			continue
		}
		desc := strings.TrimSpace(sp.Description)
		if desc == "" {
			continue
		}
		key := normalizeSQL(desc)
		if g := groups[key]; g != nil {
			g.count++
			g.totalMs += sp.DurationMs
		} else {
			groups[key] = &group{1, sp.DurationMs, desc}
		}
	}
	return len(groups)
}
