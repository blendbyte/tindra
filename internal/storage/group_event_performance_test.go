package storage_test

import (
	"context"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

// Opt in: TINDRA_GROUP_PERF_EVENTS=1000 go test -v ./internal/storage
// -run '^TestGroupEventPerformance$' -count=1
func TestGroupEventPerformance(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("TINDRA_GROUP_PERF_EVENTS"))
	if err != nil || n < 100 {
		t.Skip("set TINDRA_GROUP_PERF_EVENTS >= 100")
	}
	ctx := context.Background()
	for _, atomic := range []bool{false, true} {
		var timings []float64
		for run := 0; run < 4; run++ {
			ids := groupEventFixture(t, n)
			var projectID string
			var ts time.Time
			require.NoError(t, testPool.QueryRow(ctx, "SELECT project_id,timestamp FROM events LIMIT 1").Scan(&projectID, &ts))
			start := time.Now()
			for _, id := range ids {
				var iss *storage.Issue
				var e error
				if atomic {
					iss, _, _, e = groupOne(ctx, testPool, id)
				} else {
					iss, _, _, e = storage.UpsertIssue(ctx, testPool, projectID, "same-fingerprint", "Atomic error", "error", "error", "production", "v1", ts)
					require.NoError(t, e)
					e = storage.LinkEventToIssue(ctx, testPool, id, iss.ID, "same-fingerprint")
				}
				require.NoError(t, e)
				require.NotNil(t, iss)
			}
			elapsed := float64(time.Since(start).Microseconds()) / 1000
			var count, linked int
			require.NoError(t, testPool.QueryRow(ctx, "SELECT (SELECT event_count FROM issues),(SELECT count(*) FROM events WHERE issue_id IS NOT NULL)").Scan(&count, &linked))
			require.Equal(t, n, count)
			require.Equal(t, n, linked)
			if run > 0 {
				timings = append(timings, elapsed)
			}
		}
		sort.Float64s(timings)
		t.Logf("atomic=%v events=%d median %.3f ms (%.3f ms/event)", atomic, n, timings[1], timings[1]/float64(n))
	}
}
