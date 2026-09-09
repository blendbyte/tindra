package ingest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
)

func TestLargeFlushesUseConsistentProjectLockOrder(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := testutil.SetupDB(ctx)
	defer cleanup()
	a, err := storage.CreateProject(ctx, pool, "order-a", "A")
	require.NoError(t, err)
	b, err := storage.CreateProject(ctx, pool, "order-b", "B")
	require.NoError(t, err)
	// Pause after each statement's usage locks, making opposite chunk orders
	// overlap reliably. This trigger exists only in the disposable fixture.
	_, err = pool.Exec(ctx, `CREATE FUNCTION pause_flush() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.2); RETURN NULL; END $$; CREATE TRIGGER z_pause_flush AFTER INSERT ON events FOR EACH STATEMENT EXECUTE FUNCTION pause_flush()`)
	require.NoError(t, err)
	ready := make(chan struct{})
	var wg sync.WaitGroup
	for _, ids := range [][]string{{a.ID, b.ID}, {b.ID, a.ID}} {
		wg.Go(func() {
			var batch []BufferedEvent
			for _, id := range ids {
				for range 1000 {
					batch = append(batch, BufferedEvent{ProjectID: id, Timestamp: time.Now(), Payload: []byte(`{}`)})
				}
			}
			<-ready
			writeBatch(ctx, pool, batch)
		})
	}
	close(ready)
	wg.Wait()
	var count, usage int
	require.NoError(t, pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM events),(SELECT coalesce(sum(n),0) FROM telemetry_usage WHERE kind='events')`).Scan(&count, &usage))
	require.Equal(t, 4000, count, "neither multi-statement flush may be lost to opposite project lock ordering")
	require.Equal(t, count, usage)
}
