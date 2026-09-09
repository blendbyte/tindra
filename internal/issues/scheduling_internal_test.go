package issues

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
)

func schedulingDB(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	pool, cleanup := testutil.SetupDB(context.Background())
	t.Cleanup(cleanup)
	p, err := storage.CreateProject(context.Background(), pool, "scheduling", "Scheduling")
	require.NoError(t, err)
	return pool, p.ID
}

// Finish one bounded sweep, preserving its cursor across scheduling budgets.
// A slow database may require several passes even for fewer than 200 events.
func finishSweep(t *testing.T, g *Grouper, stats GroupingStats) GroupingStats {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	total := stats
	for stats.next != nil {
		require.NoError(t, ctx.Err(), "grouping sweep did not finish")
		stats = g.runPass(ctx, stats.next)
		total.Observed += stats.Observed
		total.Grouped += stats.Grouped
		total.Failed += stats.Failed
		total.Batches += stats.Batches
	}
	return total
}

func TestGrouperScheduling(t *testing.T) {
	pool, project := schedulingDB(t)
	ctx := context.Background()
	seed := func(n int) {
		_, err := pool.Exec(ctx, `TRUNCATE events,issues CASCADE`)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `INSERT INTO events(id,project_id,timestamp,payload) SELECT lpad(to_hex(i),32,'0')::uuid,$1,now(),'{"message":"same"}' FROM generate_series(1,$2) i`, project, n)
		require.NoError(t, err)
	}
	t.Run("drains successive pages and preserves counts", func(t *testing.T) {
		seed(450)
		g := NewGrouper(pool)
		stats := finishSweep(t, g, g.RunOnce(ctx))
		require.Equal(t, 450, stats.Observed)
		require.Equal(t, 450, stats.Grouped)
		require.Zero(t, stats.Failed)
		require.Greater(t, stats.Batches, 1)
		var count int
		require.NoError(t, pool.QueryRow(ctx, "SELECT event_count FROM issues").Scan(&count))
		require.Equal(t, 450, count)
	})
	t.Run("budgets preserve progress across passes", func(t *testing.T) {
		seed(2101)
		g := NewGrouper(pool)
		stats := g.RunOnce(ctx)
		require.True(t, stats.BudgetReached)
		require.LessOrEqual(t, stats.Observed, grouperPassLimit)
		require.NotNil(t, stats.next)
		stats = finishSweep(t, g, stats)
		require.Equal(t, 2101, stats.Grouped)
		require.Zero(t, stats.Failed)
	})
	t.Run("continues after failures and retries on the next sweep", func(t *testing.T) {
		seed(250)
		_, err := pool.Exec(ctx, `CREATE FUNCTION fail_grouping() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id <= lpad(to_hex(205),32,'0')::uuid THEN RAISE EXCEPTION 'injected'; END IF; RETURN NEW; END $$;
  CREATE TRIGGER fail_grouping BEFORE UPDATE OF issue_id ON events FOR EACH ROW EXECUTE FUNCTION fail_grouping()`)
		require.NoError(t, err)
		defer pool.Exec(ctx, `DROP TRIGGER IF EXISTS fail_grouping ON events; DROP FUNCTION fail_grouping()`)
		g := NewGrouper(pool)
		stats := finishSweep(t, g, g.RunOnce(ctx))
		require.Equal(t, 205, stats.Failed)
		require.Equal(t, 45, stats.Grouped)
		_, err = pool.Exec(ctx, "DROP TRIGGER fail_grouping ON events")
		require.NoError(t, err)
		stats = finishSweep(t, g, g.RunOnce(ctx))
		require.Equal(t, 205, stats.Grouped)
	})
	t.Run("fixed sweep ceiling leaves later arrivals for the next sweep", func(t *testing.T) {
		seed(1)
		cursor := &groupingCursor{through: &rawEvent{}}
		require.NoError(t, pool.QueryRow(ctx, "SELECT id,received_at FROM events").Scan(&cursor.through.ID, &cursor.through.ReceivedAt))
		_, err := pool.Exec(ctx, `INSERT INTO events(project_id,timestamp,received_at,payload) VALUES($1,now(),now()+interval '1 day','{"message":"later"}')`, project)
		require.NoError(t, err)
		stats := NewGrouper(pool).runPass(ctx, cursor)
		require.Equal(t, 1, stats.Grouped)
		require.Nil(t, stats.next)
		stats = NewGrouper(pool).RunOnce(ctx)
		require.Equal(t, 1, stats.Grouped)
	})
	t.Run("concurrent groupers do not inflate issue counts", func(t *testing.T) {
		seed(100)
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				g := NewGrouper(pool)
				finishSweep(t, g, g.RunOnce(ctx))
			}()
		}
		wg.Wait()
		var count int
		require.NoError(t, pool.QueryRow(ctx, "SELECT event_count FROM issues").Scan(&count))
		require.Equal(t, 100, count)
	})
	t.Run("cancellation releases a blocked event", func(t *testing.T) {
		seed(1)
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer tx.Rollback(ctx)
		_, err = tx.Exec(ctx, "SELECT id FROM events FOR UPDATE")
		require.NoError(t, err)
		timed, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		stats := NewGrouper(pool).RunOnce(timed)
		require.Less(t, stats.Duration, time.Second)
		require.ErrorIs(t, timed.Err(), context.DeadlineExceeded)
	})
}
