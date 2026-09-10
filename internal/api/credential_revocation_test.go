package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

type recoveryFixture struct {
	user      *storage.User
	sessions  []*storage.Session
	challenge string
	resets    []string
}

func seedRecovery(t *testing.T, pool *pgxpool.Pool) recoveryFixture {
	t.Helper()
	u, err := storage.CreateUser(t.Context(), pool, uuid.NewString()+"@recovery.example.com", "original-password")
	require.NoError(t, err)
	f := recoveryFixture{user: u}
	for range 2 {
		session, err := storage.CreateSession(t.Context(), pool, u.ID)
		require.NoError(t, err)
		f.sessions = append(f.sessions, session)
		reset, err := storage.CreatePasswordResetToken(t.Context(), pool, u.ID)
		require.NoError(t, err)
		f.resets = append(f.resets, reset)
	}
	challenge, err := storage.CreateMFAChallenge(t.Context(), pool, u.ID)
	require.NoError(t, err)
	f.challenge = challenge
	require.NoError(t, storage.StoreMFASecret(t.Context(), pool, u.ID, "JBSWY3DPEHPK3PXP"))
	require.NoError(t, storage.EnableMFA(t.Context(), pool, u.ID))
	active := "JBSWY3DPEHPK3PXP"
	staged, err := storage.SetPendingMFASecret(t.Context(), pool, u.ID, "pending-secret", &active)
	require.NoError(t, err)
	require.True(t, staged)
	return f
}

func TestCredentialChangesRevokeAccess(t *testing.T) {
	pool := oauthDB(t)
	for _, kind := range []string{"change", "admin", "recovery"} {
		t.Run(kind, func(t *testing.T) {
			f := seedRecovery(t, pool)
			other := seedRecovery(t, pool)
			h := NewRouter(pool, nil, nil, nil, nil, nil, nil, true, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
			method, path, body := "PATCH", "/api/me/password", `{"current_password":"original-password","new_password":"replacement-password"}`
			if kind == "admin" {
				_, err := storage.UpdateUserPermissions(t.Context(), pool, f.user.ID, storage.UserPermissions{ManageUsers: true})
				require.NoError(t, err)
				method = "PUT"
				path = "/api/users/" + f.user.ID + "/password"
				body = `{"password":"replacement-password"}`
			}
			if kind == "recovery" {
				method = "POST"
				path = "/api/auth/password-reset/" + f.resets[0]
				body = `{"password":"replacement-password"}`
			}
			req := httptest.NewRequest(method, path, strings.NewReader(body))
			req.AddCookie(&http.Cookie{Name: "tindra_session", Value: f.sessions[0].Token})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			expected := 200
			if kind == "admin" {
				expected = 204
			}
			require.Equal(t, expected, rec.Code, rec.Body.String())
			for _, session := range f.sessions {
				identity, err := storage.GetSessionIdentity(t.Context(), pool, session.Token)
				require.NoError(t, err)
				require.Nil(t, identity)
				req := httptest.NewRequest("GET", "/api/projects", nil)
				req.AddCookie(&http.Cookie{Name: "tindra_session", Value: session.Token})
				response := httptest.NewRecorder()
				h.ServeHTTP(response, req)
				require.Equal(t, 401, response.Code)
			}
			challenge, err := storage.GetMFAChallenge(t.Context(), pool, f.challenge)
			require.NoError(t, err)
			require.Empty(t, challenge)
			for _, token := range f.resets {
				u, err := storage.GetPasswordResetUser(t.Context(), pool, token)
				require.NoError(t, err)
				require.Nil(t, u)
			}
			pending, err := storage.GetPendingMFASecret(t.Context(), pool, f.user.ID)
			require.NoError(t, err)
			require.Nil(t, pending)
			user, err := storage.GetUserByID(t.Context(), pool, f.user.ID)
			require.NoError(t, err)
			require.Equal(t, kind != "recovery", user.MFAEnabled)
			fresh := ""
			for _, cookie := range rec.Result().Cookies() {
				if cookie.Name == "tindra_session" {
					fresh = cookie.Value
					require.True(t, cookie.Secure)
					require.True(t, cookie.HttpOnly)
					require.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
				}
			}
			if kind == "admin" {
				require.Empty(t, fresh)
			} else {
				require.NotEmpty(t, fresh)
				require.NotEqual(t, f.sessions[0].Token, fresh)
				identity, err := storage.GetSessionIdentity(t.Context(), pool, fresh)
				require.NoError(t, err)
				require.NotNil(t, identity)
				req := httptest.NewRequest("GET", "/api/projects", nil)
				req.AddCookie(&http.Cookie{Name: "tindra_session", Value: fresh})
				response := httptest.NewRecorder()
				h.ServeHTTP(response, req)
				status := 200
				if kind == "recovery" {
					status = 403
				}
				require.Equal(t, status, response.Code)
			}
			// Revocation is scoped to the account whose credentials changed.
			identity, err := storage.GetSessionIdentity(t.Context(), pool, other.sessions[0].Token)
			require.NoError(t, err)
			require.NotNil(t, identity)
			otherReset, err := storage.GetPasswordResetUser(t.Context(), pool, other.resets[0])
			require.NoError(t, err)
			require.NotNil(t, otherReset)
		})
	}
}

func TestCredentialChangesRollbackOnFailure(t *testing.T) {
	pool := oauthDB(t)
	for _, kind := range []string{"change", "admin", "recovery"} {
		stages := []string{"begin", "UPDATE users SET password_hash", "DELETE FROM sessions", "DELETE FROM mfa_challenges", "DELETE FROM password_reset_tokens", "INSERT INTO sessions", "commit"}
		if kind == "change" {
			stages = append(stages, "SELECT password_hash", "SELECT EXISTS(SELECT 1 FROM sessions")
		}
		if kind == "recovery" {
			stages = append(stages, "SELECT user_id FROM password_reset_tokens", "SELECT id FROM users", "UPDATE password_reset_tokens")
		}
		for _, stage := range stages {
			if kind == "admin" && stage == "INSERT INTO sessions" {
				continue
			}
			t.Run(kind+"/"+stage, func(t *testing.T) {
				f := seedRecovery(t, pool)
				trace := &cancelDashboardQuery{match: stage}
				cfg := pool.Config()
				cfg.ConnConfig.Tracer = trace
				failing, err := pgxpool.NewWithConfig(t.Context(), cfg)
				require.NoError(t, err)
				defer failing.Close()
				switch kind {
				case "change":
					_, err = storage.ChangeUserPasswordWithSession(t.Context(), failing, f.user.ID, "original-password", "replacement-password", f.sessions[0].Token)
				case "admin":
					err = storage.AdminSetPassword(t.Context(), failing, f.user.ID, "replacement-password")
				case "recovery":
					_, _, err = storage.UsePasswordResetTokenWithSession(t.Context(), failing, f.resets[0], "replacement-password")
				}
				require.True(t, trace.hit.Load())
				require.Error(t, err)
				user, err := storage.GetUserByID(t.Context(), pool, f.user.ID)
				require.NoError(t, err)
				require.Equal(t, f.user.PasswordHash, user.PasswordHash)
				require.True(t, user.MFAEnabled)
				for _, session := range f.sessions {
					identity, err := storage.GetSessionIdentity(t.Context(), pool, session.Token)
					require.NoError(t, err)
					require.NotNil(t, identity)
				}
				for _, token := range f.resets {
					u, err := storage.GetPasswordResetUser(t.Context(), pool, token)
					require.NoError(t, err)
					require.NotNil(t, u)
				}
				challenge, err := storage.GetMFAChallenge(t.Context(), pool, f.challenge)
				require.NoError(t, err)
				require.Equal(t, f.user.ID, challenge)
				pending, err := storage.GetPendingMFASecret(t.Context(), pool, f.user.ID)
				require.NoError(t, err)
				require.NotNil(t, pending)
				var sessions int
				require.NoError(t, pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE user_id=$1", f.user.ID).Scan(&sessions))
				require.Equal(t, 2, sessions)
			})
		}
	}
}

func TestCompetingRecoveryLinksOnlyOneSucceeds(t *testing.T) {
	pool := oauthDB(t)
	f := seedRecovery(t, pool)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i, token := range f.resets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, _, err := storage.UsePasswordResetTokenWithSession(t.Context(), pool, token, fmt.Sprintf("replacement-password-%d", i))
			if err == nil && u == nil {
				err = fmt.Errorf("already revoked")
			}
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.EqualError(t, err, "already revoked")
		}
	}
	require.Equal(t, 1, successes)
	var count int
	require.NoError(t, pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE user_id=$1", f.user.ID).Scan(&count))
	require.Equal(t, 1, count)
}

func TestPasswordChangeCannotRotateRevokedSession(t *testing.T) {
	pool := oauthDB(t)
	f := seedRecovery(t, pool)
	require.NoError(t, storage.DeleteSession(t.Context(), pool, f.sessions[0].Token))
	session, err := storage.ChangeUserPasswordWithSession(t.Context(), pool, f.user.ID, "original-password", "replacement-password", f.sessions[0].Token)
	require.ErrorIs(t, err, storage.ErrInvalidPassword)
	require.Nil(t, session)
	user, err := storage.GetUserByID(t.Context(), pool, f.user.ID)
	require.NoError(t, err)
	require.Equal(t, f.user.PasswordHash, user.PasswordHash)
	identity, err := storage.GetSessionIdentity(t.Context(), pool, f.sessions[1].Token)
	require.NoError(t, err)
	require.NotNil(t, identity)
}

func TestRecoveryRejectsCredentialsRevokedDuringRequest(t *testing.T) {
	pool := oauthDB(t)
	for _, deleteUser := range []bool{false, true} {
		f := seedRecovery(t, pool)
		trace := &changeMFAQuery{match: "UPDATE password_reset_tokens", change: func() {
			_, err := pool.Exec(t.Context(), "DELETE FROM password_reset_tokens WHERE user_id=$1", f.user.ID)
			require.NoError(t, err)
		}}
		if deleteUser {
			trace.match = "SELECT id FROM users"
			trace.change = func() {
				_, err := pool.Exec(t.Context(), "DELETE FROM users WHERE id=$1", f.user.ID)
				require.NoError(t, err)
			}
		}
		cfg := pool.Config()
		cfg.ConnConfig.Tracer = trace
		raced, err := pgxpool.NewWithConfig(t.Context(), cfg)
		require.NoError(t, err)
		user, session, err := storage.UsePasswordResetTokenWithSession(t.Context(), raced, f.resets[0], "replacement-password")
		raced.Close()
		require.True(t, trace.fired.Load())
		require.NoError(t, err)
		require.Nil(t, user)
		require.Nil(t, session)
	}
}

func TestCredentialChangeValidation(t *testing.T) {
	pool := oauthDB(t)
	f := seedRecovery(t, pool)
	session, err := storage.ChangeUserPasswordWithSession(t.Context(), pool, f.user.ID, "original-password", "replacement-password", "")
	require.ErrorIs(t, err, storage.ErrInvalidPassword)
	require.Nil(t, session)
	_, _, err = storage.UsePasswordResetTokenWithSession(t.Context(), pool, f.resets[0], strings.Repeat("x", 73))
	require.Error(t, err)
	require.Error(t, storage.AdminSetPassword(t.Context(), pool, f.user.ID, strings.Repeat("x", 73)))
	// Hash-generation errors must precede any credential mutations.
	previous := storage.BcryptCost
	storage.BcryptCost = 32
	t.Cleanup(func() { storage.BcryptCost = previous })
	_, err = storage.ChangeUserPasswordWithSession(t.Context(), pool, f.user.ID, "original-password", "replacement-password", f.sessions[0].Token)
	require.ErrorContains(t, err, "hash password")
	_, _, err = storage.UsePasswordResetTokenWithSession(t.Context(), pool, f.resets[0], "replacement-password")
	require.ErrorContains(t, err, "hash password")
	require.ErrorContains(t, storage.AdminSetPassword(t.Context(), pool, f.user.ID, "replacement-password"), "hash password")
	user, err := storage.GetUserByID(t.Context(), pool, f.user.ID)
	require.NoError(t, err)
	require.Equal(t, f.user.PasswordHash, user.PasswordHash)
}

func TestChangePasswordRequiresIdentityAndCookie(t *testing.T) {
	for _, withIdentity := range []bool{false, true} {
		req := httptest.NewRequest("PATCH", "/api/me/password", strings.NewReader(`{"current_password":"original-password","new_password":"replacement-password"}`))
		if withIdentity {
			req = req.WithContext(context.WithValue(req.Context(), ctxUserID, uuid.NewString()))
		}
		rec := httptest.NewRecorder()
		(&router{}).handleChangePassword(rec, req)
		require.Equal(t, 401, rec.Code)
	}
}
