package issues

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/ingest"
)

func TestGrouperEmptyUnavailableAndCancelledSweeps(t *testing.T) {
	pool, _ := schedulingDB(t)
	ctx := context.Background()
	g := NewGrouper(pool)
	empty := g.RunOnce(ctx)
	require.Zero(t, empty.Observed)
	require.Zero(t, empty.Failed)
	require.Nil(t, empty.next)
	closed, err := pgxpool.NewWithConfig(ctx, pool.Config())
	require.NoError(t, err)
	closed.Close()
	unavailable := NewGrouper(closed)
	failed := unavailable.RunOnce(ctx)
	require.Equal(t, 1, failed.Failed)
	require.Zero(t, failed.Grouped)
	cursor := &groupingCursor{through: &rawEvent{ID: "00000000-0000-0000-0000-000000000001", ReceivedAt: time.Now()}}
	failed = unavailable.runPass(ctx, cursor)
	require.Equal(t, 1, failed.Failed)
	require.Same(t, cursor, failed.next)
	timed, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); unavailable.Run(timed) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("grouper ignored cancellation while idle")
	}
}

func TestGrouperAuxiliaryFailuresDoNotLoseEvents(t *testing.T) {
	pool, project := schedulingDB(t)
	ctx := context.Background()
	g := NewGrouper(pool)
	_, err := pool.Exec(ctx, `CREATE FUNCTION reject_auxiliary() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'auxiliary unavailable'; END $$;
 CREATE TRIGGER reject_tags BEFORE INSERT ON event_tags FOR EACH ROW EXECUTE FUNCTION reject_auxiliary();
 CREATE TRIGGER reject_history BEFORE INSERT ON issue_history FOR EACH ROW EXECUTE FUNCTION reject_auxiliary()`)
	require.NoError(t, err)
	payload := json.RawMessage(`{"message":"same failure","tags":{"area":"billing"}}`)
	var issue string
	for i := 0; i < 2; i++ {
		e := rawEvent{ProjectID: project, Timestamp: time.Now(), Payload: payload}
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO events(project_id,timestamp,payload) VALUES($1,now(),$2) RETURNING id`, project, payload).Scan(&e.ID))
		grouped, err := g.group(ctx, e)
		require.NoError(t, err)
		require.True(t, grouped)
		require.NoError(t, pool.QueryRow(ctx, "SELECT issue_id FROM events WHERE id=$1", e.ID).Scan(&issue))
		var count int
		require.NoError(t, pool.QueryRow(ctx, "SELECT event_count FROM issues WHERE id=$1", issue).Scan(&count))
		require.Equal(t, i+1, count)
		if i == 0 {
			_, err = pool.Exec(ctx, "UPDATE issues SET status='resolved' WHERE id=$1", issue)
			require.NoError(t, err)
		}
	}
	var status string
	require.NoError(t, pool.QueryRow(ctx, "SELECT status FROM issues WHERE id=$1", issue).Scan(&status))
	require.Equal(t, "regressed", status)
	grouped, err := g.group(ctx, rawEvent{ID: "00000000-0000-0000-0000-000000000001", Payload: payload})
	require.NoError(t, err)
	require.False(t, grouped)
}

func TestN1CancellationBeforeBatchAndSpanScan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	detector := NewN1Detector(nil)
	tx := ingest.BufferedTransaction{Spans: make([]ingest.BufferedSpan, 5)}
	detector.ProcessBatch(ctx, nil, []ingest.BufferedTransaction{tx}, []string{"tx"})
	detector.detectTx(ctx, tx, "tx")
}

type cancelLinkContext struct{ cancel context.CancelFunc }

func (c cancelLinkContext) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, "UPDATE events SET fingerprint") {
		c.cancel()
	}
	return ctx
}
func (cancelLinkContext) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestGrouperCancelledPageLeavesEventsForNextSweep(t *testing.T) {
	pool, project := schedulingDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO events(project_id,timestamp,payload) SELECT $1,now(),'{"message":"retry"}' FROM generate_series(1,2)`, project)
	require.NoError(t, err)
	cancelled, cancel := context.WithCancel(ctx)
	defer cancel()
	cfg := pool.Config()
	cfg.ConnConfig.Tracer = cancelLinkContext{cancel: cancel}
	interrupted, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer interrupted.Close()
	stats := NewGrouper(interrupted).RunOnce(cancelled)
	require.ErrorIs(t, cancelled.Err(), context.Canceled)
	require.Equal(t, 1, stats.Observed)
	require.Equal(t, 1, stats.Failed)
	require.Zero(t, stats.Grouped)
	// A cancelled continuation must not move its cursor further.
	cursor := stats.next
	require.NotNil(t, cursor)
	stopped := NewGrouper(pool).runPass(cancelled, cursor)
	require.Same(t, cursor, stopped.next)
	require.Zero(t, stopped.Observed)
	g := NewGrouper(pool)
	retry := finishSweep(t, g, g.RunOnce(ctx))
	require.Equal(t, 2, retry.Grouped)
	// The completed ceiling can be revisited safely when there is no work left.
	complete := g.runPass(ctx, cursor)
	require.Zero(t, complete.Observed)
	require.Nil(t, complete.next)
}
