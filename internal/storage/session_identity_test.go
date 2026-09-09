package storage_test

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

type identityQueryCount struct{ queries int }

func (c *identityQueryCount) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.queries++
	return ctx
}
func (*identityQueryCount) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func identityFixture(t *testing.T) (*storage.User, *storage.Session) {
	t.Helper()
	truncateUsers(t)
	ctx := context.Background()
	u, err := storage.CreateUser(ctx, testPool, "identity@example.com", "password12345")
	require.NoError(t, err)
	s, err := storage.CreateSession(ctx, testPool, u.ID)
	require.NoError(t, err)
	return u, s
}
func TestSessionIdentityCurrentPermissionsAndSingleQuery(t *testing.T) {
	u, s := identityFixture(t)
	ctx := context.Background()
	trace := &identityQueryCount{}
	cfg := testPool.Config()
	cfg.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()
	for _, perms := range []storage.UserPermissions{
		{}, {ManageProjects: true}, {ManageUsers: true}, {ManageAlerts: true}, {ManageIssues: true},
	} {
		_, err := storage.UpdateUserPermissions(ctx, testPool, u.ID, perms)
		require.NoError(t, err)
		before := trace.queries
		identity, err := storage.GetSessionIdentity(ctx, pool, s.Token)
		require.NoError(t, err)
		require.Equal(t, &storage.SessionIdentity{UserID: u.ID, Permissions: perms}, identity)
		require.Equal(t, 1, trace.queries-before)
	}
}
func TestSessionIdentityInvalidExpiredRevokedAndDeleted(t *testing.T) {
	for _, kind := range []string{"unknown", "expired", "revoked", "deleted_user"} {
		t.Run(kind, func(t *testing.T) {
			u, s := identityFixture(t)
			ctx := context.Background()
			token := s.Token
			switch kind {
			case "unknown":
				token = "not-a-session"
			case "expired":
				_, err := testPool.Exec(ctx, "UPDATE sessions SET expires_at=NOW()-interval '1 second' WHERE user_id=$1", u.ID)
				require.NoError(t, err)
			case "revoked":
				require.NoError(t, storage.DeleteSession(ctx, testPool, s.Token))
			case "deleted_user":
				_, err := testPool.Exec(ctx, "DELETE FROM users WHERE id=$1", u.ID)
				require.NoError(t, err)
			}
			identity, err := storage.GetSessionIdentity(ctx, testPool, token)
			require.NoError(t, err)
			require.Nil(t, identity)
		})
	}
}
func TestSessionIdentityCancelled(t *testing.T) {
	_, s := identityFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	identity, err := storage.GetSessionIdentity(ctx, testPool, s.Token)
	require.Error(t, err)
	require.Nil(t, identity)
}

// Opt in: TINDRA_SESSION_IDENTITY_PERF=1 go test -v ./internal/storage
// -run '^TestSessionIdentityPerformance$' -count=1
func TestSessionIdentityPerformance(t *testing.T) {
	if os.Getenv("TINDRA_SESSION_IDENTITY_PERF") != "1" {
		t.Skip("set TINDRA_SESSION_IDENTITY_PERF=1")
	}
	u, s := identityFixture(t)
	ctx := context.Background()
	for _, combined := range []bool{false, true} {
		var timings []float64
		for run := 0; run < 4; run++ {
			start := time.Now()
			for i := 0; i < 1000; i++ {
				if combined {
					identity, err := storage.GetSessionIdentity(ctx, testPool, s.Token)
					require.NoError(t, err)
					require.Equal(t, u.ID, identity.UserID)
					require.Equal(t, u.Permissions, identity.Permissions)
				} else {
					session, err := storage.GetSession(ctx, testPool, s.Token)
					require.NoError(t, err)
					user, err := storage.GetUserByID(ctx, testPool, session.UserID)
					require.NoError(t, err)
					require.Equal(t, u.ID, user.ID)
					require.Equal(t, u.Permissions, user.Permissions)
				}
			}
			if run > 0 {
				timings = append(timings, float64(time.Since(start).Microseconds())/1000)
			}
		}
		sort.Float64s(timings)
		t.Logf("combined=%v 1000 lookups %.3f ms; per lookup %.3f ms", combined, timings[1], timings[1]/1000)
	}
}
