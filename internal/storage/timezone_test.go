package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

// A server whose database defaults to another time zone must still bucket in
// UTC, or the issue chart's day buckets never line up with the UTC gap filler.
func TestConfigurePool_pinsUTCOnNonUTCServer(t *testing.T) {
	ctx := context.Background()
	dbName := testPool.Config().ConnConfig.Database
	_, err := testPool.Exec(ctx, `ALTER DATABASE "`+dbName+`" SET timezone = 'Asia/Taipei'`)
	require.NoError(t, err)
	t.Cleanup(func() { testPool.Exec(context.Background(), `ALTER DATABASE "`+dbName+`" RESET timezone`) })

	newPool := func(configure bool) *pgxpool.Pool {
		cfg := testPool.Config().Copy()
		cfg.ConnConfig.RuntimeParams = map[string]string{}
		if configure {
			storage.ConfigurePool(cfg)
		}
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		require.NoError(t, err)
		t.Cleanup(pool.Close)
		return pool
	}
	zone := func(pool *pgxpool.Pool) string {
		var tz string
		require.NoError(t, pool.QueryRow(ctx, `SHOW timezone`).Scan(&tz))
		return tz
	}
	require.Equal(t, "Asia/Taipei", zone(newPool(false)), "precondition: the database default is not UTC")
	pool := newPool(true)
	require.Equal(t, "UTC", zone(pool))

	// An issue first seen 10 days ago is charted in daily buckets.
	project, _ := setupProjectAndEvent(t)
	firstSeen := time.Now().UTC().AddDate(0, 0, -10)
	issue, _, _, err := storage.UpsertIssue(ctx, testPool, project.ID, "fp-tz-chart", "TZ", "error", "error", "", "", firstSeen)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, NOW(), NOW(), '{}'::jsonb, 'fp-tz-chart', $2)`, project.ID, issue.ID)
	require.NoError(t, err)

	hist, err := storage.GetIssueHistogram(ctx, pool, issue.ID, firstSeen)
	require.NoError(t, err)
	var total int64
	for _, b := range hist.Buckets {
		total += b.Count
	}
	require.EqualValues(t, 1, total, "the event must land in a bucket")
}
