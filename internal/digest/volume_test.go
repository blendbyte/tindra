package digest

import (
	"context"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func checkVolume(t *testing.T, ids []string, from, to time.Time) {
	t.Helper()
	ctx := context.Background()
	errors, txs, projects, err := reportVolume(ctx, testPool, ids, from, to)
	require.NoError(t, err)
	oldErrors, err := dailyErrorCounts(ctx, testPool, ids, from, to)
	require.NoError(t, err)
	oldTxs, err := dailyTxCounts(ctx, testPool, ids, from, to)
	require.NoError(t, err)
	oldProjects, err := projectBreakdown(ctx, testPool, ids, from, to)
	require.NoError(t, err)
	require.Equal(t, oldErrors, errors)
	require.Equal(t, oldTxs, txs)
	require.ElementsMatch(t, oldProjects, projects)
}

func TestReportVolumeParity(t *testing.T) {
	truncateAll(t)
	p := seedProject(t, "volume", "Volume")
	empty := seedProject(t, "empty-volume", "Empty")
	other := seedProject(t, "other-volume", "Other")
	from := time.Date(2026, 5, 4, 23, 17, 0, 0, time.UTC)
	for _, id := range []string{p.ID, other.ID} {
		for _, offset := range []time.Duration{-time.Second, 0, 10 * time.Minute, 43 * time.Minute, 24 * time.Hour, 25 * time.Hour, 26 * time.Hour} {
			seedEvent(t, id, from.Add(offset))
			seedTransaction(t, id, "test", 50, from.Add(offset))
		}
	}
	// Deliberately separate receipt time from SDK time for both telemetry kinds.
	_, err := testPool.Exec(context.Background(), `UPDATE events SET timestamp=received_at-interval '1 year'`)
	require.NoError(t, err)
	_, err = testPool.Exec(context.Background(), `UPDATE transactions SET received_at=start_timestamp+interval '1 year'`)
	require.NoError(t, err)
	for _, duration := range []time.Duration{0, 5 * time.Minute, 43 * time.Minute, time.Hour, 25 * time.Hour, 26 * time.Hour} {
		for _, ids := range [][]string{nil, {p.ID}, {p.ID, empty.ID}, {other.ID, p.ID, empty.ID}} {
			checkVolume(t, ids, from, from.Add(duration))
		}
	}
	// A non-UTC input must still produce UTC calendar days.
	zone := time.FixedZone("offset", 19800)
	checkVolume(t, []string{p.ID}, from.In(zone), from.Add(26*time.Hour).In(zone))
	checkVolume(t, []string{p.ID}, from.Truncate(time.Hour), from.Truncate(time.Hour).Add(27*time.Hour))
}

func TestReportVolumePerformance(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("TINDRA_PERF_ROWS"))
	if n < 10000 {
		t.Skip("set TINDRA_PERF_ROWS >= 10000")
	}
	truncateAll(t)
	ctx := context.Background()
	p := seedProject(t, "volume-perf", "Volume")
	from := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(7 * 24 * time.Hour)
	_, err := testPool.Exec(ctx, `INSERT INTO events(project_id,timestamp,received_at,payload) SELECT $1,$2::timestamptz,$2::timestamptz+(i%604800)*interval '1 second','{}' FROM generate_series(1,$3) i`, p.ID, from, n)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `INSERT INTO transactions(project_id,start_timestamp,timestamp,transaction,duration_ms) SELECT $1,$2::timestamptz+(i%604800)*interval '1 second',$2::timestamptz,'test',1 FROM generate_series(1,$3) i`, p.ID, from, n)
	require.NoError(t, err)
	for _, table := range []string{"events", "transactions", "telemetry_usage"} {
		_, err = testPool.Exec(ctx, "VACUUM ANALYZE "+table)
		require.NoError(t, err)
	}
	ids := []string{p.ID}
	checkVolume(t, ids, from, to)
	for _, shared := range []bool{false, true} {
		var times []time.Duration
		for i := 0; i < 4; i++ {
			start := time.Now()
			if shared {
				_, _, _, err = reportVolume(ctx, testPool, ids, from, to)
				require.NoError(t, err)
			} else {
				_, err = dailyErrorCounts(ctx, testPool, ids, from, to)
				require.NoError(t, err)
				_, err = dailyTxCounts(ctx, testPool, ids, from, to)
				require.NoError(t, err)
				_, err = projectBreakdown(ctx, testPool, ids, from, to)
				require.NoError(t, err)
			}
			if i > 0 {
				times = append(times, time.Since(start))
			}
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		t.Logf("%d events + %d transactions shared=%v median=%s", n, n, shared, times[1])
	}
}
