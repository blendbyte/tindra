package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestInvalidResetTokensNeverReachHashing(t *testing.T) {
	pool := oauthDB(t)
	f := seedRecovery(t, pool)
	_, err := pool.Exec(t.Context(), "UPDATE password_reset_tokens SET expires_at=NOW()-interval '1 second' WHERE user_id=$1", f.user.ID)
	require.NoError(t, err)
	spent, err := storage.CreatePasswordResetToken(t.Context(), pool, f.user.ID)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), "UPDATE password_reset_tokens SET used_at=NOW() WHERE user_id=$1 AND expires_at>NOW()", f.user.ID)
	require.NoError(t, err)
	valid, err := storage.CreatePasswordResetToken(t.Context(), pool, f.user.ID)
	require.NoError(t, err)
	previous := storage.BcryptCost
	storage.BcryptCost = 32
	defer func() { storage.BcryptCost = previous }()
	for _, token := range []string{"", uuid.NewString(), f.resets[0], spent} {
		user, session, err := storage.UsePasswordResetTokenWithSession(t.Context(), pool, token, "replacement-password")
		require.NoError(t, err, "invalid token must be rejected before the deliberately broken hasher")
		require.Nil(t, user)
		require.Nil(t, session)
	}
	_, _, err = storage.UsePasswordResetTokenWithSession(t.Context(), pool, valid, "replacement-password")
	require.ErrorContains(t, err, "hash password")
	// Hash failure must roll back the claimed token and preserve old access.
	user, err := storage.GetPasswordResetUser(t.Context(), pool, valid)
	require.NoError(t, err)
	require.NotNil(t, user)
	identity, err := storage.GetSessionIdentity(t.Context(), pool, f.sessions[0].Token)
	require.NoError(t, err)
	require.NotNil(t, identity)
	storage.BcryptCost = previous
	user, _, err = storage.UsePasswordResetTokenWithSession(t.Context(), pool, valid, "replacement-password")
	require.NoError(t, err)
	require.NotNil(t, user)
}

func TestPasswordResetRequestBounds(t *testing.T) {
	pool := oauthDB(t)
	f := seedRecovery(t, pool)
	h := NewRouter(pool, nil, nil, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, false, nil)
	for _, scenario := range []struct {
		body   string
		status int
	}{
		{`{"password":"` + strings.Repeat("x", 4096) + `"}`, 413},
		{`{"password":"replacement-password"}` + strings.Repeat(" ", 4096), 413},
		{`{"password":"replacement-password"} {}`, 400},
		{`{`, 400},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/auth/password-reset/"+f.resets[0], strings.NewReader(scenario.body)))
		require.Equal(t, scenario.status, rec.Code, rec.Body.String())
		user, err := storage.GetPasswordResetUser(t.Context(), pool, f.resets[0])
		require.NoError(t, err)
		require.NotNil(t, user)
	}
	rec := httptest.NewRecorder()
	body := `{"password":"replacement-password"}`
	body += strings.Repeat(" ", maxPasswordResetBodyBytes-len(body))
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/auth/password-reset/"+f.resets[0], strings.NewReader(body)))
	require.Equal(t, 200, rec.Code, rec.Body.String())
}

func TestPasswordResetRateLimitSharesBudgetAcrossTokensAndMethods(t *testing.T) {
	h := NewRouter(oauthDB(t), nil, nil, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 2, 0, nil, false, false, nil)
	for i := range 3 {
		method := "GET"
		if i == 1 {
			method = "POST"
		}
		req := httptest.NewRequest(method, fmt.Sprintf("/api/auth/password-reset/invalid-%d", i), strings.NewReader(`{"password":"replacement-password"}`))
		req.RemoteAddr = "192.0.2.1:1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if i < 2 {
			require.Equal(t, 404, rec.Code)
		} else {
			require.Equal(t, 429, rec.Code)
			require.NotEmpty(t, rec.Header().Get("Retry-After"))
		}
	}
	// A second client retains its own budget, and reset requests do not consume login attempts.
	req := httptest.NewRequest("GET", "/api/auth/password-reset/invalid", nil)
	req.RemoteAddr = "192.0.2.2:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, 404, rec.Code)
	req = httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{}`))
	req.RemoteAddr = "192.0.2.1:1234"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, 400, rec.Code)
}

type blockingResetQuery struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingResetQuery) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "SELECT user_id FROM password_reset_tokens") {
		b.entered <- struct{}{}
		select {
		case <-b.release:
		case <-ctx.Done():
		}
	}
	return ctx
}
func (*blockingResetQuery) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestPasswordResetConcurrencyBoundAndSlotRelease(t *testing.T) {
	pool := oauthDB(t)
	f := seedRecovery(t, pool)
	blocker := &blockingResetQuery{entered: make(chan struct{}, 4), release: make(chan struct{})}
	cfg := pool.Config()
	cfg.ConnConfig.Tracer = blocker
	blocked, err := pgxpool.NewWithConfig(t.Context(), cfg)
	require.NoError(t, err)
	defer blocked.Close()
	h := NewRouter(blocked, nil, nil, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, false, nil)
	results := make(chan int, 4)
	// Always release waiting queries, including on assertion failure.
	defer func() {
		select {
		case <-blocker.release:
		default:
			close(blocker.release)
		}
	}()
	for range 4 {
		go func() {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/auth/password-reset/"+f.resets[0], strings.NewReader(`{"password":"replacement-password"}`)))
			results <- rec.Code
		}()
	}
	for range 4 {
		select {
		case <-blocker.entered:
		case <-time.After(10 * time.Second):
			t.Fatal("reset operation did not reach the gate")
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/auth/password-reset/"+f.resets[0], strings.NewReader(`{"password":"replacement-password"}`)))
	require.Equal(t, 429, rec.Code)
	require.Equal(t, "1", rec.Header().Get("Retry-After"))
	close(blocker.release)
	success := 0
	for range 4 {
		select {
		case status := <-results:
			if status == 200 {
				success++
			} else {
				require.Equal(t, 404, status)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("reset operation did not finish")
		}
	}
	require.Equal(t, 1, success)
	require.Zero(t, h.ro.passwordResetActive.Load())
	// An invalid token after completion gets its ordinary response, not a leaked busy slot.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/auth/password-reset/invalid", strings.NewReader(`{"password":"replacement-password"}`)))
	require.Equal(t, 404, rec.Code)
	require.Zero(t, h.ro.passwordResetActive.Load())
}

func TestPasswordResetReadFailureDoesNotStartWork(t *testing.T) {
	ro := &router{}
	req := httptest.NewRequest("POST", "/api/auth/password-reset/token", iotest.ErrReader(fmt.Errorf("connection interrupted")))
	rec := httptest.NewRecorder()
	ro.handleDoPasswordReset(rec, req)
	require.Equal(t, 400, rec.Code)
	require.Equal(t, "unable to read request\n", rec.Body.String())
	require.Zero(t, ro.passwordResetActive.Load())
}
