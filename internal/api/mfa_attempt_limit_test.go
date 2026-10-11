package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

// mfaLimitFixture creates an MFA-enabled user and returns it with its secret.
func mfaLimitFixture(t *testing.T, pool *pgxpool.Pool) (*storage.User, string) {
	t.Helper()
	user, err := storage.CreateOAuthUser(t.Context(), pool, uuid.NewString()+"@mfa-limit.example.com")
	require.NoError(t, err)
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Tindra", AccountName: user.Email})
	require.NoError(t, err)
	require.NoError(t, storage.StoreMFASecret(t.Context(), pool, user.ID, key.Secret()))
	require.NoError(t, storage.EnableMFA(t.Context(), pool, user.ID))
	return user, key.Secret()
}

// wrongMFACode returns a six-digit code the secret does not accept right now.
func wrongMFACode(t *testing.T, secret string) string {
	t.Helper()
	for n := 0; n < 1000000; n++ {
		code := fmt.Sprintf("%06d", n)
		if !totp.Validate(code, secret) {
			return code
		}
	}
	t.Fatal("no wrong code found")
	return ""
}

func verifyMFA(t *testing.T, pool *pgxpool.Pool, token, code string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"mfa_token":"` + token + `","code":"` + code + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/verify", strings.NewReader(body))
	rec := httptest.NewRecorder()
	(&router{pool: pool}).handleMFAVerify(rec, req)
	return rec
}

func validMFACode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	return code
}

func TestMFAVerify_challengeDiscardedAfterMaxFailures(t *testing.T) {
	pool := oauthDB(t)
	user, secret := mfaLimitFixture(t, pool)
	token, err := storage.CreateMFAChallenge(t.Context(), pool, user.ID)
	require.NoError(t, err)

	wrong := wrongMFACode(t, secret)
	for i := range storage.MFAChallengeMaxFailures {
		rec := verifyMFA(t, pool, token, wrong)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "attempt %d", i+1)
		require.Equal(t, "invalid code\n", rec.Body.String())
	}

	// Even the right code no longer works: the challenge is gone.
	rec := verifyMFA(t, pool, token, validMFACode(t, secret))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "invalid or expired MFA token\n", rec.Body.String())
}

func TestMFAVerify_userLockedAcrossChallenges(t *testing.T) {
	pool := oauthDB(t)
	user, secret := mfaLimitFixture(t, pool)
	wrong := wrongMFACode(t, secret)

	// Spread failures over several challenges, as an attacker logging in again would.
	failures := 0
	for failures < storage.MFAUserMaxFailures {
		token, err := storage.CreateMFAChallenge(t.Context(), pool, user.ID)
		require.NoError(t, err)
		for i := 0; i < storage.MFAChallengeMaxFailures-1 && failures < storage.MFAUserMaxFailures; i++ {
			require.Equal(t, http.StatusUnauthorized, verifyMFA(t, pool, token, wrong).Code)
			failures++
		}
	}

	locked, err := storage.MFALocked(t.Context(), pool, user.ID)
	require.NoError(t, err)
	require.True(t, locked)
	var challenges int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM mfa_challenges WHERE user_id=$1`, user.ID).Scan(&challenges))
	require.Zero(t, challenges, "locking discards outstanding challenges")

	// A fresh challenge with the correct code is still refused while locked.
	token, err := storage.CreateMFAChallenge(t.Context(), pool, user.ID)
	require.NoError(t, err)
	rec := verifyMFA(t, pool, token, validMFACode(t, secret))
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	var sessions int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM sessions WHERE user_id=$1`, user.ID).Scan(&sessions))
	require.Zero(t, sessions)

	// Once the lock expires the correct code works and the counters reset.
	_, err = pool.Exec(t.Context(), `UPDATE users SET mfa_locked_until = NOW() - interval '1 second' WHERE id=$1`, user.ID)
	require.NoError(t, err)
	rec = verifyMFA(t, pool, token, validMFACode(t, secret))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var attempts int
	var lockedUntil *time.Time
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT mfa_failed_attempts, mfa_locked_until FROM users WHERE id=$1`, user.ID).Scan(&attempts, &lockedUntil))
	require.Zero(t, attempts)
	require.Nil(t, lockedUntil)
}

func TestMFAVerify_successResetsFailureCount(t *testing.T) {
	pool := oauthDB(t)
	user, secret := mfaLimitFixture(t, pool)
	token, err := storage.CreateMFAChallenge(t.Context(), pool, user.ID)
	require.NoError(t, err)

	wrong := wrongMFACode(t, secret)
	for range storage.MFAChallengeMaxFailures - 1 {
		require.Equal(t, http.StatusUnauthorized, verifyMFA(t, pool, token, wrong).Code)
	}
	require.Equal(t, http.StatusOK, verifyMFA(t, pool, token, validMFACode(t, secret)).Code)

	var attempts int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT mfa_failed_attempts FROM users WHERE id=$1`, user.ID).Scan(&attempts))
	require.Zero(t, attempts)
}

func TestIPRateLimitKey(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7:1234":                     "203.0.113.7",
		"[2001:db8:1:2:aaaa::1]:443":           "2001:db8:1:2::/64",
		"[2001:db8:1:2:ffff:ffff:ffff:ff]:443": "2001:db8:1:2::/64",
		"[::ffff:203.0.113.7]:80":              "203.0.113.7",
		"[fe80::1%eth0]:80":                    "fe80::/64",
		"not-an-address":                       "not-an-address",
	}
	for in, want := range cases {
		if got := ipRateLimitKey(in); got != want {
			t.Errorf("ipRateLimitKey(%q) = %q, want %q", in, got, want)
		}
	}
}
