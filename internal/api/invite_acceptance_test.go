package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestInviteRevocationBeforeClaimPreventsAdmission(t *testing.T) {
	pool := oauthDB(t)
	email := uuid.NewString() + "@revoked.test"
	token, err := storage.CreateInvite(t.Context(), pool, "", email, "Invited")
	require.NoError(t, err)
	inv, err := storage.GetInvite(t.Context(), pool, token)
	require.NoError(t, err)
	trace := &changeMFAQuery{match: "UPDATE user_invites SET accepted_at", change: func() {
		deleted, err := storage.DeleteInvite(t.Context(), pool, inv.ID)
		require.NoError(t, err)
		require.True(t, deleted)
	}}
	cfg := pool.Config()
	cfg.ConnConfig.Tracer = trace
	traced, err := pgxpool.NewWithConfig(t.Context(), cfg)
	require.NoError(t, err)
	defer traced.Close()
	ro := &router{pool: traced}
	routes := chi.NewRouter()
	routes.Post("/{token}", ro.handleAcceptInvite)
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest("POST", "/"+token, strings.NewReader(`{"password":"new-password-1234"}`)))
	require.True(t, trace.fired.Load())
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Empty(t, rec.Result().Cookies())
	user, err := storage.GetUserByEmail(t.Context(), pool, email)
	require.NoError(t, err)
	require.Nil(t, user)
}

func TestInviteSessionFailureRollsBackAdmission(t *testing.T) {
	pool := oauthDB(t)
	email := uuid.NewString() + "@failed-invite.test"
	token, err := storage.CreateInvite(t.Context(), pool, "", email, "Invited")
	require.NoError(t, err)
	trace := &cancelDashboardQuery{match: "INSERT INTO sessions"}
	cfg := pool.Config()
	cfg.ConnConfig.Tracer = trace
	traced, err := pgxpool.NewWithConfig(t.Context(), cfg)
	require.NoError(t, err)
	defer traced.Close()
	ro := &router{pool: traced}
	routes := chi.NewRouter()
	routes.Post("/{token}", ro.handleAcceptInvite)
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest("POST", "/"+token, strings.NewReader(`{"password":"new-password-1234"}`)))
	require.True(t, trace.hit.Load())
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "internal error\n", rec.Body.String())
	require.Empty(t, rec.Result().Cookies())
	user, err := storage.GetUserByEmail(t.Context(), pool, email)
	require.NoError(t, err)
	require.Nil(t, user)
	invite, err := storage.GetInvite(t.Context(), pool, token)
	require.NoError(t, err)
	require.NotNil(t, invite, "failed admission must leave the invitation usable")
}
