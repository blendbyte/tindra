package storage_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func groupEventFixture(t *testing.T, n int) []string {
	t.Helper()
	truncateProjects(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "atomic-group", "Atomic grouping")
	require.NoError(t, err)
	rows, err := testPool.Query(ctx, `INSERT INTO events(project_id,timestamp,payload) SELECT $1,NOW(),'{}' FROM generate_series(1,$2::int) RETURNING id`, p.ID, n)
	require.NoError(t, err)
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return ids
}
func groupOne(ctx context.Context, pool *pgxpool.Pool, id string) (*storage.Issue, bool, bool, error) {
	return storage.GroupEvent(ctx, pool, id, "same-fingerprint", "Atomic error", "error", "production", "v1")
}
func TestGroupEventConcurrentExactlyOnce(t *testing.T) {
	for _, sameEvent := range []bool{true, false} {
		t.Run(map[bool]string{true: "same_event", false: "same_fingerprint"}[sameEvent], func(t *testing.T) {
			n := 12
			seedN := n
			if sameEvent {
				seedN = 1
			}
			ids := groupEventFixture(t, seedN)
			start := make(chan struct{})
			type result struct {
				issue   *storage.Issue
				created bool
				err     error
			}
			results := make(chan result, n)
			for i := 0; i < n; i++ {
				id := ids[i%len(ids)]
				go func() {
					<-start
					iss, created, _, err := groupOne(context.Background(), testPool, id)
					results <- result{iss, created, err}
				}()
			}
			close(start)
			successes, created := 0, 0
			for i := 0; i < n; i++ {
				r := <-results
				require.NoError(t, r.err)
				if r.issue != nil {
					successes++
				}
				if r.created {
					created++
				}
			}
			require.Equal(t, seedN, successes)
			require.Equal(t, 1, created)
			var count, linked, issues int
			require.NoError(t, testPool.QueryRow(context.Background(), `SELECT (SELECT event_count FROM issues),(SELECT count(*) FROM events WHERE issue_id IS NOT NULL),(SELECT count(*) FROM issues)`).Scan(&count, &linked, &issues))
			require.Equal(t, seedN, count)
			require.Equal(t, seedN, linked)
			require.Equal(t, 1, issues)
		})
	}
}
func TestGroupEventDeletedCandidate(t *testing.T) {
	id := groupEventFixture(t, 1)[0]
	_, err := testPool.Exec(context.Background(), "DELETE FROM events WHERE id=$1", id)
	require.NoError(t, err)
	issue, created, regressed, err := groupOne(context.Background(), testPool, id)
	require.NoError(t, err)
	require.Nil(t, issue)
	require.False(t, created)
	require.False(t, regressed)
	var n int
	require.NoError(t, testPool.QueryRow(context.Background(), "SELECT count(*) FROM issues").Scan(&n))
	require.Zero(t, n)
}
func TestGroupEventLinkFailureRollsBack(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new_issue", true: "existing_issue"}[existing], func(t *testing.T) {
			ids := groupEventFixture(t, 2)
			ctx := context.Background()
			if existing {
				iss, _, _, err := groupOne(ctx, testPool, ids[0])
				require.NoError(t, err)
				_, err = testPool.Exec(ctx, "UPDATE issues SET status='resolved' WHERE id=$1", iss.ID)
				require.NoError(t, err)
			}
			_, err := testPool.Exec(ctx, `CREATE FUNCTION reject_group_link() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected link failure'; END $$;
 CREATE TRIGGER reject_group_link BEFORE UPDATE OF issue_id ON events FOR EACH ROW EXECUTE FUNCTION reject_group_link()`)
			require.NoError(t, err)
			t.Cleanup(func() {
				_, e := testPool.Exec(ctx, "DROP TRIGGER reject_group_link ON events; DROP FUNCTION reject_group_link()")
				require.NoError(t, e)
			})
			issue, created, regressed, err := groupOne(ctx, testPool, ids[1])
			require.ErrorContains(t, err, "injected link failure")
			require.Nil(t, issue)
			require.False(t, created)
			require.False(t, regressed)
			var linked bool
			require.NoError(t, testPool.QueryRow(ctx, "SELECT issue_id IS NOT NULL FROM events WHERE id=$1", ids[1]).Scan(&linked))
			require.False(t, linked)
			if existing {
				var count int
				var status string
				require.NoError(t, testPool.QueryRow(ctx, "SELECT event_count,status FROM issues").Scan(&count, &status))
				require.Equal(t, 1, count)
				require.Equal(t, "resolved", status)
			} else {
				var issues, fps int
				require.NoError(t, testPool.QueryRow(ctx, "SELECT (SELECT count(*) FROM issues),(SELECT count(*) FROM issue_fingerprints)").Scan(&issues, &fps))
				require.Zero(t, issues)
				require.Zero(t, fps)
			}
		})
	}
}

type pauseGroupLink struct {
	reached, release chan struct{}
	once             sync.Once
}

func (p *pauseGroupLink) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, "UPDATE events SET fingerprint") {
		p.once.Do(func() { close(p.reached) })
		select {
		case <-p.release:
		case <-ctx.Done():
		}
	}
	return ctx
}
func (*pauseGroupLink) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func TestGroupEventCountAndLinkBecomeVisibleTogether(t *testing.T) {
	ids := groupEventFixture(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, _, err := groupOne(ctx, testPool, ids[0])
	require.NoError(t, err)
	pause := &pauseGroupLink{reached: make(chan struct{}), release: make(chan struct{})}
	cfg := testPool.Config()
	cfg.ConnConfig.Tracer = pause
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()
	// Release the callback before closing its pool, including on assertion failure.
	var release sync.Once
	defer release.Do(func() { close(pause.release) })
	done := make(chan error, 1)
	go func() { _, _, _, e := groupOne(ctx, pool, ids[1]); done <- e }()
	select {
	case <-pause.reached:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var count, linked int
	query := `SELECT (SELECT event_count FROM issues),(SELECT count(*) FROM events WHERE issue_id IS NOT NULL)`
	require.NoError(t, testPool.QueryRow(ctx, query).Scan(&count, &linked))
	require.Equal(t, 1, count)
	require.Equal(t, 1, linked)
	// The event remains locked while its count is being updated.
	_, err = testPool.Exec(ctx, "SELECT id FROM events WHERE id=$1 FOR UPDATE NOWAIT", ids[1])
	var lockErr *pgconn.PgError
	require.ErrorAs(t, err, &lockErr)
	require.Equal(t, "55P03", lockErr.Code)
	release.Do(func() { close(pause.release) })
	require.NoError(t, <-done)
	require.NoError(t, testPool.QueryRow(ctx, query).Scan(&count, &linked))
	require.Equal(t, 2, count)
	require.Equal(t, 2, linked)
}
