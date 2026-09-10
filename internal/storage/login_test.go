package storage_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func loginUser(t *testing.T, mfa bool) *storage.User {
	t.Helper()
	u, err := storage.CreateUser(t.Context(), testPool, uuid.NewString()+"@login.test", "old-password-1234")
	require.NoError(t, err)
	if mfa {
		require.NoError(t, storage.StoreMFASecret(t.Context(), testPool, u.ID, "secret"))
		require.NoError(t, storage.EnableMFA(t.Context(), testPool, u.ID))
		u.MFAEnabled = true
	}
	return u
}

func TestLoginRejectsChangedCredentials(t *testing.T) {
	for _, kind := range []string{"admin", "change", "recovery", "mfa", "deleted"} {
		for _, mfa := range []bool{false, true} {
			t.Run(kind+"/"+map[bool]string{false: "session", true: "challenge"}[mfa], func(t *testing.T) {
				u := loginUser(t, mfa)
				switch kind {
				case "admin":
					require.NoError(t, storage.AdminSetPassword(t.Context(), testPool, u.ID, "new-password-1234"))
				case "change":
					require.NoError(t, storage.ChangeUserPassword(t.Context(), testPool, u.ID, "old-password-1234", "new-password-1234"))
				case "recovery":
					token, err := storage.CreatePasswordResetToken(t.Context(), testPool, u.ID)
					require.NoError(t, err)
					_, err = storage.UsePasswordResetToken(t.Context(), testPool, token, "new-password-1234")
					require.NoError(t, err)
				case "mfa":
					if mfa {
						require.NoError(t, storage.DisableMFA(t.Context(), testPool, u.ID))
					} else {
						require.NoError(t, storage.EnableMFA(t.Context(), testPool, u.ID))
					}
				case "deleted":
					_, err := storage.DeleteUser(t.Context(), testPool, u.ID)
					require.NoError(t, err)
				}
				if mfa {
					token, err := storage.CreateAuthenticatedMFAChallenge(t.Context(), testPool, u)
					require.ErrorIs(t, err, storage.ErrAuthenticationChanged)
					require.Empty(t, token)
				} else {
					s, err := storage.CreateAuthenticatedSession(t.Context(), testPool, u)
					require.ErrorIs(t, err, storage.ErrAuthenticationChanged)
					require.Nil(t, s)
				}
			})
		}
	}
}

type pauseLoginInsert struct {
	reached, release chan struct{}
	once             sync.Once
}

func (p *pauseLoginInsert) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, "INSERT INTO sessions") || strings.Contains(d.SQL, "INSERT INTO mfa_challenges") {
		p.once.Do(func() {
			close(p.reached)
			select {
			case <-p.release:
			case <-ctx.Done():
			}
		})
	}
	return ctx
}
func (*pauseLoginInsert) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestResetRevokesConcurrentLogin(t *testing.T) {
	for _, kind := range []string{"password", "challenge", "mfa"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			u := loginUser(t, kind != "password")
			challenge := ""
			if kind == "mfa" {
				var err error
				challenge, err = storage.CreateAuthenticatedMFAChallenge(ctx, testPool, u)
				require.NoError(t, err)
			}
			pause := &pauseLoginInsert{reached: make(chan struct{}), release: make(chan struct{})}
			cfg := testPool.Config()
			cfg.ConnConfig.Tracer = pause
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			require.NoError(t, err)
			defer pool.Close()
			type result struct {
				token string
				err   error
			}
			done := make(chan result, 1)
			go func() {
				if kind == "challenge" {
					token, err := storage.CreateAuthenticatedMFAChallenge(ctx, pool, u)
					done <- result{token, err}
					return
				}
				var s *storage.Session
				var err error
				if kind == "mfa" {
					s, err = storage.CompleteMFALogin(ctx, pool, challenge, u.ID, "secret")
				} else {
					s, err = storage.CreateAuthenticatedSession(ctx, pool, u)
				}
				r := result{err: err}
				if s != nil {
					r.token = s.Token
				}
				done <- r
			}()
			select {
			case <-pause.reached:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			reset := make(chan error, 1)
			go func() { reset <- storage.AdminSetPassword(ctx, testPool, u.ID, "new-password-1234") }()
			// Observe the reset waiting on the user lock before allowing issuance.
			require.Eventually(t, func() bool {
				var waiting bool
				err := testPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%UPDATE users SET password_hash%')`).Scan(&waiting)
				return err == nil && waiting
			}, 5*time.Second, 10*time.Millisecond)
			close(pause.release)
			r := <-done
			require.NoError(t, r.err)
			require.NoError(t, <-reset)
			if kind == "challenge" {
				id, err := storage.GetMFAChallenge(ctx, testPool, r.token)
				require.NoError(t, err)
				require.Empty(t, id)
			} else {
				s, err := storage.GetSessionIdentity(ctx, testPool, r.token)
				require.NoError(t, err)
				require.Nil(t, s)
			}
		})
	}
}

func TestCompleteMFALoginRechecksAndRollsBack(t *testing.T) {
	for _, kind := range []string{"reset", "secret", "disabled", "expired", "wrong-user", "replay", "insert-failure"} {
		t.Run(kind, func(t *testing.T) {
			u := loginUser(t, true)
			token, err := storage.CreateAuthenticatedMFAChallenge(t.Context(), testPool, u)
			require.NoError(t, err)
			pool := testPool
			switch kind {
			case "reset":
				require.NoError(t, storage.AdminSetPassword(t.Context(), testPool, u.ID, "new-password-1234"))
			case "secret":
				require.NoError(t, storage.StoreMFASecret(t.Context(), testPool, u.ID, "replacement"))
			case "disabled":
				require.NoError(t, storage.DisableMFA(t.Context(), testPool, u.ID))
			case "expired":
				_, err = testPool.Exec(t.Context(), `UPDATE mfa_challenges SET expires_at=NOW()-interval '1 minute' WHERE user_id=$1`, u.ID)
				require.NoError(t, err)
			case "wrong-user":
				u = loginUser(t, true)
			case "replay":
				s, err := storage.CompleteMFALogin(t.Context(), pool, token, u.ID, "secret")
				require.NoError(t, err)
				require.NotNil(t, s)
			case "insert-failure":
				cfg := testPool.Config()
				cfg.ConnConfig.Tracer = &cancelGroupingQuery{match: "INSERT INTO sessions"}
				pool, err = pgxpool.NewWithConfig(t.Context(), cfg)
				require.NoError(t, err)
				defer pool.Close()
			}
			s, err := storage.CompleteMFALogin(t.Context(), pool, token, u.ID, "secret")
			require.Error(t, err)
			require.Nil(t, s)
			if kind == "insert-failure" {
				id, err := storage.GetMFAChallenge(t.Context(), testPool, token)
				require.NoError(t, err)
				require.Equal(t, u.ID, id)
			} else {
				require.ErrorIs(t, err, storage.ErrAuthenticationChanged)
			}
		})
	}
}

func TestLoginIssuanceFailures(t *testing.T) {
	for _, kind := range []string{"session", "challenge", "mfa"} {
		stages := []string{"begin", "FOR UPDATE", "commit"}
		if kind == "challenge" {
			stages = append(stages, "INSERT INTO mfa_challenges")
		} else {
			stages = append(stages, "INSERT INTO sessions")
		}
		if kind == "mfa" {
			stages = append(stages, "DELETE FROM mfa_challenges")
		}
		for _, stage := range stages {
			t.Run(kind+"/"+stage, func(t *testing.T) {
				u := loginUser(t, kind != "session")
				token := ""
				if kind == "mfa" {
					var err error
					token, err = storage.CreateAuthenticatedMFAChallenge(t.Context(), testPool, u)
					require.NoError(t, err)
				}
				trace := &cancelGroupingQuery{match: stage}
				cfg := testPool.Config()
				cfg.ConnConfig.Tracer = trace
				pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
				require.NoError(t, err)
				defer pool.Close()
				switch kind {
				case "session":
					s, err := storage.CreateAuthenticatedSession(t.Context(), pool, u)
					require.Error(t, err)
					require.Nil(t, s)
				case "challenge":
					token, err := storage.CreateAuthenticatedMFAChallenge(t.Context(), pool, u)
					require.Error(t, err)
					require.Empty(t, token)
				case "mfa":
					s, err := storage.CompleteMFALogin(t.Context(), pool, token, u.ID, "secret")
					require.Error(t, err)
					require.Nil(t, s)
				}
				require.True(t, trace.hit.Load())
				var count int
				require.NoError(t, testPool.QueryRow(t.Context(), `SELECT count(*) FROM sessions WHERE user_id=$1`, u.ID).Scan(&count))
				require.Zero(t, count)
			})
		}
	}
	t.Run("incorrect MFA route", func(t *testing.T) {
		u := loginUser(t, true)
		s, err := storage.CreateAuthenticatedSession(t.Context(), testPool, u)
		require.ErrorIs(t, err, storage.ErrAuthenticationChanged)
		require.Nil(t, s)
		u.MFAEnabled = false
		token, err := storage.CreateAuthenticatedMFAChallenge(t.Context(), testPool, u)
		require.ErrorIs(t, err, storage.ErrAuthenticationChanged)
		require.Empty(t, token)
	})
	t.Run("deleted MFA user", func(t *testing.T) {
		u := loginUser(t, true)
		_, err := storage.DeleteUser(t.Context(), testPool, u.ID)
		require.NoError(t, err)
		s, err := storage.CompleteMFALogin(t.Context(), testPool, "token", u.ID, "secret")
		require.ErrorIs(t, err, storage.ErrAuthenticationChanged)
		require.Nil(t, s)
	})
}
