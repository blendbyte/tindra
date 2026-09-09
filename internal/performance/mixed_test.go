package performance_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/issues"
	"github.com/blendbyte/tindra/internal/profiles"
	"github.com/blendbyte/tindra/internal/retention"
	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
)

// This deliberately exercises real workers against a disposable database. It
// reports measurements without imposing machine-specific latency assertions.
func TestMixedLoad(t *testing.T) {
	if os.Getenv("TINDRA_PERF_MIXED") != "1" {
		t.Skip("set TINDRA_PERF_MIXED=1")
	}
	require.Empty(t, os.Getenv("TINDRA_TEST_DSN"), "mixed-load tests require disposable Docker databases")
	ctx := context.Background()
	base, cleanup := testutil.SetupDB(ctx)
	defer cleanup()
	for _, connections := range []int32{4, 10} {
		t.Run(fmt.Sprintf("connections_%d", connections), func(t *testing.T) {
			_, err := base.Exec(ctx, "TRUNCATE projects CASCADE")
			require.NoError(t, err)
			cfg := base.Config().Copy()
			cfg.MaxConns = connections
			cfg.MinConns = connections
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			require.NoError(t, err)
			defer pool.Close()
			p, err := storage.CreateProject(ctx, pool, "mixed", "Mixed")
			require.NoError(t, err)
			var issue string
			require.NoError(t, pool.QueryRow(ctx, `INSERT INTO issues(project_id,fingerprint,title,first_seen,last_seen,event_count) VALUES($1,'seed','Seed',now()-interval '40 days',now(),100000) RETURNING id`, p.ID).Scan(&issue))
			queries := []string{
				`INSERT INTO events(project_id,issue_id,timestamp,received_at,payload) SELECT $1,$2,now(),now()-CASE WHEN i<=30000 THEN interval '40 days' ELSE interval '1 hour' END,'{}' FROM generate_series(1,100000) i`,
				`INSERT INTO transactions(project_id,transaction,start_timestamp,timestamp,received_at,duration_ms) SELECT $1,'seed',now(),now(),now()-CASE WHEN i<=30000 THEN interval '40 days' ELSE interval '1 hour' END,10 FROM generate_series(1,100000) i`,
				`INSERT INTO logs(project_id,timestamp,received_at,body) SELECT $1,now(),now()-CASE WHEN i<=30000 THEN interval '40 days' ELSE interval '1 hour' END,'seed' FROM generate_series(1,100000) i`,
			}
			for i, q := range queries {
				args := []any{p.ID}
				if i == 0 {
					args = append(args, issue)
				}
				_, err = pool.Exec(ctx, q, args...)
				require.NoError(t, err)
			}
			for _, table := range []string{"events", "transactions", "logs", "telemetry_usage"} {
				_, err = pool.Exec(ctx, "VACUUM ANALYZE "+table)
				require.NoError(t, err)
			}
			profileID := seedProfile(t, pool, p.ID)
			runReads(t, pool, false, nil, profileID)
			buf, txBuf, logBuf := ingest.NewBuffer(10000), ingest.NewTransactionBuffer(10000), ingest.NewLogBuffer(10000)
			txBuf.Hook = issues.NewN1Detector(pool).ProcessBatch
			workerCtx, stop := context.WithCancel(ctx)
			var workers sync.WaitGroup
			for _, run := range []func(context.Context, *pgxpool.Pool){buf.Run, txBuf.Run, logBuf.Run} {
				workers.Go(func() { run(workerCtx, pool) })
			}
			workers.Go(func() { issues.NewGrouper(pool).Run(workerCtx) })
			var peakBacklog int64
			var peakAgeMs float64
			workers.Go(func() {
				ticker := time.NewTicker(250 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-workerCtx.Done():
						return
					case <-ticker.C:
						probeCtx, cancel := context.WithTimeout(workerCtx, time.Second)
						var backlog int64
						var age float64
						err := pool.QueryRow(probeCtx, `SELECT count(*),coalesce(extract(epoch FROM now()-min(received_at))*1000,0) FROM events WHERE issue_id IS NULL`).Scan(&backlog, &age)
						cancel()
						if err == nil {
							peakBacklog = max(peakBacklog, backlog)
							peakAgeMs = max(peakAgeMs, age)
						}
					}
				}
			})
			defer func() { stop(); workers.Wait() }()
			retained := make(chan time.Duration, 1)
			workers.Go(func() {
				start := time.Now()
				retention.NewWorker(pool, 30).RunOnce(workerCtx)
				retained <- time.Since(start)
			})
			producerCtx, stopProducer := context.WithCancel(ctx)
			var producer sync.WaitGroup
			producer.Go(func() {
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for i := 0; ; i++ {
					select {
					case <-producerCtx.Done():
						return
					case at := <-ticker.C:
						id := fmt.Sprintf("mixed-%d", i)
						buf.Push(ingest.BufferedEvent{ProjectID: p.ID, EventID: &id, Timestamp: at, Payload: []byte(`{"message":"mixed workload","level":"error"}`)})
						transaction := ingest.BufferedTransaction{ProjectID: p.ID, EventID: id, Transaction: "mixed", StartTimestamp: at, Timestamp: at.Add(60 * time.Millisecond), DurationMs: 60, Status: "ok"}
						if i%10 == 0 {
							for j := 0; j < 6; j++ {
								transaction.Spans = append(transaction.Spans, ingest.BufferedSpan{SpanID: fmt.Sprintf("span-%d", j), Op: "db.sql", Description: "SELECT name FROM users WHERE id=1", DurationMs: 10, StartTimestamp: at, Timestamp: at.Add(10 * time.Millisecond), Status: "ok"})
							}
						}
						txBuf.Push(transaction)
						logBuf.Push(ingest.BufferedLog{ProjectID: p.ID, Timestamp: at, Level: "info", Body: "mixed", Attributes: []byte(`{}`)})
					}
				}
			})
			defer func() { stopProducer(); producer.Wait() }()
			runReads(t, pool, true, func() int { return buf.Stats().Queued + txBuf.Stats().Queued + logBuf.Stats().Queued }, profileID)
			stopProducer()
			producer.Wait()
			expected := int64(buf.Stats().Accepted)
			require.Zero(t, buf.Stats().Rejected+txBuf.Stats().Rejected+logBuf.Stats().Rejected)
			// Let in-flight batches and grouping finish before canceling workers.
			require.Eventually(t, func() bool {
				var e, tx, l, ungrouped, detections int64
				err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM events WHERE event_id LIKE 'mixed-%'),(SELECT count(*) FROM transactions WHERE transaction='mixed'),(SELECT count(*) FROM logs WHERE body='mixed'),(SELECT count(*) FROM events WHERE issue_id IS NULL),(SELECT count(*) FROM perf_events)`).Scan(&e, &tx, &l, &ungrouped, &detections)
				return err == nil && e == expected && tx == int64(txBuf.Stats().Accepted) && l == int64(logBuf.Stats().Accepted) && ungrouped == 0 && detections == (int64(txBuf.Stats().Accepted)+9)/10
			}, 15*time.Second, 100*time.Millisecond)
			t.Logf("retention_duration=%s accepted_per_kind=%d rejected=0 grouping_backlog_after_drain=0", <-retained, expected)
			stop()
			workers.Wait()
			t.Logf("peak_sampled_grouping_backlog=%d oldest_pending_age=%.2fms", peakBacklog, peakAgeMs)
			var lag50, lag95 float64
			require.NoError(t, pool.QueryRow(ctx, `SELECT percentile_cont(0.5) WITHIN GROUP(ORDER BY extract(epoch FROM received_at-timestamp)*1000),percentile_cont(0.95) WITHIN GROUP(ORDER BY extract(epoch FROM received_at-timestamp)*1000) FROM events WHERE event_id LIKE 'mixed-%'`).Scan(&lag50, &lag95))
			t.Logf("event_receipt_lag p50=%.2fms p95=%.2fms", lag50, lag95)
			for _, table := range []string{"events", "transactions", "logs"} {
				var raw, summary, expired int64
				require.NoError(t, pool.QueryRow(ctx, "SELECT count(*),count(*) FILTER(WHERE received_at<now()-interval '30 days') FROM "+table).Scan(&raw, &expired))
				require.NoError(t, pool.QueryRow(ctx, "SELECT coalesce(sum(n),0) FROM telemetry_usage WHERE kind=$1", table).Scan(&summary))
				require.Equal(t, raw, summary)
				require.Zero(t, expired)
			}
			var mismatches int
			require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM issues i WHERE kind='error' AND event_count<>(SELECT count(*) FROM events WHERE issue_id=i.id)`).Scan(&mismatches))
			require.Zero(t, mismatches)
			checkCancellation(t, pool)
		})
	}
}

func runReads(t *testing.T, pool *pgxpool.Pool, mixed bool, queue func() int, profileID string) {
	t.Helper()
	start := pool.Stat()
	until := time.Now().Add(8 * time.Second)
	var wg sync.WaitGroup
	var mu sync.Mutex
	samples := map[string][]time.Duration{}
	errors := make(chan error, 10000)
	maxQueue := 0
	for worker := 0; worker < 6; worker++ {
		wg.Go(func() {
			for i := worker; time.Now().Before(until); i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				at := time.Now()
				var err error
				name := ""
				switch i % 6 {
				case 0:
					name = "usage"
					_, err = storage.CountMonthlyEvents(ctx, pool)
				case 1:
					name = "transactions"
					_, err = storage.ListAllTransactions(ctx, pool, storage.TransactionFilter{Limit: 50})
				case 2:
					name = "logs"
					_, _, err = storage.ListLogs(ctx, pool, storage.LogFilter{Limit: 50})
				case 3:
					name = "dashboard"
					_, err = storage.ListDashboardIssues(ctx, pool, nil)
				case 4:
					name = "analytics"
					_, err = storage.ListTransactionSummaries(ctx, pool, nil, 24, 0, "", "", "", "", "")
				case 5:
					name = "profile"
					_, err = profiles.FlameGraphForTransaction(ctx, pool, profileID)
				}
				cancel()
				mu.Lock()
				samples[name] = append(samples[name], time.Since(at))
				if queue != nil {
					maxQueue = max(maxQueue, queue())
				}
				mu.Unlock()
				if err != nil {
					errors <- err
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	for _, name := range []string{"usage", "transactions", "logs", "dashboard", "analytics", "profile"} {
		values := samples[name]
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		t.Logf("mixed=%v query=%s samples=%d p50=%s p95=%s max=%s", mixed, name, len(values), values[len(values)/2], values[(len(values)-1)*95/100], values[len(values)-1])
	}
	end := pool.Stat()
	t.Logf("mixed=%v acquires=%d empty_acquires=%d total_pool_wait=%s canceled_acquires=%d max_observed_queue=%d", mixed, end.AcquireCount()-start.AcquireCount(), end.EmptyAcquireCount()-start.EmptyAcquireCount(), end.EmptyAcquireWaitTime()-start.EmptyAcquireWaitTime(), end.CanceledAcquireCount()-start.CanceledAcquireCount(), maxQueue)
}

func checkCancellation(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var held []*pgxpool.Conn
	for range pool.Config().MaxConns {
		conn, err := pool.Acquire(ctx)
		require.NoError(t, err)
		held = append(held, conn)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
	at := time.Now()
	_, err := storage.CountMonthlyEvents(waitCtx, pool)
	cancel()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	t.Logf("saturated_pool_cancellation=%s", time.Since(at))
	for _, conn := range held {
		conn.Release()
	}
	queryCtx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
	at = time.Now()
	_, err = pool.Exec(queryCtx, "SELECT pg_sleep(10)")
	cancel()
	require.Error(t, err)
	t.Logf("running_query_cancellation=%s", time.Since(at))
	require.NoError(t, pool.Ping(ctx))
}

func seedProfile(t *testing.T, pool *pgxpool.Pool, project string) string {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	p := &ingest.Profile{Format: ingest.ProfileFormatV1, Frames: []ingest.ProfileFrame{{Function: "request"}}, Stacks: [][]int32{{0}}, ActiveThreadID: "1", StartNs: now.UnixNano(), EndNs: now.Add(20 * time.Second).UnixNano()}
	for i := 0; i < 20000; i++ {
		p.Samples = append(p.Samples, ingest.ProfileSample{ThreadID: "1", StackID: 0, TimestampNs: now.Add(time.Duration(i) * time.Millisecond).UnixNano()})
	}
	data, encoding, err := ingest.EncodeProfile(p)
	require.NoError(t, err)
	var id string
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO transactions(project_id,event_id,transaction,start_timestamp,timestamp,duration_ms) VALUES($1,'profile-fixture','profile',now(),now(),20000) RETURNING id`, project).Scan(&id))
	_, err = pool.Exec(ctx, `INSERT INTO profile_chunks(project_id,format,transaction_event_id,start_ts,end_ts,sample_count,size_bytes,encoding,data) VALUES($1,1,'profile-fixture',now(),now()+interval '20 seconds',20000,$2,$3,$4)`, project, len(data), encoding, data)
	require.NoError(t, err)
	return id
}
