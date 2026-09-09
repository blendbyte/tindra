package storage_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestTelemetryUsageLifecycle(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "usage-one", "One")
	require.NoError(t, err)
	q, err := storage.CreateProject(ctx, testPool, "usage-two", "Two")
	require.NoError(t, err)
	for _, table := range []string{"events", "transactions", "logs"} {
		var sql string
		switch table {
		case "events":
			sql = `INSERT INTO events(project_id,timestamp,received_at,payload,event_id) SELECT $1, now(), instant, '{}', i::text FROM unnest($2::timestamptz[]) WITH ORDINALITY AS v(instant,i)`
		case "transactions":
			sql = `INSERT INTO transactions(project_id,start_timestamp,timestamp,received_at,transaction,duration_ms) SELECT $1,now(),now(),instant,'test',1 FROM unnest($2::timestamptz[]) AS v(instant)`
		case "logs":
			sql = `INSERT INTO logs(project_id,timestamp,received_at,body) SELECT $1,now(),instant,'test' FROM unnest($2::timestamptz[]) AS v(instant)`
		}
		cutoff := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
		times := []time.Time{cutoff.Add(-time.Nanosecond * 1000), cutoff, cutoff.Add(time.Hour), cutoff.Add(90 * time.Minute), cutoff.AddDate(0, 1, 0)}
		_, err = testPool.Exec(ctx, sql, p.ID, times)
		require.NoError(t, err)
	}
	assertUsageParity(t)
	_, err = testPool.Exec(ctx, `INSERT INTO events(project_id,event_id,timestamp,payload) VALUES($1,'1',now(),'{}') ON CONFLICT(project_id,event_id) WHERE event_id IS NOT NULL DO NOTHING`, p.ID)
	require.NoError(t, err)
	assertUsageParity(t)
	// The SQL function must retain exact inclusive/exclusive and timezone cutoffs.
	for _, zone := range []string{"UTC", "Asia/Kathmandu", "America/New_York"} {
		conn, err := testPool.Acquire(ctx)
		require.NoError(t, err)
		_, err = conn.Exec(ctx, `SELECT set_config('TimeZone',$1,false)`, zone)
		require.NoError(t, err)
		for _, inclusive := range []bool{true, false} {
			for _, cutoff := range []string{"2026-09-01 00:00:00", "2026-09-01 00:15:00", "2026-09-01 01:30:00"} {
				var got, want int64
				err = conn.QueryRow(ctx, `SELECT
     (SELECT COALESCE(sum(n),0) FROM telemetry_usage_since($1::timestamptz,$2,ARRAY[$3::uuid]) WHERE kind='events'),
     (SELECT count(*) FROM events WHERE project_id=$3 AND received_at >= $1::timestamptz AND ($2 OR received_at > $1::timestamptz))`, cutoff, inclusive, p.ID).Scan(&got, &want)
				require.NoError(t, err)
				require.Equal(t, want, got, "%s %s inclusive=%v", zone, cutoff, inclusive)
			}
		}
		_, err = conn.Exec(ctx, `RESET TimeZone`)
		require.NoError(t, err)
		conn.Release()
	}
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `DELETE FROM events`)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback(ctx))
	assertUsageParity(t)
	for _, table := range []string{"events", "transactions", "logs"} {
		_, err = testPool.Exec(ctx, fmt.Sprintf(`UPDATE %s SET project_id=$1,received_at=received_at+interval '2 hours'`, table), q.ID)
		require.NoError(t, err)
	}
	assertUsageParity(t)
	deleted, err := storage.DeleteProjectByID(ctx, testPool, q.ID)
	require.NoError(t, err)
	require.True(t, deleted)
	assertUsageParity(t)
	_, err = testPool.Exec(ctx, `INSERT INTO events(project_id,timestamp,payload) VALUES($1,now(),'{}')`, p.ID)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `TRUNCATE events CASCADE`)
	require.NoError(t, err)
	assertUsageParity(t)
}

func assertUsageParity(t *testing.T) {
	t.Helper()
	var differences int
	err := testPool.QueryRow(context.Background(), `WITH expected AS (
 SELECT 'events' AS kind,project_id,date_trunc('hour',received_at,'UTC') AS bucket,count(*) AS n FROM events GROUP BY 2,3
 UNION ALL SELECT 'transactions',project_id,date_trunc('hour',received_at,'UTC'),count(*) FROM transactions GROUP BY 2,3
 UNION ALL SELECT 'logs',project_id,date_trunc('hour',received_at,'UTC'),count(*) FROM logs GROUP BY 2,3
 ), differences AS ((SELECT * FROM expected EXCEPT SELECT * FROM telemetry_usage) UNION ALL (SELECT * FROM telemetry_usage EXCEPT SELECT * FROM expected))
 SELECT count(*) FROM differences`).Scan(&differences)
	require.NoError(t, err)
	require.Zero(t, differences)
}

func TestTelemetryUsageConcurrentWriters(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "usage-concurrent", "Concurrent")
	require.NoError(t, err)
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for worker := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 10 {
				_, err := testPool.Exec(ctx, `INSERT INTO events(project_id,event_id,timestamp,payload) SELECT $1, $2 || '-' || i, now(), '{}' FROM generate_series(1,10) i ON CONFLICT(project_id,event_id) WHERE event_id IS NOT NULL DO NOTHING`, p.ID, fmt.Sprint(worker))
				if err != nil {
					failures <- err
					return
				}
				if i%2 == 0 {
					_, err = testPool.Exec(ctx, `DELETE FROM events WHERE project_id=$1 AND event_id LIKE $2`, p.ID, fmt.Sprint(worker)+"-%")
					if err != nil {
						failures <- err
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	assertUsageParity(t)
}
