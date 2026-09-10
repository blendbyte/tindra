package api

import (
	"context"
	"encoding/json"
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
	"github.com/blendbyte/tindra/migrations"
)

func TestMFAReplacementPreservesActiveFactor(t *testing.T) {
	pool := oauthDB(t)
	user, err := storage.CreateOAuthUser(t.Context(), pool, uuid.NewString()+"@replacement.example.com")
	require.NoError(t, err)
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Tindra", AccountName: user.Email})
	require.NoError(t, err)
	require.NoError(t, storage.StoreMFASecret(t.Context(), pool, user.ID, key.Secret()))
	require.NoError(t, storage.EnableMFA(t.Context(), pool, user.ID))
	session, err := storage.CreateSession(t.Context(), pool, user.ID)
	require.NoError(t, err)
	h := NewRouter(pool, nil, nil, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: "tindra_session", Value: session.Token})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	assertActive := func(expected string) {
		t.Helper()
		secret, err := storage.GetMFASecret(t.Context(), pool, user.ID)
		require.NoError(t, err)
		require.NotNil(t, secret)
		require.Equal(t, expected, *secret)
		identity, err := storage.GetSessionIdentity(t.Context(), pool, session.Token)
		require.NoError(t, err)
		require.True(t, identity.MFAEnabled)
	}
	require.Equal(t, http.StatusMethodNotAllowed, request("GET", "/api/auth/mfa/setup", "").Code)
	for _, body := range []string{"", `{}`, `{"code":"invalid"}`} {
		require.Equal(t, 401, request("POST", "/api/auth/mfa/setup", body).Code)
		assertActive(key.Secret())
	}
	oldCode, err := totp.GenerateCode(key.Secret(), time.Now())
	require.NoError(t, err)
	rec := request("POST", "/api/auth/mfa/setup", `{"code":"`+oldCode+`"}`)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var pending struct {
		Secret string `json:"secret"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &pending))
	require.NotEmpty(t, pending.Secret)
	require.NotEqual(t, key.Secret(), pending.Secret)
	assertActive(key.Secret())
	require.Equal(t, 401, request("POST", "/api/auth/mfa/confirm", `{"code":"invalid"}`).Code)
	assertActive(key.Secret())
	// Password/SSO login still verifies the old active factor during replacement.
	challenge, err := storage.CreateMFAChallenge(t.Context(), pool, user.ID)
	require.NoError(t, err)
	rec = request("POST", "/api/auth/mfa/verify", `{"mfa_token":"`+challenge+`","code":"`+oldCode+`"}`)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	newCode, err := totp.GenerateCode(pending.Secret, time.Now())
	require.NoError(t, err)
	rec = request("POST", "/api/auth/mfa/confirm", `{"code":"`+newCode+`"}`)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	assertActive(pending.Secret)
	secret, err := storage.GetPendingMFASecret(t.Context(), pool, user.ID)
	require.NoError(t, err)
	require.Nil(t, secret)
	require.Equal(t, 400, request("POST", "/api/auth/mfa/confirm", `{"code":"`+newCode+`"}`).Code)
}

func TestMFAPendingSetupLifecycle(t *testing.T) {
	pool := oauthDB(t)
	user, err := storage.CreateOAuthUser(t.Context(), pool, uuid.NewString()+"@pending.example.com")
	require.NoError(t, err)
	stage := func(secret string, active *string, expected bool) {
		t.Helper()
		ok, err := storage.SetPendingMFASecret(t.Context(), pool, user.ID, secret, active)
		require.NoError(t, err)
		require.Equal(t, expected, ok)
	}
	confirm := func(secret string, expected bool) {
		t.Helper()
		ok, err := storage.ConfirmMFA(t.Context(), pool, user.ID, secret)
		require.NoError(t, err)
		require.Equal(t, expected, ok)
	}
	stage("first", nil, true)
	active, err := storage.GetMFASecret(t.Context(), pool, user.ID)
	require.NoError(t, err)
	require.Nil(t, active)
	stage("second", nil, true)
	confirm("first", false) // Restart cannot promote the previously validated secret.
	confirm("second", true)
	stage("unauthed", nil, false) // A concurrent initial-enrollment request cannot replace an active factor.
	old := "second"
	stage("third", &old, true)
	_, err = pool.Exec(t.Context(), "UPDATE users SET mfa_pending_expires_at=NOW()-interval '1 second' WHERE id=$1", user.ID)
	require.NoError(t, err)
	pending, err := storage.GetPendingMFASecret(t.Context(), pool, user.ID)
	require.NoError(t, err)
	require.Nil(t, pending)
	confirm("third", false)
	stage("fourth", &old, true)
	confirm("fourth", true)
	stage("stale-auth", &old, false) // Replacement authorization is tied to the factor that was verified.
	current := "fourth"
	stage("fifth", &current, true)
	require.NoError(t, storage.DisableMFA(t.Context(), pool, user.ID))
	confirm("fifth", false)
	pending, err = storage.GetPendingMFASecret(t.Context(), pool, user.ID)
	require.NoError(t, err)
	require.Nil(t, pending)
}

func TestMFAReplacementDatabaseFailures(t *testing.T) {
	pool := oauthDB(t)
	user, err := storage.CreateOAuthUser(t.Context(), pool, uuid.NewString()+"@replacement-fail.example.com")
	require.NoError(t, err)
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Tindra", AccountName: user.Email})
	require.NoError(t, err)
	require.NoError(t, storage.StoreMFASecret(t.Context(), pool, user.ID, key.Secret()))
	require.NoError(t, storage.EnableMFA(t.Context(), pool, user.ID))
	for _, scenario := range []struct {
		query   string
		confirm bool
	}{
		{"SELECT id, email", false}, {"SELECT mfa_secret", false}, {"UPDATE users SET mfa_pending_secret", false},
		{"SELECT mfa_pending_secret", true}, {"UPDATE users SET mfa_secret = mfa_pending_secret", true},
	} {
		t.Run(scenario.query, func(t *testing.T) {
			secret := key.Secret()
			ok, err := storage.SetPendingMFASecret(t.Context(), pool, user.ID, secret, &secret)
			require.NoError(t, err)
			require.True(t, ok)
			trace := &cancelDashboardQuery{match: scenario.query}
			cfg := pool.Config()
			cfg.ConnConfig.Tracer = trace
			failing, err := pgxpool.NewWithConfig(t.Context(), cfg)
			require.NoError(t, err)
			defer failing.Close()
			code, err := totp.GenerateCode(secret, time.Now())
			require.NoError(t, err)
			req := httptest.NewRequest("POST", "/", strings.NewReader(`{"code":"`+code+`"}`))
			req = req.WithContext(context.WithValue(req.Context(), ctxUserID, user.ID))
			rec := httptest.NewRecorder()
			ro := &router{pool: failing}
			if scenario.confirm {
				ro.handleMFAConfirm(rec, req)
			} else {
				ro.handleMFASetup(rec, req)
			}
			require.True(t, trace.hit.Load())
			require.Equal(t, 500, rec.Code)
			require.Equal(t, "internal error\n", rec.Body.String())
			active, err := storage.GetMFASecret(t.Context(), pool, user.ID)
			require.NoError(t, err)
			require.Equal(t, secret, *active)
		})
	}
}

type changeMFAQuery struct {
	match  string
	fired  atomic.Bool
	change func()
}

func (c *changeMFAQuery) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, c.match) && c.fired.CompareAndSwap(false, true) {
		c.change()
	}
	return ctx
}
func (*changeMFAQuery) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestMFAReplacementConcurrentChanges(t *testing.T) {
	pool := oauthDB(t)
	for _, confirm := range []bool{false, true} {
		user, err := storage.CreateOAuthUser(t.Context(), pool, uuid.NewString()+"@replacement-race.example.com")
		require.NoError(t, err)
		key, err := totp.Generate(totp.GenerateOpts{Issuer: "Tindra", AccountName: user.Email})
		require.NoError(t, err)
		secret := key.Secret()
		require.NoError(t, storage.StoreMFASecret(t.Context(), pool, user.ID, secret))
		require.NoError(t, storage.EnableMFA(t.Context(), pool, user.ID))
		ok, err := storage.SetPendingMFASecret(t.Context(), pool, user.ID, secret, &secret)
		require.NoError(t, err)
		require.True(t, ok)
		trace := &changeMFAQuery{match: "UPDATE users SET mfa_pending_secret", change: func() { require.NoError(t, storage.DisableMFA(t.Context(), pool, user.ID)) }}
		if confirm {
			trace.match = "UPDATE users SET mfa_secret = mfa_pending_secret"
		}
		cfg := pool.Config()
		cfg.ConnConfig.Tracer = trace
		raced, err := pgxpool.NewWithConfig(t.Context(), cfg)
		require.NoError(t, err)
		code, err := totp.GenerateCode(secret, time.Now())
		require.NoError(t, err)
		req := httptest.NewRequest("POST", "/", strings.NewReader(`{"code":"`+code+`"}`))
		req = req.WithContext(context.WithValue(req.Context(), ctxUserID, user.ID))
		rec := httptest.NewRecorder()
		ro := &router{pool: raced}
		if confirm {
			ro.handleMFAConfirm(rec, req)
		} else {
			ro.handleMFASetup(rec, req)
		}
		raced.Close()
		require.True(t, trace.fired.Load())
		require.Equal(t, 409, rec.Code, rec.Body.String())
		active, err := storage.GetMFASecret(t.Context(), pool, user.ID)
		require.NoError(t, err)
		require.Nil(t, active)
	}
}

func TestPendingMFAMigrationPreservesEnrolledSecrets(t *testing.T) {
	pool := oauthDB(t)
	tx, err := pool.Begin(t.Context())
	require.NoError(t, err)
	defer tx.Rollback(t.Context()) //nolint:errcheck
	_, err = tx.Exec(t.Context(), `CREATE TEMP TABLE users (mfa_enabled boolean, mfa_secret text) ON COMMIT DROP;
 INSERT INTO users VALUES (true,'active-secret'),(false,'unconfirmed-secret')`)
	require.NoError(t, err)
	sql, err := migrations.FS.ReadFile("0023_mfa_pending_secret.up.sql")
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), string(sql))
	require.NoError(t, err)
	var active string
	require.NoError(t, tx.QueryRow(t.Context(), "SELECT mfa_secret FROM users WHERE mfa_enabled").Scan(&active))
	require.Equal(t, "active-secret", active)
	var cleared bool
	require.NoError(t, tx.QueryRow(t.Context(), "SELECT mfa_secret IS NULL AND mfa_pending_secret IS NULL AND mfa_pending_expires_at IS NULL FROM users WHERE NOT mfa_enabled").Scan(&cleared))
	require.True(t, cleared)
}

func TestMFASetupUnavailableAccountAndQRFailure(t *testing.T) {
	pool := oauthDB(t)
	for _, scenario := range []struct {
		name, email string
		status      int
	}{
		{"deleted-account", "", 404},
		{"missing-account-name", "", 500},
		{"oversized-qr", strings.Repeat("a", 10000) + "@example.com", 500},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			id := uuid.NewString()
			if scenario.status != 404 {
				user, err := storage.CreateOAuthUser(t.Context(), pool, id+"@qr.example.com")
				require.NoError(t, err)
				id = user.ID
				_, err = pool.Exec(t.Context(), "UPDATE users SET email=$2 WHERE id=$1", id, scenario.email)
				require.NoError(t, err)
			}
			req := httptest.NewRequest("POST", "/", nil)
			req = req.WithContext(context.WithValue(req.Context(), ctxUserID, id))
			rec := httptest.NewRecorder()
			(&router{pool: pool}).handleMFASetup(rec, req)
			require.Equal(t, scenario.status, rec.Code, rec.Body.String())
			active, err := storage.GetMFASecret(t.Context(), pool, id)
			require.NoError(t, err)
			require.Nil(t, active)
		})
	}
	closed, err := pgxpool.New(t.Context(), "postgres://unused@localhost:1/unused?sslmode=disable")
	require.NoError(t, err)
	closed.Close()
	require.Error(t, storage.DisableMFA(t.Context(), closed, uuid.NewString()))
}
