package retention_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/retention"
)

type tokenCleanupTrace struct {
	capTrace
	table      string
	selections int
}

func (c *tokenCleanupTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(d.SQL, "SELECT token_hash FROM "+c.table) {
		c.selections++
	}
	if strings.HasPrefix(d.SQL, "DELETE FROM "+c.table+" WHERE token_hash = ANY") {
		return context.WithValue(ctx, capTraceKey{}, time.Now())
	}
	return ctx
}
func tokenPool(t *testing.T, trace *tokenCleanupTrace) *pgxpool.Pool {
	t.Helper()
	cfg := testPool.Config()
	cfg.ConnConfig.Tracer = trace
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(p.Close)
	return p
}
func seedTokenBacklog(t *testing.T, table string, expired, fresh int) {
	t.Helper()
	ctx := context.Background()
	_, err := testPool.Exec(ctx, "TRUNCATE oauth_states,mfa_challenges")
	require.NoError(t, err)
	t.Cleanup(func() { _, e := testPool.Exec(ctx, "TRUNCATE oauth_states,mfa_challenges"); require.NoError(t, e) })
	if table == "oauth_states" {
		_, err = testPool.Exec(ctx, `INSERT INTO oauth_states(token_hash,provider,verifier,expires_at)
 SELECT repeat(md5(i::text),2),'github','test',CASE WHEN i <= $1 THEN NOW()-interval '1 day' ELSE NOW()+interval '1 day' END FROM generate_series(1,$2::int) i`, expired, expired+fresh)
	} else {
		var user string
		require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO users(email,password_hash) VALUES ('token-budget@example.com','x') RETURNING id`).Scan(&user))
		t.Cleanup(func() { _, e := testPool.Exec(ctx, "DELETE FROM users WHERE id=$1", user); require.NoError(t, e) })
		_, err = testPool.Exec(ctx, `INSERT INTO mfa_challenges(token_hash,user_id,expires_at)
 SELECT repeat(md5(i::text),2),$1,CASE WHEN i <= $2 THEN NOW()-interval '1 day' ELSE NOW()+interval '1 day' END FROM generate_series(1,$3::int) i`, user, expired, expired+fresh)
	}
	require.NoError(t, err)
}
func TestWorkerTokenBatchesPreserveFreshAndExtendedTokens(t *testing.T) {
	for _, table := range []string{"oauth_states", "mfa_challenges"} {
		t.Run(table, func(t *testing.T) {
			seedTokenBacklog(t, table, 12000, 7)
			ctx := context.Background()
			trace := &tokenCleanupTrace{table: table}
			trace.afterBatch = func() {
				trace.afterBatch = nil
				// Extend all remaining selected tokens. Later batches must recheck expiry.
				_, err := testPool.Exec(ctx, "UPDATE "+table+" SET expires_at=NOW()+interval '1 day' WHERE expires_at < NOW()")
				require.NoError(t, err)
			}
			retention.NewWorker(tokenPool(t, trace), 90).RunOnce(ctx)
			require.Equal(t, 1, trace.selections)
			require.Equal(t, []int64{5000, 0, 0}, trace.batches)
			require.Equal(t, 7007, capCount(t, table))
		})
	}
}
func TestWorkerTokenCancellationResumes(t *testing.T) {
	for _, table := range []string{"oauth_states", "mfa_challenges"} {
		t.Run(table, func(t *testing.T) {
			seedTokenBacklog(t, table, 12000, 7)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			trace := &tokenCleanupTrace{table: table}
			trace.cancel = cancel
			retention.NewWorker(tokenPool(t, trace), 90).RunOnce(ctx)
			require.Equal(t, []int64{5000}, trace.batches)
			require.Equal(t, 7007, capCount(t, table))
			retention.NewWorker(testPool, 90).RunOnce(context.Background())
			require.Equal(t, 7, capCount(t, table))
		})
	}
}
func TestWorkerTokenPassBudget(t *testing.T) {
	seedTokenBacklog(t, "oauth_states", 200001, 7)
	ctx := context.Background()
	trace := &tokenCleanupTrace{table: "oauth_states"}
	worker := retention.NewWorker(tokenPool(t, trace), 90)
	worker.RunOnce(ctx)
	require.Equal(t, 1, trace.selections)
	require.Len(t, trace.batches, 40)
	for _, n := range trace.batches {
		require.EqualValues(t, 5000, n)
	}
	require.Equal(t, 8, capCount(t, "oauth_states"))
	worker.RunOnce(ctx)
	require.Equal(t, 7, capCount(t, "oauth_states"))
}
