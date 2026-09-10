package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func singleUseMFAFixture(t *testing.T, pool *pgxpool.Pool) (*storage.User, string, string) {
	t.Helper()
	user, err := storage.CreateOAuthUser(t.Context(), pool, uuid.NewString()+"@single-use.example.com")
	require.NoError(t, err)
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Tindra", AccountName: user.Email})
	require.NoError(t, err)
	require.NoError(t, storage.StoreMFASecret(t.Context(), pool, user.ID, key.Secret()))
	require.NoError(t, storage.EnableMFA(t.Context(), pool, user.ID))
	token, err := storage.CreateMFAChallenge(t.Context(), pool, user.ID)
	require.NoError(t, err)
	code, err := totp.GenerateCode(key.Secret(), time.Now())
	require.NoError(t, err)
	return user, token, code
}

// Both requests must validate their codes before either consumes the challenge.
type concurrentMFAConsume struct {
	arrived atomic.Int32
	ready   chan struct{}
}

func (c *concurrentMFAConsume) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "SELECT mfa_secret, mfa_enabled FROM users") {
		if c.arrived.Add(1) == 2 {
			close(c.ready)
		}
		select {
		case <-c.ready:
		case <-ctx.Done():
		}
	}
	return ctx
}

func (*concurrentMFAConsume) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestMFAVerifyConcurrentSingleUse(t *testing.T) {
	pool := oauthDB(t)
	for _, cookie := range []bool{false, true} {
		name := "password token"
		if cookie {
			name = "OAuth cookie"
		}
		t.Run(name, func(t *testing.T) {
			user, token, code := singleUseMFAFixture(t, pool)
			trace := &concurrentMFAConsume{ready: make(chan struct{})}
			cfg := pool.Config()
			cfg.MaxConns = 2
			cfg.ConnConfig.Tracer = trace
			concurrent, err := pgxpool.NewWithConfig(t.Context(), cfg)
			require.NoError(t, err)
			defer concurrent.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			ro := &router{pool: concurrent}
			results := make(chan *httptest.ResponseRecorder, 2)
			for range 2 {
				go func() {
					body := `{"mfa_token":"` + token + `","code":"` + code + `"}`
					if cookie {
						body = `{"code":"` + code + `"}`
					}
					req := httptest.NewRequest("POST", "/api/auth/mfa/verify", strings.NewReader(body)).WithContext(ctx)
					if cookie {
						req.AddCookie(&http.Cookie{Name: "tindra_mfa", Value: token})
					}
					rec := httptest.NewRecorder()
					ro.handleMFAVerify(rec, req)
					results <- rec
				}()
			}
			first, second := <-results, <-results
			require.EqualValues(t, 2, trace.arrived.Load())
			require.ElementsMatch(t, []int{http.StatusOK, http.StatusUnauthorized}, []int{first.Code, second.Code})
			for _, rec := range []*httptest.ResponseRecorder{first, second} {
				if rec.Code == http.StatusUnauthorized {
					require.Equal(t, "invalid or expired MFA token\n", rec.Body.String())
					require.Empty(t, rec.Result().Cookies())
				}
			}
			var sessions int
			require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM sessions WHERE user_id=$1`, user.ID).Scan(&sessions))
			require.Equal(t, 1, sessions)
			remaining, err := storage.GetMFAChallenge(t.Context(), pool, token)
			require.NoError(t, err)
			require.Empty(t, remaining)
		})
	}
}

func TestMFAVerifyChallengeChangesAfterValidation(t *testing.T) {
	pool := oauthDB(t)
	for _, scenario := range []string{"expired", "consumed", "different user"} {
		t.Run(scenario, func(t *testing.T) {
			user, token, code := singleUseMFAFixture(t, pool)
			other, err := storage.CreateOAuthUser(t.Context(), pool, uuid.NewString()+"@other.example.com")
			require.NoError(t, err)
			trace := &changeMFAQuery{match: "DELETE FROM mfa_challenges", change: func() {
				switch scenario {
				case "expired":
					_, err = pool.Exec(t.Context(), `UPDATE mfa_challenges SET expires_at=NOW()-interval '1 second' WHERE user_id=$1`, user.ID)
					require.NoError(t, err)
				case "consumed":
					consumed, err := storage.ConsumeMFAChallenge(t.Context(), pool, token)
					require.NoError(t, err)
					require.Equal(t, user.ID, consumed)
				case "different user":
					_, err = pool.Exec(t.Context(), `UPDATE mfa_challenges SET user_id=$2 WHERE user_id=$1`, user.ID, other.ID)
					require.NoError(t, err)
				}
			}}
			cfg := pool.Config()
			cfg.ConnConfig.Tracer = trace
			changing, err := pgxpool.NewWithConfig(t.Context(), cfg)
			require.NoError(t, err)
			defer changing.Close()
			req := httptest.NewRequest("POST", "/api/auth/mfa/verify", strings.NewReader(`{"mfa_token":"`+token+`","code":"`+code+`"}`))
			rec := httptest.NewRecorder()
			(&router{pool: changing}).handleMFAVerify(rec, req)
			require.True(t, trace.fired.Load())
			require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
			require.Empty(t, rec.Result().Cookies())
			var sessions int
			require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM sessions WHERE user_id IN ($1, $2)`, user.ID, other.ID).Scan(&sessions))
			require.Zero(t, sessions)
		})
	}
}
